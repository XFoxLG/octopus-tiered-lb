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
		wantState := state
		if state == "recorded" {
			wantState = "completed"
		}
		if got.State != wantState || got.TraceID != "trace" || len(got.RequestModel) > 256 || got.InstanceID == "" || got.Sequence == 0 {
			t.Fatalf("invalid state: %+v", got)
		}
		if state == "recorded" && got.PersistenceState != "stored" {
			t.Fatal("persistence revived live state")
		}
	}
	if entry := store.requests["trace"]; entry.State != "completed" || entry.PersistenceState != "stored" {
		t.Fatal("terminal tombstone was not retained")
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

func TestLiveTerminalStateRejectsLateAttempt(t *testing.T) {
	store := newLiveRequestStore()
	store.update("trace", func(entry *LiveRequest) { entry.State = "completed" })
	store.update("trace", func(entry *LiveRequest) { entry.State = "recorded" })
	sequence := store.requests["trace"].Sequence
	store.update("trace", func(entry *LiveRequest) { entry.State = "attempt"; entry.Attempt = 2 })
	entry := store.requests["trace"]
	if entry.State != "completed" || entry.Sequence <= sequence || entry.PersistenceState != "stored" {
		t.Fatalf("late update revived terminal state: %+v", entry)
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
