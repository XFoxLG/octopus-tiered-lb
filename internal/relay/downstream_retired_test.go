package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	dbmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/ratelimitstore"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
)

func TestHandlerIgnoresLegacyDownstreamQuotas(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	calls := 0
	prepareIndependentRequestFixture(t, func(request *http.Request) (*http.Response, error) {
		calls++
		return newIndependentFixtureResponse(request, http.StatusOK, `{"choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}}`), nil
	})
	// Exhaust the legacy bucket so even the first request would previously fail.
	ratelimitstore.CheckRateLimit(890000, "creative-fixture", 1, 1, 100)
	for range 3 {
		recorder := httptest.NewRecorder()
		requestContext, _ := gin.CreateTestContext(recorder)
		requestContext.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"creative-fixture","messages":[{"role":"user","content":"Hello"}],"stream":false}`))
		requestContext.Request.Header.Set("Content-Type", "application/json")
		requestContext.Set("api_key_id", 890000)
		requestContext.Set("rate_limit_rpm", 1)
		requestContext.Set("rate_limit_tpm", 1)
		requestContext.Set("per_model_quota_json", `{"creative-fixture":{"rpm":1,"tpm":1}}`)
		Handler(dbmodel.EndpointTypeChat, inbound.InboundTypeOpenAIChat, requestContext)
		if recorder.Code != http.StatusOK {
			t.Fatalf("got %d: %s", recorder.Code, recorder.Body.String())
		}
		if recorder.Header().Get("X-RateLimit-Remaining") != "" {
			t.Fatal("legacy quota header emitted")
		}
	}
	if calls != 3 {
		t.Fatalf("upstream calls = %d, want 3", calls)
	}
}
