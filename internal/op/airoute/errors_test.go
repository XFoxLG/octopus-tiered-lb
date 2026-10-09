package airoute

import (
	"errors"
	"net/http"
	"testing"
)

func TestBuildAIRouteUpstreamStatusError_RateLimitedCarriesI18nKey(t *testing.T) {
	err := buildAIRouteUpstreamStatusError(http.StatusTooManyRequests, []byte(`too many requests`))

	var callErr *aiRouteCallError
	if !errors.As(err, &callErr) {
		t.Fatalf("want *aiRouteCallError, got %T", err)
	}
	if !callErr.Retryable {
		t.Fatal("rate limited error should be retryable")
	}
	if callErr.MessageKey != I18nKeyAIRouteRateLimited {
		t.Fatalf("MessageKey = %q, want %q", callErr.MessageKey, I18nKeyAIRouteRateLimited)
	}
	if callErr.MessageArgs["body_suffix"] != ": too many requests" {
		t.Fatalf("MessageArgs[body_suffix] = %v, want %q", callErr.MessageArgs["body_suffix"], ": too many requests")
	}
}

func TestBuildAIRouteUpstreamStatusError_NonRetryableUsesAIRouteError(t *testing.T) {
	// 404 不在可重试列表：应返回实现 AIRouteI18nError 的 aiRouteError。
	err := buildAIRouteUpstreamStatusError(http.StatusNotFound, nil)

	var i18nErr AIRouteI18nError
	if !errors.As(err, &i18nErr) {
		t.Fatalf("want AIRouteI18nError, got %T", err)
	}
	if key := i18nErr.I18nMessageKey(); key != I18nKeyAIRouteUpstreamStatus {
		t.Fatalf("I18nMessageKey() = %q, want %q", key, I18nKeyAIRouteUpstreamStatus)
	}
	args := i18nErr.I18nMessageArgs()
	if args == nil {
		t.Fatal("I18nMessageArgs() = nil, want status arg")
	}
	if status, ok := args["status"].(int); !ok || status != http.StatusNotFound {
		t.Fatalf("args[status] = %v, want %d", args["status"], http.StatusNotFound)
	}
}

func TestBuildAIRouteUpstreamStatusError_UnavailableCarriesI18nKey(t *testing.T) {
	err := buildAIRouteUpstreamStatusError(http.StatusServiceUnavailable, []byte(`upstream down`))

	var callErr *aiRouteCallError
	if !errors.As(err, &callErr) {
		t.Fatalf("want *aiRouteCallError, got %T", err)
	}
	if callErr.MessageKey != I18nKeyAIRouteUnavailable {
		t.Fatalf("MessageKey = %q, want %q", callErr.MessageKey, I18nKeyAIRouteUnavailable)
	}
	if callErr.MessageArgs["body_suffix"] != ": upstream down" {
		t.Fatalf("MessageArgs[body_suffix] = %v", callErr.MessageArgs["body_suffix"])
	}
}
func TestBuildAIRouteUpstreamStatusError_NoBodyEmptySuffix(t *testing.T) {
	err := buildAIRouteUpstreamStatusError(http.StatusTooManyRequests, nil)

	var callErr *aiRouteCallError
	if !errors.As(err, &callErr) {
		t.Fatalf("want *aiRouteCallError, got %T", err)
	}
	if suffix, ok := callErr.MessageArgs["body_suffix"].(string); !ok || suffix != "" {
		t.Fatalf("MessageArgs[body_suffix] = %v, want empty string (avoid literal {body_suffix} in UI)", callErr.MessageArgs["body_suffix"])
	}
}
