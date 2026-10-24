package openai

import (
	"context"
	"testing"
)

// REQ-01: enable_thinking carries a reasoning intent even though the internal
// request struct documents it as a Qwen/Alibaba wire field. A client that sends
// "enable_thinking": false must count as an explicit opt-out, otherwise a group
// default effort would override the user's choice. Likewise true is intent.
func TestChatInboundEnableThinkingMarksReasoningExplicit(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantExplicit  bool
		wantThinking  *bool
	}{
		{
			name:         "enable_thinking false is explicit intent",
			body:         `{"model":"qwen3","messages":[{"role":"user","content":"hi"}],"enable_thinking":false}`,
			wantExplicit: true,
			wantThinking: boolPtr(false),
		},
		{
			name:         "enable_thinking true is explicit intent",
			body:         `{"model":"qwen3","messages":[{"role":"user","content":"hi"}],"enable_thinking":true}`,
			wantExplicit: true,
			wantThinking: boolPtr(true),
		},
		{
			name:         "omitted enable_thinking stays silent",
			body:         `{"model":"qwen3","messages":[{"role":"user","content":"hi"}]}`,
			wantExplicit: false,
			wantThinking: nil,
		},
		{
			name:         "reasoning_effort alone still marks explicit",
			body:         `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"none"}`,
			wantExplicit: true,
			wantThinking: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inbound := &ChatInbound{}
			req, err := inbound.TransformRequest(context.Background(), []byte(tc.body))
			if err != nil {
				t.Fatalf("TransformRequest() error = %v", err)
			}
			if req.ReasoningExplicit != tc.wantExplicit {
				t.Fatalf("ReasoningExplicit = %v, want %v", req.ReasoningExplicit, tc.wantExplicit)
			}
			if tc.wantThinking == nil {
				if req.EnableThinking != nil {
					t.Fatalf("EnableThinking = %v, want nil", *req.EnableThinking)
				}
				return
			}
			if req.EnableThinking == nil {
				t.Fatalf("EnableThinking = nil, want %v", *tc.wantThinking)
			}
			if *req.EnableThinking != *tc.wantThinking {
				t.Fatalf("EnableThinking = %v, want %v", *req.EnableThinking, *tc.wantThinking)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }
