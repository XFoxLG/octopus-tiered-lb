package relay

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// upstreamHTTPError keeps the response metadata that is needed after the
// response body has been consumed. In particular, Retry-After must survive
// the forward() boundary so the retry policy can make a timing-aware choice.
type upstreamHTTPError struct {
	statusCode int
	body       string
	retryAfter time.Duration
}

func (err *upstreamHTTPError) Error() string {
	if err == nil {
		return ""
	}
	if err.body == "" {
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

	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	if int64(len(body)) > maxBodyBytes {
		return &upstreamHTTPError{
			statusCode: response.StatusCode,
			body:       "response body too large",
			retryAfter: parseRetryAfterHeader(response.Header.Get("Retry-After"), time.Now()),
		}
	}

	return &upstreamHTTPError{
		statusCode: response.StatusCode,
		body:       string(body),
		retryAfter: parseRetryAfterHeader(response.Header.Get("Retry-After"), time.Now()),
	}
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
		return capRetryAfter(time.Duration(seconds) * time.Second)
	}

	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return capRetryAfter(when.Sub(now))
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
	body := strings.ToLower(strings.TrimSpace(upstreamErrorBody(err)))
	if body == "" {
		body = strings.ToLower(errString(err))
	}

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
