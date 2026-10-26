package relay

import (
	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/model"
	"strings"
)

func applyAssistantPrefillCompatibility(request *model.InternalLLMRequest, group *appmodel.Group) {
	if request == nil || group == nil || !group.AssistantPrefillCompat || len(request.Messages) == 0 {
		return
	}
	last := request.Messages[len(request.Messages)-1]
	if last.Role != "assistant" || last.Content.Content == nil || strings.TrimSpace(*last.Content.Content) == "" || len(last.Content.MultipleContent) > 0 || len(last.ToolCalls) > 0 || len(last.Images) > 0 || last.GetReasoningContent() != "" || last.Refusal != "" {
		return
	}
	instruction := "Continue the assistant's last message from where it stopped."
	request.Messages = append(request.Messages, model.Message{Role: "user", Content: model.MessageContent{Content: &instruction}})
}
