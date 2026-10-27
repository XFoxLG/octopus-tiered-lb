package openai

import (
	"context"
	"io"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer"
	"github.com/lingyuins/octopus/internal/transformer/model"
)

// relays log the attempt request after group/route injection but before the
// adapter's provider-specific normalization. Pin both halves of that contract:
// the DeepSeek wire body carries the documented mapped level, while the
// attempt request keeps the configured level so retries and logs stay stable.
func TestChatOutboundDeepSeekWireEffortKeepsAttemptIntent(t *testing.T) {
	tests := []struct {
		name     string
		effort   string
		baseURL  string
		wantWire string
	}{
		{name: "medium maps to high", effort: "medium", baseURL: "https://api.deepseek.com/v1", wantWire: "high"},
		{name: "minimal maps to low", effort: "minimal", baseURL: "https://api.deepseek.com/v1", wantWire: "low"},
		{name: "max stays max", effort: "max", baseURL: "https://api.deepseek.com/v1", wantWire: "max"},
		{name: "auto omits the level", effort: "auto", baseURL: "https://api.deepseek.com/v1", wantWire: ""},
		{name: "mimo xhigh maps to max", effort: "xhigh", baseURL: "https://api.xiaomimimo.com/v1", wantWire: "max"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			request := &model.InternalLLMRequest{
				Model:           "fixture-model",
				ReasoningEffort: testCase.effort,
				Messages: []model.Message{{
					Role:    "user",
					Content: model.MessageContent{Content: loPtr("hello")},
				}},
			}
			httpRequest, err := (&ChatOutbound{}).TransformRequest(
				context.Background(), request, testCase.baseURL, "sk-test")
			if err != nil {
				t.Fatalf("TransformRequest() error = %v", err)
			}
			body, err := io.ReadAll(httpRequest.Body)
			if err != nil {
				t.Fatalf("read outbound body: %v", err)
			}
			var wire struct {
				ReasoningEffort string `json:"reasoning_effort"`
			}
			if err := transformer.Unmarshal(body, &wire); err != nil {
				t.Fatalf("unmarshal outbound body: %v", err)
			}
			if wire.ReasoningEffort != testCase.wantWire {
				t.Fatalf("wire reasoning_effort = %q, want %q", wire.ReasoningEffort, testCase.wantWire)
			}
			// The attempt-private request is the value relay logs; the adapter
			// must not rewrite it into the provider-specific wire value.
			if request.ReasoningEffort != testCase.effort {
				t.Fatalf("attempt reasoning_effort mutated to %q, want %q", request.ReasoningEffort, testCase.effort)
			}
		})
	}
}
