package relaylog

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const liveRequestLimit = 256

type LiveRequest struct {
	EndpointType string `json:"endpoint_type"`
	TraceID      string `json:"trace_id"`
	InstanceID   string `json:"instance_id"`
	StartedAt    int64  `json:"started_at"`
	UpdatedAt    int64  `json:"updated_at"`
	State        string `json:"state"`
	RequestModel string `json:"request_model"`
	ActualModel  string `json:"actual_model"`
	ChannelID    int    `json:"channel_id"`
	ChannelName  string `json:"channel_name"`
	APIKeyID     int    `json:"api_key_id"`
	Attempt      int    `json:"attempt"`
	HTTPStatus   int    `json:"http_status"`
	LogID        string `json:"log_id,omitempty"`
}

type liveRequestStore struct {
	mutex       sync.Mutex
	instance    string
	requests    map[string]LiveRequest
	subscribers map[chan LiveRequest]struct{}
}

func newLiveRequestStore() *liveRequestStore {
	return &liveRequestStore{instance: uuid.NewString(), requests: make(map[string]LiveRequest), subscribers: make(map[chan LiveRequest]struct{})}
}

var liveRequests = newLiveRequestStore()

func liveLabel(value string) string {
	if len(value) > 256 {
		value = value[:256]
	}
	return strings.ToValidUTF8(value, "")
}

func UpdateLiveRequest(traceID string, update func(*LiveRequest)) {
	if traceID != "" {
		liveRequests.update(traceID, update)
	}
}

func (store *liveRequestStore) update(traceID string, update func(*LiveRequest)) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	now := time.Now().UnixMilli()
	entry, exists := store.requests[traceID]
	if !exists {
		entry = LiveRequest{TraceID: traceID, InstanceID: store.instance, StartedAt: now}
	}
	previous := entry
	update(&entry)
	entry.RequestModel = liveLabel(entry.RequestModel)
	entry.ActualModel = liveLabel(entry.ActualModel)
	entry.ChannelName = liveLabel(entry.ChannelName)
	if exists && previous == entry {
		return
	}
	entry.UpdatedAt = now
	for key, candidate := range store.requests {
		if now-candidate.UpdatedAt > int64(10*time.Minute/time.Millisecond) && (candidate.State == "unavailable" || candidate.State == "completed") {
			delete(store.requests, key)
		}
	}
	if !exists && len(store.requests) >= liveRequestLimit {
		oldestKey, oldestAt := "", now+1
		for key, candidate := range store.requests {
			if candidate.UpdatedAt < oldestAt {
				oldestKey, oldestAt = key, candidate.UpdatedAt
			}
		}
		delete(store.requests, oldestKey)
	}
	store.requests[traceID] = entry
	for subscriber := range store.subscribers {
		select {
		case subscriber <- entry:
		default:
			close(subscriber)
			delete(store.subscribers, subscriber)
		}
	}
	if entry.State == "recorded" {
		delete(store.requests, traceID)
	}
}

func SubscribeLiveRequests() (chan LiveRequest, []LiveRequest, func()) {
	store := liveRequests
	store.mutex.Lock()
	subscriber := make(chan LiveRequest, 64)
	store.subscribers[subscriber] = struct{}{}
	snapshot := make([]LiveRequest, 0, len(store.requests))
	for _, entry := range store.requests {
		snapshot = append(snapshot, entry)
	}
	store.mutex.Unlock()
	sort.Slice(snapshot, func(left, right int) bool { return snapshot[left].StartedAt > snapshot[right].StartedAt })
	return subscriber, snapshot, func() {
		store.mutex.Lock()
		defer store.mutex.Unlock()
		if _, exists := store.subscribers[subscriber]; exists {
			delete(store.subscribers, subscriber)
			close(subscriber)
		}
	}
}
