package relay

import (
	"errors"
	"net/http"
	"testing"
	"time"

	dbmodel "github.com/lingyuins/octopus/internal/model"
)

func TestFailureHintCacheStoresRetryableStatuses(t *testing.T) {
	resetFailureHintCache()
	recordFailureHint(1, 2, "gpt-4.1", RetryDecision{Scope: ScopeSameChannel, Reason: "rate limited", Code: http.StatusTooManyRequests, IsError: true}, errors.New("429"), 10)
	if _, ok := globalFailureHintCache.get(1, 2, "gpt-4.1"); !ok {
		t.Fatal("expected failure hint to be stored")
	}
}

func TestFailureHintCacheSkipsBadRequest(t *testing.T) {
	resetFailureHintCache()
	recordFailureHint(1, 2, "gpt-4.1", RetryDecision{Scope: ScopeNone, Reason: "bad request", Code: http.StatusBadRequest, IsError: true}, errors.New("400"), 10)
	if _, ok := globalFailureHintCache.get(1, 2, "gpt-4.1"); ok {
		t.Fatal("did not expect failure hint for bad request")
	}
}

func TestFailureHintCacheExpires(t *testing.T) {
	resetFailureHintCache()
	globalFailureHintCache.set(1, 2, "gpt-4.1", failureHintEntry{statusCode: http.StatusTooManyRequests, expiresAt: time.Now().Add(-time.Second)})
	if _, ok := globalFailureHintCache.get(1, 2, "gpt-4.1"); ok {
		t.Fatal("expected expired hint to be removed")
	}
}

func TestPrepareCandidateSkipsFailureHint(t *testing.T) {
	resetFailureHintCache()
	globalFailureHintCache.set(1, 2, "gpt-4.1", failureHintEntry{statusCode: http.StatusTooManyRequests, expiresAt: time.Now().Add(time.Second)})
	reason := failureHintSkipReason(failureHintEntry{statusCode: http.StatusTooManyRequests, expiresAt: time.Now().Add(time.Second)})
	if reason == "" {
		t.Fatal("expected skip reason")
	}
	_ = dbmodel.Channel{}
}
