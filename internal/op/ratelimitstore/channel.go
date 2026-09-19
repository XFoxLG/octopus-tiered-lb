package ratelimitstore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lingyuins/octopus/internal/store"
	"github.com/lingyuins/octopus/internal/utils/ratelimit"
)

// channel.go 承载渠道级容量配额（阶段1）：并发信号量 + RPM 令牌桶。
//
// 放 ratelimitstore 而非 balancer：balancer 已 import op/channel（circuit.go），
// op/channel 删除渠道时要做桶清理，反向引用 balancer 会形成循环依赖；
// ratelimitstore 只依赖 store 与 utils，op/channel 可以安全引用它。
//
// 并发信号量语义：key = channelID（按渠道计，不分模型——上游对单渠道的
// 并发上限通常是全局的）。两层设计：
//  1. 候选级快速检查（InFlight，非阻塞）：满员时候选被 Skip，留下干净跳过记录；
//  2. attempt 级原子占用（Acquire，INCR 后比较）：竞态兜底，获取/释放之间
//     没有任何提前退出路径，配对绝对成立。
//
// Redis 计数器带 TTL 自愈（每次占用续期）：部署重启、进程崩溃或请求挂死
// 留下的"满员"假象在 TTL 内自愈，不会永久阻塞渠道。Redis 故障时回退内存
// 路径（限流不失效，与 CheckRateLimit 的容错策略一致）。

const (
	// concKeyPrefix 是并发计数器的子系统前缀。完整 Redis key: octopus:conc:{channelID}。
	concKeyPrefix = "conc:"
	// concKeyTTL 并发计数器 TTL。每次占用续期；须大于最长的 attempt 时长
	//（分组 AttemptTimeOut 可配置，10 分钟覆盖绝大多数配置；挂死请求占用的
	// 名额至多延迟这么久才自愈）。
	concKeyTTL = 10 * time.Minute
	// rlChReqKeyPrefix 是渠道 RPM 令牌桶的前缀。完整 Redis key:
	// octopus:rl:ch:req:{channelID}:{modelName}。
	rlChReqKeyPrefix = "rl:ch:req:"
)

var (
	concCounters   sync.Map // "channelID" -> *channelConcurrencyEntry
	channelBuckets sync.Map // "channelID:modelName" -> *ratelimit.TokenBucket
)

// channelConcurrencyEntry 内存回退路径的并发计数条目。count 用原子操作
//（多 goroutine 并发占用/释放），lastAcquireAt 驱动幽灵名额自愈：持有者
// 挂死（attempt 超时被禁用）导致满员假象时，超过 TTL 的占用被重置。
type channelConcurrencyEntry struct {
	count         atomic.Int64
	lastAcquireAt atomic.Int64 // unix 秒
}

func concKey(channelID int) string {
	return fmt.Sprintf("%s%d", concKeyPrefix, channelID)
}

func channelRateLimitKey(channelID int, modelName string) string {
	return rlChReqKeyPrefix + fmt.Sprintf("%d:%s", channelID, modelName)
}

// AcquireChannelSlot 原子占用一个并发名额。maxConcurrency <= 0 视为不限制
//（防御：调用方已判）。返回是否占用成功；成功后调用方必须在 attempt 结束
//（无论成败）时调用 ReleaseChannelSlot。
func AcquireChannelSlot(channelID int, maxConcurrency int) bool {
	if maxConcurrency <= 0 {
		return true
	}
	// Redis 路径：INCR 返回本次占用后的新计数（原子），比较在其返回值上进行；
	// 超限时 DECR 回退占用。err 时回退内存路径（限流不失效）。
	if store.Enabled() {
		count, err := store.GetKV().Incr(context.Background(), concKey(channelID), concKeyTTL)
		if err == nil {
			if count > int64(maxConcurrency) {
				_, _ = store.GetKV().Decr(context.Background(), concKey(channelID))
				return false
			}
			return true
		}
	}
	return memoryAcquireChannelSlot(channelID, maxConcurrency)
}

// ReleaseChannelSlot 释放一个并发名额（AcquireChannelSlot 的配对操作）。
// 计数漂移（负值，如 TTL 过期后旧持有者释放）由负值检测自愈。
func ReleaseChannelSlot(channelID int) {
	if store.Enabled() {
		if count, err := store.GetKV().Decr(context.Background(), concKey(channelID)); err == nil {
			if count >= 0 {
				return
			}
			// 负值漂移：重置为 0（TTL 过期后旧持有者释放撞上新占用者）。
			// 置 0 与 Get 语义一致：0 个在途请求。
			_ = store.GetKV().Set(context.Background(), concKey(channelID), []byte("0"), concKeyTTL)
			return
		}
	}
	memoryReleaseChannelSlot(channelID)
}

// InFlightChannel 非阻塞读取渠道当前在途请求数（近似值，供候选级快速检查）。
// Redis 故障时回退内存路径。
func InFlightChannel(channelID int) int {
	if store.Enabled() {
		val, found, err := store.GetKV().Get(context.Background(), concKey(channelID))
		if err == nil {
			if !found {
				return 0
			}
			count, parseErr := strconv.Atoi(string(val))
			if parseErr == nil {
				return count
			}
		}
	}
	return memoryInFlightChannel(channelID)
}

// CheckChannelRPM 检查渠道级 RPM 令牌桶（每候选选中消耗 1 个 token）。
// rpm <= 0 视为不限制。返回 allowed 与 retry-after（unix 秒，调用方减去当前
// 时间得到等待时长，与 CheckRateLimit 的返回约定一致）。
func CheckChannelRPM(channelID int, modelName string, rpm int) (allowed bool, retryAfter int) {
	if rpm <= 0 {
		return true, 0
	}
	key := channelRateLimitKey(channelID, modelName)
	// Redis 路径成功时直接返回；err 时回退内存路径（下方继续执行）。
	if store.Enabled() {
		ok, _, resetAt, err := store.GetRateLimit().CheckAndConsume(context.Background(), key, rpm, rpm, 1)
		if err == nil {
			if !ok {
				return false, int(resetAt.Unix())
			}
			return true, 0
		}
	}
	bucket := getOrCreateBucket(&channelBuckets, key, rpm, rpm)
	if !bucket.Allow() {
		return false, int(bucket.ResetAt().Unix())
	}
	return true, 0
}

// RemoveChannelBuckets 删除指定渠道的并发计数器与 RPM 桶（渠道删除时调用，
// 避免残留驻留）。
func RemoveChannelBuckets(channelID int) {
	if channelID <= 0 {
		return
	}
	if store.Enabled() {
		ctx := context.Background()
		_ = store.GetKV().Del(ctx, concKey(channelID))
		// RPM 桶 key 带 tokenBucketTTL，由 Redis 自动过期；删除时按前缀清理。
		_ = store.GetKV().DelByPrefix(ctx, rlChReqKeyPrefix+strconv.Itoa(channelID)+":")
	}
	concCounters.Delete(strconv.Itoa(channelID))
	channelBuckets.Range(func(k, _ any) bool {
		if s, ok := k.(string); ok && strings.HasPrefix(s, strconv.Itoa(channelID)+":") {
			channelBuckets.Delete(k)
		}
		return true
	})
}

// PurgeStaleChannelState 清理内存回退路径的陈旧条目（防无界增长），由 relay
// log flush 定时任务周期性调用（与 PurgeStaleBuckets 同源）。Redis 模式下
// key 带 TTL 自动过期，返回 0。
func PurgeStaleChannelState(maxAge time.Duration) int {
	if store.Enabled() {
		return 0
	}
	if maxAge <= 0 {
		return 0
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	concCounters.Range(func(k, v any) bool {
		entry, ok := v.(*channelConcurrencyEntry)
		if !ok {
			concCounters.Delete(k)
			removed++
			return true
		}
		// 只清理空闲（count<=0）且陈旧的条目；在途条目由 TTL 自愈语义保护。
		if entry.count.Load() <= 0 && entry.lastAcquireAt.Load() < cutoff.Unix() {
			concCounters.Delete(k)
			removed++
		}
		return true
	})
	removed += purgeStaleBucketMap(&channelBuckets, cutoff)
	return removed
}

func memoryAcquireChannelSlot(channelID int, maxConcurrency int) bool {
	entry := getOrCreateConcurrencyEntry(channelID)
	for {
		current := entry.count.Load()
		if current < int64(maxConcurrency) {
			if entry.count.CompareAndSwap(current, current+1) {
				entry.lastAcquireAt.Store(time.Now().Unix())
				return true
			}
			// CAS 失败 = 别的请求在动计数，重读重试。
			continue
		}
		// 满员。幽灵名额自愈：上次占用超过 TTL 仍满员，说明持有者已挂死
		//（attempt 超时被禁用或代码泄漏），重置计数让新请求不被永久阻塞。
		if entry.lastAcquireAt.Load() < time.Now().Add(-concKeyTTL).Unix() {
			if entry.count.CompareAndSwap(current, 0) {
				continue
			}
		}
		return false
	}
}

func memoryReleaseChannelSlot(channelID int) {
	entry := getOrCreateConcurrencyEntry(channelID)
	for {
		current := entry.count.Load()
		if current <= 0 {
			// 释放漂移（重复释放/TTL 过期后旧持有者释放）：钳制在 0。
			if entry.count.CompareAndSwap(current, 0) {
				return
			}
			continue
		}
		if entry.count.CompareAndSwap(current, current-1) {
			return
		}
	}
}

func memoryInFlightChannel(channelID int) int {
	if entry, ok := concCounters.Load(strconv.Itoa(channelID)); ok {
		if e, ok := entry.(*channelConcurrencyEntry); ok {
			return int(e.count.Load())
		}
	}
	return 0
}

func getOrCreateConcurrencyEntry(channelID int) *channelConcurrencyEntry {
	key := strconv.Itoa(channelID)
	if v, ok := concCounters.Load(key); ok {
		if e, ok := v.(*channelConcurrencyEntry); ok {
			return e
		}
	}
	entry := &channelConcurrencyEntry{}
	actual, _ := concCounters.LoadOrStore(key, entry)
	if e, ok := actual.(*channelConcurrencyEntry); ok {
		return e
	}
	return entry
}

func purgeStaleBucketMap(m *sync.Map, cutoff time.Time) int {
	removed := 0
	m.Range(func(k, v any) bool {
		b, ok := v.(*ratelimit.TokenBucket)
		if !ok {
			m.Delete(k)
			removed++
			return true
		}
		if b.LastUpdate().Before(cutoff) {
			m.Delete(k)
			removed++
		}
		return true
	})
	return removed
}
