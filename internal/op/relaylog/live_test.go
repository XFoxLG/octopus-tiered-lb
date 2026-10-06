package relaylog

import (
	"fmt"
	"strings"
	"testing"
)

func TestLiveRequestsLifecycleAndBounds(t *testing.T) {
	store := newLiveRequestStore()
	subscriber := make(chan LiveRequest, 16)
	store.subscribers[subscriber] = struct{}{}
	for _, state := range []string{"received", "waiting", "attempt", "streaming", "completed", "recorded"} {
		store.update("trace", func(entry *LiveRequest) { entry.State = state; entry.RequestModel = strings.Repeat("x", 1000) })
		got := <-subscriber
		if got.State != state || got.TraceID != "trace" || len(got.RequestModel) > 256 || got.InstanceID == "" {
			t.Fatalf("invalid state: %+v", got)
		}
	}
	if len(store.requests) != 0 {
		t.Fatal("final trace retained in in-flight set")
	}
	for index := 0; index < liveRequestLimit+10; index++ {
		store.update(fmt.Sprint(index), func(entry *LiveRequest) { entry.State = "waiting" })
	}
	if len(store.requests) != liveRequestLimit {
		t.Fatalf("unbounded requests: %d", len(store.requests))
	}
	if len(store.subscribers) != 0 {
		t.Fatal("slow subscriber was not disconnected for resync")
	}
}

func TestLiveRequestsNoDuplicateEvents(t *testing.T) {
	store := newLiveRequestStore()
	subscriber := make(chan LiveRequest, 4)
	store.subscribers[subscriber] = struct{}{}
	update := func(entry *LiveRequest) { entry.State = "streaming" }
	store.update("trace", update)
	store.update("trace", update)
	if len(subscriber) != 1 {
		t.Fatalf("per-token duplicate event: %d", len(subscriber))
	}
}
