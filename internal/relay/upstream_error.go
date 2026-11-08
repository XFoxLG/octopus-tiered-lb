package relay

import (
	"context"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/relay/balancer"
	"github.com/lingyuins/octopus/internal/utils/json"
)

// upstreamHTTPError keeps the response metadata that is needed after the
// response body has been consumed. In particular, Retry-After must survive
// the forward() boundary so the retry policy can make a timing-aware choice.
type upstreamHTTPError struct {
	statusCode int
	body       string
	retryAfter time.Duration
	receivedAt time.Time
	cause      error
}

func (err *upstreamHTTPError) Unwrap() error { return err.cause }

func (err *upstreamHTTPError) Error() string {
	if err == nil {
		return ""
	}
	if err.body == "" {
		if err.cause != nil {
			return fmt.Sprintf("upstream error: %d: failed to read response body: %v", err.statusCode, err.cause)
		}
		return fmt.Sprintf("upstream error: %d", err.statusCode)
	}
	return fmt.Sprintf("upstream error: %d: %s", err.statusCode, err.body)
}

// readUpstreamHTTPError consumes a bounded upstream error body and preserves
// Retry-After. The same helper is used by LLM and media relay paths so their
// 429 decisions do not silently diverge.
func readUpstreamHTTPError(response *http.Response, maxBodyBytes int64) error {
	if response == nil {
		return errors.New("upstream response is nil")
	}
	if maxBodyBytes <= 0 {
		maxBodyBytes = 64 << 10
	}

	now := time.Now()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if int64(len(body)) > maxBodyBytes {
		body = body[:maxBodyBytes]
		readErr = errors.New("upstream error response body too large")
	}
	result := &upstreamHTTPError{
		statusCode: response.StatusCode,
		body:       string(body),
		retryAfter: parseRetryAfterHeader(response.Header.Get("Retry-After"), now),
		receivedAt: now,
		cause:      readErr,
	}
	if fields, ok := upstreamErrorFields(result); ok && fields.code == "model_cooldown" {
		var seconds int64
		if json.Unmarshal(fields.resetSeconds, &seconds) == nil && seconds > 0 {
			result.retryAfter = max(result.retryAfter, retryAfterSeconds(seconds))
		}
	}
	return result
}

func retryAfterFromError(err error) time.Duration {
	var upstreamErr *upstreamHTTPError
	if !errors.As(err, &upstreamErr) || upstreamErr == nil {
		return 0
	}
	return upstreamErr.retryAfter
}

func upstreamErrorBody(err error) string {
	var upstreamErr *upstreamHTTPError
	if !errors.As(err, &upstreamErr) || upstreamErr == nil {
		return ""
	}
	return upstreamErr.body
}

// parseRetryAfterHeader accepts both HTTP-date and delta-seconds forms. A
// bounded maximum prevents an untrusted upstream from pinning a key forever.
func parseRetryAfterHeader(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}

	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return retryAfterSeconds(seconds)
	}

	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return capRetryAfter(when.Sub(now))
}

func retryAfterSeconds(seconds int64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	if seconds >= int64((24*time.Hour)/time.Second) {
		return 24 * time.Hour
	}
	return time.Duration(seconds) * time.Second
}

type providerErrorFields struct {
	code, typ, message string
	resetSeconds       json.RawMessage
}

// Only real HTTP error objects are eligible. Local cancellation/timeouts and
// ordinary generated text cannot supply provider rules or recovery times.
func upstreamErrorFields(err *upstreamHTTPError) (providerErrorFields, bool) {
	if err.statusCode < 400 || err.cause != nil {
		return providerErrorFields{}, false
	}
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(err.body), &root) != nil {
		return providerErrorFields{}, false
	}
	if nested, ok := root["error"]; ok {
		if json.Unmarshal(nested, &root) != nil {
			return providerErrorFields{}, false
		}
	}
	var fields providerErrorFields
	fields.code = errorScalar(root["code"])
	_ = json.Unmarshal(root["type"], &fields.typ)
	_ = json.Unmarshal(root["message"], &fields.message)
	fields.resetSeconds = root["reset_seconds"]
	return fields, fields.code != "" || fields.typ != "" || fields.message != ""
}

func errorScalar(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var number stdjson.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func upstreamErrorEvidence(err error) *model.RelayUpstreamError {
	var upstream *upstreamHTTPError
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &upstream) {
		return nil
	}
	fields, _ := upstreamErrorFields(upstream)
	evidence := &model.RelayUpstreamError{StatusCode: upstream.statusCode, Code: fields.code, Type: fields.typ, Source: "upstream_http_error"}
	for _, code := range []string{fields.code, fields.typ} {
		switch code {
		case "insufficient_user_quota", "insufficient_quota":
			evidence.Category = "insufficient_balance"
		case "invalid_api_key", "authentication_error":
			evidence.Category = "credential_invalid"
		case "model_cooldown":
			evidence.Category = "model_cooldown"
		case "all_accounts_limited":
			evidence.Category = "account_pool_exhausted"
		case "overloaded_error", "capacity_exceeded":
			evidence.Category = "capacity_unavailable"
		case "unsupported_country_region_territory", "region_restricted":
			evidence.Category = "region_restricted"
		case "no_route":
			evidence.Category = "protocol_no_route"
		case "model_not_found":
			evidence.Category = "model_unavailable"
		case "invalid_request_error":
			if upstream.statusCode == 400 {
				evidence.Category = "invalid_request"
			}
		}
		if evidence.Category != "" {
			break
		}
	}
	if upstream.retryAfter > 0 && !upstream.receivedAt.IsZero() {
		evidence.RecoveryAt = upstream.receivedAt.Add(upstream.retryAfter).UnixMilli()
	}
	return evidence
}

var errRecoveryProbeUnavailable = errors.New("upstream target is cooling down or has a recovery probe in flight")

// Recovery is separate from health accounting. Request-specific quota and
// routing errors do not quarantine a credential for smaller/different requests.
func recordUpstreamRecovery(channelID, keyID int, modelName string, decision RetryDecision) {
	if decision.Code < 400 {
		return
	}
	if decision.SkipFailureAccounting && decision.UpstreamError == nil {
		return
	}
	if e := decision.UpstreamError; e != nil {
		switch e.Category {
		case "insufficient_balance", "protocol_no_route", "region_restricted", "model_unavailable", "invalid_request":
			return
		}
	}
	balancer.RecordKeyCooldownWithRetryAfter(channelID, keyID, modelName, decision.Code, decision.RetryAfter)
	if decision.Code == http.StatusTooManyRequests || decision.RateLimitScope == RateLimitScopeChannel {
		balancer.RecordChannelRateLimit(channelID, keyID, modelName, decision.RateLimitScope == RateLimitScopeChannel, decision.RetryAfter)
	}
}

func classifyStructuredUpstreamError(status int, err error) (RetryDecision, bool) {
	evidence := upstreamErrorEvidence(err)
	if evidence == nil || evidence.Category == "" {
		return RetryDecision{}, false
	}
	d := RetryDecision{Code: status, IsError: true, SkipFailureAccounting: true, UpstreamError: evidence, RetryAfter: retryAfterFromError(err), Reason: evidence.Category}
	switch evidence.Category {
	case "insufficient_balance", "credential_invalid":
		d.Scope = ScopeSameChannel
	case "model_cooldown":
		d.Scope = ScopeSameChannel
		d.RateLimitScope = RateLimitScopeKey
	case "account_pool_exhausted":
		d.Scope = ScopeNextChannel
		d.RateLimitScope = RateLimitScopeChannel
	case "capacity_unavailable":
		d.Scope = ScopeNextChannel
		d.RateLimitScope = RateLimitScopeKey
	case "region_restricted", "protocol_no_route", "model_unavailable":
		d.Scope = ScopeNextChannel
	case "invalid_request":
		d.Scope = ScopeNone
	}
	return d, true
}

func capRetryAfter(duration time.Duration) time.Duration {
	const maximumRetryAfter = 24 * time.Hour
	if duration <= 0 {
		return 0
	}
	if duration > maximumRetryAfter {
		return maximumRetryAfter
	}
	return duration
}

// isChannelWideRateLimitError recognizes provider messages that describe an
// exhausted account pool rather than one rejected credential. Generic "rate
// limit exceeded" messages intentionally remain key-scoped and are handled by
// key rotation first.
func isChannelWideRateLimitError(err error) bool {
	var upstream *upstreamHTTPError
	if !errors.As(err, &upstream) {
		return false
	}
	fields, ok := upstreamErrorFields(upstream)
	if !ok {
		return false
	}
	if fields.code == "all_accounts_limited" {
		return true
	}
	body := strings.ToLower(fields.message)

	channelWideMarkers := []string{
		"all available accounts exhausted",
		"all available account exhausted",
		"all accounts exhausted",
		"no capacity available",
		"no available accounts",
		"no available account",
		"account pool exhausted",
		"account pool is exhausted",
		"all accounts are rate limited",
		"all accounts rate limited",
		"no healthy accounts",
	}
	for _, marker := range channelWideMarkers {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
