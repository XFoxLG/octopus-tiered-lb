package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
)

func TestSemanticAnswerCacheBypassesConversationAndCreativeRequests(t *testing.T) {
	text := "continue the story"
	temperature := 0.0
	makeRequest := func() *transmodel.InternalLLMRequest {
		return &transmodel.InternalLLMRequest{
			Model: "chat-model", Temperature: &temperature,
			Messages: []transmodel.Message{{Role: "user", Content: transmodel.MessageContent{Content: &text}}},
		}
	}
	if !canReuseSemanticAnswer(makeRequest(), "chat") {
		t.Fatal("explicit deterministic single-question cache use should remain available")
	}
	cases := map[string]func(*transmodel.InternalLLMRequest){
		"creative temperature": func(request *transmodel.InternalLLMRequest) { value := 0.8; request.Temperature = &value },
		"unspecified sampling": func(request *transmodel.InternalLLMRequest) { request.Temperature = nil },
		"stream":               func(request *transmodel.InternalLLMRequest) { value := true; request.Stream = &value },
		"conversation":         func(request *transmodel.InternalLLMRequest) { request.ConversationID = "conversation-a" },
		"character prompt": func(request *transmodel.InternalLLMRequest) {
			request.Messages = append([]transmodel.Message{{Role: "system", Content: transmodel.MessageContent{Content: &text}}}, request.Messages...)
		},
		"chat history": func(request *transmodel.InternalLLMRequest) {
			request.Messages = append(request.Messages, transmodel.Message{Role: "assistant"})
		},
		"image": func(request *transmodel.InternalLLMRequest) {
			request.Messages[0].Content.MultipleContent = []transmodel.MessageContentPart{{Type: "image_url"}}
		},
		"tools": func(request *transmodel.InternalLLMRequest) { request.Tools = []transmodel.Tool{{}} },
	}
	for name, configure := range cases {
		t.Run(name, func(t *testing.T) {
			request := makeRequest()
			configure(request)
			if canReuseSemanticAnswer(request, "chat") {
				t.Fatal("conversation, protocol-bearing, and creative requests must bypass answer reuse")
			}
			recorder := httptest.NewRecorder()
			ginContext, _ := gin.CreateTestContext(recorder)
			ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			served, _, err := maybeServeSemanticCacheHit(ginContext, &relayRequest{internalRequest: request, operationCtx: context.Background()}, "chat")
			if served || err != nil || recorder.Body.Len() != 0 {
				t.Fatal("bypassed request must remain untouched for actual generation")
			}
		})
	}
	if canReuseSemanticAnswer(makeRequest(), "responses") {
		t.Fatal("Responses cannot reuse a Chat-shaped answer")
	}
}
