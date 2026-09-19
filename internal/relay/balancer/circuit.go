package balancer

import (
	"context"
	"math/bits"
	"strings"
	"sync"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	ch "github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/utils/log"
)

// CircuitState 熔断器状态
type CircuitState int

const (
	StateClosed   CircuitState = iota // 正常通行
	StateOpen                         // 熔断中，拒绝所有请求
	StateHalfOpen                     // 半开，仅允许单个试探请求
)

// circuitEntry 单个熔断器条目
type circuitEntry struct {
	State               CircuitState
	ConsecutiveFailures int64
	LastFailureTime     time.Time
	TripCount           int // 累计熔断触发次数（用于指数退避）
	// HalfOpenSince 记录进入 HalfOpen 状态的时刻，用于探测超时判定。
	// 半开态本应允许单个试探请求并在其完成后转 Closed/Open，但若试探请求被
	// 中途放弃（客户端断连、响应关键词拦截等 attempt 未记录结果），状态会
	// 卡死在 HalfOpen，IsTripped 永远返回 true → 渠道被永久跳过（issue #162）。
	// 通过 HalfOpenSince + 探测超时，让丢失的试探可在超时后被新的试探接管。
	HalfOpenSince time.Time
	mu            sync.Mutex
}

// 全局熔断器存储
var globalBreaker sync.Map // key: string -> value: *circuitEntry

// circuitKey 生成熔断器键：channelID:channelKeyID:modelName
func circuitKey(channelID, keyID int, modelName string) string {
	return buildKey3(channelID, keyID, modelName)
}

// getOrCreateEntry 获取或创建熔断器条目
func getOrCreateEntry(key string) *circuitEntry {
	if v, ok := globalBreaker.Load(key); ok {
		if entry, ok := v.(*circuitEntry); ok {
			return entry
		}
	}
	entry := &circuitEntry{State: StateClosed}
	actual, _ := globalBreaker.LoadOrStore(key, entry)
	if e, ok := actual.(*circuitEntry); ok {
		return e
	}
	return entry
}

// getThreshold 获取熔断阈值配置
func getThreshold() int64 {
	v, err := setting.GetInt(model.SettingKeyCircuitBreakerThreshold)
	if err != nil || v <= 0 {
		return 5
	}
	return int64(v)
}

// cooldownFromConfig 计算带指数退避的熔断冷却时间（纯函数）。
// 渠道级覆盖与全局设置共用同一套退避数学：cooldown = base * 2^(tripCount-1)，
// 上限 maxCooldown，位移/溢出双保护。
func cooldownFromConfig(baseCooldownSec, maxCooldownSec, tripCount int) time.Duration {
	if maxCooldownSec <= 0 {
		maxCooldownSec = 600
	}

	// 指数退避：baseCooldown * 2^(tripCount-1)
	cooldown := baseCooldownSec
	if tripCount > 1 {
		shift := tripCount - 1
		if shift > 20 { // 防止过大的位移
			shift = 20
		}
		// 防止 base << shift 溢出 int：若 base 的二进制位数加上 shift
		// 超过 int 的位宽，左移会溢出产生负值，直接使用最大冷却时间。
		if baseCooldownSec > 0 && shift >= bits.Len(uint(baseCooldownSec)) {
			cooldown = maxCooldownSec
		} else {
			cooldown = baseCooldownSec << shift
		}
	}
	if cooldown > maxCooldownSec {
		cooldown = maxCooldownSec
	}

	return time.Duration(cooldown) * time.Second
}

// GetCooldown 获取全局配置下的冷却时间（带指数退避）。
func GetCooldown(tripCount int) time.Duration {
	base, err := setting.GetInt(model.SettingKeyCircuitBreakerCooldown)
	if err != nil || base <= 0 {
		base = 60
	}
	maxCooldown, err := setting.GetInt(model.SettingKeyCircuitBreakerMaxCooldown)
	if err != nil || maxCooldown <= 0 {
		maxCooldown = 600
	}
	return cooldownFromConfig(base, maxCooldown, tripCount)
}

// channelCircuitCooldown 渠道级熔断冷却（阶段3）：渠道 CircuitBreakerCooldown /
// MaxCooldown 覆盖值 >0 时优先，否则逐项跟随全局。通过渠道缓存读取（缓存优先，
// 未命中才落库），不做每请求直查数据库。
func channelCircuitCooldown(channelID, tripCount int) time.Duration {
	base, maxCooldown := 0, 0
	if channel, err := ch.Get(channelID, context.Background()); err == nil && channel != nil {
		base = channel.CircuitBreakerCooldown
		maxCooldown = channel.CircuitBreakerMaxCooldown
	}
	if base <= 0 {
		v, err := setting.GetInt(model.SettingKeyCircuitBreakerCooldown)
		if err != nil || v <= 0 {
			v = 60
		}
		base = v
	}
	if maxCooldown <= 0 {
		v, err := setting.GetInt(model.SettingKeyCircuitBreakerMaxCooldown)
		if err != nil || v <= 0 {
			v = 600
		}
		maxCooldown = v
	}
	return cooldownFromConfig(base, maxCooldown, tripCount)
}

// channelCircuitThreshold 渠道级熔断阈值（阶段3）：渠道覆盖 >0 时优先，
// 否则跟随全局。
func channelCircuitThreshold(channelID int) int64 {
	if channel, err := ch.Get(channelID, context.Background()); err == nil && channel != nil && channel.CircuitBreakerThreshold > 0 {
		return int64(channel.CircuitBreakerThreshold)
	}
	return getThreshold()
}

// getHalfOpenProbeTimeout 返回 HalfOpen 探测超时（秒）。
// 半开态只允许单个试探请求；若试探被中途放弃（客户端断连、响应关键词拦截等
// 未记录结果的路径），状态会卡死在 HalfOpen 永远跳过该渠道（issue #162）。
// 超过此超时后，下一次 IsTripped 会视为"试探已丢失"并允许发起一次新的试探，
// 同时刷新 HalfOpenSince 重置超时窗口。0 表示禁用超时（保持旧行为，不推荐）。
//
// 通过 halfOpenProbeTimeoutFunc 函数变量间接调用，便于测试注入短超时，
// 不必依赖 setting 缓存。
var halfOpenProbeTimeoutFunc = getHalfOpenProbeTimeoutConfig

func getHalfOpenProbeTimeoutConfig() time.Duration {
	v, err := setting.GetInt(model.SettingKeyCircuitBreakerHalfOpenProbeTimeout)
	if err != nil || v < 0 {
		return 60 * time.Second
	}
	return time.Duration(v) * time.Second
}

// IsTripped 检查通道是否处于熔断状态
// 返回 tripped=true 表示该通道应被跳过，remaining 为剩余冷却时间
func IsTripped(channelID, keyID int, modelName string) (tripped bool, remaining time.Duration) {
	key := circuitKey(channelID, keyID, modelName)
	v, ok := globalBreaker.Load(key)
	if !ok {
		return false, 0 // 无记录，视为 Closed
	}
	entry, ok := v.(*circuitEntry)
	if !ok {
		return false, 0
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	switch entry.State {
	case StateClosed:
		return false, 0

	case StateOpen:
		cooldown := channelCircuitCooldown(channelID, entry.TripCount)
		elapsed := time.Since(entry.LastFailureTime)
		if elapsed >= cooldown {
			entry.State = StateHalfOpen
			entry.HalfOpenSince = time.Now()
			log.Infof("circuit breaker [%s] Open -> HalfOpen (cooldown %v elapsed)", key, cooldown)
			return false, 0
		}
		// 仍在冷却中
		return true, cooldown - elapsed

	case StateHalfOpen:
		// 已有试探请求在进行中，拒绝其他请求。
		// 但若试探请求被中途放弃（客户端断连、响应关键词拦截等未记录结果路径），
		// 状态会卡死在 HalfOpen 永远跳过该渠道（issue #162）。超过探测超时后，
		// 视为"试探已丢失"，允许一次新的试探并刷新超时窗口，避免永久跳过。
		probeTimeout := halfOpenProbeTimeoutFunc()
		if probeTimeout > 0 && time.Since(entry.HalfOpenSince) >= probeTimeout {
			entry.HalfOpenSince = time.Now()
			log.Infof("circuit breaker [%s] HalfOpen probe timed out, allowing a new probe", key)
			return false, 0
		}
		return true, 0

	default:
		return false, 0
	}
}

// isKeyTrippedReadOnly 只读检查单个 key 是否处于熔断状态，不触发 Open->HalfOpen 状态转换。
// 与 IsTripped 的区别：IsTripped 在 Open 冷却到期时会转为 HalfOpen（有副作用），本函数仅做判定。
func isKeyTrippedReadOnly(channelID, keyID int, modelName string) bool {
	key := circuitKey(channelID, keyID, modelName)
	v, ok := globalBreaker.Load(key)
	if !ok {
		return false
	}
	entry, ok := v.(*circuitEntry)
	if !ok {
		return false
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	switch entry.State {
	case StateOpen:
		// 只读：冷却到期也不转 HalfOpen，仅判定当前是否仍应跳过。
		// 冷却已到期的 Open 视为"即将可探测"，不计为 tripped，避免误降权。
		cooldown := channelCircuitCooldown(channelID, entry.TripCount)
		return time.Since(entry.LastFailureTime) < cooldown
	case StateHalfOpen:
		// 与 IsTripped 的探测超时语义一致：超过探测超时的 HalfOpen 视为"试探已丢失"，
		// 不计为 tripped，避免 Auto 策略把卡死的 HalfOpen 渠道误判为全熔断并永久降权，
		// 进而因被降权跳过而无法发起新的试探（issue #162）。
		probeTimeout := halfOpenProbeTimeoutFunc()
		if probeTimeout > 0 && time.Since(entry.HalfOpenSince) >= probeTimeout {
			return false
		}
		return true
	default:
		return false
	}
}

// IsChannelAllKeysTripped 只读检查：channel+model 下所有启用的 key 是否都处于熔断状态。
// 不会触发 Open->HalfOpen 状态转换（与 IsTripped 不同），仅供 Auto 策略评分降权使用。
// channel 不存在、无 key、无启用 key 时返回 false（视为健康，不降权）。
func IsChannelAllKeysTripped(channelID int, modelName string) bool {
	channel, err := ch.Get(channelID, context.Background())
	if err != nil || channel == nil {
		return false
	}
	hasEnabledKey := false
	for _, key := range channel.Keys {
		if !key.Enabled {
			continue
		}
		hasEnabledKey = true
		if !isKeyTrippedReadOnly(channelID, key.ID, modelName) {
			return false // 有任一健康 key，未全熔断
		}
	}
	return hasEnabledKey
}

// RecordSuccess 记录成功，重置熔断器状态
func RecordSuccess(channelID, keyID int, modelName string) {
	key := circuitKey(channelID, keyID, modelName)
	v, ok := globalBreaker.Load(key)
	if !ok {
		return
	}
	entry, ok := v.(*circuitEntry)
	if !ok {
		return
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.State == StateHalfOpen {
		log.Infof("circuit breaker [%s] HalfOpen -> Closed (probe succeeded)", key)
	}

	// 重置全部状态
	entry.State = StateClosed
	entry.ConsecutiveFailures = 0
	entry.TripCount = 0
	entry.HalfOpenSince = time.Time{}
}

// RecordFailure 记录失败，可能触发熔断
func RecordFailure(channelID, keyID int, modelName string) {
	key := circuitKey(channelID, keyID, modelName)
	entry := getOrCreateEntry(key)

	entry.mu.Lock()
	defer entry.mu.Unlock()

	switch entry.State {
	case StateClosed:
		entry.LastFailureTime = time.Now()
		entry.ConsecutiveFailures++
		threshold := channelCircuitThreshold(channelID)
		if entry.ConsecutiveFailures >= threshold {
			entry.State = StateOpen
			entry.TripCount++
			log.Warnf("circuit breaker [%s] Closed -> Open (failures=%d >= threshold=%d, tripCount=%d, cooldown=%v)",
				key, entry.ConsecutiveFailures, threshold, entry.TripCount, channelCircuitCooldown(channelID, entry.TripCount))
		}
	case StateHalfOpen:
		// 试探失败，重新进入 Open 状态，TripCount 递增（冷却时间翻倍）
		entry.LastFailureTime = time.Now()
		entry.State = StateOpen
		entry.TripCount++
		entry.ConsecutiveFailures = 0 // 重新开始计数
		entry.HalfOpenSince = time.Time{}
		log.Warnf("circuit breaker [%s] HalfOpen -> Open (probe failed, tripCount=%d, cooldown=%v)",
			key, entry.TripCount, channelCircuitCooldown(channelID, entry.TripCount))

	case StateOpen:
		// Open 状态下不更新 LastFailureTime，避免冷却计时器被反复重置
		// 导致熔断器无法转为 HalfOpen（C-04）。
	}
}

// RemoveChannelEntries 删除指定渠道的所有熔断器条目。
// 在渠道被删除时调用，防止 globalBreaker map 无限增长。
func RemoveChannelEntries(channelID int) {
	prefix := buildKeyPrefix(channelID)
	globalBreaker.Range(func(key, _ any) bool {
		if k, ok := key.(string); ok && len(k) > 0 && strings.HasPrefix(k, prefix) {
			globalBreaker.Delete(key)
		}
		return true
	})
}

// PurgeIdleEntries 删除空闲时长超过 idleFor 的熔断器条目。globalBreaker 的 key 含
// 客户端请求携带的 modelName（基数不受控），缺少按空闲时长的周期回收会导致 map
// 无界增长（见 issue #46）。仅回收处于 Closed 且最近一次失败已超过 idleFor 的条目，
// 不动 Open/HalfOpen 状态（它们正在主动保护上游）。返回删除的条目数。
func PurgeIdleEntries(idleFor time.Duration) int {
	if idleFor <= 0 {
		return 0
	}
	now := time.Now()
	removed := 0
	globalBreaker.Range(func(key, value any) bool {
		entry, ok := value.(*circuitEntry)
		if !ok {
			globalBreaker.Delete(key)
			removed++
			return true
		}
		entry.mu.Lock()
		state := entry.State
		last := entry.LastFailureTime
		entry.mu.Unlock()
		// 仅清理 Closed 状态的条目：Open/HalfOpen 正在保护上游，不能丢弃。
		// LastFailureTime 为零值（从未失败）或已超过空闲阈值的 Closed 条目可回收。
		if state == StateClosed && (last.IsZero() || now.Sub(last) >= idleFor) {
			globalBreaker.Delete(key)
			removed++
		}
		return true
	})
	return removed
}
