package openai

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer"
	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestChatOutboundPreservesClientReasoningFields(t *testing.T) {
	for _, endpoint := range []string{"https://api.example.test/v1", "https://api.deepseek.com/v1", "https://api.xiaomimimo.com/v1"} {
		t.Run(endpoint, func(t *testing.T) {
			request := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion, Model: "example", Messages: []model.Message{
				{Role: "assistant", Reasoning: loPtr("  original reasoning\n"), ReasoningContent: loPtr(""), ReasoningSignature: loPtr("private signature")},
				{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "call-1", Type: "function", Function: model.FunctionCall{Name: "weather", Arguments: "{}"}}}},
				{Role: "tool", ToolCallID: loPtr("call-1"), Content: model.MessageContent{Content: loPtr("sunny")}},
			}}
			before, _ := transformer.Marshal(request)
			httpRequest, err := (&ChatOutbound{}).TransformRequest(context.Background(), request, endpoint, "")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(httpRequest.Body)
			var got model.InternalLLMRequest
			if err := transformer.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Messages) != 3 {
				t.Fatalf("client message order/count changed: %s", body)
			}
			first := got.Messages[0]
			if first.Reasoning == nil || *first.Reasoning != "  original reasoning\n" || first.ReasoningContent == nil || *first.ReasoningContent != "" {
				t.Fatalf("original fields changed: %s", body)
			}
			if first.ReasoningSignature != nil || got.Messages[1].ReasoningContent != nil || got.Messages[1].Reasoning != nil {
				t.Fatalf("signature leaked or history invented: %s", body)
			}
			after, _ := transformer.Marshal(request)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("source request mutated")
			}
		})
	}
}

func TestChatOutboundExplicitBudgetIsNeverRaised(t *testing.T) {
	for _, endpoint := range []string{"https://api.example.test/v1", "https://api.deepseek.com/v1", "https://api.xiaomimimo.com/v1"} {
		for _, field := range []string{"max_tokens", "max_completion_tokens"} {
			for _, limit := range []int64{0, 50, 32768, 65536} {
				t.Run(fmt.Sprintf("%s/%s/%d", endpoint, field, limit), func(t *testing.T) {
					request := &model.InternalLLMRequest{Model: "mimo-v2.5-pro", Messages: []model.Message{{Role: "user", Content: model.MessageContent{Content: loPtr("hello")}}}}
					if field == "max_tokens" {
						request.MaxTokens = &limit
					} else {
						request.MaxCompletionTokens = &limit
					}
					httpRequest, err := (&ChatOutbound{}).TransformRequest(context.Background(), request, endpoint, "")
					if err != nil {
						t.Fatal(err)
					}
					body, _ := io.ReadAll(httpRequest.Body)
					var got map[string]any
					if err := transformer.Unmarshal(body, &got); err != nil {
						t.Fatal(err)
					}
					if got[field] != float64(limit) {
						t.Fatalf("explicit budget changed: %s", body)
					}
					if request.TransformerMetadata[model.TransformerMetadataOutboundMaxTokensField] != field {
						t.Fatalf("wire field missing from diagnostics: %v", request.TransformerMetadata)
					}
					other := "max_tokens"
					if field == other {
						other = "max_completion_tokens"
					}
					if _, ok := got[other]; ok {
						t.Fatalf("another alias injected: %s", body)
					}
				})
			}
		}
	}
}
