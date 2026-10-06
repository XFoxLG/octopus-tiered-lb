package helper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

func TestReviewStructuredOutputRealEnvelopes(t *testing.T) {
	bodies := map[string]string{
		"chat":      `{"choices":[{"message":{"content":"{\"ok\":true}"},"finish_reason":"stop"}]}`,
		"responses": `{"output":[{"type":"message","content":[{"type":"output_text","text":"{\"ok\":true}"}]}]}`,
		"anthropic": `{"content":[{"type":"text","text":"{\"ok\":true}"}],"stop_reason":"end_turn"}`,
		"gemini":    `{"candidates":[{"content":{"parts":[{"text":"{\"ok\":true}"}]},"finishReason":"STOP"}]}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			got := judgeCapabilityResponse(appmodel.ProbeItemStructuredOutput, &probeRequestOutcome{Verdict: appmodel.ProbeVerdictPass, Body: body})
			if got != appmodel.ProbeVerdictPass {
				t.Errorf("valid structured output classified as %s", got)
			}
		})
	}
}

func TestReviewProtocolProbeRejectsFalseSuccess(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	cases := []struct {
		name, body string
		status     int
		want       appmodel.ProbeVerdict
	}{
		{"error-envelope", `{"error":{"message":"backend unavailable"}}`, 200, appmodel.ProbeVerdictUnknown},
		{"HTML-login", `<html><body>Sign in to continue</body></html>`, 200, appmodel.ProbeVerdictUnknown},
		{"empty", ``, 200, appmodel.ProbeVerdictUnknown},
		{"503-unsupported", `{"error":{"message":"unsupported route while backend is restarting"}}`, 503, appmodel.ProbeVerdictUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			ch := &appmodel.Channel{Type: outbound.OutboundTypeOpenAIChat, BaseUrls: []appmodel.BaseUrl{{URL: srv.URL}}}
			target := buildProtocolProbeTargets(ch.Type, "test-model")[0]
			got := sendProbeRequest(context.Background(), ch, "sk-test-key", target.AdapterType, target.Request)
			t.Logf("status=%d verdict=%s body=%q", got.StatusCode, got.Verdict, got.Body)
			if tc.status == 200 && got.Verdict == appmodel.ProbeVerdictPass {
				t.Error("invalid upstream response marked as supported protocol")
			}
			if tc.status == 503 && got.Verdict != tc.want {
				t.Errorf("temporary server error became conclusive verdict %s", got.Verdict)
			}
		})
	}
}

func TestReviewResponsesOnlyCapabilityProbe(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/responses") {
			_, _ = w.Write([]byte(`{"id":"r1","object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
			return
		}
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
	}))
	defer srv.Close()
	ch := &appmodel.Channel{Type: outbound.OutboundTypeOpenAIResponse, UpstreamProtocols: []string{"responses_only"}, BaseUrls: []appmodel.BaseUrl{{URL: srv.URL}}, Keys: []appmodel.ChannelKey{{Enabled: true, ChannelKey: "sk-test-key"}}}
	run, err := RunChannelCapabilityProbe(context.Background(), ch, appmodel.ChannelProbeRequest{ModelName: "test-model", KeyIndex: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("requested paths=%v", paths)
	for _, r := range run.Results {
		t.Logf("%s: %s (%d)", r.Item, r.Verdict, r.StatusCode)
		if r.Item == appmodel.ProbeItemProtocolResponses && r.Verdict != appmodel.ProbeVerdictPass {
			t.Fatal("fixture Responses endpoint should pass")
		}
		if r.Item == appmodel.ProbeItemTextGeneration && r.Verdict != appmodel.ProbeVerdictPass {
			t.Errorf("working Responses text generation misclassified as %s", r.Verdict)
		}
	}
}
