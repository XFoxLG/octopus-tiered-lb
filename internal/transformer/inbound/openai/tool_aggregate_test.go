package openai

import (
	"context"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestStreamingAggregationSortsPartialToolCallsByIndex(t *testing.T) {
	first := "get_weather"
	second := "send_mail"
	weatherArgs := `{"city":"Tokyo"`
	weatherArgsEnd := `}`
	mailArgs := `{"to":"user@example.com","body":"hello"}`

	for name, inbound := range map[string]interface {
		GetInternalResponse(context.Context) (*model.InternalLLMResponse, error)
	}{
		"chat": &ChatInbound{
			streamChunks: []*model.InternalLLMResponse{
				makeToolCallChunk(0, 1, second, "", mailArgs),
				makeToolCallChunk(0, 0, first, "call_weather", weatherArgs),
				makeToolCallChunk(0, 0, "", "", weatherArgsEnd),
			},
		},
		"responses": &ResponseInbound{
			streamChunks: []*model.InternalLLMResponse{
				makeToolCallChunk(0, 1, second, "", mailArgs),
				makeToolCallChunk(0, 0, first, "call_weather", weatherArgs),
				makeToolCallChunk(0, 0, "", "", weatherArgsEnd),
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := inbound.GetInternalResponse(context.Background())
			if err != nil {
				t.Fatalf("GetInternalResponse() error = %v", err)
			}
			if len(resp.Choices) != 1 || len(resp.Choices[0].Message.ToolCalls) != 2 {
				t.Fatalf("response choices/tool calls = %+v", resp)
			}
			tools := resp.Choices[0].Message.ToolCalls
			if tools[0].Index != 0 || tools[0].Function.Name != first || tools[0].Function.Arguments != weatherArgs+weatherArgsEnd {
				t.Fatalf("tool call 0 = %+v", tools[0])
			}
			if tools[1].Index != 1 || tools[1].Function.Name != second || tools[1].Function.Arguments != mailArgs {
				t.Fatalf("tool call 1 = %+v", tools[1])
			}
		})
	}
}

func makeToolCallChunk(choiceIndex, toolIndex int, name, id, arguments string) *model.InternalLLMResponse {
	return &model.InternalLLMResponse{
		ID:      "resp-id",
		Object:  "chat.completion.chunk",
		Created: 1,
		Model:   "gpt-test",
		Choices: []model.Choice{{
			Index: choiceIndex,
			Delta: &model.Message{
				ToolCalls: []model.ToolCall{{
					Index: toolIndex,
					ID:    id,
					Function: model.FunctionCall{
						Name:      name,
						Arguments: arguments,
					},
				}},
			},
		}},
	}
}
