package store

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestKVCASFencesConcurrentAndLateRecovery(t *testing.T) {
	client, _ := newTestRedis(t)
	for name, kv := range map[string]KVStore{"memory": &memoryKV{}, "redis": newRedisKV(client)} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if err := kv.Set(ctx, "recovery-test", []byte("expired"), time.Minute); err != nil {
				t.Fatal(err)
			}
			var winners atomic.Int32
			var wg sync.WaitGroup
			for range 20 {
				wg.Go(func() {
					ok, err := kv.CompareAndSwap(ctx, "recovery-test", []byte("expired"), []byte("probe"), time.Minute, "")
					if err != nil {
						t.Error(err)
					}
					if ok {
						winners.Add(1)
					}
				})
			}
			wg.Wait()
			if winners.Load() != 1 {
				t.Fatalf("recovery probes=%d", winners.Load())
			}
			if err := kv.Set(ctx, "recovery-test", []byte("new-cooldown"), time.Minute); err != nil {
				t.Fatal(err)
			}
			if ok, err := kv.CompareAndSwap(ctx, "recovery-test", []byte("probe"), nil, 0, ""); ok || err != nil {
				t.Fatalf("late completion erased newer cooldown: %t %v", ok, err)
			}
			value, found, err := kv.Get(ctx, "recovery-test")
			if err != nil || !found || string(value) != "new-cooldown" {
				t.Fatal("newer state not retained")
			}
		})
	}
}
