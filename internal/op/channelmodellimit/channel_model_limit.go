package channelmodellimit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Entry 是渠道×模型上下文上限的一条记录（内存快照形态）。
type Entry struct {
	ChannelID        int       `json:"channel_id"`
	ModelName        string    `json:"model_name"`
	MaxContextTokens int       `json:"max_context_tokens"`
	Source           string    `json:"source"`
	EvidenceCount    int       `json:"evidence_count"`
	UpdatedAt        time.Time `json:"updated_at"`
}

var (
	cacheMu sync.RWMutex
	cache   = map[string]Entry{}

	// evidenceDedupe 抑制同一渠道×模型在短时间内重复计数（重试风暴/并发重复请求）。
	// 「独立」超限证据的判定窗口：60 秒内同一目标只计一次。
	evidenceMu     sync.Mutex
	evidenceDedupe = map[string]time.Time{}
)

const evidenceIndependenceWindow = time.Minute

func normalizeModel(modelName string) string {
	return strings.ToLower(strings.TrimSpace(modelName))
}

func cacheKey(channelID int, modelName string) string {
	return fmt.Sprintf("%d\x00%s", channelID, normalizeModel(modelName))
}

// InitCache 载入全部上限到内存（启动时调用一次；写入路径会同步刷新）。
func InitCache(ctx context.Context) error {
	var rows []model.ChannelModelLimit
	if err := db.GetDB().WithContext(ctx).Find(&rows).Error; err != nil {
		return fmt.Errorf("load channel model limits: %w", err)
	}
	next := make(map[string]Entry, len(rows))
	for _, row := range rows {
		next[cacheKey(row.ChannelID, row.ModelName)] = Entry{
			ChannelID:        row.ChannelID,
			ModelName:        normalizeModel(row.ModelName),
			MaxContextTokens: row.MaxContextTokens,
			Source:           row.Source,
			EvidenceCount:    row.EvidenceCount,
			UpdatedAt:        row.UpdatedAt,
		}
	}
	cacheMu.Lock()
	cache = next
	cacheMu.Unlock()
	return nil
}

// Lookup 查询已知上限。ok=false 表示未声明/未知（预检不生效）。
func Lookup(channelID int, modelName string) (Entry, bool) {
	if channelID <= 0 || strings.TrimSpace(modelName) == "" {
		return Entry{}, false
	}
	cacheMu.RLock()
	entry, ok := cache[cacheKey(channelID, modelName)]
	cacheMu.RUnlock()
	if !ok || entry.MaxContextTokens <= 0 {
		return Entry{}, false
	}
	return entry, true
}

// LookupAll 返回当前缓存里的全部上限（分析/诊断用）。
func LookupAll() []Entry {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	out := make([]Entry, 0, len(cache))
	for _, entry := range cache {
		out = append(out, entry)
	}
	return out
}

func storeCache(entry Entry) {
	cacheMu.Lock()
	cache[cacheKey(entry.ChannelID, entry.ModelName)] = entry
	cacheMu.Unlock()
}

func dropCache(channelID int, modelName string) {
	cacheMu.Lock()
	delete(cache, cacheKey(channelID, modelName))
	cacheMu.Unlock()
}

// ListByChannel 返回某渠道的全部上限记录（渠道编辑页读取）。
func ListByChannel(ctx context.Context, channelID int) ([]Entry, error) {
	if channelID <= 0 {
		return nil, nil
	}
	var rows []model.ChannelModelLimit
	if err := db.GetDB().WithContext(ctx).
		Where("channel_id = ?", channelID).
		Order("model_name ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list channel model limits: %w", err)
	}
	out := make([]Entry, 0, len(rows))
	for _, row := range rows {
		out = append(out, Entry{
			ChannelID:        row.ChannelID,
			ModelName:        normalizeModel(row.ModelName),
			MaxContextTokens: row.MaxContextTokens,
			Source:           row.Source,
			EvidenceCount:    row.EvidenceCount,
			UpdatedAt:        row.UpdatedAt,
		})
	}
	return out, nil
}

// SetManual 写入/更新人工声明的上下文上限（source=manual，永不被学习覆盖）。
// maxContextTokens <= 0 时删除该条记录（等价于「未声明」）。
func SetManual(ctx context.Context, channelID int, modelName string, maxContextTokens int) error {
	normalized := normalizeModel(modelName)
	if channelID <= 0 || normalized == "" {
		return errors.New("channel id and model name are required")
	}
	if maxContextTokens <= 0 {
		return Delete(ctx, channelID, normalized)
	}
	entry := Entry{
		ChannelID:        channelID,
		ModelName:        normalized,
		MaxContextTokens: maxContextTokens,
		Source:           model.ChannelModelLimitSourceManual,
		UpdatedAt:        time.Now(),
	}
	if err := upsert(ctx, entry); err != nil {
		return err
	}
	storeCache(entry)
	return nil
}

// Delete 删除一条上限记录。
func Delete(ctx context.Context, channelID int, modelName string) error {
	normalized := normalizeModel(modelName)
	if channelID <= 0 || normalized == "" {
		return nil
	}
	if err := db.GetDB().WithContext(ctx).
		Where("channel_id = ? AND model_name = ?", channelID, normalized).
		Delete(&model.ChannelModelLimit{}).Error; err != nil {
		return fmt.Errorf("delete channel model limit: %w", err)
	}
	dropCache(channelID, normalized)
	return nil
}

// upsert 写入一行（值全量覆盖；调用方已在读-改-写中算出最终值）。
func upsert(ctx context.Context, entry Entry) error {
	now := time.Now()
	row := model.ChannelModelLimit{
		ChannelID:        entry.ChannelID,
		ModelName:        normalizeModel(entry.ModelName),
		MaxContextTokens: entry.MaxContextTokens,
		Source:           entry.Source,
		EvidenceCount:    entry.EvidenceCount,
		UpdatedAt:        now,
	}
	return db.GetDB().WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "channel_id"}, {Name: "model_name"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"max_context_tokens": row.MaxContextTokens,
			"source":             row.Source,
			"evidence_count":     row.EvidenceCount,
			"updated_at":         now,
		}),
	}).Create(&row).Error
}

// learnThreshold 返回写入 learned 上限所需的独立超限次数（默认 2）。
func learnThreshold() int {
	v, err := setting.GetInt(model.SettingKeyChannelModelLimitLearnThreshold)
	if err != nil || v < 1 {
		return 2
	}
	return v
}

func learningEnabled() bool {
	v, err := setting.GetBool(model.SettingKeyChannelModelLimitLearnEnabled)
	if err != nil {
		return true
	}
	return v
}

// RecordOverflow 记录一次上游上下文超限（400 类）。
//
// 语义（v2.9.0 计划）：
//   - 同一渠道×模型的「独立」超限证据累计达到阈值（默认 2 次）后写入 learned 上限；
//   - 学习值取「观察到的最小失败请求估算 token 数」——比它大的请求至少要冒同样风险，
//     取最小值是保守上界；
//   - source=manual 的记录永不被覆盖，只累计 evidence（提示人工声明与上游实际冲突）；
//   - 60 秒内同一目标只计一次，避免重试风暴刷出假证据。
//
// 返回是否本次写入/更新了 learned 上限与当前证据数。
func RecordOverflow(ctx context.Context, channelID int, modelName string, estimatedTokens int) (bool, int) {
	normalized := normalizeModel(modelName)
	if channelID <= 0 || normalized == "" {
		return false, 0
	}
	if !learningEnabled() {
		return false, 0
	}
	key := cacheKey(channelID, normalized)
	evidenceMu.Lock()
	if last, ok := evidenceDedupe[key]; ok && time.Since(last) < evidenceIndependenceWindow {
		evidenceMu.Unlock()
		if entry, ok := Lookup(channelID, normalized); ok {
			return false, entry.EvidenceCount
		}
		return false, 0
	}
	evidenceDedupe[key] = time.Now()
	evidenceMu.Unlock()

	var row model.ChannelModelLimit
	err := db.GetDB().WithContext(ctx).
		Where("channel_id = ? AND model_name = ?", channelID, normalized).
		Take(&row).Error
	exists := err == nil
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, 0
	}

	evidence := 1
	source := model.ChannelModelLimitSourceLearned
	maxTokens := estimatedTokens
	if exists {
		evidence = row.EvidenceCount + 1
		source = row.Source
		maxTokens = row.MaxContextTokens
		if source == model.ChannelModelLimitSourceLearned && row.MaxContextTokens > 0 && estimatedTokens > 0 {
			// 取更保守（更小）的上限。
			if estimatedTokens < row.MaxContextTokens {
				maxTokens = estimatedTokens
			}
		}
	}

	learnedWritten := false
	if source != model.ChannelModelLimitSourceManual {
		// 未达阈值时保留已有学习值（可能为 0=未知），只累加证据计数；
		// 达阈值且本次有有效估算时写入更保守（更小）的 learned 上限。
		if evidence >= learnThreshold() && estimatedTokens > 0 {
			source = model.ChannelModelLimitSourceLearned
			if maxTokens <= 0 || estimatedTokens < maxTokens {
				maxTokens = estimatedTokens
			}
			learnedWritten = true
		} else {
			source = model.ChannelModelLimitSourceLearned
		}
	}

	entry := Entry{
		ChannelID:        channelID,
		ModelName:        normalized,
		MaxContextTokens: maxTokens,
		Source:           source,
		EvidenceCount:    evidence,
		UpdatedAt:        time.Now(),
	}
	if err := upsert(ctx, entry); err != nil {
		return false, evidence
	}
	storeCache(entry)
	return learnedWritten, evidence
}
