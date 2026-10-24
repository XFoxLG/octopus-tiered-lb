package relay

import (
	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"testing"
)

func TestAssistantPrefillIsOptInAndAttemptPrivate(t *testing.T) {
	text := "unfinished answer"
	source := &model.InternalLLMRequest{Messages: []model.Message{{Role: "assistant", Content: model.MessageContent{Content: &text}}}}
	channel := &appmodel.Channel{Type: outbound.OutboundTypeOpenAIChat}
	group := &appmodel.Group{AssistantPrefillCompat: true}
	for range 2 {
		request, _, err := prepareInternalRequestForOutbound(channel, source, "chat", group)
		if err != nil {
			t.Fatal(err)
		}
		if len(request.Messages) != 2 || request.Messages[1].Role != "user" {
			t.Fatalf("prefill missing: %+v", request.Messages)
		}
		applyAssistantPrefillCompatibility(request, group)
		if len(request.Messages) != 2 {
			t.Fatal("duplicate continuation")
		}
	}
	if len(source.Messages) != 1 {
		t.Fatal("prefill polluted source")
	}
	request, _, _ := prepareInternalRequestForOutbound(channel, source, "chat", &appmodel.Group{})
	if len(request.Messages) != 1 {
		t.Fatal("prefill not opt-in")
	}
	source.Messages[0].ToolCalls = []model.ToolCall{{Type: "function"}}
	applyAssistantPrefillCompatibility(source, group)
	if len(source.Messages) != 1 {
		t.Fatal("prefill rewrote tool turn")
	}
}
