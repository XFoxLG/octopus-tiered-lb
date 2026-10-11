package relay

import (
	"sync"
	"testing"
)

// Notifications are hints: producers, unsubscribe and Finish share ownership
// of their channels and may execute concurrently without a send/close panic.
func TestRelayStreamSessionConcurrentNotificationLifecycle(t *testing.T) {
	for round := 0; round < 20; round++ {
		store := &relayStreamSessionStore{
			byKey:                make(map[string]*relayStreamSession),
			activeByConversation: make(map[string]string),
		}
		session := &relayStreamSession{store: store, subscribers: make(map[chan struct{}]struct{})}
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(3)
		go func() {
			defer workers.Done()
			<-start
			for i := 0; i < 100; i++ {
				session.AddPayload([]byte("data: {\"text\":\"hello\"}\n\n"))
			}
		}()
		go func() {
			defer workers.Done()
			<-start
			for i := 0; i < 100; i++ {
				notification, unsubscribe := session.Subscribe()
				unsubscribe()
				unsubscribe()
				for range notification {
					// A buffered wake-up may precede the closed channel.
				}
			}
		}()
		go func() {
			defer workers.Done()
			<-start
			session.Finish(nil)
			session.Finish(nil)
		}()
		close(start)
		workers.Wait()
		session.mu.RLock()
		count := len(session.subscribers)
		session.mu.RUnlock()
		if count != 0 {
			t.Fatalf("retained %d subscribers", count)
		}
	}
}
