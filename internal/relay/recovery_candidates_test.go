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
		if recorder.Code != 503 || recorder.Header().Get("Retry-After") != "900" || !strings.Contains(recorder.Body.String(), "upstream_recovery_pending") {
			t.Fatalf("response=%d %v %s", recorder.Code, recorder.Header(), recorder.Body)
		}
		balancer.ClearKeyCooldown(91002, 91002, "model-a")
		if !candidates.earliestRecovery().IsZero() {
			t.Fatal("available candidate incorrectly hidden by global cooldown")
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
	if recorder.Code != 200 || recorder.Header().Get("Retry-After") != "" || strings.Count(recorder.Body.String(), "event: error") != 1 || strings.Contains(recorder.Body.String(), "[DONE]") {
		t.Fatalf("invalid committed response: %d %v %s", recorder.Code, recorder.Header(), recorder.Body)
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
