package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/relay/balancer"
	llmmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func TestAllCandidatesCoolingReturnsAccurate503(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		candidates := make(recoveryCandidates)
		for id, wait := range map[int]time.Duration{91001: 15 * time.Minute, 91002: 20 * time.Minute} {
			channel := &model.Channel{ID: id, Enabled: true, Keys: []model.ChannelKey{{ID: id, Enabled: true, ChannelKey: "synthetic"}}}
			candidates.observe(channel, "model-a")
			balancer.RecordKeyCooldownWithRetryAfter(id, id, "model-a", 429, wait)
			t.Cleanup(func() { balancer.ClearKeyCooldown(id, id, "model-a") })
		}
		until := candidates.earliestRecovery()
		if got := time.Until(until); got != 15*time.Minute {
			t.Fatalf("recovery=%v", got)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		deadline, _ := ctx.Deadline()
		if !until.After(deadline) {
			t.Fatal("fixture must exceed request budget")
		}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", http.NoBody)
		writeCandidatesCooling(c, &candidatesCoolingError{until})
		if recorder.Code != 503 || recorder.Header().Get("Retry-After") != "900" ||
			!strings.Contains(recorder.Body.String(), "upstream_recovery_pending") ||
			!strings.Contains(recorder.Body.String(), "北京时间，UTC+08:00") ||
			strings.Contains(recorder.Body.String(), "all eligible upstream targets") {
			t.Fatalf("response=%d %v %s", recorder.Code, recorder.Header(), recorder.Body)
		}
		balancer.ClearKeyCooldown(91002, 91002, "model-a")
		if !candidates.earliestRecovery().IsZero() {
			t.Fatal("available candidate incorrectly hidden by global cooldown")
		}
	})
}

func TestCandidatesCoolingClientMessageUsesBeijingTimeAndKeepsDiagnosticEnglish(t *testing.T) {
	err := &candidatesCoolingError{
		until: time.Date(2026, time.November, 21, 17, 18, 14, 0, time.UTC),
	}
	const wantClient = "所有可用上游目标均在冷却中，预计最早于 2026-11-22 01:18:14（北京时间，UTC+08:00）可重试。"
	if got := err.clientMessage(); got != wantClient {
		t.Fatalf("clientMessage() = %q, want %q", got, wantClient)
	}
	if got, want := err.Error(), "all eligible upstream targets are cooling down; earliest scheduled retry at 2026-11-21T17:18:14Z"; got != want {
		t.Fatalf("Error() = %q, want diagnostic %q", got, want)
	}
}

func TestAutoSyncUnknownModelKeyCooldownContributesRecoveryTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const channelID, keyID = 91005, 91005
		channel := &model.Channel{
			ID:                channelID,
			Enabled:           true,
			AutoSyncKeyModels: true,
			Keys:              []model.ChannelKey{{ID: keyID, Enabled: true, ChannelKey: "synthetic", SupportedModels: "other-model"}},
		}
		candidates := make(recoveryCandidates)
		candidates.observe(channel, "configured-model")
		balancer.RecordKeyCooldownWithRetryAfter(channelID, keyID, "configured-model", http.StatusTooManyRequests, 15*time.Minute)
		t.Cleanup(func() { balancer.ClearKeyCooldown(channelID, keyID, "configured-model") })

		if got := time.Until(candidates.earliestRecovery()); got != 15*time.Minute {
			t.Fatalf("recovery=%v, want cooldown for the unknown auto-sync key", got)
		}
	})
}

func TestCandidatesCoolingAfterPingUsesOneStreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", http.NoBody)
	w := installRelayResponseWriter(c)
	defer w.close()
	stop, err := w.keepalive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stop()
	writeCandidatesCooling(c, &candidatesCoolingError{time.Now().Add(15 * time.Minute)})
	if recorder.Code != 200 || recorder.Header().Get("Retry-After") != "" ||
		strings.Count(recorder.Body.String(), "event: error") != 1 ||
		strings.Contains(recorder.Body.String(), "[DONE]") ||
		!strings.Contains(recorder.Body.String(), "北京时间，UTC+08:00") ||
		strings.Contains(recorder.Body.String(), "all eligible upstream targets") {
		t.Fatalf("invalid committed response: %d %v %s", recorder.Code, recorder.Header(), recorder.Body)
	}
}

func TestModelTargetsUnavailableWritesStructured503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", http.NoBody)

	writeClientTerminalError(c, nil, outbound.OutboundTypeOpenAIChat, http.StatusServiceUnavailable, &modelTargetsUnavailableClientError{})

	body := recorder.Body.String()
	for _, want := range []string{
		`"type":"upstream_unavailable"`,
		`"code":"upstream_model_unavailable"`,
		"当前没有上游接受分组配置的模型映射",
	} {
		if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(body, want) {
			t.Fatalf("response=%d body=%s, want %q", recorder.Code, body, want)
		}
	}
}

func TestModelTargetsUnavailableAfterStreamCommitWritesOneErrorEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", http.NoBody)
	w := installRelayResponseWriter(c)
	defer w.close()
	stop, err := w.keepalive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stop()

	writeClientTerminalError(c, nil, outbound.OutboundTypeOpenAIChat, http.StatusServiceUnavailable, &modelTargetsUnavailableClientError{})

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK ||
		strings.Count(body, "event: error") != 1 ||
		!strings.Contains(body, `"code":"upstream_model_unavailable"`) ||
		strings.Contains(body, "[DONE]") {
		t.Fatalf("invalid committed response: %d %s", recorder.Code, body)
	}
}

func TestRequestSpecificQuotaDoesNotQuarantineKey(t *testing.T) {
	err := &upstreamHTTPError{statusCode: 403, body: `{"error":{"code":"insufficient_user_quota"}}`}
	d := ClassifyRelayError(403, err, false)
	recordUpstreamRecovery(91003, 91003, "opus", d)
	recordFailureHint(91003, 91003, "opus", d, err, 300)
	if blocked, _ := balancer.KeyCooldownRemaining(91003, 91003, "opus"); blocked {
		t.Fatal("large-request precharge failure blocks smaller requests")
	}
	if _, ok := globalFailureHintCache.get(91003, 91003, "opus"); ok {
		t.Fatal("quota failure cached as auth failure")
	}
	if !requestSpecificRecoveryFailure(d) {
		t.Fatal("same request should not retry rejected target")
	}
}

func TestUpstreamRecoveryEvidencePersistsWithoutMessage(t *testing.T) {
	trace := &relayRequestTrace{id: "synthetic", startedAt: time.Now()}
	trace.startDiagnosticAttempt(1, 42, "chat", context.Background())
	trace.recordUpstreamError(1, &upstreamHTTPError{statusCode: 429, body: `{"error":{"code":"model_cooldown","message":"private message omitted"}}`, receivedAt: time.Now(), retryAfter: 904 * time.Second})
	d := trace.diagnosticsSnapshot().Attempts[0].UpstreamError
	if d == nil || d.StatusCode != 429 || d.Category != "model_cooldown" || d.Source != "upstream_http_error" || d.RecoveryAt == 0 {
		t.Fatalf("evidence=%+v", d)
	}
}

func TestRecoveryAdmissionRejectionDoesNotConsumeForwardBudget(t *testing.T) {
	ra := newTerminationFidelityRelayAttempt(t, httptest.NewRecorder(), nil, nil)
	ra.channel.ID = 91004
	ra.usedKey = model.ChannelKey{ID: 91004, Enabled: true, ChannelKey: "synthetic"}
	ra.internalRequest = &llmmodel.InternalLLMRequest{Model: "fixture"}
	ra.iter = balancer.NewIterator(model.Group{}, 0, "fixture", nil)
	balancer.RecordKeyCooldownWithRetryAfter(91004, 91004, "fixture", 429, time.Minute)
	t.Cleanup(func() { balancer.ClearKeyCooldown(91004, 91004, "fixture") })
	result := ra.attempt()
	if result.Decision.Scope != ScopeChannelCapacity || ra.iter.ForwardedAttempts() != 0 {
		t.Fatalf("decision=%+v forwarded=%d", result.Decision, ra.iter.ForwardedAttempts())
	}
	attempts := ra.iter.Attempts()
	if len(attempts) != 1 || attempts[0].Status != model.AttemptSkipped {
		t.Fatalf("attempts=%+v", attempts)
	}
}
