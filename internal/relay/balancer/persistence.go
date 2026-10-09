package balancer

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	ch "github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/store"
	"github.com/lingyuins/octopus/internal/utils/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func LoadRuntimeState(ctx context.Context) error {
	dbConn := db.GetDB()
	if dbConn == nil {
		return nil
	}

	// Redis 后端优先：多实例共享的实时状态从 Redis 加载（issue #123）。
	// Redis 为空（首次启动/Redis 刚清空）时回落 DB，保证不丢历史状态。
	var autoStates []model.AutoStrategyState
	var circuitStates []model.CircuitBreakerState
	loadedFromRedis := false
	if store.Enabled() {
		rs := store.GetRuntimeState()
		if as, err := rs.LoadAuto(ctx); err == nil && len(as) > 0 {
			autoStates = as
			loadedFromRedis = true
		}
		if cs, err := rs.LoadCircuit(ctx); err == nil && len(cs) > 0 {
			circuitStates = cs
			loadedFromRedis = true
		}
	}
	if !loadedFromRedis {
		if err := dbConn.WithContext(ctx).Find(&autoStates).Error; err != nil {
			return err
		}
		if err := dbConn.WithContext(ctx).Find(&circuitStates).Error; err != nil {
			return err
		}
	}

	clearAutoStats()
	clearCircuitBreaker()

	now := time.Now()
	timeWindow := getTimeWindow()
	sampleThreshold := getSampleThreshold()

	autoLoaded := 0
	for _, state := range autoStates {
		if state.ChannelID > 0 && !channelExists(state.ChannelID) {
			continue
		}

		stats := restoreChannelStats(state.Records, now, timeWindow, sampleThreshold)
		if stats == nil {
			continue
		}

		key := strings.TrimSpace(state.Key)
		if key == "" {
			key = statsKey(state.ChannelID, state.ModelName)
		}
		globalAutoStats.Store(key, stats)
		autoLoaded++
	}

	circuitLoaded := 0
	for _, state := range circuitStates {
		if state.ChannelID > 0 && state.ChannelKeyID > 0 && !channelKeyExists(state.ChannelID, state.ChannelKeyID) {
			continue
		}

		entry := restoreCircuitEntry(state)
		if entry == nil {
			continue
		}

		key := strings.TrimSpace(state.Key)
		if key == "" {
			key = circuitKey(state.ChannelID, state.ChannelKeyID, state.ModelName)
		}
		globalBreaker.Store(key, entry)
		circuitLoaded++
	}

	log.Infof("balancer runtime state loaded: auto=%d circuit=%d", autoLoaded, circuitLoaded)
	return nil
}

func SaveRuntimeState(ctx context.Context) error {
	dbConn := db.GetDB()
	if dbConn == nil {
		return nil
	}

	now := time.Now()
	generation := now.UnixMilli()
	autoStates := snapshotAutoStrategyStates(now)
	circuitStates := snapshotCircuitBreakerStates(now)

	for i := range autoStates {
		autoStates[i].UpdatedAt = generation
	}
	for i := range circuitStates {
		circuitStates[i].UpdatedAt = generation
	}

	// Redis 后端：双写到 Redis（多实例共享状态，issue #123）。
	// DB 仍是持久快照，Redis 作为实时共享态。失败仅记录日志，不阻塞 DB 落盘。
	if store.Enabled() {
		saveRuntimeStateToRedis(ctx, autoStates, circuitStates)
	}

	return dbConn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(autoStates) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key"}},
				UpdateAll: true,
			}).Create(&autoStates).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("updated_at < ?", generation).Delete(&model.AutoStrategyState{}).Error; err != nil {
			return err
		}

		if len(circuitStates) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key"}},
				UpdateAll: true,
			}).Create(&circuitStates).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("updated_at < ?", generation).Delete(&model.CircuitBreakerState{}).Error; err != nil {
			return err
		}

		return nil
	})
}

// saveRuntimeStateToRedis 将 circuit breaker / auto strategy 状态快照写入 Redis
// （多实例共享，issue #123）。失败仅记录日志，不影响 DB 落盘主流程。
func saveRuntimeStateToRedis(ctx context.Context, autoStates []model.AutoStrategyState, circuitStates []model.CircuitBreakerState) {
	rt := store.GetRuntimeState()
	for _, s := range autoStates {
		key := strings.TrimSpace(s.Key)
		if key == "" {
			key = statsKey(s.ChannelID, s.ModelName)
		}
		if err := rt.SaveAuto(ctx, key, s); err != nil {
			log.Warnf("redis save auto state %s: %v", key, err)
		}
	}
	for _, s := range circuitStates {
		key := strings.TrimSpace(s.Key)
		if key == "" {
			key = circuitKey(s.ChannelID, s.ChannelKeyID, s.ModelName)
		}
		if err := rt.SaveCircuit(ctx, key, s); err != nil {
			log.Warnf("redis save circuit state %s: %v", key, err)
		}
	}
}

func RuntimeStateSaveDBTask() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	log.Debugf("balancer runtime state save db task started")
	startTime := time.Now()
	defer func() {
		log.Debugf("balancer runtime state save db task finished, save time: %s", time.Since(startTime))
	}()

	if err := SaveRuntimeState(ctx); err != nil {
		log.Errorf("balancer runtime state save db error: %v", err)
	}
}

func clearAutoStats() {
	globalAutoStats.Range(func(key, _ any) bool {
		globalAutoStats.Delete(key)
		return true
	})
}

func clearCircuitBreaker() {
	globalBreaker.Range(func(key, _ any) bool {
		globalBreaker.Delete(key)
		return true
	})
}

func channelExists(channelID int) bool {
	_, err := ch.Get(channelID, context.Background())
	return err == nil
}

func channelKeyExists(channelID, keyID int) bool {
	channel, err := ch.Get(channelID, context.Background())
	if err != nil {
		return false
	}
	for _, key := range channel.Keys {
		if key.ID == keyID {
			return true
		}
	}
	return false
}

func parseAutoStatsKey(key string) (int, string, bool) {
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return 0, "", false
	}

	channelID, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", false
	}

	return channelID, parts[1], true
}

func parseCircuitStateKey(key string) (int, int, string, bool) {
	parts := strings.SplitN(key, ":", 3)
	if len(parts) != 3 {
		return 0, 0, "", false
	}

	channelID, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, "", false
	}
	keyID, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, "", false
	}

	return channelID, keyID, parts[2], true
}

func newChannelStatsWithCapacity(capacity int) *ChannelStats {
	if capacity < 1 {
		capacity = 1
	}
	return &ChannelStats{
		window:             make([]RequestRecord, capacity),
		cacheValidDuration: 5 * time.Second,
	}
}

// snapshotRecords serializes the sliding-window request records for persistence.
// TTFT EMA is intentionally NOT persisted (issue #183): AutoStrategyRecord lives
// in the model package (out of this change's scope) and adding a parallel TTFT
// field there plus restore logic would persist a scalar that the EMA can rebuild
// from scratch within ~8 samples after restart (alpha=0.3). The trade-off is a
// short cold-start window where the TTFT input to the Auto score is 0 ("no
// evidence"), during which the scorer falls back to total latency — acceptable
// for a heuristic score, and it keeps the persisted schema unchanged.
func (cs *ChannelStats) snapshotRecords(now time.Time, timeWindow time.Duration, sampleThreshold int) []model.AutoStrategyRecord {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	if cs.count == 0 || len(cs.window) == 0 {
		return nil
	}

	cutoff := now.Add(-timeWindow)
	start := 0
	if cs.count == len(cs.window) {
		start = cs.head
	}

	records := make([]model.AutoStrategyRecord, 0, cs.count)
	for i := 0; i < cs.count; i++ {
		idx := (start + i) % len(cs.window)
		record := cs.window[idx]
		if record.Timestamp.IsZero() || record.Timestamp.Before(cutoff) {
			continue
		}
		records = append(records, model.AutoStrategyRecord{
			Timestamp: record.Timestamp.UnixMilli(),
			Success:   record.Success,
		})
	}

	if sampleThreshold > 0 && len(records) > sampleThreshold {
		records = append([]model.AutoStrategyRecord(nil), records[len(records)-sampleThreshold:]...)
	}

	return records
}

func restoreChannelStats(records []model.AutoStrategyRecord, now time.Time, timeWindow time.Duration, sampleThreshold int) *ChannelStats {
	if sampleThreshold < 1 {
		sampleThreshold = 1
	}

	cutoff := now.Add(-timeWindow).UnixMilli()
	filtered := make([]model.AutoStrategyRecord, 0, len(records))
	for _, record := range records {
		if record.Timestamp <= 0 || record.Timestamp < cutoff {
			continue
		}
		filtered = append(filtered, record)
	}

	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) > sampleThreshold {
		filtered = filtered[len(filtered)-sampleThreshold:]
	}

	stats := newChannelStatsWithCapacity(sampleThreshold)
	for _, record := range filtered {
		stats.window[stats.head] = RequestRecord{
			Timestamp: time.UnixMilli(record.Timestamp),
			Success:   record.Success,
		}
		stats.head = (stats.head + 1) % len(stats.window)
		if stats.count < len(stats.window) {
			stats.count++
		}
	}

	return stats
}

func snapshotAutoStrategyStates(now time.Time) []model.AutoStrategyState {
	timeWindow := getTimeWindow()
	sampleThreshold := getSampleThreshold()
	updatedAt := now.UnixMilli()

	states := make([]model.AutoStrategyState, 0)
	globalAutoStats.Range(func(rawKey, value any) bool {
		key, ok := rawKey.(string)
		if !ok {
			return true
		}
		stats, ok := value.(*ChannelStats)
		if !ok || stats == nil {
			return true
		}

		channelID, modelName, ok := parseAutoStatsKey(key)
		if !ok || !channelExists(channelID) {
			return true
		}

		records := stats.snapshotRecords(now, timeWindow, sampleThreshold)
		if len(records) == 0 {
			return true
		}

		states = append(states, model.AutoStrategyState{
			Key:       key,
			ChannelID: channelID,
			ModelName: modelName,
			Records:   records,
			UpdatedAt: updatedAt,
		})
		return true
	})

	sort.Slice(states, func(i, j int) bool {
		return states[i].Key < states[j].Key
	})
	return states
}

func snapshotCircuitEntry(
	key string,
	channelID int,
	channelKeyID int,
	modelName string,
	entry *circuitEntry,
	updatedAt int64,
) (model.CircuitBreakerState, bool) {
	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.State == StateClosed && entry.ConsecutiveFailures == 0 && entry.TripCount == 0 && entry.LastFailureTime.IsZero() {
		return model.CircuitBreakerState{}, false
	}

	state := entry.State
	if state == StateHalfOpen {
		state = StateOpen
	}

	lastFailureTime := int64(0)
	if !entry.LastFailureTime.IsZero() {
		lastFailureTime = entry.LastFailureTime.UnixMilli()
	}

	return model.CircuitBreakerState{
		Key:                 key,
		ChannelID:           channelID,
		ChannelKeyID:        channelKeyID,
		ModelName:           modelName,
		State:               int(state),
		ConsecutiveFailures: entry.ConsecutiveFailures,
		LastFailureTime:     lastFailureTime,
		TripCount:           entry.TripCount,
		UpdatedAt:           updatedAt,
	}, true
}

func restoreCircuitEntry(state model.CircuitBreakerState) *circuitEntry {
	circuitState := CircuitState(state.State)
	switch circuitState {
	case StateHalfOpen:
		circuitState = StateOpen
	case StateClosed, StateOpen:
	default:
		circuitState = StateClosed
	}

	entry := &circuitEntry{
		State:               circuitState,
		ConsecutiveFailures: state.ConsecutiveFailures,
		TripCount:           state.TripCount,
	}
	if state.LastFailureTime > 0 {
		entry.LastFailureTime = time.UnixMilli(state.LastFailureTime)
	}

	if entry.State == StateClosed && entry.ConsecutiveFailures == 0 && entry.TripCount == 0 && entry.LastFailureTime.IsZero() {
		return nil
	}

	return entry
}

func snapshotCircuitBreakerStates(now time.Time) []model.CircuitBreakerState {
	updatedAt := now.UnixMilli()
	states := make([]model.CircuitBreakerState, 0)

	globalBreaker.Range(func(rawKey, value any) bool {
		key, ok := rawKey.(string)
		if !ok {
			return true
		}
		entry, ok := value.(*circuitEntry)
		if !ok || entry == nil {
			return true
		}

		channelID, channelKeyID, modelName, ok := parseCircuitStateKey(key)
		if !ok || !channelKeyExists(channelID, channelKeyID) {
			return true
		}

		state, ok := snapshotCircuitEntry(key, channelID, channelKeyID, modelName, entry, updatedAt)
		if !ok {
			return true
		}
		states = append(states, state)
		return true
	})

	sort.Slice(states, func(i, j int) bool {
		return states[i].Key < states[j].Key
	})
	return states
}
