package relay

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestReadUpstreamHTTPErrorPreservesRetryAfter(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"17"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":"try later"}`)),
	}

	err := readUpstreamHTTPError(response, 1024)
	if err == nil {
		t.Fatal("readUpstreamHTTPError() error = nil, want error")
	}
	if got := retryAfterFromError(err); got != 17*time.Second {
		t.Fatalf("retryAfterFromError() = %v, want 17s", got)
	}
	if !strings.Contains(err.Error(), "upstream error: 429") {
		t.Fatalf("error = %q, want upstream status", err)
	}
}

func TestParseRetryAfterHeaderSupportsHTTPDate(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	value := now.Add(25 * time.Second).Format(http.TimeFormat)

	got := parseRetryAfterHeader(value, now)
	if got != 25*time.Second {
		t.Fatalf("parseRetryAfterHeader() = %v, want 25s", got)
	}
}

func TestClassifyRelayErrorDistinguishesChannelWide429(t *testing.T) {
	err := &upstreamHTTPError{
		statusCode: http.StatusTooManyRequests,
		body:       `{"message":"All available accounts exhausted"}`,
	}

	decision := ClassifyRelayError(http.StatusTooManyRequests, err, false)
	if decision.RateLimitScope != RateLimitScopeChannel {
		t.Fatalf("RateLimitScope = %v, want channel", decision.RateLimitScope)
	}
	if shouldHoldOnRateLimit(rateLimitHoldConfig{Enabled: true, Interval: time.Second, MaxWait: time.Minute}, decision) {
		t.Fatal("channel-wide 429 must not enter per-key hold")
	}
}

func TestRateLimitHoldUsesProviderRetryAfterWithinBudget(t *testing.T) {
	cfg := rateLimitHoldConfig{Enabled: true, Interval: 10 * time.Second, MaxWait: 60 * time.Second}
	decision := RetryDecision{
		Scope:          ScopeSameChannel,
		Code:           http.StatusTooManyRequests,
		RateLimitScope: RateLimitScopeKey,
		RetryAfter:     20 * time.Second,
	}

	waitFor, ok := rateLimitHoldWaitDuration(cfg, decision, 30*time.Second)
	if !ok || waitFor != 20*time.Second {
		t.Fatalf("rateLimitHoldWaitDuration() = (%v, %t), want (20s, true)", waitFor, ok)
	}
	if _, ok := rateLimitHoldWaitDuration(cfg, decision, 45*time.Second); ok {
		t.Fatal("retry-after exceeding remaining hold budget should fail over")
	}
}
