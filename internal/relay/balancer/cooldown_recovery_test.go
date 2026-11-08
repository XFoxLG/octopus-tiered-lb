package balancer

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestCooldownRecoverySingleProbeAndFencedResults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		resetKeyCooldown()
		t.Cleanup(resetKeyCooldown)
		storeCooldown(801, 1, "model-a", 429, time.Now().Add(15*time.Minute))
		if blocked, remaining := KeyCooldownRemaining(801, 1, "model-a"); !blocked || remaining != 15*time.Minute {
			t.Fatalf("cooldown=%t %v", blocked, remaining)
		}
		if blocked, _ := KeyCooldownRemaining(801, 1, "model-b"); blocked {
			t.Fatal("other model cooled down")
		}
		if _, allowed, err := AcquireCooldownProbe(context.Background(), 801, 1, "model-a"); allowed || err != nil {
			t.Fatalf("premature probe: %t %v", allowed, err)
		}
		time.Sleep(15 * time.Minute)
		var wins atomic.Int32
		var winner func(bool)
		var wg sync.WaitGroup
		for range 20 {
			wg.Go(func() {
				finish, allowed, err := AcquireCooldownProbe(context.Background(), 801, 1, "model-a")
				if err != nil {
					t.Error(err)
				}
				if allowed {
					wins.Add(1)
					winner = finish
				}
			})
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("concurrent probes=%d", wins.Load())
		}
		winner(false) // cancelled request releases immediately
		finish, allowed, err := AcquireCooldownProbe(context.Background(), 801, 1, "model-a")
		if err != nil || !allowed {
			t.Fatalf("cancel left probe stuck: %t %v", allowed, err)
		}
		storeCooldown(801, 1, "model-a", 429, time.Now().Add(20*time.Minute))
		finish(true)
		if blocked, remaining := KeyCooldownRemaining(801, 1, "model-a"); !blocked || remaining != 20*time.Minute {
			t.Fatal("late success erased newer recovery")
		}
		time.Sleep(20 * time.Minute)
		finish, allowed, err = AcquireCooldownProbe(context.Background(), 801, 1, "model-a")
		if err != nil || !allowed {
			t.Fatal("second recovery did not open")
		}
		finish(true)
		if blocked, _ := KeyCooldownRemaining(801, 1, "model-a"); blocked {
			t.Fatal("successful recovery still blocked")
		}
	})
}

func TestCooldownRecoveryRedisSingleProbe(t *testing.T) {
	mr, _ := startMiniredisForCooldown(t)
	resetKeyCooldown()
	t.Cleanup(resetKeyCooldown)
	storeCooldown(802, 0, "a", 429, time.Now().Add(time.Minute))
	storeCooldown(802, 1, "a", 429, time.Now().Add(time.Minute))
	mr.FastForward(61 * time.Second)
	finish, allowed, err := AcquireCooldownProbe(context.Background(), 802, 1, "a")
	if err != nil || !allowed {
		t.Fatalf("expired Redis cooldown: %t %v", allowed, err)
	}
	if _, other, err := AcquireCooldownProbe(context.Background(), 802, 2, "a"); err != nil || other {
		t.Fatal("channel half-open admitted another key")
	}
	storeCooldown(802, 0, "a", 503, time.Now().Add(5*time.Minute))
	finish(true)
	if blocked, remaining := IsChannelRateLimited(802, "a"); !blocked || remaining <= 0 {
		t.Fatal("late Redis success erased newer cooldown")
	}
	if _, other, err := AcquireCooldownProbe(context.Background(), 802, 2, "b"); err != nil || !other {
		t.Fatal("other model was blocked")
	}
}
