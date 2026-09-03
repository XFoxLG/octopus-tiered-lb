package gemini

import (
	"context"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestCleanGeminiSchemaRemovesNewlyUnsupportedKeywords(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":          "string",
				"pattern":       "^[a-z]+$",
				"minLength":     1,
				"maxLength":     32,
				"propertyNames": map[string]any{"type": "string"},
			},
			"count": map[string]any{
				"type":       "number",
				"minimum":    1,
				"maximum":    10,
				"multipleOf": 1,
			},
			"items": map[string]any{
				"type":        "array",
				"minItems":    1,
				"maxItems":    3,
				"uniqueItems": true,
				"items": map[string]any{
					"type": "string",
				},
			},
		},
	}

	cleanGeminiSchema(schema)

	nameSchema := schema["properties"].(map[string]any)["name"].(map[string]any)
	for _, key := range []string{"pattern", "minLength", "maxLength", "propertyNames"} {
		if _, exists := nameSchema[key]; exists {
			t.Fatalf("nameSchema still contains unsupported key %q", key)
		}
	}

	countSchema := schema["properties"].(map[string]any)["count"].(map[string]any)
	for _, key := range []string{"minimum", "maximum", "multipleOf"} {
		if _, exists := countSchema[key]; exists {
			t.Fatalf("countSchema still contains unsupported key %q", key)
		}
	}

	itemsSchema := schema["properties"].(map[string]any)["items"].(map[string]any)
	for _, key := range []string{"minItems", "maxItems", "uniqueItems"} {
		if _, exists := itemsSchema[key]; exists {
			t.Fatalf("itemsSchema still contains unsupported key %q", key)
		}
	}
}

func TestGeminiTerminationForFinishReason(t *testing.T) {
	testCases := []struct {
		name                   string
		providerReason         string
		expectedCause          model.TerminationCause
		expectedTerminalEvent  bool
		expectedWireFinish     string
		expectsWireFinishValue bool
	}{
		{
			name:                   "natural stop",
			providerReason:         "STOP",
			expectedCause:          model.TerminationCauseComplete,
			expectedTerminalEvent:  true,
			expectedWireFinish:     "stop",
			expectsWireFinishValue: true,
		},
		{
			name:                   "token limit",
			providerReason:         "MAX_TOKENS",
			expectedCause:          model.TerminationCauseTokenLimit,
			expectedTerminalEvent:  true,
			expectedWireFinish:     "length",
			expectsWireFinishValue: true,
		},
		{
			name:                   "safety filter",
			providerReason:         "SAFETY",
			expectedCause:          model.TerminationCauseContentFilter,
			expectedTerminalEvent:  true,
			expectedWireFinish:     "content_filter",
			expectsWireFinishValue: true,
		},
		{
			name:                   "recitation filter",
			providerReason:         "RECITATION",
			expectedCause:          model.TerminationCauseRecitation,
			expectedTerminalEvent:  true,
			expectedWireFinish:     "content_filter",
			expectsWireFinishValue: true,
		},
		{
			name:                   "sensitive personal information filter",
			providerReason:         "SPII",
			expectedCause:          model.TerminationCauseContentFilter,
			expectedTerminalEvent:  true,
			expectedWireFinish:     "content_filter",
			expectsWireFinishValue: true,
		},
		{
			name:                   "malformed function call",
			providerReason:         "MALFORMED_FUNCTION_CALL",
			expectedCause:          model.TerminationCauseMalformedToolCall,
			expectedTerminalEvent:  true,
			expectedWireFinish:     "length",
			expectsWireFinishValue: true,
		},
		{
			name:                   "unknown provider reason",
			providerReason:         "FUTURE_REASON",
			expectedCause:          model.TerminationCauseUnknown,
			expectedTerminalEvent:  true,
			expectedWireFinish:     "length",
			expectsWireFinishValue: true,
		},
		{
			name:                  "unspecified reason is not terminal",
			providerReason:        "FINISH_REASON_UNSPECIFIED",
			expectedCause:         model.TerminationCauseUnspecified,
			expectedTerminalEvent: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actualTermination, actualTerminalEvent := geminiTerminationForFinishReason(testCase.providerReason)
			if actualTermination.Cause != testCase.expectedCause {
				t.Fatalf("cause = %q, want %q", actualTermination.Cause, testCase.expectedCause)
			}
			if actualTermination.ProviderReason != testCase.providerReason {
				t.Fatalf("provider reason = %q, want %q", actualTermination.ProviderReason, testCase.providerReason)
			}
			if actualTerminalEvent != testCase.expectedTerminalEvent {
				t.Fatalf("terminal event = %t, want %t", actualTerminalEvent, testCase.expectedTerminalEvent)
			}
			if !testCase.expectsWireFinishValue {
				return
			}

			actualWireFinish := convertGeminiFinishReason(actualTermination.Cause)
			if actualWireFinish != testCase.expectedWireFinish {
				t.Fatalf("wire finish reason = %q, want %q", actualWireFinish, testCase.expectedWireFinish)
			}
		})
	}
}

func TestConvertGeminiResponsePreservesCandidateTermination(t *testing.T) {
	providerReason := "MAX_TOKENS"
	geminiResponse := &model.GeminiGenerateContentResponse{
		Candidates: []*model.GeminiCandidate{{
			Index:        0,
			FinishReason: &providerReason,
		}},
	}

	internalResponse := convertGeminiToLLMResponse(geminiResponse, true)
	if !internalResponse.SawTerminalEvent {
		t.Fatal("candidate finish reason did not mark the stream terminal")
	}
	if len(internalResponse.Choices) != 1 {
		t.Fatalf("choice count = %d, want 1", len(internalResponse.Choices))
	}

	termination := internalResponse.Choices[0].Termination
	if termination.Cause != model.TerminationCauseTokenLimit {
		t.Fatalf("candidate cause = %q, want %q", termination.Cause, model.TerminationCauseTokenLimit)
	}
	if termination.ProviderReason != providerReason {
		t.Fatalf("candidate provider reason = %q, want %q", termination.ProviderReason, providerReason)
	}
	if internalResponse.Choices[0].FinishReason == nil || *internalResponse.Choices[0].FinishReason != "length" {
		t.Fatalf("wire finish reason = %v, want length", internalResponse.Choices[0].FinishReason)
	}
}

func TestConvertGeminiResponsePreservesPromptBlock(t *testing.T) {
	geminiResponse := &model.GeminiGenerateContentResponse{
		PromptFeedback: &model.GeminiPromptFeedback{
			BlockReason: "SAFETY",
		},
	}

	internalResponse := convertGeminiToLLMResponse(geminiResponse, true)
	if len(internalResponse.Choices) != 0 {
		t.Fatalf("choice count = %d, want 0", len(internalResponse.Choices))
	}
	if !internalResponse.SawTerminalEvent {
		t.Fatal("prompt block did not mark the stream terminal")
	}
	if internalResponse.Termination.Cause != model.TerminationCausePromptBlocked {
		t.Fatalf("prompt block cause = %q, want %q", internalResponse.Termination.Cause, model.TerminationCausePromptBlocked)
	}
	if internalResponse.Termination.BlockReason != "SAFETY" {
		t.Fatalf("prompt block reason = %q, want SAFETY", internalResponse.Termination.BlockReason)
	}
}

func TestConvertGeminiResponseTreatsUnspecifiedPromptFeedbackAsUnknownTerminal(t *testing.T) {
	geminiResponse := &model.GeminiGenerateContentResponse{
		PromptFeedback: &model.GeminiPromptFeedback{
			BlockReason: "BLOCK_REASON_UNSPECIFIED",
		},
	}

	internalResponse := convertGeminiToLLMResponse(geminiResponse, true)
	if !internalResponse.SawTerminalEvent {
		t.Fatal("no-candidate prompt feedback must mark the stream terminal")
	}
	if internalResponse.Termination.Cause != model.TerminationCauseUnknown {
		t.Fatalf("prompt feedback cause = %q, want %q", internalResponse.Termination.Cause, model.TerminationCauseUnknown)
	}
	if internalResponse.Termination.Detail == "" {
		t.Fatal("unknown prompt feedback must retain diagnostic detail")
	}
}

func TestMessagesOutboundTransformStreamDistinguishesEmptyFrameAndTerminalMarker(t *testing.T) {
	outbound := &MessagesOutbound{}
	emptyResponse, err := outbound.TransformStream(context.Background(), nil)
	if err != nil {
		t.Fatalf("transform empty SSE frame: %v", err)
	}
	if emptyResponse != nil {
		t.Fatalf("empty SSE frame response = %#v, want nil", emptyResponse)
	}

	terminalResponse, err := outbound.TransformStream(context.Background(), []byte("[DONE]"))
	if err != nil {
		t.Fatalf("transform terminal marker: %v", err)
	}
	if terminalResponse == nil || !terminalResponse.SawTerminalEvent {
		t.Fatalf("terminal marker response = %#v, want terminal response", terminalResponse)
	}
}
