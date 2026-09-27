package ratelimitstore

import (
	"context"
	"strconv"
	"time"

	"github.com/lingyuins/octopus/internal/store"
)

// global.go 承载全站转发限速(下游限速):请求频率 + 总并发。
//
// 作用域仅 4 条转发路由(chat/responses/messages/embeddings),管理 API、登录、
// 日志不受影响——避免把运维入口一起锁死。两种计数默认均为 0(关闭),升级后
// 行为不变。
//
// 复用已有的 store KV 与内存降级路径,不新建连接、不引入新依赖:Redis 路径与
// 渠道级计数同源(原子 INCR + TTL 自愈),Redis 故障时回退内存。

const (
	// globalConcKey 是全站并发计数器 key。与渠道级 concKey 命名空间分离,
	// 不会互相干扰。
	globalConcKey = "conc:__global__"
	// globalRLKey 是全站请求频率令牌桶 key。
	globalRLKey = "rl:global:req"
)

// globalConcurrencyEntry 是内存回退路径的全站并发计数条目。语义与渠道级
// channelConcurrencyEntry 一致:原子计数 + 幽灵名额 TTL 自愈。
var globalConcurrencyEntry = &channelConcurrencyEntry{}

// CheckGlobalRPM 检查全站转发请求频率。
// rpm <= 0 视为不限制。返回 allowed 与 retry-after(unix 秒,与其它 Check*
// 的返回约定一致)。
func CheckGlobalRPM(rpm int) (allowed bool, retryAfter int) {
	if rpm <= 0 {
		return true, 0
	}
	if store.Enabled() {
		ok, _, resetAt, err := store.GetRateLimit().CheckAndConsume(context.Background(), globalRLKey, rpm, rpm, 1)
		if err == nil {
			if !ok {
				return false, int(resetAt.Unix())
			}
			return true, 0
		}
	}
	bucket := getOrCreateBucket(&channelBuckets, globalRLKey, rpm, rpm)
	if !bucket.Allow() {
		return false, int(bucket.ResetAt().Unix())
	}
	return true, 0
}

// AcquireGlobalSlot 原子占用一个全站并发名额。maxConcurrency <= 0 视为不限制
// (直接返回 true,不计数)。成功后调用方必须在转发结束(无论成败)时调用
// ReleaseGlobalSlot。
func AcquireGlobalSlot(maxConcurrency int) bool {
	if maxConcurrency <= 0 {
		return true
	}
	if store.Enabled() {
		count, err := store.GetKV().Incr(context.Background(), globalConcKey, concKeyTTL)
		if err == nil {
			if count > int64(maxConcurrency) {
				_, _ = store.GetKV().Decr(context.Background(), globalConcKey)
				return false
			}
			return true
		}
	}
	return memoryAcquireGlobalSlot(maxConcurrency)
}

// ReleaseGlobalSlot 释放一个全站并发名额(AcquireGlobalSlot 的配对操作)。
func ReleaseGlobalSlot() {
	if store.Enabled() {
		if count, err := store.GetKV().Decr(context.Background(), globalConcKey); err == nil {
			if count >= 0 {
				return
			}
			_ = store.GetKV().Set(context.Background(), globalConcKey, []byte("0"), concKeyTTL)
			return
		}
	}
	memoryReleaseGlobalSlot()
}

// InFlightGlobal 非阻塞读取当前全站在途转发请求数。
func InFlightGlobal() int {
	if store.Enabled() {
		val, found, err := store.GetKV().Get(context.Background(), globalConcKey)
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
	return int(globalConcurrencyEntry.count.Load())
}

// PurgeGlobalState 清理全站限速的内存回退条目。仅在无人转发且空闲时可用,
// 主要供测试隔离使用。
func PurgeGlobalState() {
	globalConcurrencyEntry.count.Store(0)
	globalConcurrencyEntry.lastAcquireAt.Store(0)
	channelBuckets.Delete(globalRLKey)
}

func memoryAcquireGlobalSlot(maxConcurrency int) bool {
	entry := globalConcurrencyEntry
	for {
		current := entry.count.Load()
		if current < int64(maxConcurrency) {
			if entry.count.CompareAndSwap(current, current+1) {
				entry.lastAcquireAt.Store(time.Now().Unix())
				return true
			}
			continue
		}
		// 幽灵名额自愈:持有者挂死超过 TTL 仍满员时重置计数,避免全站被永久锁死。
		if entry.lastAcquireAt.Load() < time.Now().Add(-concKeyTTL).Unix() {
			if entry.count.CompareAndSwap(current, 0) {
				continue
			}
		}
		return false
	}
}

func memoryReleaseGlobalSlot() {
	entry := globalConcurrencyEntry
	for {
		current := entry.count.Load()
		if current <= 0 {
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
