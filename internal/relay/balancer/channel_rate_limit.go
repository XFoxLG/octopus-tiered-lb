package balancer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/store"
)

// channelRateLimitEntry aggregates 429s by channel and model. A single 429
// remains a key-scoped event; repeated failures in a short window indicate
// that the channel/account pool itself has no usable capacity.
type channelRateLimitEntry struct {
	mu            sync.Mutex
	keyFailures   map[int]time.Time
	failureTimes  []time.Time
	blockedUntil  time.Time
	lastActivity  time.Time
}

var globalChannelRateLimits sync.Map // key: "channelID:modelName" -> *channelRateLimitEntry

const channelRateLimitKVPrefix = "channel-rate-limit:"

func channelRateLimitKey(channelID int, modelName string) string {
	return buildKey2(channelID, strings.TrimSpace(modelName))
}

func channelRateLimitKVKey(channelID int, modelName string) string {
	return channelRateLimitKVPrefix + channelRateLimitKey(channelID, modelName)
}

func getChannelRateLimitThreshold() int {
	v, err := setting.GetInt(model.SettingKeyRateLimitChannelThreshold)
	if err != nil || v < 1 {
		return 2
	}
	return v
}

func getChannelRateLimitWindow() time.Duration {
	v, err := setting.GetInt(model.SettingKeyRateLimitChannelWindow)
	if err != nil || v < 1 {
		return 30 * time.Second
	}
	return time.Duration(v) * time.Second
}

func getChannelRateLimitCooldown() time.Duration {
	v, err := setting.GetInt(model.SettingKeyRateLimitChannelCooldown)
	if err != nil || v < 1 {
		return 30 * time.Second
	}
	return time.Duration(v) * time.Second
}

func getOrCreateChannelRateLimitEntry(key string) *channelRateLimitEntry {
	if value, ok := globalChannelRateLimits.Load(key); ok {
		if entry, ok := value.(*channelRateLimitEntry); ok {
			return entry
		}
	}
	entry := &channelRateLimitEntry{
		keyFailures:  make(map[int]time.Time),
		lastActivity: time.Now(),
	}
	actual, _ := globalChannelRateLimits.LoadOrStore(key, entry)
	if existing, ok := actual.(*channelRateLimitEntry); ok {
		return existing
	}
	return entry
}

func purgeChannelRateLimitEvents(entry *channelRateLimitEntry, now time.Time) {
	cutoff := now.Add(-getChannelRateLimitWindow())
	if len(entry.failureTimes) > 0 {
		kept := entry.failureTimes[:0]
		for _, timestamp := range entry.failureTimes {
			if timestamp.After(cutoff) {
				kept = append(kept, timestamp)
			}
		}
		entry.failureTimes = kept
	}
	for keyID, timestamp := range entry.keyFailures {
		if !timestamp.After(cutoff) {
			delete(entry.keyFailures, keyID)
		}
	}
}

// IsChannelRateLimited reports whether a channel/model is temporarily
// quarantined because its account pool repeatedly returned 429.
func IsChannelRateLimited(channelID int, modelName string) (bool, time.Duration) {
	if channelID == 0 || strings.TrimSpace(modelName) == "" {
		return false, 0
	}

	if store.Enabled() {
		_, found, err := store.GetKV().Get(context.Background(), channelRateLimitKVKey(channelID, modelName))
		if err == nil && found {
			if entry, ok := globalChannelRateLimits.Load(channelRateLimitKey(channelID, modelName)); ok {
				if local, ok := entry.(*channelRateLimitEntry); ok {
					local.mu.Lock()
					remaining := time.Until(local.blockedUntil)
					local.mu.Unlock()
					if remaining > 0 {
						return true, remaining
					}
				}
			}
			return true, 0
		}
	}

	value, ok := globalChannelRateLimits.Load(channelRateLimitKey(channelID, modelName))
	if !ok {
		return false, 0
	}
	entry, ok := value.(*channelRateLimitEntry)
	if !ok {
		globalChannelRateLimits.Delete(channelRateLimitKey(channelID, modelName))
		return false, 0
	}

	now := time.Now()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.blockedUntil.After(now) {
		return true, time.Until(entry.blockedUntil)
	}
	if !entry.blockedUntil.IsZero() {
		entry.blockedUntil = time.Time{}
		entry.failureTimes = nil
		entry.keyFailures = make(map[int]time.Time)
	}
	return false, 0
}

// RecordChannelRateLimit records one 429. explicitChannelScope is set when
// the provider explicitly says that its account pool is exhausted. Otherwise
// the channel opens only after the configured number of failures in the
// window; both repeated failures on one key and failures on different keys
// count, because a one-key channel must not spin forever.
func RecordChannelRateLimit(channelID, keyID int, modelName string, explicitChannelScope bool, retryAfter time.Duration) (bool, time.Duration) {
	if channelID == 0 || strings.TrimSpace(modelName) == "" {
		return false, 0
	}

	now := time.Now()
	key := channelRateLimitKey(channelID, modelName)
	entry := getOrCreateChannelRateLimitEntry(key)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	entry.lastActivity = now
	purgeChannelRateLimitEvents(entry, now)
	entry.failureTimes = append(entry.failureTimes, now)
	entry.keyFailures[keyID] = now

	thresholdReached := len(entry.failureTimes) >= getChannelRateLimitThreshold()
	if !explicitChannelScope && !thresholdReached {
		return false, 0
	}

	cooldown := getChannelRateLimitCooldown()
	if retryAfter > cooldown {
		cooldown = retryAfter
	}
	if cooldown <= 0 {
		return false, 0
	}

	blockedUntil := now.Add(cooldown)
	if blockedUntil.After(entry.blockedUntil) {
		entry.blockedUntil = blockedUntil
	}
	remaining := time.Until(entry.blockedUntil)
	if store.Enabled() {
		_ = store.GetKV().Set(context.Background(), channelRateLimitKVKey(channelID, modelName), []byte("1"), remaining)
	}
	return true, remaining
}

// RecordChannelRateLimitSuccess clears the short-lived aggregate after a
// successful request. A healthy response is stronger evidence than stale 429s
// and prevents a recovered channel from being skipped for the whole window.
func RecordChannelRateLimitSuccess(channelID int, modelName string) {
	if channelID == 0 || strings.TrimSpace(modelName) == "" {
		return
	}
	key := channelRateLimitKey(channelID, modelName)
	globalChannelRateLimits.Delete(key)
	if store.Enabled() {
		_ = store.GetKV().Del(context.Background(), channelRateLimitKVKey(channelID, modelName))
	}
}

// PurgeIdleChannelRateLimits bounds memory when clients submit unbounded model
// names. Active blocked entries are retained until their cooldown expires.
func PurgeIdleChannelRateLimits(idleFor time.Duration) int {
	if idleFor <= 0 {
		return 0
	}
	threshold := time.Now().Add(-idleFor)
	removed := 0
	globalChannelRateLimits.Range(func(key, value any) bool {
		entry, ok := value.(*channelRateLimitEntry)
		if !ok {
			globalChannelRateLimits.Delete(key)
			removed++
			return true
		}
		entry.mu.Lock()
		idle := entry.lastActivity.Before(threshold) && !entry.blockedUntil.After(time.Now())
		entry.mu.Unlock()
		if idle {
			globalChannelRateLimits.Delete(key)
			removed++
		}
		return true
	})
	return removed
}

// RemoveChannelRateLimitEntries removes local and Redis state for a deleted
// channel. The model suffix is intentionally handled by the namespace prefix.
func RemoveChannelRateLimitEntries(channelID int) {
	prefix := fmt.Sprintf("%s%d:", channelRateLimitKVPrefix, channelID)
	if store.Enabled() {
		_ = store.GetKV().DelByPrefix(context.Background(), prefix)
	}
	localPrefix := fmt.Sprintf("%d:", channelID)
	globalChannelRateLimits.Range(func(key, _ any) bool {
		if keyString, ok := key.(string); ok && strings.HasPrefix(keyString, localPrefix) {
			globalChannelRateLimits.Delete(key)
		}
		return true
	})
}
