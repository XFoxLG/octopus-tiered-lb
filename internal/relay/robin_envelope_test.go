package relay

import (
	"testing"

	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
)

func TestClassifyRobinEnvelope(t *testing.T) {
	toolCall := func(name, arguments string) transmodel.ToolCall {
		return transmodel.ToolCall{
			Type: "function",
			Function: transmodel.FunctionCall{
				Name:      name,
				Arguments: arguments,
			},
		}
	}
	message := func(content *string, calls ...transmodel.ToolCall) *transmodel.Message {
		return &transmodel.Message{
			Role:      "assistant",
			Content:   transmodel.MessageContent{Content: content},
			ToolCalls: calls,
		}
	}
	text := func(value string) *string { return &value }

	tests := []struct {
		name            string
		message         *transmodel.Message
		declared        []string
		wantKind        RobinEnvelopeKind
		wantReason      RobinEnvelopeReason
		wantToolName    string
		wantText        string
		wantExtractable bool
	}{
		{
			name:       "normal text",
			message:    message(text("正常正文")),
			wantKind:   RobinEnvelopeNormalContent,
			wantReason: RobinReasonNormalContent,
		},
		{
			name:       "empty response",
			message:    message(nil),
			wantKind:   RobinEnvelopeInsufficientOutput,
			wantReason: RobinReasonEmptyResponse,
		},
		{
			name:         "declared actual_content always wins whitelist",
			message:      message(nil, toolCall("actual_content", "{}")),
			declared:     []string{"actual_content"},
			wantKind:     RobinEnvelopeDeclaredToolCall,
			wantReason:   RobinReasonDeclaredFunctionCall,
			wantToolName: "actual_content",
		},
		{
			name:         "unknown ordinary tool is preserved as unknown",
			message:      message(nil, toolCall("other_tool", `{"value":"x"}`)),
			wantKind:     RobinEnvelopeUnknownToolCall,
			wantReason:   RobinReasonUnexpectedToolCall,
			wantToolName: "other_tool",
		},
		{
			name:            "actual_content with extractable text",
			message:         message(nil, toolCall("actual_content", `{"content":"正文内容"}`)),
			wantKind:        RobinEnvelopeUnknownToolCall,
			wantReason:      RobinReasonActualContentText,
			wantToolName:    "actual_content",
			wantText:        "正文内容",
			wantExtractable: true,
		},
		{
			name:         "actual_content empty object",
			message:      message(nil, toolCall("actual_content", "{}")),
			wantKind:     RobinEnvelopeUnknownToolCall,
			wantReason:   RobinReasonActualContentEmpty,
			wantToolName: "actual_content",
		},
		{
			name:         "actual_content null content",
			message:      message(nil, toolCall("actual_content", `{"content":null}`)),
			wantKind:     RobinEnvelopeUnknownToolCall,
			wantReason:   RobinReasonActualContentEmpty,
			wantToolName: "actual_content",
		},
		{
			name:         "actual_content whitespace is not a body",
			message:      message(nil, toolCall("actual_content", `{"content":"  \n "}`)),
			wantKind:     RobinEnvelopeUnknownToolCall,
			wantReason:   RobinReasonActualContentEmpty,
			wantToolName: "actual_content",
		},
		{
			name:         "actual_content extra arguments are rejected",
			message:      message(nil, toolCall("actual_content", `{"content":"正文","other":1}`)),
			wantKind:     RobinEnvelopeUnknownToolCall,
			wantReason:   RobinReasonActualContentMalformed,
			wantToolName: "actual_content",
		},
		{
			name:         "actual_content visible content is not rewritten",
			message:      message(text("already visible"), toolCall("actual_content", `{"content":"正文"}`)),
			wantKind:     RobinEnvelopeUnknownToolCall,
			wantReason:   RobinReasonActualContentUnexpectedContent,
			wantToolName: "actual_content",
		},
		{
			name:         "dispatch payload empty",
			message:      message(nil, toolCall("dispatch_stream_payload_0123456789abcdef", "{}")),
			wantKind:     RobinEnvelopeInsufficientOutput,
			wantReason:   RobinReasonDispatchPayloadEmpty,
			wantToolName: "dispatch_stream_payload_0123456789abcdef",
		},
		{
			name:         "dispatch payload malformed arguments",
			message:      message(nil, toolCall("dispatch_stream_payload_0123456789abcdef", `{"content":"hidden"}`)),
			wantKind:     RobinEnvelopeInsufficientOutput,
			wantReason:   RobinReasonDispatchPayloadMalformed,
			wantToolName: "dispatch_stream_payload_0123456789abcdef",
		},
		{
			name:         "dispatch payload malformed name",
			message:      message(nil, toolCall("dispatch_stream_payload_ABC", "{}")),
			wantKind:     RobinEnvelopeInsufficientOutput,
			wantReason:   RobinReasonDispatchPayloadMalformed,
			wantToolName: "dispatch_stream_payload_ABC",
		},
		{
			name: "multiple unknown calls are not interpreted",
			message: message(nil,
				toolCall("actual_content", `{"content":"one"}`),
				toolCall("other_tool", "{}"),
			),
			wantKind:   RobinEnvelopeUnknownToolCall,
			wantReason: RobinReasonMultipleToolCalls,
		},
		{
			name:       "nil message",
			wantKind:   RobinEnvelopeMalformedOutput,
			wantReason: RobinReasonMalformedMessage,
		},
		{
			name: "image output is visible and not an empty envelope",
			message: &transmodel.Message{
				Role:   "assistant",
				Images: []transmodel.MessageContentPart{{Type: "image_url"}},
			},
			wantKind:   RobinEnvelopeNormalContent,
			wantReason: RobinReasonNormalContent,
		},
		{
			name: "refusal text is visible",
			message: &transmodel.Message{
				Role:    "assistant",
				Refusal: "I cannot help with that.",
			},
			wantKind:   RobinEnvelopeNormalContent,
			wantReason: RobinReasonNormalContent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ClassifyRobinEnvelope(test.message, test.declared)
			if got.Kind != test.wantKind {
				t.Fatalf("kind = %q, want %q", got.Kind, test.wantKind)
			}
			if got.Reason != test.wantReason {
				t.Fatalf("reason = %q, want %q", got.Reason, test.wantReason)
			}
			if got.ToolName != test.wantToolName {
				t.Fatalf("tool name = %q, want %q", got.ToolName, test.wantToolName)
			}
			if got.Text != test.wantText {
				t.Fatalf("text = %q, want %q", got.Text, test.wantText)
			}
			if got.TextExtractable != test.wantExtractable {
				t.Fatalf("text extractable = %t, want %t", got.TextExtractable, test.wantExtractable)
			}
		})
	}
}
