package store

import (
	"context"
	"fmt"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/redis/go-redis/v9"
)

// 这是存储后端抽象层（issue #123）。
//
// 设计原则：
//   - internal/utils/cache.Cache[K,V] 是进程内分片 map（返回 map[K]V，无 ctx/TTL/
//     序列化），承载 channel/group/apikey 实体镜像缓存，本层不改造它。
//   - 每个子系统定义一个窄接口，memory 实现保持现有行为，redis 实现新增。
//   - cmd/start.go 根据 conf.AppConfig.Cache.Type 选择后端：未配置 Redis 时
//     defaultXxx 指向 memory 实现（零破坏），配置后切换到 redis 实现。
//   - Redis key 统一前缀 octopus:{subsystem}:{key}，复用现有 "a:b:c" 冒号格式。
//
// 降级策略：Redis 宕机时各实现返回 err，调用方降级到内存行为（KV miss=不冷却/
// 不提示；stats 退化为仅内存累积）。具体降级点见各子系统注释。

// KVStore 是带 TTL 的键值存储，承载失败提示缓存 / key 冷却 / 分组探测进度。
// 这些子系统都是 string key -> 小结构体 + TTL，天然适合 Redis SET ... EX。
type KVStore interface {
	// Set 写入 key（带 TTL）。ttl <= 0 表示永久（本层调用方均为有限 TTL）。
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	// Get 读取 key。found=false 表示 key 不存在或已过期。
	Get(ctx context.Context, key string) (val []byte, found bool, err error)
	// Del 删除指定 key。
	Del(ctx context.Context, keys ...string) error
	// DelByPrefix 删除所有以 prefix 开头的 key（渠道/key 删除时用）。
	DelByPrefix(ctx context.Context, prefix string) error
	// DelBySubstring 删除命名空间 namespace 下所有 key 中包含 substr 的 key。
	// 用于 key 冷却的 ":keyID:" 子串匹配（无法用前缀表达）。namespace 不含统一前缀
	// （如 "cooldown:"），实现负责加前缀并 SCAN 全命名空间。
	DelBySubstring(ctx context.Context, namespace, substr string) error
}

// RateLimitStore 承载 RPM/TPM 限流的 token bucket 语义。
// Redis 实现用单个 Lua 脚本原子执行「refill + 比较 + 扣减 + 返回剩余/重置时间」，
// 避免 GET/SET 竞态。每 (apiKeyID, modelName) 一个 Redis key。
type RateLimitStore interface {
	// CheckAndConsume 检查并消费 n 个 token。返回是否允许、剩余 token、
	// 桶清空重填时间（用于 Retry-After）。rate/burst 单位：每分钟。
	CheckAndConsume(ctx context.Context, key string, rate, burst, n int) (allowed bool, remaining int, resetAt time.Time, err error)
	// ConsumeTokens 扣减 token（请求成功后按实际 token 数回扣 TPM）。
	ConsumeTokens(ctx context.Context, key string, rate, burst, n int) error
	// RemoveByAPIKey 删除指定 API key 的所有限流 bucket（跨模型）。
	RemoveByAPIKey(ctx context.Context, apiKeyID int) error
}

// StatsStore 承载统计指标的增量累加。
//
// 语义变更（issue #123）：从「内存累积 + 定时全行覆盖写 DB（snapshot/last-write-wins）」
// 改为「实时增量写 Redis + 定时快照 DB」。计数器字段用 HINCRBY/HINCRBYFLOAT，
// 百分位/max 字段（LatencyP50/95/99、Ftut*）用 Lua 原子 max 脚本。
// 语义与增量 upsert 一致（col + EXCLUDED.col），崩溃不丢增量。scope/id 映射见
// stats 包调用点。
type StatsStore interface {
	// IncrMetrics 将 delta 增量累加到 scope:id 维度。
	IncrMetrics(ctx context.Context, scope, id string, m model.StatsMetrics) error
	// GetMetrics 读取 scope:id 维度的当前累积值。
	GetMetrics(ctx context.Context, scope, id string) (model.StatsMetrics, error)
	// SnapshotAll 读取 scope 下所有维度的累积值，供 SaveDB 落盘。
	SnapshotAll(ctx context.Context, scope string) (map[string]model.StatsMetrics, error)
	// Delete 删除 scope:id 维度（渠道/apikey 删除时调用）。
	Delete(ctx context.Context, scope, id string) error
}

// RuntimeStateStore 承载 circuit breaker / auto strategy 运行时状态。
// Redis 实现以 JSON 序列化存储，TTL 防 stale（breaker key TTL = max cooldown × 2）。
type RuntimeStateStore interface {
	SaveCircuit(ctx context.Context, key string, state model.CircuitBreakerState) error
	SaveAuto(ctx context.Context, key string, state model.AutoStrategyState) error
	LoadCircuit(ctx context.Context) ([]model.CircuitBreakerState, error)
	LoadAuto(ctx context.Context) ([]model.AutoStrategyState, error)
	// DeleteStale 删除 UpdatedAt 早于 beforeGeneration 的条目（generation-replace 语义）。
	DeleteStale(ctx context.Context, kind string, beforeGeneration int64) error
}

// ChannelDelayStore 承载频道 base URL 延迟探测结果。
// 现状探测结果仅存内存 chCache，重启丢失；Redis 实现持久化探测结果。
type ChannelDelayStore interface {
	// SetDelay 记录某 channel 某 url 的延迟（毫秒）。
	SetDelay(ctx context.Context, channelID int, url string, delay int) error
	// GetDelays 读取某 channel 所有 url 的延迟。
	GetDelays(ctx context.Context, channelID int) (map[string]int, error)
	// DeleteChannel 删除某 channel 的所有延迟记录。
	DeleteChannel(ctx context.Context, channelID int) error
}

// 后端实例注入点。Init() 在 Redis 启用时替换为 redis 实现；默认 memory。
var (
	defaultKV           KVStore           = &memoryKV{}
	defaultRateLimit    RateLimitStore    = &memoryRateLimit{}
	defaultStats        StatsStore        = &memoryStats{}
	defaultRuntimeState RuntimeStateStore = &memoryRuntimeState{}
	defaultChannelDelay ChannelDelayStore = &memoryChannelDelay{}
)

// GetKV 返回当前 KVStore 后端。
func GetKV() KVStore {
	mu.RLock()
	defer mu.RUnlock()
	return defaultKV
}

// GetRateLimit 返回当前 RateLimitStore 后端。
func GetRateLimit() RateLimitStore {
	mu.RLock()
	defer mu.RUnlock()
	return defaultRateLimit
}

// GetStats 返回当前 StatsStore 后端。
func GetStats() StatsStore {
	mu.RLock()
	defer mu.RUnlock()
	return defaultStats
}

// GetRuntimeState 返回当前 RuntimeStateStore 后端。
func GetRuntimeState() RuntimeStateStore {
	mu.RLock()
	defer mu.RUnlock()
	return defaultRuntimeState
}

// GetChannelDelay 返回当前 ChannelDelayStore 后端。
func GetChannelDelay() ChannelDelayStore {
	mu.RLock()
	defer mu.RUnlock()
	return defaultChannelDelay
}

// ErrBackendDisabled 表示 Redis 后端未启用时调用 Redis 实现。
// 正常注入流程下不会触发（Init 成功后才切换到 redis 实现），
// 仅作为防御性返回值。
var ErrBackendDisabled = fmt.Errorf("store: redis backend disabled")

// switchToRedis 在锁保护下原子地切换 client + enabled + 所有 defaultXxx 到
// Redis 实现。供 Init 成功和后台重连成功两条路径共用。
//
// 注意：调用方负责保证传入的 client 已通过 Ping 验证。切换是「单向」的--
// 从内存切到 Redis；运行中不反向回退（Redis 运行中宕机时各实现返回 err，
// 调用方自行降级，见 store.go 顶部降级策略注释）。
func switchToRedis(c *redis.Client) {
	mu.Lock()
	client = c
	enabled = true
	defaultKV = newRedisKV(c)
	defaultRateLimit = newRedisRateLimit(c)
	defaultStats = newRedisStats(c)
	defaultRuntimeState = newRedisRuntimeState(c)
	defaultChannelDelay = newRedisChannelDelay(c)
	mu.Unlock()
}

// InjectForTest 为测试注入一个已连接的 Redis client 并切换所有后端到 Redis
// 实现，返回恢复函数（切回内存实现）。仅供跨包子测调用：子系统测试启动
// miniredis 后调用本函数启用 Redis 路径，defer 调用返回值恢复。
// 绕过 Init 的 ping 验证，直接设置 client + enabled + 各 redis 实现。
func InjectForTest(c *redis.Client) (restore func()) {
	mu.Lock()
	origClient := client
	origEnabled := enabled
	origKV := defaultKV
	origRL := defaultRateLimit
	origStats := defaultStats
	origRT := defaultRuntimeState
	origCD := defaultChannelDelay
	client = c
	enabled = true
	defaultKV = newRedisKV(c)
	defaultRateLimit = newRedisRateLimit(c)
	defaultStats = newRedisStats(c)
	defaultRuntimeState = newRedisRuntimeState(c)
	defaultChannelDelay = newRedisChannelDelay(c)
	mu.Unlock()
	return func() {
		mu.Lock()
		client = origClient
		enabled = origEnabled
		defaultKV = origKV
		defaultRateLimit = origRL
		defaultStats = origStats
		defaultRuntimeState = origRT
		defaultChannelDelay = origCD
		mu.Unlock()
	}
}

// ResetForTest 将所有后端重置为内存实现并清空 client，供测试 cleanup 使用。
// 与 InjectForTest 配合：InjectForTest 返回的 restore 也能恢复，但某些测试
// 在 t.Cleanup 中需要无参调用，故提供此便捷函数。
func ResetForTest() {
	mu.Lock()
	client = nil
	enabled = false
	defaultKV = &memoryKV{}
	defaultRateLimit = &memoryRateLimit{}
	defaultStats = &memoryStats{}
	defaultRuntimeState = &memoryRuntimeState{}
	defaultChannelDelay = &memoryChannelDelay{}
	mu.Unlock()
}
