package relaylog

import (
	"testing"

	"github.com/lingyuins/octopus/internal/model"
)

func TestRelayLogContentExceedsLimit(t *testing.T) {
	cases := []struct {
		name      string
		size      int64
		limitMB   int
		wantExceed bool
	}{
		{"unlimited never exceeds", 1 << 30, -1, false},
		{"exactly at limit passes", 2 * 1024 * 1024, 2, false},
		{"one byte over limit exceeds", 2*1024*1024 + 1, 2, true},
		{"zero limit skips any content", 1, 0, true},
		{"empty content with zero limit passes", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RelayLogContentExceedsLimit(tc.size, tc.limitMB); got != tc.wantExceed {
				t.Fatalf("RelayLogContentExceedsLimit(%d, %d) = %v, want %v", tc.size, tc.limitMB, got, tc.wantExceed)
			}
		})
	}
}

func TestHalveMemoryLogCacheKeepsNewestHalf(t *testing.T) {
	relayLogCacheLock.Lock()
	relayLogCache = make([]model.RelayLog, 0, relayLogMaxSizeNoDB)
	for i := int64(1); i <= 10; i++ {
		relayLogCache = append(relayLogCache, model.RelayLog{ID: i, RequestContent: "body"})
	}
	relayLogCacheLock.Unlock()
	t.Cleanup(func() {
		relayLogCacheLock.Lock()
		relayLogCache = make([]model.RelayLog, 0, relayLogMaxSize)
		relayLogCacheLock.Unlock()
	})

	relayLogCacheLock.Lock()
	halved := halveMemoryLogCache(10)
	relayLogCacheLock.Unlock()
	if !halved {
		t.Fatal("halveMemoryLogCache(10) with 10 entries = false, want true")
	}

	relayLogCacheLock.Lock()
	defer relayLogCacheLock.Unlock()
	if len(relayLogCache) != 5 {
		t.Fatalf("cache len after halve = %d, want 5", len(relayLogCache))
	}
	for i, entry := range relayLogCache {
		if want := int64(i + 6); entry.ID != want {
			t.Fatalf("cache[%d].ID = %d, want %d (must keep newest half)", i, entry.ID, want)
		}
	}
}

func TestHalveMemoryLogCacheNoopWhenSmall(t *testing.T) {
	relayLogCacheLock.Lock()
	relayLogCache = []model.RelayLog{{ID: 1}, {ID: 2}}
	relayLogCacheLock.Unlock()
	t.Cleanup(func() {
		relayLogCacheLock.Lock()
		relayLogCache = make([]model.RelayLog, 0, relayLogMaxSize)
		relayLogCacheLock.Unlock()
	})

	relayLogCacheLock.Lock()
	halved := halveMemoryLogCache(10)
	cacheLen := len(relayLogCache)
	relayLogCacheLock.Unlock()
	if halved {
		t.Fatal("halveMemoryLogCache with small cache = true, want false")
	}
	if cacheLen != 2 {
		t.Fatalf("cache len = %d, want 2 (must be untouched)", cacheLen)
	}
}
