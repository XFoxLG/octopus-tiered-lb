package store

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/utils/ratelimit"
)

// memory.go 包含所有存储接口的进程内内存实现。
//
// 这些实现包装现有子系统（failureHintCache / globalKeyCooldown / TokenBucket /
// stats 缓存 / balancer sync.Map / chCache 延迟）的逻辑，保持未启用 Redis 时
// 行为完全一致。Redis 启用时由 redis_*.go 替换，本文件始终编译但可能不被使用。

// ---------------------------------------------------------------------------
// memoryKV — KVStore
// ---------------------------------------------------------------------------

type memoryKVEntry struct {
	val       []byte
	expiresAt time.Time // zero 表示永久
}

type memoryKV struct {
	mu      sync.Mutex
	entries map[string]memoryKVEntry
}

func (m *memoryKV) Set(_ context.Context, key string, val []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = make(map[string]memoryKVEntry)
	}
	e := memoryKVEntry{val: val}
	if ttl > 0 {
		e.expiresAt = time.Now().Add(ttl)
	}
	m.entries[key] = e
	return nil
}

func (m *memoryKV) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		return nil, false, nil
	}
	e, ok := m.entries[key]
	if !ok {
		return nil, false, nil
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		delete(m.entries, key)
		return nil, false, nil
	}
	return e.val, true, nil
}

func (m *memoryKV) Del(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.entries, k)
	}
	return nil
}

func (m *memoryKV) DelByPrefix(_ context.Context, prefix string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.entries {
		if strings.HasPrefix(k, prefix) {
			delete(m.entries, k)
		}
	}
	return nil
}

// Incr 原子递增计数器（锁内读-改-写）。ttl > 0 时同步刷新过期时间，
// 与 Redis 实现语义一致：占用停止后 ttl 内自愈。计数器值以十进制字符串存储，
// 与 Redis 原生计数器的 GET 表现一致。
func (m *memoryKV) Incr(_ context.Context, key string, ttl time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = make(map[string]memoryKVEntry)
	}
	var count int64
	if e, ok := m.entries[key]; ok && !expiredLocked(e) {
		parsed, err := strconv.ParseInt(string(e.val), 10, 64)
		if err != nil {
			return 0, err
		}
		count = parsed
	}
	count++
	e := memoryKVEntry{val: []byte(strconv.FormatInt(count, 10))}
	if ttl > 0 {
		e.expiresAt = time.Now().Add(ttl)
	}
	m.entries[key] = e
	return count, nil
}

// Decr 原子递减计数器。key 不存在或已过期时从 0 起算（结果为负值，与 Redis 一致）。
func (m *memoryKV) Decr(_ context.Context, key string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = make(map[string]memoryKVEntry)
	}
	var count int64
	if e, ok := m.entries[key]; ok && !expiredLocked(e) {
		parsed, err := strconv.ParseInt(string(e.val), 10, 64)
		if err != nil {
			return 0, err
		}
		count = parsed
	}
	count--
	m.entries[key] = memoryKVEntry{val: []byte(strconv.FormatInt(count, 10))}
	return count, nil
}

func expiredLocked(e memoryKVEntry) bool {
	return !e.expiresAt.IsZero() && time.Now().After(e.expiresAt)
}

// DelBySubstring 删除所有 key 中包含 sub 子串的条目，仅扫描以 namespace 开头的 key
// （限制扫描范围，避免遍历全表）。
func (m *memoryKV) DelBySubstring(_ context.Context, namespace, sub string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.entries {
		if namespace != "" && !strings.HasPrefix(k, namespace) {
			continue
		}
		if strings.Contains(k, sub) {
			delete(m.entries, k)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// memoryRateLimit — RateLimitStore（包装 internal/utils/ratelimit.TokenBucket）
// ---------------------------------------------------------------------------

type memoryRateLimit struct {
	mu         sync.Mutex
	reqBuckets map[string]*ratelimit.TokenBucket // "apiKeyID:modelName" -> bucket
	tokBuckets map[string]*ratelimit.TokenBucket
}

func (m *memoryRateLimit) getOrCreate(buckets map[string]*ratelimit.TokenBucket, key string, rate, burst int) *ratelimit.TokenBucket {
	if b, ok := buckets[key]; ok {
		return b
	}
	b := ratelimit.NewTokenBucket(rate, burst)
	buckets[key] = b
	return b
}

func (m *memoryRateLimit) ensureMaps() {
	if m.reqBuckets == nil {
		m.reqBuckets = make(map[string]*ratelimit.TokenBucket)
	}
	if m.tokBuckets == nil {
		m.tokBuckets = make(map[string]*ratelimit.TokenBucket)
	}
}

func (m *memoryRateLimit) CheckAndConsume(_ context.Context, key string, rate, burst, n int) (bool, int, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMaps()
	b := m.getOrCreate(m.reqBuckets, key, rate, burst)
	if !b.Allow() {
		return false, 0, b.ResetAt(), nil
	}
	return true, b.TokensRemaining(), time.Time{}, nil
}

func (m *memoryRateLimit) ConsumeTokens(_ context.Context, key string, rate, burst, n int) error {
	if n <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMaps()
	b := m.getOrCreate(m.tokBuckets, key, rate, burst)
	b.AllowN(n)
	return nil
}

func (m *memoryRateLimit) RemoveByAPIKey(_ context.Context, apiKeyID int) error {
	if apiKeyID <= 0 {
		return nil
	}
	prefix := apiKeyPrefix(apiKeyID)
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.reqBuckets {
		if strings.HasPrefix(k, prefix) {
			delete(m.reqBuckets, k)
		}
	}
	for k := range m.tokBuckets {
		if strings.HasPrefix(k, prefix) {
			delete(m.tokBuckets, k)
		}
	}
	return nil
}

// apiKeyPrefix 返回限流 key 中代表某 apiKeyID 的前缀 "apiKeyID:"。
func apiKeyPrefix(apiKeyID int) string {
	return itoa(apiKeyID) + ":"
}

// ---------------------------------------------------------------------------
// memoryStats — StatsStore
// 现状 stats 包已维护内存累积（totalCache/dailyCache/...），此处实现仅作为
// 未启用 Redis 时的占位/直通：IncrMetrics/GetMetrics/SnapshotAll 在内存模式下
// 不被调用（stats 包直接操作自己的全局缓存）。Delete 同理。
// ---------------------------------------------------------------------------

type memoryStats struct{}

func (memoryStats) IncrMetrics(context.Context, string, string, model.StatsMetrics) error {
	return nil // 内存模式由 stats 包自行累积
}
func (memoryStats) GetMetrics(context.Context, string, string) (model.StatsMetrics, error) {
	return model.StatsMetrics{}, nil
}
func (memoryStats) SnapshotAll(context.Context, string) (map[string]model.StatsMetrics, error) {
	return nil, nil
}
func (memoryStats) Delete(context.Context, string, string) error { return nil }

// ---------------------------------------------------------------------------
// memoryRuntimeState — RuntimeStateStore
// 同 memoryStats：内存模式由 balancer/persistence.go 直接操作 sync.Map + DB，
// 此处为占位。
// ---------------------------------------------------------------------------

type memoryRuntimeState struct{}

func (memoryRuntimeState) SaveCircuit(context.Context, string, model.CircuitBreakerState) error {
	return nil
}
func (memoryRuntimeState) SaveAuto(context.Context, string, model.AutoStrategyState) error {
	return nil
}
func (memoryRuntimeState) LoadCircuit(context.Context) ([]model.CircuitBreakerState, error) {
	return nil, nil
}
func (memoryRuntimeState) LoadAuto(context.Context) ([]model.AutoStrategyState, error) {
	return nil, nil
}
func (memoryRuntimeState) DeleteStale(context.Context, string, int64) error { return nil }

// ---------------------------------------------------------------------------
// memoryChannelDelay — ChannelDelayStore
// 现状延迟探测结果直接写入 chCache（Channel.BaseUrls[].Delay），重启丢失。
// 内存实现为 no-op（保持现状）；Redis 实现持久化。
// ---------------------------------------------------------------------------

type memoryChannelDelay struct{}

func (memoryChannelDelay) SetDelay(context.Context, int, string, int) error { return nil }
func (memoryChannelDelay) GetDelays(context.Context, int) (map[string]int, error) {
	return nil, nil
}
func (memoryChannelDelay) DeleteChannel(context.Context, int) error { return nil }
