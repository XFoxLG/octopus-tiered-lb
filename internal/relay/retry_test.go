package relay

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func TestClassifyRelayError_Success(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		err        error
		written    bool
		wantScope  RetryScope
		wantError  bool
	}{
		{
			name:       "success 200",
			statusCode: 200,
			err:        nil,
			written:    false,
			wantScope:  ScopeNone,
			wantError:  false,
		},
		{
			name:       "success 201",
			statusCode: 201,
			err:        nil,
			written:    false,
			wantScope:  ScopeNone,
			wantError:  false,
		},
		{
			name:       "success 299",
			statusCode: 299,
			err:        nil,
			written:    false,
			wantScope:  ScopeNone,
			wantError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyRelayError(tt.statusCode, tt.err, tt.written)
			if got.Scope != tt.wantScope {
				t.Errorf("ClassifyRelayError() Scope = %v, want %v", got.Scope, tt.wantScope)
			}
			if got.IsError != tt.wantError {
				t.Errorf("ClassifyRelayError() IsError = %v, want %v", got.IsError, tt.wantError)
			}
		})
	}
}

func TestClassifyRelayError_Written(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		err        error
		written    bool
		wantScope  RetryScope
		wantError  bool
	}{
		{
			name:       "written with error",
			statusCode: 500,
			err:        errors.New("some error"),
			written:    true,
			wantScope:  ScopeAbortAll,
			wantError:  true,
		},
		{
			name:       "written with success",
			statusCode: 200,
			err:        nil,
			written:    true,
			wantScope:  ScopeNone,
			wantError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyRelayError(tt.statusCode, tt.err, tt.written)
			if got.Scope != tt.wantScope {
				t.Errorf("ClassifyRelayError() Scope = %v, want %v", got.Scope, tt.wantScope)
			}
			if got.IsError != tt.wantError {
				t.Errorf("ClassifyRelayError() IsError = %v, want %v", got.IsError, tt.wantError)
			}
		})
	}
}

func TestClassifyRelayError_HTTP4xx(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		err        error
		wantScope  RetryScope
		wantReason string
	}{
		{
			name:       "400 bad request",
			statusCode: 400,
			err:        errors.New("bad request"),
			wantScope:  ScopeNone,
			wantReason: "bad request",
		},
		{
			name:       "401 unauthorized",
			statusCode: 401,
			err:        errors.New("unauthorized"),
			wantScope:  ScopeSameChannel,
			wantReason: "unauthorized",
		},
		{
			name:       "403 forbidden",
			statusCode: 403,
			err:        errors.New("forbidden"),
			wantScope:  ScopeSameChannel,
			wantReason: "forbidden",
		},
		{
			name:       "404 not found",
			statusCode: 404,
			err:        errors.New("not found"),
			wantScope:  ScopeNextChannel,
			wantReason: "not found",
		},
		{
			name:       "429 rate limited",
			statusCode: 429,
			err:        errors.New("rate limited"),
			wantScope:  ScopeSameChannel,
			wantReason: "rate limited",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyRelayError(tt.statusCode, tt.err, false)
			if got.Scope != tt.wantScope {
				t.Errorf("ClassifyRelayError() Scope = %v, want %v", got.Scope, tt.wantScope)
			}
			if !got.IsError {
				t.Errorf("ClassifyRelayError() IsError should be true for %d", tt.statusCode)
			}
		})
	}
}

func TestClassifyRelayError_HTTP5xx(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		err        error
		wantScope  RetryScope
	}{
		{
			name:       "500 internal server error",
			statusCode: 500,
			err:        errors.New("internal error"),
			wantScope:  ScopeNextChannel,
		},
		{
			name:       "502 bad gateway",
			statusCode: 502,
			err:        errors.New("bad gateway"),
			wantScope:  ScopeNextChannel,
		},
		{
			name:       "503 service unavailable",
			statusCode: 503,
			err:        errors.New("service unavailable"),
			wantScope:  ScopeNextChannel,
		},
		{
			name:       "504 gateway timeout",
			statusCode: 504,
			err:        errors.New("gateway timeout"),
			wantScope:  ScopeNextChannel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyRelayError(tt.statusCode, tt.err, false)
			if got.Scope != tt.wantScope {
				t.Errorf("ClassifyRelayError() Scope = %v, want %v", got.Scope, tt.wantScope)
			}
			if !got.IsError {
				t.Errorf("ClassifyRelayError() IsError should be true for %d", tt.statusCode)
			}
		})
	}
}

func TestClassifyRelayError_NetworkErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantScope RetryScope
	}{
		{
			name:      "timeout error",
			err:       &timeoutError{},
			wantScope: ScopeNextChannel,
		},
		{
			name:      "connection refused",
			err:       errors.New("connection refused"),
			wantScope: ScopeNextChannel,
		},
		{
			name:      "connection reset",
			err:       errors.New("connection reset by peer"),
			wantScope: ScopeNextChannel,
		},
		{
			name:      "network unreachable",
			err:       errors.New("network is unreachable"),
			wantScope: ScopeNextChannel,
		},
		{
			name:      "DNS error",
			err:       errors.New("no such host"),
			wantScope: ScopeNextChannel,
		},
		{
			name:      "generic error",
			err:       errors.New("some unknown error"),
			wantScope: ScopeNextChannel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyRelayError(0, tt.err, false)
			if got.Scope != tt.wantScope {
				t.Errorf("ClassifyRelayError() Scope = %v, want %v", got.Scope, tt.wantScope)
			}
			if !got.IsError {
				t.Errorf("ClassifyRelayError() IsError should be true")
			}
		})
	}
}

func TestClassifyRelayError_TransformerError(t *testing.T) {
	// 200 status code but with error (transformer error)
	got := ClassifyRelayError(200, errors.New("transformer failed"), false)
	if got.Scope != ScopeNextChannel {
		t.Errorf("ClassifyRelayError() Scope = %v, want %v", got.Scope, ScopeNextChannel)
	}
	if !got.IsError {
		t.Errorf("ClassifyRelayError() IsError should be true for transformer error")
	}
}

func TestRelayAttemptForward_ReturnsUpstreamStatusCodeOnError(t *testing.T) {
	ra := &relayAttempt{}
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(strings.NewReader("rate limited")),
	}

	statusCode, err := ra.handleForwardResponse(resp)
	if statusCode != http.StatusTooManyRequests {
		t.Fatalf("handleForwardResponse() statusCode = %d, want %d", statusCode, http.StatusTooManyRequests)
	}
	if err == nil {
		t.Fatal("handleForwardResponse() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("handleForwardResponse() error = %q, want to contain 429", err.Error())
	}
}

func TestGetMaxAttemptsPerCandidate_Default(t *testing.T) {
	got := getMaxAttemptsPerCandidate()
	want := defaultMaxRetryPerCandidate + 1
	if got != want {
		t.Fatalf("getMaxAttemptsPerCandidate() = %d, want %d", got, want)
	}
}

func TestRetryScope_String(t *testing.T) {
	tests := []struct {
		scope RetryScope
		want  string
	}{
		{ScopeNone, "none"},
		{ScopeSameChannel, "same_channel"},
		{ScopeNextChannel, "next_channel"},
		{ScopeAbortAll, "abort_all"},
		{RetryScope(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.scope.String(); got != tt.want {
				t.Errorf("RetryScope.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsRetryAllowed(t *testing.T) {
	tests := []struct {
		name         string
		decision     RetryDecision
		wantContinue bool
		wantSwitch   bool
	}{
		{
			name:         "ScopeNone - no retry",
			decision:     RetryDecision{Scope: ScopeNone},
			wantContinue: false,
			wantSwitch:   false,
		},
		{
			name:         "ScopeSameChannel - retry same channel",
			decision:     RetryDecision{Scope: ScopeSameChannel},
			wantContinue: true,
			wantSwitch:   false,
		},
		{
			name:         "ScopeNextChannel - switch channel",
			decision:     RetryDecision{Scope: ScopeNextChannel},
			wantContinue: true,
			wantSwitch:   true,
		},
		{
			name:         "ScopeAbortAll - stop all",
			decision:     RetryDecision{Scope: ScopeAbortAll},
			wantContinue: false,
			wantSwitch:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			continueRetry, switchChannel := IsRetryAllowed(tt.decision)
			if continueRetry != tt.wantContinue {
				t.Errorf("IsRetryAllowed() continueRetry = %v, want %v", continueRetry, tt.wantContinue)
			}
			if switchChannel != tt.wantSwitch {
				t.Errorf("IsRetryAllowed() switchChannel = %v, want %v", switchChannel, tt.wantSwitch)
			}
		})
	}
}

// timeoutError implements net.Error with Timeout() returning true
type timeoutError struct{}

func (e *timeoutError) Error() string   { return "timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

var _ net.Error = &timeoutError{}

func TestExtractUpstreamErrorDetail(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "nil error returns empty",
			err:  nil,
			want: "",
		},
		{
			name: "upstream error with status and body",
			err:  errors.New("upstream error: 429: {\"error\":\"rate limit exceeded\"}"),
			want: "429: {\"error\":\"rate limit exceeded\"}",
		},
		{
			name: "upstream error body too large",
			err:  errors.New("upstream error: 500: response body too large"),
			want: "500: response body too large",
		},
		{
			name: "non-http network error returned as-is",
			err:  errors.New("connection refused"),
			want: "connection refused",
		},
		{
			name: "transformer error returned as-is",
			err:  errors.New("failed to create request: invalid payload"),
			want: "failed to create request: invalid payload",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractUpstreamErrorDetail(tt.err); got != tt.want {
				t.Errorf("extractUpstreamErrorDetail() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractUpstreamErrorDetailFromWrappedAttemptError(t *testing.T) {
	// attempt() 会把 forward 错误再包一层 "channel X adapter=Y attempt N/M: ..."
	// 透传逻辑必须仍能抽出上游 body。
	err := errors.New(`channel foo adapter=openai-chat attempt 1/4: upstream error: 400: {"error":{"message":"输入内容过长","type":"invalid_request_error","code":"context_length_exceeded"}}`)
	got := extractUpstreamErrorDetail(err)
	want := `400: {"error":{"message":"输入内容过长","type":"invalid_request_error","code":"context_length_exceeded"}}`
	if got != want {
		t.Fatalf("extractUpstreamErrorDetail() = %q, want %q", got, want)
	}
}

func TestWriteClientTerminalErrorPassesUpstreamJSONBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	upstreamBody := `{"error":{"message":"输入内容过长，已超过当前模型的上下文限制，请减少历史消息、文件内容或提示词后重试。","type":"invalid_request_error","param":"","code":"context_length_exceeded"}}`
	err := fmt.Errorf("channel demo adapter=openai-chat attempt 1/4: upstream error: 400: %s", upstreamBody)

	writeClientTerminalError(c, outbound.OutboundTypeOpenAIChat, http.StatusBadRequest, err)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	if got := strings.TrimSpace(w.Body.String()); got != upstreamBody {
		t.Fatalf("body = %s, want %s", got, upstreamBody)
	}
	if !strings.Contains(w.Body.String(), "context_length_exceeded") {
		t.Fatalf("body missing context_length_exceeded: %s", w.Body.String())
	}
}

func TestWriteClientTerminalErrorWrapsPlainTextBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	err := errors.New("upstream error: 400: prompt is too long")
	writeClientTerminalError(c, outbound.OutboundTypeOpenAIChat, http.StatusBadRequest, err)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := jsonAPI.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal body: %v; body=%s", err, w.Body.String())
	}
	if payload.Error.Message != "prompt is too long" {
		t.Fatalf("message = %q, want prompt is too long", payload.Error.Message)
	}
	if payload.Error.Type != "invalid_request_error" {
		t.Fatalf("type = %q, want invalid_request_error", payload.Error.Type)
	}
}

func TestWriteClientTerminalErrorFallsBackToBadGatewayWithoutHTTPStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	writeClientTerminalError(c, outbound.OutboundTypeOpenAIChat, 0, errors.New("connection refused"))

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadGateway, w.Body.String())
	}
}

func TestGetMaxRetryPerCandidate_AllowsZero(t *testing.T) {
	// 设为 0 表示该候选渠道内不进行 Key 级重试（issue #95 改动1）。
	// 这里只验证函数在内存未配置时回退默认值，0 的行为由 setting 校验保证。
	got := getMaxRetryPerCandidate()
	if got != defaultMaxRetryPerCandidate {
		t.Fatalf("getMaxRetryPerCandidate() = %d, want default %d", got, defaultMaxRetryPerCandidate)
	}
}

func TestGetMaxTotalAttempts_FallsBackToDefaultWhenZero(t *testing.T) {
	// issue #192：所有渠道均不可用（key 全冷却/熔断）时，若 0 被当作「不限制」会让
	// attempts 无上限膨胀，单条 relay_log 可达数百 MB。0 现在回退到内置默认上限。
	got := getMaxTotalAttempts()
	if got != defaultMaxTotalAttempts {
		t.Fatalf("getMaxTotalAttempts() = %d, want default %d", got, defaultMaxTotalAttempts)
	}
	if got <= 0 {
		t.Fatalf("getMaxTotalAttempts() = %d, want a positive safety cap", got)
	}
}
