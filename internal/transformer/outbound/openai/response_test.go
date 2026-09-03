package openai

import (
	"context"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestConvertToResponsesRequest_OmitsNoneReasoningEffort(t *testing.T) {
	req := &model.InternalLLMRequest{
		Model:           "mimo-v2.5-pro",
		ReasoningEffort: "none",
	}

	got := ConvertToResponsesRequest(req)
	if got.Reasoning != nil {
		t.Fatalf("expected reasoning to be omitted, got %#v", got.Reasoning)
	}
}

func TestConvertToResponsesRequest_PreservesValidReasoningEffort(t *testing.T) {
	req := &model.InternalLLMRequest{
		Model:           "o3",
		ReasoningEffort: "high",
	}

	got := ConvertToResponsesRequest(req)
	if got.Reasoning == nil {
		t.Fatalf("expected reasoning to be present")
	}
	if got.Reasoning.Effort != "high" {
		t.Fatalf("expected reasoning effort high, got %q", got.Reasoning.Effort)
	}
}

func TestConvertToResponsesRequest_PreservesMaxAndXHighReasoningEffort(t *testing.T) {
	for _, effort := range []string{"max", "xhigh", "minimal"} {
		req := &model.InternalLLMRequest{
			Model:           "gpt-5.5",
			ReasoningEffort: effort,
		}
		got := ConvertToResponsesRequest(req)
		if got.Reasoning == nil {
			t.Fatalf("effort %q: expected reasoning to be present", effort)
		}
		if got.Reasoning.Effort != effort {
			t.Fatalf("effort %q: got %q", effort, got.Reasoning.Effort)
		}
	}
}

func TestNormalizeOpenAICompatReasoningEffort_PreservesExtendedLevels(t *testing.T) {
	cases := map[string]string{
		"":        "",
		"none":    "",
		"NONE":    "",
		"minimal": "minimal",
		"low":     "low",
		"medium":  "medium",
		"high":    "high",
		"xhigh":   "xhigh",
		"max":     "max",
		"MAX":     "max",
		"bogus":   "",
	}
	for in, want := range cases {
		if got := normalizeOpenAICompatReasoningEffort(in); got != want {
			t.Fatalf("normalizeOpenAICompatReasoningEffort(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestResponseOutboundTransformStreamPreservesIncompleteDetails(t *testing.T) {
	outbound := &ResponseOutbound{}
	internalResponse, err := outbound.TransformStream(context.Background(), []byte(`{
		"type":"response.incomplete",
		"response":{
			"id":"resp_1",
			"model":"gpt-test",
			"status":"incomplete",
			"incomplete_details":{"reason":"max_tokens"}
		}
	}`))
	if err != nil {
		t.Fatalf("transform incomplete response: %v", err)
	}
	if internalResponse == nil || !internalResponse.SawTerminalEvent {
		t.Fatalf("terminal response = %#v, want terminal response", internalResponse)
	}
	if internalResponse.Termination.Cause != model.TerminationCauseTokenLimit {
		t.Fatalf("response cause = %q, want %q", internalResponse.Termination.Cause, model.TerminationCauseTokenLimit)
	}
	if internalResponse.Termination.Detail != "max_tokens" {
		t.Fatalf("incomplete detail = %q, want max_tokens", internalResponse.Termination.Detail)
	}
	if len(internalResponse.Choices) != 1 || internalResponse.Choices[0].FinishReason == nil || *internalResponse.Choices[0].FinishReason != "length" {
		t.Fatalf("choices = %#v, want one length terminal choice", internalResponse.Choices)
	}
}

func TestResponseOutboundTransformStreamPreservesFailure(t *testing.T) {
	outbound := &ResponseOutbound{}
	internalResponse, err := outbound.TransformStream(context.Background(), []byte(`{
		"type":"response.failed",
		"response":{
			"id":"resp_1",
			"model":"gpt-test",
			"status":"failed",
			"error":{"code":"server_error","message":"upstream failed"}
		}
	}`))
	if err != nil {
		t.Fatalf("transform failed response: %v", err)
	}
	if internalResponse == nil || internalResponse.Termination.Cause != model.TerminationCauseError {
		t.Fatalf("response = %#v, want error termination", internalResponse)
	}
	if internalResponse.Termination.Detail != "upstream failed" {
		t.Fatalf("failure detail = %q, want upstream failed", internalResponse.Termination.Detail)
	}
}
