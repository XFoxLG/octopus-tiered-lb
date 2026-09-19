package ratelimitstore

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/store"
	"github.com/redis/go-redis/v9"
)

// channel_test.go 渠道级容量配额测试（阶段1）。
// 重点覆盖获取/释放配对（含并发）与内存幽灵名额自愈——这两类缺陷会让渠道
// 被假性满员永久跳过（502），是本功能最危险的回归方向。

// resetChannelState 清空两个全局 sync.Map，隔离测试间状态。
func resetChannelState(t *testing.T) {
	t.Helper()
	concCounters = sync.Map{}
	channelBuckets = sync.Map{}
	t.Cleanup(func() {
		concCounters = sync.Map{}
		channelBuckets = sync.Map{}
	})
}

func TestAcquireChannelSlot_Unlimited(t *testing.T) {
	resetChannelState(t)
	// 未配置（0/负数）= 不限制，直接放行且不创建计数条目。
	for range 5 {
		if !AcquireChannelSlot(1, 0) {
			t.Fatal("AcquireChannelSlot(1, 0) should always allow")
		}
	}
	if !AcquireChannelSlot(1, -3) {
		t.Fatal("AcquireChannelSlot(1, -3) should always allow")
	}
	if InFlightChannel(1) != 0 {
		t.Fatalf("unlimited channel should not track slots, got %d", InFlightChannel(1))
	}
}

func TestAcquireChannelSlot_BoundsConcurrency(t *testing.T) {
	resetChannelState(t)
	const maxConcurrency = 2

	if !AcquireChannelSlot(7, maxConcurrency) {
		t.Fatal("first acquire should succeed")
	}
	if !AcquireChannelSlot(7, maxConcurrency) {
		t.Fatal("second acquire should succeed")
	}
	// 第三个超过上限：拒绝。
	if AcquireChannelSlot(7, maxConcurrency) {
		t.Fatal("third acquire over limit should be rejected")
	}
	if InFlightChannel(7) != maxConcurrency {
		t.Fatalf("in-flight = %d, want %d (rejected acquire must not occupy)", InFlightChannel(7), maxConcurrency)
	}
	// 释放一个后重新可占：获取/释放配对正确归位。
	ReleaseChannelSlot(7)
	if !AcquireChannelSlot(7, maxConcurrency) {
		t.Fatal("acquire after release should succeed")
	}
	if InFlightChannel(7) != maxConcurrency {
		t.Fatalf("in-flight after re-acquire = %d, want %d", InFlightChannel(7), maxConcurrency)
	}
}

func TestAcquireChannelSlot_IndependentChannels(t *testing.T) {
	resetChannelState(t)
	const maxConcurrency = 1
	if !AcquireChannelSlot(1, maxConcurrency) {
		t.Fatal("channel 1 acquire should succeed")
	}
	// 渠道 2 不受渠道 1 的满员影响：计数按 channelID 隔离。
	if !AcquireChannelSlot(2, maxConcurrency) {
		t.Fatal("channel 2 acquire should be independent of channel 1")
	}
	if AcquireChannelSlot(1, maxConcurrency) {
		t.Fatal("channel 1 should still be full")
	}
}

func TestAcquireChannelSlot_ConcurrentPairing(t *testing.T) {
	resetChannelState(t)
	const maxConcurrency = 8
	const goroutines = 32
	const acquiresPerGoroutine = 25

	var wg sync.WaitGroup
	overLimit := make(chan struct{}, 1)
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range acquiresPerGoroutine {
				if AcquireChannelSlot(9, maxConcurrency) {
					// 立刻释放，模拟 attempt 结束后的配对操作。
					ReleaseChannelSlot(9)
				}
				// 并发占用后计数永不超上限（INCR 原子比较的语义保证）。
				if inFlight := InFlightChannel(9); inFlight > maxConcurrency {
					select {
					case overLimit <- struct{}{}:
					default:
					}
				}
			}
		}()
	}
	wg.Wait()
	close(overLimit)
	if _, overflowed := <-overLimit; overflowed {
		t.Fatalf("concurrent acquire/release exceeded limit %d", maxConcurrency)
	}
	// 全部配对结束后计数归零：没有泄漏的名额。
	if inFlight := InFlightChannel(9); inFlight != 0 {
		t.Fatalf("in-flight after all paired acquire/release = %d, want 0 (slot leak)", inFlight)
	}
}

func TestMemoryAcquire_GhostSlotSelfHeal(t *testing.T) {
	resetChannelState(t)
	const maxConcurrency = 1

	if !AcquireChannelSlot(3, maxConcurrency) {
		t.Fatal("first acquire should succeed")
	}
	// 满员拒绝（持有者仍在 TTL 内：刚占用的 lastAcquireAt 是现在）。
	if AcquireChannelSlot(3, maxConcurrency) {
		t.Fatal("full channel should reject while holder is fresh")
	}

	// 模拟持有者挂死：把 lastAcquireAt 拨到 TTL 之前，下次占用触发自愈。
	key := "3"
	entry, ok := concCounters.Load(key)
	if !ok {
		t.Fatal("expected concurrency entry for channel 3")
	}
	concurrencyEntry, ok := entry.(*channelConcurrencyEntry)
	if !ok {
		t.Fatalf("unexpected entry type %T", entry)
	}
	staleSeconds := time.Now().Add(-concKeyTTL - time.Minute).Unix()
	concurrencyEntry.lastAcquireAt.Store(staleSeconds)

	// 自愈：占用成功（计数被重置），渠道不再被幽灵名额永久阻塞。
	if !AcquireChannelSlot(3, maxConcurrency) {
		t.Fatal("ghost slot self-heal should allow acquire after TTL")
	}
	if InFlightChannel(3) != 1 {
		t.Fatalf("in-flight after self-heal acquire = %d, want 1", InFlightChannel(3))
	}
}

func TestMemoryRelease_ClampsDrift(t *testing.T) {
	resetChannelState(t)
	// 从未占用的渠道释放：漂移被钳制，计数不为负。
	ReleaseChannelSlot(4)
	ReleaseChannelSlot(4)
	if inFlight := InFlightChannel(4); inFlight != 0 {
		t.Fatalf("in-flight after drift releases = %d, want 0", inFlight)
	}
}

func TestCheckChannelRPM_ConsumesToken(t *testing.T) {
	resetChannelState(t)
	const rpm = 2

	if allowed, _ := CheckChannelRPM(5, "gpt-4o", rpm); !allowed {
		t.Fatal("first RPM check should allow")
	}
	if allowed, _ := CheckChannelRPM(5, "gpt-4o", rpm); !allowed {
		t.Fatal("second RPM check should allow")
	}
	// 第三个超限：拒绝。
	if allowed, _ := CheckChannelRPM(5, "gpt-4o", rpm); allowed {
		t.Fatal("third RPM check over limit should be rejected")
	}
	// 未配置（0）= 不限制。
	for range 5 {
		if allowed, _ := CheckChannelRPM(5, "gpt-4o", 0); !allowed {
			t.Fatal("rpm=0 should always allow")
		}
	}
	// 模型维度隔离：不同模型各自的桶。
	if allowed, _ := CheckChannelRPM(5, "claude-3", rpm); !allowed {
		t.Fatal("different model should have its own bucket")
	}
	// 渠道维度隔离：不同渠道各自的桶。
	if allowed, _ := CheckChannelRPM(6, "gpt-4o", rpm); !allowed {
		t.Fatal("different channel should have its own bucket")
	}
}

func TestCheckChannelRPM_RedisPath(t *testing.T) {
	resetChannelState(t)
	newRLTestRedis(t)

	const rpm = 1
	if allowed, _ := CheckChannelRPM(8, "gpt-4o", rpm); !allowed {
		t.Fatal("first Redis RPM check should allow")
	}
	if allowed, retryAfter := CheckChannelRPM(8, "gpt-4o", rpm); allowed {
		t.Fatal("second Redis RPM check should be rejected")
	} else if retryAfter <= 0 {
		t.Fatalf("rejected check should return retry-after > 0, got %d", retryAfter)
	}
}

func TestAcquireChannelSlot_RedisPathAndPairing(t *testing.T) {
	resetChannelState(t)
	mr := newRLTestRedis(t)

	const maxConcurrency = 1
	if !AcquireChannelSlot(11, maxConcurrency) {
		t.Fatal("first Redis acquire should succeed")
	}
	if AcquireChannelSlot(11, maxConcurrency) {
		t.Fatal("second Redis acquire over limit should be rejected")
	}
	// Redis 计数器应为 1（超限的 DECR 已回退占用）。
	val, found, err := store.GetKV().Get(context.Background(), concKey(11))
	if err != nil || !found {
		t.Fatalf("expected Redis counter, found=%v err=%v", found, err)
	}
	if got := string(val); got != "1" {
		t.Fatalf("Redis counter = %q, want 1", got)
	}
	// TTL 已设置：自愈语义依赖它。
	queryClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = queryClient.Close() })
	ttl, err := queryClient.TTL(context.Background(), "octopus:"+concKey(11)).Result()
	if err != nil {
		t.Fatalf("read TTL: %v", err)
	}
	if ttl <= 0 {
		t.Fatalf("concurrency counter should carry TTL for self-heal, got %v", ttl)
	}

	ReleaseChannelSlot(11)
	if !AcquireChannelSlot(11, maxConcurrency) {
		t.Fatal("acquire after Redis release should succeed")
	}
}

func TestRemoveChannelBuckets_ClearsState(t *testing.T) {
	resetChannelState(t)
	newRLTestRedis(t)

	if !AcquireChannelSlot(12, 5) {
		t.Fatal("acquire should succeed")
	}
	if allowed, _ := CheckChannelRPM(12, "gpt-4o", 5); !allowed {
		t.Fatal("RPM check should allow")
	}

	RemoveChannelBuckets(12)

	if InFlightChannel(12) != 0 {
		t.Fatalf("in-flight after removal = %d, want 0", InFlightChannel(12))
	}
	// RPM 桶被删：配额重新可用。
	if allowed, _ := CheckChannelRPM(12, "gpt-4o", 5); !allowed {
		t.Fatal("RPM quota should reset after bucket removal")
	}
}

func TestPurgeStaleChannelState_RemovesIdleEntries(t *testing.T) {
	resetChannelState(t)
	// 空闲条目（count<=0 且 lastAcquireAt 陈旧）被清理。
	AcquireChannelSlot(13, 5)
	ReleaseChannelSlot(13)
	// 陈旧化：把 lastAcquireAt 拨回远处。
	if entry, ok := concCounters.Load("13"); ok {
		if e, ok := entry.(*channelConcurrencyEntry); ok {
			e.lastAcquireAt.Store(time.Now().Add(-2 * time.Hour).Unix())
		}
	}
	// 在途条目（count>0）：即使陈旧也不被清理（TTL 自愈语义保护）。
	AcquireChannelSlot(14, 5)

	removed := PurgeStaleChannelState(time.Hour)
	if removed < 1 {
		t.Fatalf("PurgeStaleChannelState removed %d, want >= 1", removed)
	}
	if _, ok := concCounters.Load("13"); ok {
		t.Fatal("idle stale entry should be removed")
	}
	if _, ok := concCounters.Load("14"); !ok {
		t.Fatal("in-flight entry should be retained")
	}
}
