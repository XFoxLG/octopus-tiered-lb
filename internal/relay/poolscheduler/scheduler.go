package poolscheduler

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/pool"
	"github.com/lingyuins/octopus/internal/op/setting"
	"gorm.io/gorm"
)

const ewmaAlpha = 0.3

var (
	ErrNoAvailableAccount = errors.New("no available account in pool")

	// globalPoolStats key: "poolID:accountID" -> *accountStats
	globalPoolStats sync.Map
	// globalPoolSlots key: "poolID:accountID" -> *int64 (atomic current concurrency)
	globalPoolSlots sync.Map
	// globalPoolSticky key: "poolID:sessionHash" -> *stickyEntry
	// sessionHash 含客户端请求携带的 model 名（基数不受控），缺少周期回收会导致
	// map 无界增长（见 issue #46 同类遗漏）。stickyEntry.LastActivity 用于 PurgeStaleSticky。
	globalPoolSticky sync.Map
	// globalRoundRobin key: poolID -> *uint64 (atomic counter)
	globalRoundRobin sync.Map

	// TriggerRefreshAsync 由 pooltokenrefresh 包在 init 时注入。
	// 选号遇到 token 过期的 OAuth 账号时异步触发刷新，不阻塞本次选号。
	// nil 表示刷新服务未启用（跳过触发）。
	TriggerRefreshAsync func(poolID, accountID int)

	// poolReportCh 是 ReportResult DB 写入的有界 worker pool。
	// 之前每个号池请求完成都 go func() 同步执行两次 pool.UpdateAccount（DB 写），
	// 无信号量/超时，高 QPS + 慢 DB 下 goroutine 会无限堆积（风暴）。改为固定 worker
	// + 带缓冲队列：入队非阻塞，队列满则丢弃当前 job（best-effort 计数，下次请求会
	// 再累积，且内存 EWMA 已在 ReportResult 同步更新，丢弃不影响正确性）。
	poolReportCh = make(chan poolReportJob, 1024)
	// poolReportWorkers 固定 worker 数，控制并发 DB 写 goroutine 上限。
	poolReportWorkers = 4
	// poolReportStartOnce 保证 worker pool 幂等启动（task.Init 可能被多次调用）。
	poolReportStartOnce sync.Once
	// poolReportDroppedCount 计数因队列满而丢弃的 ReportResult 上报次数（可观测）。
	poolReportDroppedCount atomic.Int64
)

// poolReportJob 是 ReportResult 异步 DB 写任务。
type poolReportJob struct {
	poolID       int
	accountID    int
	success      bool
	outputTokens int64
}

type accountStats struct {
	mu           sync.Mutex
	errorRate    float64
	ttftMs       float64
	lastActivity time.Time
}

// stickyEntry 粘性会话条目。LastActivity 用于 PurgeStaleSticky 按空闲时长回收，
// 与 balancer.SessionEntry 同模式（见 issue #46 内存暴涨防护）。
type stickyEntry struct {
	AccountID    int
	LastActivity time.Time
}

func statsKey(poolID, accountID int) string {
	return fmt.Sprintf("%d:%d", poolID, accountID)
}

func stickyKey(poolID int, sessionHash string) string {
	return fmt.Sprintf("%d:%s", poolID, sessionHash)
}

// SelectAccount 从指定池选择一个可用账号。
// sessionHash 非空时启用粘性；excludeIDs 排除已尝试过的账号。
// modelName 非空时按账号绑定的模型列表过滤（空 Models 表示不限）。
// 返回选中的账号（已 acquire 并发槽位），调用方完成后必须调用 ReleaseSlot。
func SelectAccount(poolID int, sessionHash string, excludeIDs []int, poolDefaultConcurrency int, modelName string) (*model.PoolAccount, error) {
	// L1: 粘性会话
	if sessionHash != "" {
		if acct, ok := trySticky(poolID, sessionHash, excludeIDs, poolDefaultConcurrency, modelName); ok {
			return acct, nil
		}
	}

	// L2: 获取可调度候选
	candidates, err := pool.ListSchedulableAccounts(poolID)
	if err != nil {
		return nil, err
	}
	candidates = filterExcluded(candidates, excludeIDs)
	candidates = filterByModel(candidates, modelName)
	// L2.5: 可选分层过滤（priority 阈值），管理员显式配置即遵守（不 fallback）。
	candidates = filterLayeredByPriority(candidates)
	if len(candidates) == 0 {
		// 候选为空时，尝试触发池内 token 过期的 OAuth 账号刷新（异步，不阻塞）。
		triggerRefreshForExpired(poolID, modelName)
		return nil, ErrNoAvailableAccount
	}

	// L3: 并发槽位过滤
	candidates = filterBySlot(candidates, poolID, poolDefaultConcurrency)
	if len(candidates) == 0 {
		return nil, ErrNoAvailableAccount
	}

	// L4: 评分排序 + 选择
	selected := selectByStrategy(candidates, poolID)

	// L5: acquire 槽位 + 绑定粘性
	acquireSlot(poolID, selected.ID)
	if sessionHash != "" {
		globalPoolSticky.Store(stickyKey(poolID, sessionHash), &stickyEntry{
			AccountID:    selected.ID,
			LastActivity: time.Now(),
		})
	}
	return &selected, nil
}

// ReportResult 上报请求结果，更新 EWMA 统计和 DB 累计计数。
func ReportResult(poolID, accountID int, success bool, ttftMs float64, outputTokens int64) {
	key := statsKey(poolID, accountID)
	val, _ := globalPoolStats.LoadOrStore(key, &accountStats{lastActivity: time.Now()})
	stats := val.(*accountStats)
	stats.mu.Lock()
	if success {
		stats.errorRate = (1-ewmaAlpha)*stats.errorRate + ewmaAlpha*0
		if ttftMs > 0 {
			if stats.ttftMs == 0 {
				stats.ttftMs = ttftMs
			} else {
				stats.ttftMs = (1-ewmaAlpha)*stats.ttftMs + ewmaAlpha*ttftMs
			}
		}
	} else {
		stats.errorRate = (1-ewmaAlpha)*stats.errorRate + ewmaAlpha*1
	}
	stats.lastActivity = time.Now()
	stats.mu.Unlock()

	// 成功请求后清零鉴权错误计数（等价 sub2api clear-error 于测试成功）。
	if success {
		ResetAuthError(poolID, accountID)
	}

	// 异步更新 DB 累计（best-effort，不阻塞请求路径）。经有界 worker pool 执行，
	// 入队非阻塞，队列满则丢弃当前 job（计数降级，下次请求会再累积）。
	job := poolReportJob{poolID: poolID, accountID: accountID, success: success, outputTokens: outputTokens}
	select {
	case poolReportCh <- job:
	default:
		poolReportDroppedCount.Add(1)
	}
}

// applyReportToDB 执行 DB 累计写入（由 worker pool 调用）。
func applyReportToDB(job poolReportJob) {
	updates := map[string]interface{}{
		"total_requests": gormExpr("total_requests + 1"),
	}
	if !job.success {
		updates["total_errors"] = gormExpr("total_errors + 1")
	}
	if job.outputTokens > 0 {
		updates["total_tokens"] = gormExpr("total_tokens + ?", job.outputTokens)
	}
	_ = pool.UpdateAccount(job.poolID, job.accountID, updates)
	if job.success {
		// 同步清零 auth_error_count 窗口数据库列，供恢复面板与统计查看。
		_ = pool.UpdateAccount(job.poolID, job.accountID, map[string]interface{}{
			"auth_error_count":        0,
			"auth_error_window_start": int64(0),
		})
	}
}

// StartReportWorkerPool 启动固定数量的 worker 消费 ReportResult 的 DB 写任务。
// 幂等：多次调用只启动一次 worker。ctx 取消后停止派发并丢弃残留 job。
// 应在 task.Init 时调用。
func StartReportWorkerPool(ctx context.Context) {
	poolReportStartOnce.Do(func() {
		for i := 0; i < poolReportWorkers; i++ {
			go func() {
				for {
					select {
					case job := <-poolReportCh:
						applyReportToDB(job)
					case <-ctx.Done():
						return
					}
				}
			}()
		}
	})
}

// DroppedReportCount 返回因队列满而被丢弃的 ReportResult 上报次数（可观测性）。
func DroppedReportCount() int64 {
	return poolReportDroppedCount.Load()
}

// SetRateLimitCooldown 设置 429 冷却。
func SetRateLimitCooldown(poolID, accountID int, until time.Time) {
	_ = pool.UpdateAccount(poolID, accountID, map[string]interface{}{
		"rate_limit_reset_at": until.Unix(),
	})
}

// SetOverload 设置过载冷却。
func SetOverload(poolID, accountID int, until time.Time) {
	_ = pool.UpdateAccount(poolID, accountID, map[string]interface{}{
		"overload_until": until.Unix(),
	})
}

// SetError 将账号标记为 error 状态。
func SetError(poolID, accountID int) {
	_ = pool.UpdateAccount(poolID, accountID, map[string]interface{}{
		"status": "error",
	})
}

// SetTempUnsched 设置临时不可调度（直到 until；reason 为 TempUnschedState JSON 或空字符串）。
// until 为零值表示清除。
func SetTempUnsched(poolID, accountID int, until time.Time, reason string) {
	updates := map[string]interface{}{
		"temp_unsched_until":  until.Unix(),
		"temp_unsched_reason": reason,
	}
	if until.IsZero() {
		updates["temp_unsched_until"] = int64(0)
		updates["temp_unsched_reason"] = ""
	}
	_ = pool.UpdateAccount(poolID, accountID, updates)
}

// ClearTempUnsched 手动清除临时不可调度（测试成功/管理员恢复时）。
func ClearTempUnsched(poolID, accountID int) {
	SetTempUnsched(poolID, accountID, time.Time{}, "")
}

// ReportAuthErrorCount 上报当前鉴权错误计数到 DB（供管理员查看当前窗口计数）。
// 同时刷新窗口起点 best-effort（本身不明示窗口起点，仅写入计数）。
func ReportAuthErrorCount(poolID, accountID int, count int) error {
	return pool.UpdateAccount(poolID, accountID, map[string]interface{}{
		"auth_error_count": count,
	})
}

// RecoverAccount 管理员手动恢复账号：清除错误状态与所有冷却/禁用标记。
func RecoverAccount(poolID, accountID int) error {
	return pool.UpdateAccount(poolID, accountID, map[string]interface{}{
		"status":                  "active",
		"error_message":           "",
		"temp_unsched_until":      int64(0),
		"temp_unsched_reason":     "",
		"rate_limit_reset_at":     int64(0),
		"overload_until":          int64(0),
		"auth_error_count":        0,
		"auth_error_window_start": int64(0),
	})
}

// ReleaseSlot 释放并发槽位。
func ReleaseSlot(poolID, accountID int) {
	key := statsKey(poolID, accountID)
	if val, ok := globalPoolSlots.Load(key); ok {
		atomic.AddInt64(val.(*int64), -1)
	}
}

// RemovePool 清理池相关的所有内存状态。
func RemovePool(poolID int) {
	globalPoolStats.Range(func(k, _ interface{}) bool {
		if s := k.(string); len(s) > 0 && parsePoolID(s) == poolID {
			globalPoolStats.Delete(k)
			globalPoolSlots.Delete(k)
		}
		return true
	})
	globalPoolSticky.Range(func(k, _ interface{}) bool {
		if s := k.(string); len(s) > 0 && parsePoolID(s) == poolID {
			globalPoolSticky.Delete(k)
		}
		return true
	})
	globalRoundRobin.Delete(poolID)
}

// RemoveAccount 清理单个账号的内存状态。
func RemoveAccount(poolID, accountID int) {
	key := statsKey(poolID, accountID)
	globalPoolStats.Delete(key)
	globalPoolSlots.Delete(key)
	RemoveAuthError(poolID, accountID)
	// 清理指向该账号的粘性条目。
	globalPoolSticky.Range(func(k, v interface{}) bool {
		if s := k.(string); len(s) > 0 && parsePoolID(s) == poolID {
			if entry, ok := v.(*stickyEntry); ok && entry.AccountID == accountID {
				globalPoolSticky.Delete(k)
			}
		}
		return true
	})
}

// PurgeStale 清理长时间无活动的内存统计（后台任务调用）。
func PurgeStale(idleThreshold time.Duration) {
	cutoff := time.Now().Add(-idleThreshold)
	globalPoolStats.Range(func(k, v interface{}) bool {
		stats := v.(*accountStats)
		stats.mu.Lock()
		idle := stats.lastActivity.Before(cutoff)
		stats.mu.Unlock()
		if idle {
			globalPoolStats.Delete(k)
			globalPoolSlots.Delete(k)
		}
		return true
	})
}

// PurgeStaleSticky 清理长时间无活动的粘性会话条目（后台任务调用）。globalPoolSticky
// 的 key 含客户端请求携带的 model 名（基数不受控），仅靠 RemovePool/RemoveAccount
// 和 trySticky 惰性删除无法回收一次性/随机 model 名的条目，会无界增长（见 issue #46
// 同类遗漏，balancer.PurgeIdleSessions 已修复，此处补齐）。返回删除的条目数。
func PurgeStaleSticky(idleThreshold time.Duration) int {
	if idleThreshold <= 0 {
		return 0
	}
	now := time.Now()
	removed := 0
	globalPoolSticky.Range(func(key, value any) bool {
		entry, ok := value.(*stickyEntry)
		if !ok {
			globalPoolSticky.Delete(key)
			removed++
			return true
		}
		if now.Sub(entry.LastActivity) >= idleThreshold {
			globalPoolSticky.Delete(key)
			removed++
		}
		return true
	})
	return removed
}

func trySticky(poolID int, sessionHash string, excludeIDs []int, poolDefaultConcurrency int, modelName string) (*model.PoolAccount, bool) {
	key := stickyKey(poolID, sessionHash)
	val, ok := globalPoolSticky.Load(key)
	if !ok {
		return nil, false
	}
	entry, ok := val.(*stickyEntry)
	if !ok {
		globalPoolSticky.Delete(key)
		return nil, false
	}
	accountID := entry.AccountID
	for _, id := range excludeIDs {
		if id == accountID {
			return nil, false
		}
	}
	acct, err := pool.GetAccount(poolID, accountID)
	if err != nil || !acct.IsSchedulable() {
		globalPoolSticky.Delete(key)
		return nil, false
	}
	if !model.ModelMatches(acct.Models, modelName) {
		return nil, false
	}
	limit := acct.EffectiveLoadFactor()
	if limit <= 0 {
		limit = acct.EffectiveConcurrency(poolDefaultConcurrency)
	}
	if !tryAcquireSlot(poolID, accountID, limit) {
		return nil, false
	}
	// 粘性命中，刷新 LastActivity（活跃会话续期，与 balancer.SetSticky 一致）。
	entry.LastActivity = time.Now()
	return acct, true
}

// filterByModel 按账号绑定的模型列表过滤候选。models 为空表示不限。
func filterByModel(candidates []model.PoolAccount, modelName string) []model.PoolAccount {
	if modelName == "" {
		return candidates
	}
	result := make([]model.PoolAccount, 0, len(candidates))
	for i := range candidates {
		if model.ModelMatches(candidates[i].Models, modelName) {
			result = append(result, candidates[i])
		}
	}
	return result
}

// triggerRefreshForExpired 扫描池内 token 过期的 OAuth 账号，异步触发刷新。
// 仅在候选为空时调用，避免每次请求都扫描。失败不影响调用方。
func triggerRefreshForExpired(poolID int, modelName string) {
	if TriggerRefreshAsync == nil {
		return
	}
	// ListAccounts 返回池内全部账号（不过滤可调度性），用于发现过期 OAuth 账号。
	accounts, err := pool.ListAccounts(poolID)
	if err != nil {
		return
	}
	now := time.Now()
	for i := range accounts {
		acct := &accounts[i]
		if acct.Type != model.PoolTypeOAuth {
			continue
		}
		if !acct.IsTokenExpired() {
			continue
		}
		// 仍在退避窗口内的账号跳过（避兔选号路径绕过剈新退避机制）。
		if !acct.IsRefreshAllowed(now) {
			continue
		}
		// 仅刷新与请求模型匹配的账号（避免刷新无关账号）。
		if !model.ModelMatches(acct.Models, modelName) {
			continue
		}
		go TriggerRefreshAsync(poolID, acct.ID)
	}
}

func filterExcluded(candidates []model.PoolAccount, excludeIDs []int) []model.PoolAccount {
	if len(excludeIDs) == 0 {
		return candidates
	}
	excludeSet := make(map[int]struct{}, len(excludeIDs))
	for _, id := range excludeIDs {
		excludeSet[id] = struct{}{}
	}
	result := make([]model.PoolAccount, 0, len(candidates))
	for i := range candidates {
		if _, excluded := excludeSet[candidates[i].ID]; !excluded {
			result = append(result, candidates[i])
		}
	}
	return result
}

// filterLayeredByPriority 可选分层过滤（设置启用时按 min_priority 过滤低优先级候选）。
// 结果为空不 fallback（管理员显式配置）。
func filterLayeredByPriority(candidates []model.PoolAccount) []model.PoolAccount {
	enabled, err := setting.GetBool(model.SettingKeyPoolLayeredFilterEnabled)
	if err != nil || !enabled {
		return candidates
	}
	minPriority, err := setting.GetInt(model.SettingKeyPoolMinPriority)
	if err != nil {
		return candidates
	}
	result := make([]model.PoolAccount, 0, len(candidates))
	for i := range candidates {
		if candidates[i].Priority >= minPriority {
			result = append(result, candidates[i])
		}
	}
	return result
}

func filterBySlot(candidates []model.PoolAccount, poolID, poolDefaultConcurrency int) []model.PoolAccount {
	result := make([]model.PoolAccount, 0, len(candidates))
	for i := range candidates {
		limit := candidates[i].EffectiveLoadFactor()
		if limit <= 0 {
			limit = candidates[i].EffectiveConcurrency(poolDefaultConcurrency)
		}
		key := statsKey(poolID, candidates[i].ID)
		val, _ := globalPoolSlots.LoadOrStore(key, new(int64))
		current := atomic.LoadInt64(val.(*int64))
		if current < int64(limit) {
			result = append(result, candidates[i])
		}
	}
	return result
}

func selectByStrategy(candidates []model.PoolAccount, poolID int) model.PoolAccount {
	// 获取池策略（best-effort，失败默认 ewma）。
	strategy := "ewma"
	if p, err := pool.GetPool(poolID); err == nil {
		strategy = p.Strategy
	}

	switch strategy {
	case "round_robin":
		val, _ := globalRoundRobin.LoadOrStore(poolID, new(uint64))
		idx := atomic.AddUint64(val.(*uint64), 1) - 1
		return candidates[idx%uint64(len(candidates))]
	case "random":
		return candidates[rand.IntN(len(candidates))]
	case "least_loaded":
		return selectByLeastLoaded(candidates, poolID)
	case "tiered_adaptive":
		return selectByTieredAdaptive(candidates, poolID)
	default: // "ewma"
		return selectByEWMA(candidates, poolID)
	}
}

// selectByLeastLoaded 按当前占用槽位 / EffectiveLoadFactor 最小者选择。
// 无槽位记录视为 0，并列取首个。
func selectByLeastLoaded(candidates []model.PoolAccount, poolID int) model.PoolAccount {
	bestIdx := 0
	bestLoad := math.MaxFloat64
	for i := range candidates {
		key := statsKey(poolID, candidates[i].ID)
		load := 0.0
		if val, ok := globalPoolSlots.Load(key); ok {
			load = float64(atomic.LoadInt64(val.(*int64)))
		}
		factor := candidates[i].EffectiveLoadFactor()
		if factor <= 0 {
			factor = 1
		}
		ratio := load / float64(factor)
		if ratio < bestLoad {
			bestLoad = ratio
			bestIdx = i
		}
	}
	return candidates[bestIdx]
}

// ewmaScoreOf 返回账号当前 EWMA 综合得分：错误率权重 0.7 + 归一化 TTFT 权重 0.3，
// 再叠加 weight/priority 减分 tiebreaker。分数越低越优。无统计视为 0。
func ewmaScoreOf(candidate model.PoolAccount, poolID int) float64 {
	key := statsKey(poolID, candidate.ID)
	score := 0.0
	if val, ok := globalPoolStats.Load(key); ok {
		stats := val.(*accountStats)
		stats.mu.Lock()
		score = stats.errorRate*0.7 + (stats.ttftMs/10000.0)*0.3
		stats.mu.Unlock()
	}
	// weight 先于 priority 作为 tiebreaker：权重越高得分越低（越容易选中）。
	score -= float64(candidate.Weight) * 0.001
	// priority 作为第二 tiebreaker：高优先级减分。
	score -= float64(candidate.Priority) * 0.001
	return score
}

func selectByEWMA(candidates []model.PoolAccount, poolID int) model.PoolAccount {
	bestIdx := 0
	bestScore := math.MaxFloat64
	for i := range candidates {
		score := ewmaScoreOf(candidates[i], poolID)
		if score < bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	return candidates[bestIdx]
}

// tieredHealthyErrorRate 划分健康层/观察层的错误率阈值（EWMA，α=0.3）。
// 含义：1 次失败把全新账号抬到 0.3（沉入观察层），连续 2 次成功恢复健康层（0.21→0.147）。
const tieredHealthyErrorRate = 0.15

// selectByTieredAdaptive 运行时自适应分层调度：
// 先按 EWMA 错误率把候选分为健康层/观察层，健康层内用 EWMA 打分选择；
// 健康层为空时回退全量 EWMA（保证永远有返回）。冷却/过载账号已在候选过滤阶段排除，
// 分层解决的是"静态熔断未触发但持续轻微失败"的账号——动态降级、EWMA 恢复后自动回升。
func selectByTieredAdaptive(candidates []model.PoolAccount, poolID int) model.PoolAccount {
	healthy := make([]model.PoolAccount, 0, len(candidates))
	for i := range candidates {
		key := statsKey(poolID, candidates[i].ID)
		if val, ok := globalPoolStats.Load(key); ok {
			stats := val.(*accountStats)
			stats.mu.Lock()
			isHealthy := stats.errorRate <= tieredHealthyErrorRate
			stats.mu.Unlock()
			if isHealthy {
				healthy = append(healthy, candidates[i])
			}
			continue
		}
		// 无统计视为健康（新账号冷启动可被选中，与 ewma 策略 0 分语义一致）。
		healthy = append(healthy, candidates[i])
	}
	if len(healthy) == 0 {
		return selectByEWMA(candidates, poolID)
	}
	return selectByEWMA(healthy, poolID)
}

func acquireSlot(poolID, accountID int) {
	key := statsKey(poolID, accountID)
	val, _ := globalPoolSlots.LoadOrStore(key, new(int64))
	atomic.AddInt64(val.(*int64), 1)
}

func tryAcquireSlot(poolID, accountID int, limit int) bool {
	key := statsKey(poolID, accountID)
	val, _ := globalPoolSlots.LoadOrStore(key, new(int64))
	ptr := val.(*int64)
	for {
		current := atomic.LoadInt64(ptr)
		if current >= int64(limit) {
			return false
		}
		if atomic.CompareAndSwapInt64(ptr, current, current+1) {
			return true
		}
	}
}

func parsePoolID(key string) int {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			id := 0
			for j := 0; j < i; j++ {
				id = id*10 + int(key[j]-'0')
			}
			return id
		}
	}
	return -1
}

func gormExpr(expr string, args ...interface{}) interface{} {
	return gorm.Expr(expr, args...)
}
