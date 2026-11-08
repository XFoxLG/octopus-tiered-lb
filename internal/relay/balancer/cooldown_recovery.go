package balancer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lingyuins/octopus/internal/store"
	"github.com/lingyuins/octopus/internal/utils/json"
	"github.com/lingyuins/octopus/internal/utils/log"
)

// Expired evidence uses the existing one-hour idle retention window. Retention
// permits one half-open recovery probe; it does not extend the cooldown.
const cooldownRecoveryRetention = time.Hour

type cooldownWire struct {
	StatusCode int       `json:"status_code"`
	ExpiresAt  time.Time `json:"expires_at"`
	ProbeSince time.Time `json:"probe_since,omitempty"`
	Generation string    `json:"generation"`
}

func (e keyCooldownEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(cooldownWire{e.statusCode, e.expiresAt, e.probeSince, e.generation})
}
func (e *keyCooldownEntry) UnmarshalJSON(data []byte) error {
	var wire cooldownWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	e.statusCode, e.expiresAt, e.probeSince, e.generation = wire.StatusCode, wire.ExpiresAt, wire.ProbeSince, wire.Generation
	return nil
}

func recoveryKVKey(channelID, keyID int, modelName string) string {
	return "recovery:" + activeCooldownKVKey(channelID, keyID, modelName)
}
func activeCooldownKVKey(channelID, keyID int, modelName string) string {
	if keyID == 0 {
		return channelRateLimitKVKey(channelID, modelName)
	}
	return cooldownKVKey(channelID, keyID, modelName)
}

func loadCooldown(channelID, keyID int, modelName string) (*keyCooldownEntry, []byte, bool, error) {
	if store.Enabled() {
		data, found, err := store.GetKV().Get(context.Background(), recoveryKVKey(channelID, keyID, modelName))
		if err != nil {
			return nil, nil, found, err
		}
		_, active, activeErr := store.GetKV().Get(context.Background(), activeCooldownKVKey(channelID, keyID, modelName))
		if activeErr != nil {
			return nil, nil, false, activeErr
		}
		if !found {
			if active {
				return &keyCooldownEntry{}, nil, true, nil
			}
			return nil, nil, false, nil
		}
		// Published cache entries {} / 1 have an authoritative Redis TTL but
		// no exact deadline. Keep them blocked without inventing a timestamp.
		if string(data) == "1" || string(data) == "{}" {
			return &keyCooldownEntry{}, data, true, nil
		}
		var entry keyCooldownEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return nil, nil, true, fmt.Errorf("decode cooldown %s: %w", recoveryKVKey(channelID, keyID, modelName), err)
		}
		entry.expired = !active
		if active && !entry.expiresAt.After(time.Now()) {
			// An active TTL may belong to a concurrent newer record whose
			// metadata write has not completed. Its deadline is not known yet.
			entry.expiresAt = time.Time{}
		}
		return &entry, data, true, nil
	}
	value, found := globalKeyCooldown.Load(cooldownKey(channelID, keyID, modelName))
	if !found {
		return nil, nil, false, nil
	}
	return value.(*keyCooldownEntry), nil, true, nil
}

func cooldownTTL(entry *keyCooldownEntry) time.Duration {
	return max(time.Millisecond, time.Until(entry.expiresAt.Add(cooldownRecoveryRetention)))
}

func storeCooldown(channelID, keyID int, modelName string, status int, until time.Time) {
	entry := &keyCooldownEntry{statusCode: status, expiresAt: until, generation: uuid.NewString()}
	if store.Enabled() {
		data, err := json.Marshal(entry)
		if err == nil {
			err = store.GetKV().Set(context.Background(), activeCooldownKVKey(channelID, keyID, modelName), data, max(time.Millisecond, time.Until(until)))
		}
		if err == nil {
			err = store.GetKV().Set(context.Background(), recoveryKVKey(channelID, keyID, modelName), data, cooldownTTL(entry))
		}
		if err != nil {
			log.Warnf("persist cooldown: channel_id=%d key_id=%d: %v", channelID, keyID, err)
		}
		return
	}
	globalKeyCooldown.Store(cooldownKey(channelID, keyID, modelName), entry)
}

// Shared with the circuit breaker's configured lost-probe timeout.
func probePending(since, now time.Time) bool {
	if since.IsZero() {
		return false
	}
	timeout := halfOpenProbeTimeoutFunc()
	return timeout <= 0 || now.Sub(since) < timeout
}

func cooldownStatus(channelID, keyID int, modelName string) (bool, time.Duration) {
	entry, _, found, err := loadCooldown(channelID, keyID, modelName)
	if err != nil {
		log.Warnf("read cooldown: channel_id=%d key_id=%d: %v", channelID, keyID, err)
		return true, 0
	}
	if !found {
		return false, 0
	}
	if entry.expiresAt.IsZero() {
		return true, 0
	}
	if remaining := time.Until(entry.expiresAt); !entry.expired && remaining > 0 {
		return true, remaining
	}
	return probePending(entry.probeSince, time.Now()), 0
}

// blocked with zero remaining means unknown time (legacy cache / active probe).
func KeyCooldownRemaining(channelID, keyID int, modelName string) (bool, time.Duration) {
	return cooldownStatus(channelID, keyID, modelName)
}

func swapCooldown(ctx context.Context, channelID, keyID int, modelName string, old *keyCooldownEntry, raw []byte, next *keyCooldownEntry) (bool, error) {
	if store.Enabled() {
		var data []byte
		var err error
		ttl := time.Duration(0)
		if next != nil {
			data, err = json.Marshal(next)
			ttl = cooldownTTL(next)
		}
		if err != nil {
			return false, err
		}
		return store.GetKV().CompareAndSwap(ctx, recoveryKVKey(channelID, keyID, modelName), raw, data, ttl, activeCooldownKVKey(channelID, keyID, modelName))
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	key := cooldownKey(channelID, keyID, modelName)
	if next == nil {
		return globalKeyCooldown.CompareAndDelete(key, old), nil
	}
	return globalKeyCooldown.CompareAndSwap(key, old, next), nil
}

// AcquireCooldownProbe admits one request after channel/model or key/model
// cooldown. Completion is fenced to its generation: a late result cannot erase
// a newer cooldown or another probe. The caller releases on cancellation too.
func AcquireCooldownProbe(ctx context.Context, channelID, keyID int, modelName string) (finish func(bool), allowed bool, err error) {
	var releases []func(bool)
	var once sync.Once
	finish = func(success bool) {
		once.Do(func() {
			for i := len(releases) - 1; i >= 0; i-- {
				releases[i](success)
			}
		})
	}
	for _, scopeKey := range []int{0, keyID} {
		entry, raw, found, loadErr := loadCooldown(channelID, scopeKey, modelName)
		if loadErr != nil {
			finish(false)
			return finish, false, loadErr
		}
		if !found {
			continue
		}
		if entry.expiresAt.IsZero() || (!entry.expired && time.Now().Before(entry.expiresAt)) || probePending(entry.probeSince, time.Now()) {
			finish(false)
			return finish, false, nil
		}
		claimed := *entry
		claimed.probeSince = time.Now()
		claimed.generation = uuid.NewString()
		ok, claimErr := swapCooldown(ctx, channelID, scopeKey, modelName, entry, raw, &claimed)
		if claimErr != nil || !ok {
			finish(false)
			return finish, false, claimErr
		}
		claimedRaw, _ := json.Marshal(&claimed)
		releases = append(releases, func(success bool) {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var next *keyCooldownEntry
			if !success {
				copy := claimed
				copy.probeSince = time.Time{}
				copy.generation = uuid.NewString()
				next = &copy
			}
			if _, releaseErr := swapCooldown(cleanupCtx, channelID, scopeKey, modelName, &claimed, claimedRaw, next); releaseErr != nil {
				log.Warnf("release cooldown probe: channel_id=%d key_id=%d: %v", channelID, scopeKey, releaseErr)
			}
		})
	}
	return finish, true, nil
}
