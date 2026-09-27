package ratelimitstore

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/store"
)

// global_test.go 全站下游限速(PR B)测试:请求频率 + 总并发。
// 重点覆盖 0=关闭、并发获取/释放配对、内存幽灵名额自愈。

func resetGlobalState(t *testing.T) {
	t.Helper()
	PurgeGlobalState()
	t.Cleanup(PurgeGlobalState)
}

func TestCheckGlobalRPM_UnlimitedWhenZero(t *testing.T) {
	resetGlobalState(t)
	for range 5 {
		if allowed, _ := CheckGlobalRPM(0); !allowed {
			t.Fatal("rpm=0 should always allow")
		}
		if allowed, _ := CheckGlobalRPM(-1); !allowed {
			t.Fatal("negative rpm should always allow")
		}
	}
}

func TestCheckGlobalRPM_BoundsRequests(t *testing.T) {
	resetGlobalState(t)
	const rpm = 2
	if allowed, _ := CheckGlobalRPM(rpm); !allowed {
		t.Fatal("first global RPM check should allow")
	}
	if allowed, _ := CheckGlobalRPM(rpm); !allowed {
		t.Fatal("second global RPM check should allow")
	}
	if allowed, retryAfter := CheckGlobalRPM(rpm); allowed {
		t.Fatal("third global RPM check over limit should be rejected")
	} else if retryAfter <= 0 {
		t.Fatalf("rejected check should return retry-after > 0, got %d", retryAfter)
	}
}

func TestAcquireGlobalSlot_UnlimitedWhenZero(t *testing.T) {
	resetGlobalState(t)
	for range 5 {
		if !AcquireGlobalSlot(0) {
			t.Fatal("maxConcurrency=0 should always allow")
		}
	}
	if InFlightGlobal() != 0 {
		t.Fatalf("unlimited global should not track slots, got %d", InFlightGlobal())
	}
}

func TestAcquireGlobalSlot_BoundsConcurrency(t *testing.T) {
	resetGlobalState(t)
	const maxConcurrency = 2
	if !AcquireGlobalSlot(maxConcurrency) {
		t.Fatal("first acquire should succeed")
	}
	if !AcquireGlobalSlot(maxConcurrency) {
		t.Fatal("second acquire should succeed")
	}
	if AcquireGlobalSlot(maxConcurrency) {
		t.Fatal("third acquire over limit should be rejected")
	}
	if InFlightGlobal() != maxConcurrency {
		t.Fatalf("in-flight = %d, want %d", InFlightGlobal(), maxConcurrency)
	}
	ReleaseGlobalSlot()
	if !AcquireGlobalSlot(maxConcurrency) {
		t.Fatal("acquire after release should succeed")
	}
}

func TestAcquireGlobalSlot_ConcurrentPairing(t *testing.T) {
	resetGlobalState(t)
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
				if AcquireGlobalSlot(maxConcurrency) {
					ReleaseGlobalSlot()
				}
				if inFlight := InFlightGlobal(); inFlight > maxConcurrency {
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
	if inFlight := InFlightGlobal(); inFlight != 0 {
		t.Fatalf("in-flight after all paired acquire/release = %d, want 0 (slot leak)", inFlight)
	}
}

func TestMemoryAcquireGlobal_GhostSlotSelfHeal(t *testing.T) {
	resetGlobalState(t)
	const maxConcurrency = 1
	if !AcquireGlobalSlot(maxConcurrency) {
		t.Fatal("first acquire should succeed")
	}
	if AcquireGlobalSlot(maxConcurrency) {
		t.Fatal("full global should reject while holder is fresh")
	}
	// 模拟持有者挂死:把 lastAcquireAt 拨到 TTL 之前。
	globalConcurrencyEntry.lastAcquireAt.Store(time.Now().Add(-concKeyTTL - time.Minute).Unix())
	if !AcquireGlobalSlot(maxConcurrency) {
		t.Fatal("ghost slot self-heal should allow acquire after TTL")
	}
}

func TestCheckGlobalRPM_RedisPath(t *testing.T) {
	resetGlobalState(t)
	newRLTestRedis(t)
	const rpm = 1
	if allowed, _ := CheckGlobalRPM(rpm); !allowed {
		t.Fatal("first Redis global RPM check should allow")
	}
	if allowed, retryAfter := CheckGlobalRPM(rpm); allowed {
		t.Fatal("second Redis global RPM check should be rejected")
	} else if retryAfter <= 0 {
		t.Fatalf("rejected Redis check should return retry-after > 0, got %d", retryAfter)
	}
}

func TestAcquireGlobalSlot_RedisPath(t *testing.T) {
	resetGlobalState(t)
	newRLTestRedis(t)
	const maxConcurrency = 1
	if !AcquireGlobalSlot(maxConcurrency) {
		t.Fatal("first Redis global acquire should succeed")
	}
	if AcquireGlobalSlot(maxConcurrency) {
		t.Fatal("second Redis global acquire over limit should be rejected")
	}
	val, found, err := store.GetKV().Get(context.Background(), globalConcKey)
	if err != nil || !found {
		t.Fatalf("expected Redis counter, found=%v err=%v", found, err)
	}
	if got := string(val); got != "1" {
		t.Fatalf("Redis global counter = %q, want 1", got)
	}
}
