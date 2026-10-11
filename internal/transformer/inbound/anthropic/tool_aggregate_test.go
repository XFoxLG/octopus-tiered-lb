package anthropic

import (
	"context"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestMessagesInboundAggregationSortsPartialToolCallsByIndex(t *testing.T) {
	first := "get_weather"
	second := "send_mail"
	inbound := &MessagesInbound{
		streamChunks: []*model.InternalLLMResponse{
			{
				ID:      "resp-id",
				Object:  "chat.completion.chunk",
				Created: 1,
				Model:   "claude-test",
				Choices: []model.Choice{{
					Index: 0,
					Delta: &model.Message{
						ToolCalls: []model.ToolCall{{
							Index:    1,
							Function: model.FunctionCall{Name: second, Arguments: `{"to":"user@example.com"}`},
						}},
					},
				}},
			},
			{
				ID:      "resp-id",
				Object:  "chat.completion.chunk",
				Created: 1,
				Model:   "claude-test",
				Choices: []model.Choice{{
					Index: 0,
					Delta: &model.Message{
						ToolCalls: []model.ToolCall{{
							Index:    0,
							Function: model.FunctionCall{Name: first, Arguments: `{"city":"Tok`},
						}},
					},
				}},
			},
			{
				ID:      "resp-id",
				Object:  "chat.completion.chunk",
				Created: 1,
				Model:   "claude-test",
				Choices: []model.Choice{{
					Index: 0,
					Delta: &model.Message{
						ToolCalls: []model.ToolCall{{
							Index:    0,
							Function: model.FunctionCall{Arguments: `yo"}`},
						}},
					},
				}},
			},
		},
	}

	resp, err := inbound.GetInternalResponse(context.Background())
	if err != nil {
		t.Fatalf("GetInternalResponse() error = %v", err)
	}
	if len(resp.Choices) != 1 || len(resp.Choices[0].Message.ToolCalls) != 2 {
		t.Fatalf("response choices/tool calls = %+v", resp)
	}
	tools := resp.Choices[0].Message.ToolCalls
	if tools[0].Index != 0 || tools[0].Function.Name != first || tools[0].Function.Arguments != `{"city":"Tokyo"}` {
		t.Fatalf("tool call 0 = %+v", tools[0])
	}
	if tools[1].Index != 1 || tools[1].Function.Name != second || tools[1].Function.Arguments != `{"to":"user@example.com"}` {
		t.Fatalf("tool call 1 = %+v", tools[1])
	}
}
