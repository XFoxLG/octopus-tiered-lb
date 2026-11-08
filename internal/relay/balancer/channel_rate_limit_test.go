package balancer

import (
	"context"
	"testing"
	"time"
)

func clearChannelRateLimitForTest(channelID int, modelName string) {
	globalChannelRateLimits.Delete(channelRateLimitKey(channelID, modelName))
	globalKeyCooldown.Delete(cooldownKey(channelID, 0, modelName))
}

func TestChannelRateLimitOpensAfterRepeated429s(t *testing.T) {
	channelID := 7801
	modelName := "channel-rate-limit-test"
	defer clearChannelRateLimitForTest(channelID, modelName)

	if blocked, _ := RecordChannelRateLimit(channelID, 11, modelName, false, 0); blocked {
		t.Fatal("first key-scoped 429 should not open the channel")
	}
	if blocked, remaining := RecordChannelRateLimit(channelID, 12, modelName, false, 0); !blocked || remaining <= 0 {
		t.Fatalf("second 429 should open the channel, blocked=%t remaining=%v", blocked, remaining)
	}
	if blocked, remaining := IsChannelRateLimited(channelID, modelName); !blocked || remaining <= 0 {
		t.Fatalf("IsChannelRateLimited() = (%t, %v), want active quarantine", blocked, remaining)
	}

	// Only a successful probe of the expired generation clears its cooldown.
	storeCooldown(channelID, 0, modelName, 429, time.Now().Add(-time.Second))
	finish, allowed, err := AcquireCooldownProbe(context.Background(), channelID, 11, modelName)
	if err != nil || !allowed {
		t.Fatalf("probe=%t %v", allowed, err)
	}
	finish(true)
	if blocked, _ := IsChannelRateLimited(channelID, modelName); blocked {
		t.Fatal("successful request should clear channel rate-limit state")
	}
}

func TestChannelRateLimitExplicitScopeHonorsRetryAfter(t *testing.T) {
	channelID := 7802
	modelName := "channel-rate-limit-explicit"
	defer clearChannelRateLimitForTest(channelID, modelName)

	blocked, remaining := RecordChannelRateLimit(channelID, 21, modelName, true, 45*time.Second)
	if !blocked {
		t.Fatal("explicit account-pool exhaustion should open the channel immediately")
	}
	if remaining < 44*time.Second {
		t.Fatalf("remaining cooldown = %v, want at least 44s", remaining)
	}
}
