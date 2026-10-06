package helper

import (
	"context"
	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReviewApplyProtocolConflict(t *testing.T) {
	for _, protocol := range []string{"chat", "passthrough", "raw"} {
		t.Run(protocol, func(t *testing.T) {
			ch := &appmodel.Channel{Type: outbound.OutboundTypeOpenAIChat, UpstreamProtocols: []string{protocol}}
			run := &appmodel.ChannelProbeRun{Results: []appmodel.ChannelProbeResult{{Kind: appmodel.ProbeKindProtocol, Item: appmodel.ProbeItemProtocolChat, Verdict: appmodel.ProbeVerdictPass}}}
			_, err := ApplyChannelProbeRun(context.Background(), ch, run)
			if err != nil {
				t.Errorf("existing valid declaration prevents applying successful probe: %v", err)
			}
		})
	}
}
func TestReviewGroupProbeUsesModelEligibleKey(t *testing.T) {
	setupHelperDB(t)
	xurl.SetSSRFAllowPrivateForTest(true)
	defer xurl.SetSSRFAllowPrivateForTest(false)
	var tokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		tokens = append(tokens, token)
		w.Header().Set("Content-Type", "application/json")
		if token != "Bearer sk-correct" {
			w.WriteHeader(403)
			_, _ = w.Write([]byte("{\"error\":\"wrong key for model\"}"))
			return
		}
		_, _ = w.Write([]byte("{\"id\":\"test\",\"object\":\"chat.completion\",\"choices\":[{\"index\":0,\"message\":{\"role\":\"assistant\",\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}"))
	}))
	defer srv.Close()
	ch := appmodel.Channel{ID: 981, Enabled: true, Name: "review-key-probe", Type: outbound.OutboundTypeOpenAIChat, UpstreamProtocols: []string{"chat_only"}, BaseUrls: []appmodel.BaseUrl{{URL: srv.URL}}, KeySelectionStrategy: "cost", Keys: []appmodel.ChannelKey{
		{ID: 991, Enabled: true, ChannelKey: "sk-wrong", SupportedModels: "other-model", TotalCost: 0},
		{ID: 992, Enabled: true, ChannelKey: "sk-correct", SupportedModels: "test-model", TotalCost: 10},
	}}
	result := testGroupModelItem(context.Background(), appmodel.EndpointTypeChat, appmodel.GroupItem{ID: 1, ChannelID: 981, ModelName: "test-model"}, map[int]appmodel.Channel{981: ch})
	t.Logf("passed=%v tokens=%v message=%s", result.Passed, tokens, result.Message)
	if !result.Passed {
		t.Error("available model failed because probe selected a key disallowed for it")
	}
	for _, token := range tokens {
		if token != "Bearer sk-correct" {
			t.Errorf("probe used model-ineligible key: %s", token)
			break
		}
	}
}
func TestReviewCancelledProbeDoesNotWaitForSemaphore(t *testing.T) {
	for i := 0; i < cap(capabilityProbeSemaphore); i++ {
		capabilityProbeSemaphore <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		_, _ = RunChannelCapabilityProbe(ctx, &appmodel.Channel{Type: outbound.OutboundTypeOpenAIChat}, appmodel.ChannelProbeRequest{ModelName: "test-model", KeyIndex: -1})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Error("already-cancelled probe is blocked waiting for a global semaphore slot")
	}
	for i := 0; i < cap(capabilityProbeSemaphore); i++ {
		<-capabilityProbeSemaphore
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("probe did not drain after semaphore release")
	}
}
func TestReviewProbeSummaryRedactsKey(t *testing.T) {
	secret := "sk-review-secret-never-real"
	result := buildProbeResult(&appmodel.ChannelProbeRun{}, appmodel.ProbeKindProtocol, appmodel.ProbeItemProtocolChat, &probeRequestOutcome{Verdict: appmodel.ProbeVerdictUnknown, Summary: "Post https://example.invalid/v1?key=" + secret + ": connection reset"}, secret)
	if strings.Contains(result.Summary, secret) {
		t.Errorf("persisted probe summary exposes supplied key: %s", result.Summary)
	}
}
