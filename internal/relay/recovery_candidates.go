package relay

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/relay/balancer"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

type recoveryCandidate struct {
	channel *model.Channel
	model   string
}
type recoveryCandidates map[string]recoveryCandidate

func (c recoveryCandidates) observe(channel *model.Channel, modelName string) {
	c[fmt.Sprintf("%d:%s", channel.ID, modelName)] = recoveryCandidate{channel, modelName}
}

// Every eligible candidate must have a known cooldown. Disabled, unsupported,
// missing-key and unknown-time targets cannot fabricate a global recovery ETA.
func (c recoveryCandidates) earliestRecovery() time.Time {
	if len(c) == 0 {
		return time.Time{}
	}
	var earliest time.Time
	for _, candidate := range c {
		var keyRemaining time.Duration
		eligible, allKeysCooling := false, true
		for _, key := range candidate.channel.Keys {
			if !key.Enabled || key.ChannelKey == "" || !model.ModelMatches(key.SupportedModels, candidate.model) {
				continue
			}
			eligible = true
			blocked, remaining := balancer.KeyCooldownRemaining(candidate.channel.ID, key.ID, candidate.model)
			if !blocked || remaining <= 0 {
				allKeysCooling = false
				continue
			}
			if keyRemaining == 0 || remaining < keyRemaining {
				keyRemaining = remaining
			}
		}
		if !eligible {
			return time.Time{}
		}
		channelBlocked, channelRemaining := balancer.IsChannelRateLimited(candidate.channel.ID, candidate.model)
		if channelBlocked && channelRemaining <= 0 {
			return time.Time{}
		}
		var remaining time.Duration
		if channelBlocked {
			remaining = channelRemaining
		}
		if allKeysCooling {
			remaining = max(remaining, keyRemaining)
		}
		if remaining <= 0 {
			return time.Time{}
		}
		until := time.Now().Add(remaining)
		if earliest.IsZero() || until.Before(earliest) {
			earliest = until
		}
	}
	return earliest
}

type candidatesCoolingError struct{ until time.Time }

func (e *candidatesCoolingError) Error() string {
	return fmt.Sprintf("all eligible upstream targets are cooling down; earliest scheduled retry at %s", e.until.UTC().Format(time.RFC3339))
}

func writeCandidatesCooling(c *gin.Context, err *candidatesCoolingError) {
	seconds := max(1, int(math.Ceil(time.Until(err.until).Seconds())))
	if c.Writer.Written() {
		writeClientTerminalError(c, nil, outbound.OutboundTypeOpenAIChat, http.StatusServiceUnavailable, err)
		return
	}
	c.Header("Retry-After", strconv.Itoa(seconds))
	c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": err.Error(), "type": "upstream_unavailable", "code": "upstream_recovery_pending", "retry_after": seconds, "retry_at": err.until.UTC().Format(time.RFC3339)}})
}

func requestSpecificRecoveryFailure(decision RetryDecision) bool {
	if decision.UpstreamError == nil {
		return false
	}
	switch decision.UpstreamError.Category {
	case "insufficient_balance", "region_restricted", "protocol_no_route", "model_unavailable":
		return true
	default:
		return false
	}
}

func recoveryTargetKey(channelID, keyID int, modelName string) string {
	return fmt.Sprintf("%d:%d:%s", channelID, keyID, modelName)
}
