package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStructuredUpstreamRecovery(t *testing.T) {
	for _, tt := range []struct {
		name, body, category string
		status               int
		scope                RetryScope
		wait                 time.Duration
	}{
		{"quota is not invalid credentials", `{"error":{"type":"new_api_error","code":"insufficient_user_quota","message":"precharge exceeds remaining balance"}}`, "insufficient_balance", 403, ScopeSameChannel, 0},
		{"model cooldown", `{"error":{"code":"model_cooldown","model":"claude-opus-4-6-thinking","reset_seconds":904,"reset_time":"15m3s"}}`, "model_cooldown", 429, ScopeSameChannel, 904 * time.Second},
		{"account pool without structured recovery time", `{"error":{"code":"all_accounts_limited","type":"rate_limit_error","message":"All accounts limited. Wait 5887s."}}`, "account_pool_exhausted", 503, ScopeNextChannel, 0},
		{"protocol route unavailable", `{"error":{"type":"no_route","message":"model only supports chat"}}`, "protocol_no_route", 503, ScopeNextChannel, 0},
		{"regional restriction", `{"error":{"code":"unsupported_country_region_territory"}}`, "region_restricted", 403, ScopeNextChannel, 0},
		{"credential invalid", `{"error":{"code":"invalid_api_key"}}`, "credential_invalid", 401, ScopeSameChannel, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := readUpstreamHTTPError(&http.Response{StatusCode: tt.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tt.body))}, 1024)
			d := ClassifyRelayError(tt.status, err, false)
			if d.Scope != tt.scope || d.Code != tt.status || d.RetryAfter != tt.wait || d.UpstreamError == nil || d.UpstreamError.Category != tt.category {
				t.Fatalf("decision = %+v, evidence=%+v", d, d.UpstreamError)
			}
			if shouldRecordChannelFailure(d) {
				t.Fatal("structured non-health failure penalized channel")
			}
			if tt.wait > 0 && shouldHoldOnRateLimit(rateLimitHoldConfig{Enabled: true, Interval: 10 * time.Second, MaxWait: time.Minute}, d) {
				if _, ok := rateLimitHoldWaitDuration(rateLimitHoldConfig{Enabled: true, Interval: 10 * time.Second, MaxWait: time.Minute}, d, 0); ok {
					t.Fatal("long cooldown entered short hold")
				}
			}
		})
	}
}

func TestRecoveryClassificationDoesNotMatchArbitraryErrors(t *testing.T) {
	for _, err := range []error{errors.New("all accounts exhausted model_cooldown"), context.Canceled, context.DeadlineExceeded} {
		if evidence := upstreamErrorEvidence(err); evidence != nil {
			t.Fatalf("local error got upstream evidence: %+v", evidence)
		}
		if isChannelWideRateLimitError(err) {
			t.Fatal("local error matched provider rule")
		}
	}
	for _, body := range []string{`{"choices":[{"message":{"content":"model_cooldown 904"}}]}`, `{"error":{"code":"model_cooldown","reset_seconds":"904"}}`, `{"error":{"code":"model_cooldown","message":"retry in 904 seconds"}}`} {
		err := readUpstreamHTTPError(&http.Response{StatusCode: 429, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, 1024)
		if retryAfterFromError(err) != 0 {
			t.Fatal("unverified field interpreted as recovery time")
		}
	}
	if d := ClassifyRelayError(200, nil, true); d.IsError {
		t.Fatal("ordinary success became error")
	}
}

type recoveryReadError struct{}

func (recoveryReadError) Read([]byte) (int, error) { return 0, context.Canceled }
func (recoveryReadError) Close() error             { return nil }
func TestUpstreamErrorPreservesReadFailureAndBoundedRetryAfter(t *testing.T) {
	err := readUpstreamHTTPError(&http.Response{StatusCode: 503, Header: http.Header{"Retry-After": []string{"17"}}, Body: recoveryReadError{}}, 1024)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost transport error: %v", err)
	}
	var upstream *upstreamHTTPError
	if !errors.As(err, &upstream) || upstream.statusCode != 503 || retryAfterFromError(err) != 17*time.Second {
		t.Fatalf("lost HTTP metadata: %v", err)
	}
	if got := parseRetryAfterHeader("9223372036854775807", time.Now()); got != 24*time.Hour {
		t.Fatalf("overflowed Retry-After: %v", got)
	}
}
