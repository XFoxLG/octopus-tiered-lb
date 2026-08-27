package balancer

import (
	"testing"
	"time"
)

func clearChannelRateLimitForTest(channelID int, modelName string) {
	globalChannelRateLimits.Delete(channelRateLimitKey(channelID, modelName))
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

	RecordChannelRateLimitSuccess(channelID, modelName)
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
