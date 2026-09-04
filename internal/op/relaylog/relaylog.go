package relaylog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/lingyuins/octopus/internal/utils/json"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/cacheusage"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/utils/log"
	"github.com/lingyuins/octopus/internal/utils/snowflake"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const relayLogMaxSize = 200
const relayLogMaxSizeNoDB = 200 // 当不保存到数据库时，允许更大的缓存用于实时查询
const relayLogStreamTokenTTL = 5 * time.Minute

// DefaultRelayLogMaxContentSizeMB 是单条日志正文上限默认值（MiB）。
const DefaultRelayLogMaxContentSizeMB = 2

// DefaultRelayLogMemoryLogMaxDimidiateTimes 是仅内存模式下折半次数阈值默认值。
const DefaultRelayLogMemoryLogMaxDimidiateTimes = 15

// memoryLogDimidiateTimes 记录仅内存模式下日志折半发生的次数；
// 达到阈值后主动调用一次 runtime.GC，及时回收旧日志正文占用的堆内存。
// 计数更新复用 relayLogCacheLock，无需额外加锁。
var memoryLogDimidiateTimes int

// GetRelayLogMaxContentSizeMB 读取单条日志正文上限（MiB），-1=不限。
// 读取失败或非法值回退默认值。
func GetRelayLogMaxContentSizeMB() int {
	v, err := setting.GetInt(model.SettingKeyRelayLogMaxContentSizeMB)
	if err != nil || v < -1 {
		return DefaultRelayLogMaxContentSizeMB
	}
	return v
}

// RelayLogContentExceedsLimit 报告正文合计字节数是否超限。-1 表示不限。
func RelayLogContentExceedsLimit(contentSize int64, maxContentSizeMB int) bool {
	if maxContentSizeMB == -1 {
		return false
	}
	const bytesPerMiB = 1024 * 1024
	return contentSize > int64(maxContentSizeMB)*bytesPerMiB
}

// getMemoryLogMaxDimidiateTimes 读取折半 GC 阈值，-1=关闭。非法值回退默认值。
func getMemoryLogMaxDimidiateTimes() int {
	v, err := setting.GetInt(model.SettingKeyRelayLogMemoryLogMaxDimidiateTimes)
	if err != nil {
		return DefaultRelayLogMemoryLogMaxDimidiateTimes
	}
	if v == -1 {
		return -1
	}
	if v < 1 {
		return DefaultRelayLogMemoryLogMaxDimidiateTimes
	}
	return v
}

// halveMemoryLogCache 保留最新一半日志并重建底层数组。
// 必须用 make+copy 而不是 reslice，否则旧底层数组仍被引用，
// 被淘汰日志的 Request/ResponseContent 大字段无法释放。
// 调用方必须持有 relayLogCacheLock。返回是否发生了折半。
func halveMemoryLogCache(maxSize int) bool {
	keepSize := maxSize / 2
	if len(relayLogCache) <= keepSize {
		return false
	}
	newCache := make([]model.RelayLog, keepSize, maxSize)
	copy(newCache, relayLogCache[len(relayLogCache)-keepSize:])
	relayLogCache = newCache
	return true
}

// noteMemoryLogDimidiate 记录一次折半，达到阈值时返回 true（调用方在锁外 GC）。
func noteMemoryLogDimidiate() bool {
	maxTimes := getMemoryLogMaxDimidiateTimes()
	if maxTimes == -1 {
		return false
	}
	memoryLogDimidiateTimes++
	if memoryLogDimidiateTimes > maxTimes {
		memoryLogDimidiateTimes = 0
		return true
	}
	return false
}

var relayLogCache = make([]model.RelayLog, 0, relayLogMaxSize)
var relayLogCacheLock sync.Mutex

func GetCacheAndLock() ([]model.RelayLog, *sync.Mutex) { return relayLogCache, &relayLogCacheLock }

var relayLogFlushLock sync.Mutex

var flushCh = make(chan struct{}, 1)

// notifyCh 缓冲待广播给实时订阅者的日志。RelayLogAdd 在每条日志写入时把日志
// 非阻塞地推入该 channel，由单个常驻分发 goroutine（见 startNotifyWorker）
// 顺序广播给订阅者。这样避免了每条日志都启动一个短命 goroutine（高 QPS 下的
// goroutine 风暴），并保证订阅者按写入顺序收到日志。channel 满时丢弃最新日志，
// 与 notifySubscribers 对慢订阅者的丢弃语义一致。
var notifyCh = make(chan model.RelayLog, 1024)

func startNotifyWorker(ctx context.Context) {
	go func() {
		for {
			select {
			case relayLog := <-notifyCh:
				notifySubscribers(relayLog)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func triggerFlush() {
	select {
	case flushCh <- struct{}{}:
	default:
	}
}

func StartFlushWorker(ctx context.Context) {
	startNotifyWorker(ctx)
	go func() {
		for {
			select {
			case <-flushCh:
				if db.IsLogSQLite() {
					db.EnqueueWrite(db.WriteJob{Name: "relay_log_flush", Fn: func(_ context.Context) error {
						return relayLogFlushToDB(context.Background())
					}})
				} else {
					if err := relayLogFlushToDB(context.Background()); err != nil {
						log.Warnf("async relay log flush failed: %v", err)
					}
				}
			case <-ctx.Done():
				_ = relayLogFlushToDB(context.Background())
				return
			}
		}
	}()
}

var relayLogSubscribers = make(map[chan model.RelayLog]struct{})
var relayLogSubscribersLock sync.RWMutex

var relayLogStreamTokens = make(map[string]time.Time)
var relayLogStreamTokensLock sync.RWMutex

func RelayLogStreamTokenCreate() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)
	createdAt := time.Now()

	relayLogStreamTokensLock.Lock()
	relayLogStreamTokens[token] = createdAt
	relayLogStreamTokensLock.Unlock()

	return token, nil
}

func RelayLogStreamTokenVerify(token string) bool {
	now := time.Now()

	relayLogStreamTokensLock.Lock()
	createdAt, ok := relayLogStreamTokens[token]
	if !ok {
		relayLogStreamTokensLock.Unlock()
		return false
	}
	if now.Sub(createdAt) > relayLogStreamTokenTTL {
		delete(relayLogStreamTokens, token)
		relayLogStreamTokensLock.Unlock()
		return false
	}
	relayLogStreamTokensLock.Unlock()
	return true
}

func RelayLogStreamTokenRevoke(token string) {
	relayLogStreamTokensLock.Lock()
	delete(relayLogStreamTokens, token)
	relayLogStreamTokensLock.Unlock()
}

// PurgeExpiredStreamTokens 清理过期的 SSE 流 token（5 分钟 TTL）。
// 虽然 Verify 会惰性删除过期条目，但长期未访问的 token 会驻留；
// 周期主动清理防止 map 在高频创建 + 低频访问场景下无界增长。
func PurgeExpiredStreamTokens() int {
	now := time.Now()
	relayLogStreamTokensLock.Lock()
	defer relayLogStreamTokensLock.Unlock()

	deleted := 0
	for token, createdAt := range relayLogStreamTokens {
		if now.Sub(createdAt) > relayLogStreamTokenTTL {
			delete(relayLogStreamTokens, token)
			deleted++
		}
	}
	return deleted
}

func RelayLogSubscribe() chan model.RelayLog {
	ch := make(chan model.RelayLog, 10)
	relayLogSubscribersLock.Lock()
	relayLogSubscribers[ch] = struct{}{}
	relayLogSubscribersLock.Unlock()
	return ch
}

func RelayLogUnsubscribe(ch chan model.RelayLog) {
	relayLogSubscribersLock.Lock()
	if _, ok := relayLogSubscribers[ch]; ok {
		delete(relayLogSubscribers, ch)
		close(ch)
	}
	relayLogSubscribersLock.Unlock()
}

func notifySubscribers(relayLog model.RelayLog) {
	relayLogSubscribersLock.RLock()
	defer relayLogSubscribersLock.RUnlock()

	for ch := range relayLogSubscribers {
		select {
		case ch <- relayLog:
		default:
		}
	}
}

func relayLogStreamTokenCleanup(now time.Time) {
	relayLogStreamTokensLock.Lock()
	for token, createdAt := range relayLogStreamTokens {
		if now.Sub(createdAt) > relayLogStreamTokenTTL {
			delete(relayLogStreamTokens, token)
		}
	}
	relayLogStreamTokensLock.Unlock()
}

func relayLogFlushToDB(ctx context.Context) error {
	relayLogFlushLock.Lock()
	defer relayLogFlushLock.Unlock()

	relayLogCacheLock.Lock()
	if len(relayLogCache) == 0 {
		relayLogCacheLock.Unlock()
		return nil
	}
	batch := make([]model.RelayLog, len(relayLogCache))
	copy(batch, relayLogCache)
	// 记录 batch 中最后一条日志的 ID，用于安全截断
	lastFlushedID := batch[len(batch)-1].ID
	relayLogCacheLock.Unlock()

	// 独立日志库被关闭（CloseLogDB）时 GetLogDB 返回 nil；此时不应写入，
	// 保留缓存等待下次（重开后）刷盘。共用主库模式下永远非 nil。
	conn := db.GetLogDB()
	if conn == nil {
		return nil
	}

	// Create 前剥离大字段的副本？不行——需要把 content 写入 DB。
	// Create 成功后截断缓存即可释放内存；截断前 batch 持有 content 是短暂尖刺。
	result := conn.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&batch)
	if result.Error != nil {
		return result.Error
	}

	// 尽快丢弃 batch 对大字段的引用，帮助 GC。
	for i := range batch {
		batch[i].RequestContent = ""
		batch[i].ResponseContent = ""
	}

	relayLogCacheLock.Lock()
	// 安全截断：只移除 ID <= lastFlushedID 的前缀部分
	cutIdx := 0
	for i, l := range relayLogCache {
		if l.ID == lastFlushedID {
			cutIdx = i + 1
			break
		}
		if l.ID > lastFlushedID {
			// 遇到比 batch 更新的日志，说明截断点已过
			break
		}
	}
	if cutIdx > 0 {
		// 显式清空被截断前缀的大字段，避免底层数组仍被引用时拖住内存。
		for i := 0; i < cutIdx; i++ {
			relayLogCache[i].RequestContent = ""
			relayLogCache[i].ResponseContent = ""
		}
		relayLogCache = relayLogCache[cutIdx:]
	}
	if len(relayLogCache) == 0 {
		relayLogCache = make([]model.RelayLog, 0, relayLogMaxSize)
	}
	relayLogCacheLock.Unlock()

	return nil
}

func RelayLogAdd(ctx context.Context, relayLog model.RelayLog) error {
	enabled, err := setting.GetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return err
	}
	maxSize := relayLogMaxSize
	if !enabled {
		maxSize = relayLogMaxSizeNoDB
	}
	relayLog.ID = snowflake.GenerateID()
	// 非阻塞地推入通知 channel，由常驻分发 goroutine 顺序广播给订阅者。
	// 避免每条日志启动一个短命 goroutine；channel 满时丢弃。
	select {
	case notifyCh <- relayLog:
	default:
	}

	relayLogCacheLock.Lock()
	needMemoryLogGC := false
	// GC 必须在锁外触发，避免阻塞日志热路径。
	defer func() {
		if needMemoryLogGC {
			runtime.GC()
		}
	}()

	// 应用队列丢弃策略，防止高 QPS 下内存无界增长（见 issue OOM 诊断报告）。
	if len(relayLogCache) >= maxSize {
		dropPolicy, _ := setting.GetString(model.SettingKeyRelayLogQueueDropPolicy)
		if dropPolicy == "" {
			dropPolicy = "oldest" // 默认丢弃最旧
		}

		switch dropPolicy {
		case "disabled":
			// 阻塞触发刷盘（旧行为：满 200 触发异步 flush，本次直接返回）
			if enabled {
				relayLogCacheLock.Unlock()
				triggerFlush()
				return nil
			}
			// 不保存到数据库时，保留最近一半并重建底层数组，
			// 否则旧数组仍引用被淘汰日志的大字段，内存无法回收。
			if halveMemoryLogCache(maxSize) {
				needMemoryLogGC = noteMemoryLogDimidiate()
			}
		case "oldest":
			// 丢弃最旧日志（推荐：保留最新的诊断数据）
			relayLogCache = relayLogCache[1:]
		case "newest":
			// 丢弃最新日志（保留历史序列完整性，但可能丢失最新错误）
			// 不追加新日志，直接返回
			relayLogCacheLock.Unlock()
			return nil
		default:
			// 未知策略，回退到丢弃最旧
			relayLogCache = relayLogCache[1:]
		}
	}

	relayLogCache = append(relayLogCache, relayLog)
	relayLogCacheLock.Unlock()
	return nil
}

// RelayLogAttemptsAdd 把一条 RelayLog 的各次尝试写入 relay_log_attempts 关联表，
// 使失败尝试（尤其"渠道A 失败→重试到B 成功"中的渠道A）可被按 channel_id 过滤/聚合。
// 日志关闭时不写（enabled=false）。所有数据库方言均同步写入，确保 attempts 在
// relay_logs 异步刷盘并截断内存缓存之前已落库，消除"relay_logs 已入 DB 但 attempts
// 尚未写入"的竞态窗口（issue #121）。relayLogID 必须已分配（即 RelayLogAdd 之后
// 调用）。返回错误仅供记录，调用方通常忽略。
func RelayLogAttemptsAdd(ctx context.Context, relayLogID int64, attempts []model.ChannelAttempt, logTime int64) error {
	if len(attempts) == 0 {
		return nil
	}
	enabled, err := setting.GetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	rows := make([]model.RelayLogAttempt, 0, len(attempts))
	for _, a := range attempts {
		if a.ChannelID == 0 {
			continue // 跳过无渠道归属的占位尝试
		}
		rows = append(rows, model.RelayLogAttempt{
			RelayLogID:  relayLogID,
			ChannelID:   a.ChannelID,
			ChannelName: a.ChannelName,
			ModelName:   a.ModelName,
			Status:      string(a.Status),
			Duration:    a.Duration,
			Time:        logTime,
		})
	}
	if len(rows) == 0 {
		return nil
	}

	conn := db.GetLogDB()
	if conn == nil {
		return nil
	}
	return conn.WithContext(ctx).Create(&rows).Error
}

func RelayLogSaveDBTask(ctx context.Context) error {
	log.Debugf("relay log save db task started")
	startTime := time.Now()
	defer func() {
		log.Debugf("relay log save db task finished, save time: %s", time.Since(startTime))
	}()
	now := time.Now()
	defer relayLogStreamTokenCleanup(now)
	enabled, err := setting.GetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return err
	}

	if enabled {
		if err := relayLogFlushToDB(ctx); err != nil {
			return err
		}
		return relayLogCleanup(ctx)
	}

	// 日志关闭：清空数据库中所有历史日志以释放磁盘空间
	if err := relayLogCleanupAll(ctx); err != nil {
		log.Warnf("failed to cleanup all logs from DB: %v", err)
	}

	relayLogCacheLock.Lock()
	needMemoryLogGC := halveMemoryLogCache(relayLogMaxSizeNoDB) && noteMemoryLogDimidiate()
	relayLogCacheLock.Unlock()
	// GC 在锁外触发，避免阻塞定时任务。
	if needMemoryLogGC {
		runtime.GC()
	}

	return nil
}

// ApplyKeepEnabledChange 在「保留历史日志」开关变更后调整独立日志库连接：
//   - 关闭日志：先清空日志表释放空间，再断开独立日志库连接（释放文件句柄/连接池）；
//   - 开启日志：重连独立日志库。
//
// 仅在「独立日志库」模式下有实际效果；共用主库时 db.CloseLogDB/ReopenLogDB 均为
// 空操作，绝不会触碰主库连接。关闭日志后 RelayLogAdd 不再触发 DB 写入，因此断连安全。
func ApplyKeepEnabledChange(ctx context.Context, enabled bool) error {
	if !db.IsLogDBSeparate() {
		return nil
	}
	if enabled {
		return db.ReopenLogDB()
	}
	// 关闭：先清空（此时连接仍在），再断开。
	if err := relayLogCleanupAll(ctx); err != nil {
		log.Warnf("failed to clear logs before closing log DB: %v", err)
	}
	return db.CloseLogDB()
}

func relayLogCleanup(ctx context.Context) error {
	conn := db.GetLogDB()
	if conn == nil {
		// 独立日志库已断开（如日志已关闭），无需清理。
		return nil
	}

	// Priority: keep count > keep period (days)
	keepCount, err := setting.GetInt(model.SettingKeyRelayLogKeepCount)
	if err != nil {
		return err
	}

	if keepCount > 0 {
		// Count-based cleanup with batch deletion (50% when over threshold)
		// Avoids high-frequency small deletes under heavy load
		var total int64
		if err := conn.Model(&model.RelayLog{}).Count(&total).Error; err != nil {
			return err
		}
		if total <= int64(keepCount) {
			return nil // Under threshold, no cleanup needed
		}
		// Delete 50% of current records to create buffer before next cleanup.
		// 先取出待删区间的边界 id（按 id 升序第 deleteCount 个），再以
		// `id < threshold` 做范围删除——走主键范围扫描，避免 `id IN (子查询)`
		// 在大表上重复扫描子查询结果集，显著加快清理速度。
		deleteCount := total / 2
		var thresholdID int64
		if err := conn.WithContext(ctx).Model(&model.RelayLog{}).
			Order("id ASC").
			Offset(int(deleteCount)).
			Limit(1).
			Pluck("id", &thresholdID).Error; err != nil {
			return err
		}
		if thresholdID == 0 {
			return nil
		}
		// 同步删除关联的 relay_log_attempts，避免 relay_logs 行被删后留下孤儿
		// attempts（无外键级联）。否则 analytics 的 INNER JOIN 会排除这些孤儿，
		// 表现为"数据消失"（issue #93）。
		if err := conn.WithContext(ctx).
			Where("relay_log_id < ?", thresholdID).
			Delete(&model.RelayLogAttempt{}).Error; err != nil {
			return err
		}
		return conn.WithContext(ctx).
			Where("id < ?", thresholdID).
			Delete(&model.RelayLog{}).Error
	}

	// Fallback to days-based cleanup
	keepPeriod, err := setting.GetInt(model.SettingKeyRelayLogKeepPeriod)
	if err != nil {
		return err
	}

	if keepPeriod <= 0 {
		return nil
	}

	cutoffTime := time.Now().Add(-time.Duration(keepPeriod) * 24 * time.Hour).Unix()
	// 先删 relay_log_attempts 中早于阈值的行，再删 relay_logs。两表无外键级联，
	// 若只删 relay_logs 会留下指向已删 id 的孤儿 attempts 行，导致 analytics 的
	// INNER JOIN 聚合查不到数据（渠道×模型/利用率卡片随旧日志清理逐渐清空）。
	if err := conn.WithContext(ctx).
		Where("time < ?", cutoffTime).
		Delete(&model.RelayLogAttempt{}).Error; err != nil {
		return err
	}
	return conn.WithContext(ctx).Where("time < ?", cutoffTime).Delete(&model.RelayLog{}).Error
}

// relayLogCleanupAll 删除数据库中所有日志记录，用于日志关闭时释放磁盘空间。
//
// 使用 db.FastClearTable（TRUNCATE / DROP+重建）而非逐行 DELETE：百万级日志在
// SQLite + WAL + 单连接下逐行删可能耗时数十分钟，整表清空近乎瞬时。
//
// relay_log_attempts 与 relay_logs 无外键级联，必须显式清空，否则关闭日志后
// 残留的 attempts 孤儿行会在重新开启日志后被 analytics 的 INNER JOIN 排除，
// 造成"渠道×模型/利用率查不到历史数据"的假象。
func relayLogCleanupAll(ctx context.Context) error {
	conn := db.GetLogDB()
	if conn == nil {
		// 独立日志库已断开（日志关闭场景）：无需清理。
		return nil
	}
	if err := db.FastClearTable(conn.WithContext(ctx), &model.RelayLogAttempt{}, "relay_log_attempts"); err != nil {
		return err
	}
	return db.FastClearTable(conn.WithContext(ctx), &model.RelayLog{}, "relay_logs")
}

// loadExcludedGroupSet 读取被屏蔽分组的设置（JSON 字符串数组），返回以分组
// 名称为键的集合。配置缺失或解析失败时返回空集合（即不屏蔽任何分组）。
//
// 设计说明：本系统中分组的 Name 即客户端请求时使用的“模型名”，日志的
// request_model_name 正是这个值（见 internal/relay/metrics.go 与
// op/group.GroupGetEnabledMapByEndpoint 的解析逻辑）。因此按分组名屏蔽日志
// 只需匹配 request_model_name，无需把分组解析为渠道集合，也不会因渠道被多个
// 分组共享而误伤其它分组的日志。
func loadExcludedGroupSet() map[string]struct{} {
	raw, err := setting.GetString(model.SettingKeyLogExcludedGroups)
	if err != nil || raw == "" {
		return nil
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil
	}
	if len(names) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" {
			continue
		}
		set[name] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// RelayLogStreamExcluded 判断某条实时日志是否应在 SSE 流中被屏蔽。
// 供 streamLog 处理器在广播前过滤被屏蔽分组的日志。
func RelayLogStreamExcluded(requestModelName string) bool {
	excluded := loadExcludedGroupSet()
	if excluded == nil {
		return false
	}
	_, ok := excluded[requestModelName]
	return ok
}

// LogFilter 日志列表筛选参数
type LogFilter struct {
	StartTime *int
	EndTime   *int
	Model     string // 模糊匹配 request_model_name 或 actual_model_name
	// Models 指定一个或多个模型名做精确匹配（不区分大小写），命中
	// request_model_name 或 actual_model_name 任一即通过。与 Model 模糊
	// 匹配可共存，两者为 OR 关系（issue #117）。
	Models       []string
	ChannelID    *int
	APIKeyID     *int
	EndpointType string
	HasError     *bool // nil=全部, false=仅成功, true=仅失败
	// IncludeAttempts 控制 channel_id / HasError 过滤是否"穿透"到单次尝试维度。
	// 为 true 时，"在渠道A 失败→重试到B 成功"的请求也会被 ChannelID=A 命中，
	// HasError=true 也会命中整体成功但含失败尝试的请求（issue #67）。
	IncludeAttempts bool
	// IsTest 控制"测试日志"过滤：nil=全部, true=仅测试, false=仅非测试（issue #82）。
	IsTest *bool
}

// logHasFailedAttempt 报告该日志是否存在任意一次失败的渠道尝试。
func logHasFailedAttempt(l model.RelayLog) bool {
	for _, a := range l.Attempts {
		if a.Status == model.AttemptFailed {
			return true
		}
	}
	return false
}

// logMatchesAttemptChannel 报告该日志是否在指定渠道上有过尝试（任意状态）。
// 成败维度由调用方经 HasError 单独判定，避免 ChannelID 与 HasError 语义耦合。
func logMatchesAttemptChannel(l model.RelayLog, channelID int) bool {
	for _, a := range l.Attempts {
		if a.ChannelID == channelID {
			return true
		}
	}
	return false
}

// RelayLogList 查询日志列表，支持可选的筛选条件
// 返回轻量条目，不包含 request_content 和 response_content 大字段
func RelayLogList(ctx context.Context, filter LogFilter, page, pageSize int) ([]model.RelayLogListItem, error) {
	enabled, err := setting.GetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return nil, err
	}
	hasFilter := filter.StartTime != nil || filter.EndTime != nil ||
		filter.Model != "" || len(filter.Models) > 0 || filter.ChannelID != nil ||
		filter.APIKeyID != nil || filter.EndpointType != "" || filter.HasError != nil ||
		filter.IsTest != nil
	// modelsLower 预计算 Models 的 小写集合，供缓存路径与 DB 路径共用。
	modelsLower := make(map[string]struct{}, len(filter.Models))
	for _, m := range filter.Models {
		if m = strings.TrimSpace(m); m != "" {
			modelsLower[strings.ToLower(m)] = struct{}{}
		}
	}
	excludedGroups := loadExcludedGroupSet()

	matchesFilter := func(log model.RelayLog) bool {
		if filter.StartTime != nil && log.Time < int64(*filter.StartTime) {
			return false
		}
		if filter.EndTime != nil && log.Time > int64(*filter.EndTime) {
			return false
		}
		if excludedGroups != nil {
			if _, ok := excludedGroups[log.RequestModelName]; ok {
				return false
			}
		}
		// 模型过滤：Model（模糊）与 Models（精确，不区分大小写）为 OR 关系。
		// 二者均设置时命中任一即通过；均未设置时跳过。issue #117。
		if filter.Model != "" || len(modelsLower) > 0 {
			ok := false
			if filter.Model != "" {
				modelLower := strings.ToLower(filter.Model)
				if strings.Contains(strings.ToLower(log.RequestModelName), modelLower) ||
					strings.Contains(strings.ToLower(log.ActualModelName), modelLower) {
					ok = true
				}
			}
			if !ok && len(modelsLower) > 0 {
				if _, hit := modelsLower[strings.ToLower(log.RequestModelName)]; hit {
					ok = true
				} else if _, hit := modelsLower[strings.ToLower(log.ActualModelName)]; hit {
					ok = true
				}
			}
			if !ok {
				return false
			}
		}
		if filter.ChannelID != nil {
			if log.ChannelId == *filter.ChannelID {
				// 顶层渠道命中，直接通过
			} else if filter.IncludeAttempts && logMatchesAttemptChannel(log, *filter.ChannelID) {
				// 该请求在某次尝试中用到了目标渠道，命中（成败由 HasError 单独判定）
			} else {
				return false
			}
		}
		if filter.APIKeyID != nil && log.RequestAPIKeyID != *filter.APIKeyID {
			return false
		}
		if filter.EndpointType != "" && log.EndpointType != filter.EndpointType {
			return false
		}
		if filter.HasError != nil {
			if *filter.HasError {
				// 只看"失败"：整体失败 或（开启穿透时）任一次尝试失败
				if log.Error == "" && !(filter.IncludeAttempts && logHasFailedAttempt(log)) {
					return false
				}
			} else {
				// 只看"成功"：整体成功 且（开启穿透时）不含失败尝试
				if log.Error != "" {
					return false
				}
				if filter.IncludeAttempts && logHasFailedAttempt(log) {
					return false
				}
			}
		}
		if filter.IsTest != nil && log.IsTest != *filter.IsTest {
			return false
		}
		return true
	}

	// 锁内只做一次预分配的整体拷贝（单次 memmove，无分支、无扩容），尽量缩短
	// 与热路径 RelayLogAdd 争用同一把 Mutex 的时间；时间过滤移到锁外执行。
	relayLogCacheLock.Lock()
	snapshot := make([]model.RelayLog, len(relayLogCache))
	copy(snapshot, relayLogCache)
	relayLogCacheLock.Unlock()

	// 在锁外按条件过滤（保持原始顺序：旧 -> 新）
	var cachedLogs []model.RelayLog
	if hasFilter || excludedGroups != nil {
		cachedLogs = make([]model.RelayLog, 0, len(snapshot))
		for _, log := range snapshot {
			if !matchesFilter(log) {
				continue
			}
			cachedLogs = append(cachedLogs, log)
		}
	} else {
		cachedLogs = snapshot
	}

	cacheCount := len(cachedLogs)
	offset := (page - 1) * pageSize

	var result []model.RelayLogListItem

	// 先从缓存中按"新 -> 旧"顺序分页提取，不再整段 reverse。
	if offset < cacheCount {
		cacheTake := min(pageSize, cacheCount-offset)
		start := cacheCount - offset - 1
		for i := 0; i < cacheTake; i++ {
			idx := start - i
			if idx < 0 {
				break
			}
			result = append(result, cachedLogs[idx].ToListItem())
		}
	}

	// 如果启用了日志保存，缓存不够时从数据库补充。
	// conn 可能为 nil（独立日志库已被 CloseLogDB 断开），此时只返回缓存结果。
	conn := db.GetLogDB()
	if enabled && conn != nil {
		remaining := pageSize - len(result)
		if remaining > 0 {
			dbOffset := 0
			if offset > cacheCount {
				dbOffset = offset - cacheCount
			}

			query := conn.WithContext(ctx).
				Select("id", "time", "request_model_name", "request_api_key_id", "request_api_key_name",
					"client_ip",
					"endpoint_type", "channel_id", "channel_name", "actual_model_name",
					"input_tokens", "output_tokens", "semantic_cache_hit", "cache_read_tokens",
					"reasoning_effort", "reasoning_tokens", "reasoning_chars",
					"ftut", "use_time",
					"cost", "error", "attempts", "total_attempts", "is_test")
			if filter.StartTime != nil {
				query = query.Where("time >= ?", *filter.StartTime)
			}
			if filter.EndTime != nil {
				query = query.Where("time <= ?", *filter.EndTime)
			}
			if len(excludedGroups) > 0 {
				names := make([]string, 0, len(excludedGroups))
				for name := range excludedGroups {
					names = append(names, name)
				}
				query = query.Where("request_model_name NOT IN ?", names)
			}
			if filter.Model != "" || len(modelsLower) > 0 {
				// Model（模糊）与 Models（精确，不区分大小写）为 OR 关系（issue #117）。
				// 用 LOWER() 比较，兼容 SQLite/MySQL/Postgres。
				// GORM 不会自动给 OR 组加括号，这里用 Raw 子句包裹以免与其它 WHERE
				// 条件（AND）发生优先级错误。
				var conds []string
				var args []any
				if filter.Model != "" {
					modelPattern := "%" + strings.ToLower(filter.Model) + "%"
					conds = append(conds, "LOWER(request_model_name) LIKE ?", "LOWER(actual_model_name) LIKE ?")
					args = append(args, modelPattern, modelPattern)
				}
				if len(modelsLower) > 0 {
					names := make([]string, 0, len(modelsLower))
					for n := range modelsLower {
						names = append(names, n)
					}
					conds = append(conds, "LOWER(request_model_name) IN ?", "LOWER(actual_model_name) IN ?")
					args = append(args, names, names)
				}
				clause := strings.Join(conds, " OR ")
				query = query.Where("("+clause+")", args...)
			}
			if filter.ChannelID != nil {
				if filter.IncludeAttempts {
					// 顶层渠道 或 该请求在目标渠道上有过任意尝试（成败由 HasError 单独判定）
					query = query.Where(
						"channel_id = ? OR EXISTS (SELECT 1 FROM relay_log_attempts WHERE relay_log_id = relay_logs.id AND channel_id = ?)",
						*filter.ChannelID, *filter.ChannelID,
					)
				} else {
					query = query.Where("channel_id = ?", *filter.ChannelID)
				}
			}
			if filter.APIKeyID != nil {
				query = query.Where("request_api_key_id = ?", *filter.APIKeyID)
			}
			if filter.EndpointType != "" {
				query = query.Where("endpoint_type = ?", filter.EndpointType)
			}
			if filter.HasError != nil {
				if *filter.HasError {
					if filter.IncludeAttempts {
						// 整体失败 或 含任意失败尝试
						query = query.Where("error != '' OR EXISTS (SELECT 1 FROM relay_log_attempts WHERE relay_log_id = relay_logs.id AND status = ?)", string(model.AttemptFailed))
					} else {
						query = query.Where("error != ''")
					}
				} else {
					if filter.IncludeAttempts {
						// 整体成功 且 不含任何失败尝试
						query = query.Where("(error = '' OR error IS NULL) AND NOT EXISTS (SELECT 1 FROM relay_log_attempts WHERE relay_log_id = relay_logs.id AND status = ?)", string(model.AttemptFailed))
					} else {
						query = query.Where("error = '' OR error IS NULL")
					}
				}
			}

			if filter.IsTest != nil {
				if *filter.IsTest {
					query = query.Where("is_test = true")
				} else {
					query = query.Where("is_test = false OR is_test IS NULL")
				}
			}

			var dbLogs []model.RelayLogListItem
			if err := query.Order("id DESC").Offset(dbOffset).Limit(remaining).Find(&dbLogs).Error; err != nil {
				return nil, err
			}
			// semantic_cache_hit / cache_read_tokens 已在写入时落库，直接返回，
			// 无需读取并重新解析 response_content 大字段。
			result = append(result, dbLogs...)
		}
	}

	return result, nil
}

// SetCacheForTest replaces the in-memory relay log cache for testing.
// Returns a cleanup function that restores the previous cache.
func SetCacheForTest(logs []model.RelayLog) func() {
	relayLogCacheLock.Lock()
	old := relayLogCache
	relayLogCache = make([]model.RelayLog, len(logs))
	copy(relayLogCache, logs)
	relayLogCacheLock.Unlock()
	return func() {
		relayLogCacheLock.Lock()
		relayLogCache = old
		relayLogCacheLock.Unlock()
	}
}

func RelayLogCacheReadTokens(responseContent string) int {
	usage, ok := cacheusage.ParseProviderPromptCacheUsageSignals(responseContent)
	if !ok || usage.CachedTokens <= 0 {
		return 0
	}
	return int(usage.CachedTokens)
}

func RelayLogClear(ctx context.Context) error {
	relayLogCacheLock.Lock()
	relayLogCache = make([]model.RelayLog, 0, relayLogMaxSize)
	relayLogCacheLock.Unlock()
	conn := db.GetLogDB()
	if conn == nil {
		// 独立日志库已断开（后台日志关闭中）：内存缓存已清，无需触碰数据库。
		return nil
	}
	// 整表清空走 FastClearTable，避免百万级逐行 DELETE 卡住数十分钟。
	// 同步清空 relay_log_attempts，避免残留孤儿行（其 relay_log_id 指向已删
	// 的 relay_logs）被 analytics 的 INNER JOIN 排除，造成统计莫名消失。
	if err := db.FastClearTable(conn.WithContext(ctx), &model.RelayLogAttempt{}, "relay_log_attempts"); err != nil {
		return err
	}
	return db.FastClearTable(conn.WithContext(ctx), &model.RelayLog{}, "relay_logs")
}

// RelayLogClearContents 清空所有历史日志的 request_content / response_content
// 大字段，保留全部元数据（token、cost、渠道、时间、attempts 等）。用于在关闭
// 「记录请求/响应内容」开关后立即释放存量磁盘占用，而不必等待 retention 逐行删除。
//
// 同时清空内存缓存中的大字段（在途未刷盘的日志），保证开关生效后缓存里也不残留。
// relay_log_attempts 不受影响——它不含大字段。
func RelayLogClearContents(ctx context.Context) error {
	// 清空内存缓存中在途日志的大字段。
	relayLogCacheLock.Lock()
	for i := range relayLogCache {
		relayLogCache[i].RequestContent = ""
		relayLogCache[i].ResponseContent = ""
	}
	relayLogCacheLock.Unlock()

	conn := db.GetLogDB()
	if conn == nil {
		// 独立日志库已断开：内存缓存已清，无需触碰数据库。
		return nil
	}
	// 单条 UPDATE 清空两列，跨方言通用。不删行，故 relay_log_attempts 无孤儿风险。
	return conn.WithContext(ctx).
		Model(&model.RelayLog{}).
		Where("1 = 1").
		Updates(map[string]any{
			"request_content":  "",
			"response_content": "",
		}).Error
}

// RelayLogGetByID 根据ID获取完整日志详情（包含 request_content 和 response_content）
func RelayLogGetByID(ctx context.Context, id int64) (*model.RelayLog, error) {
	// 在缓存中查找的闭包：日志库关闭或 DB 未命中时回落到内存缓存。
	lookupCache := func() (*model.RelayLog, error) {
		relayLogCacheLock.Lock()
		defer relayLogCacheLock.Unlock()
		for i := range relayLogCache {
			if relayLogCache[i].ID == id {
				cached := relayLogCache[i]
				if usage, ok := cacheusage.ParseProviderPromptCacheUsageSignals(cached.ResponseContent); ok {
					cached.SemanticCacheHit = usage.SemanticCacheHit
					if !usage.SemanticCacheHit {
						cached.CacheReadTokens = int(usage.CachedTokens)
					}
				}
				return &cached, nil
			}
		}
		return nil, nil
	}

	conn := db.GetLogDB()
	if conn == nil {
		// 日志库已断开（如关闭后台日志）：只查内存缓存。
		return lookupCache()
	}

	var relayLog model.RelayLog
	if err := conn.WithContext(ctx).Where("id = ?", id).First(&relayLog).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		return lookupCache()
	}
	if usage, ok := cacheusage.ParseProviderPromptCacheUsageSignals(relayLog.ResponseContent); ok {
		relayLog.SemanticCacheHit = usage.SemanticCacheHit
		if !usage.SemanticCacheHit {
			relayLog.CacheReadTokens = int(usage.CachedTokens)
		}
	}
	return &relayLog, nil
}
