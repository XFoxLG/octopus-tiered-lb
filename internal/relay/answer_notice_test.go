package relay

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/transformer/inbound"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

const testGoogleNotice = "The prompt could not be submitted. The prompt contains sensitive words that violate Google's [Generative AI Prohibited Use policy](https://policies.google.com/terms/generative-ai/use-policy). Try rephrasing the prompt. If you think this was an error, [send feedback](https://ai.google.dev/gemini-api/docs/troubleshooting)."

func TestAnswerNoticeExactOnly(t *testing.T) {
	for _, text := range []string{testGoogleNotice, strings.ReplaceAll(testGoogleNotice, "'", "’")} {
		if !isExplicitAnswerNotice(text) {
			t.Fatalf("missed complete notice: %s", text)
		}
	}
	for _, text := range []string{"Example: " + testGoogleNotice, "\"" + testGoogleNotice + "\"", testGoogleNotice + " This is an example.", "sensitive words", "The prompt could not be submitted.", ""} {
		if isExplicitAnswerNotice(text) {
			t.Fatalf("false positive: %s", text)
		}
	}
}

func TestAnswerNoticeExplicitSafetyRefusals(t *testing.T) {
	messages := []string{
		"I cannot fulfill this request. The prompt asks for the generation of sexually explicit fiction depicting graphic sexual acts and erotica. My safety policies strictly prohibit generating highly explicit sexual content, even within creative writing, fictional roleplay, or user-provided narrative frameworks.",
		"I cannot fulfill this request. The prompt asks for the continuation of a sexually explicit narrative involving graphic physical and sexual descriptions. Under safety guidelines, I am unable to generate highly explicit sexual content or erotica, even within fictional creative writing or roleplay contexts.",
		"我无法协助生成高度露骨的色情描写及显式性行为细节。根据安全准则，涉及详细性器官描写及成人色情互动的内容均受到限制。如果您希望继续推进故事的主线剧情、角色对话或非露骨的情节发展，可以在合规范围内提出调整方向。",
	}
	for _, message := range messages {
		if !isExplicitAnswerNotice(message) {
			t.Fatalf("missed complete safety refusal: %s", message)
		}
	}

	for _, message := range []string{
		"Example: " + messages[0],
		`"` + messages[0] + `"`,
		messages[0] + " If you disagree, say so.",
		"I cannot fulfill this request.",
		"我无法协助生成高度露骨的色情描写及显式性行为细节。根据安全准则，涉及详细性器官描写及成人色情互动的内容均受到限制。",
	} {
		if isExplicitAnswerNotice(message) {
			t.Fatalf("false positive safety refusal: %s", message)
		}
	}
}

func TestAnswerNoticeResponseAndGeminiBlock(t *testing.T) {
	for _, scenario := range []struct {
		name, body, contentType string
		adapter                 outbound.OutboundType
		blocked                 bool
	}{
		{"plain", testGoogleNotice, "text/plain", outbound.OutboundTypeOpenAIChat, true},
		{"prompt-block", "{\"promptFeedback\":{\"blockReason\":\"SAFETY\"},\"usageMetadata\":{\"promptTokenCount\":12}}", "application/json", outbound.OutboundTypeGemini, true},
		{"candidate-block", "{\"candidates\":[{\"content\":{\"parts\":[]},\"finishReason\":\"SAFETY\"}]}", "application/json", outbound.OutboundTypeGemini, true},
		{"notice", testGoogleNotice, "application/json", outbound.OutboundTypeOpenAIChat, true},
		{"quote", "Example: " + testGoogleNotice, "application/json", outbound.OutboundTypeOpenAIChat, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			attempt, recorder := newStreamTestAttempt(t, context.Background())
			attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
			attempt.outAdapter = outbound.Get(scenario.adapter)
			body := scenario.body
			if scenario.adapter == outbound.OutboundTypeOpenAIChat && scenario.contentType == "application/json" {
				payload, _ := jsonAPI.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": body}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 7, "total_tokens": 19}})
				body = string(payload)
			}
			response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{scenario.contentType}}, Body: io.NopCloser(strings.NewReader(body))}
			err := attempt.handleResponse(context.Background(), response)
			if scenario.blocked {
				termination, found := terminalCauseFromError(err)
				if !found || !termination.Cause.IsProviderRefusal() || recorder.Body.Len() != 0 {
					t.Fatalf("block failed: err=%v body=%s", err, recorder.Body.String())
				}
			} else if err != nil || !strings.Contains(recorder.Body.String(), "Example:") {
				t.Fatalf("normal quote changed: %v", err)
			}
		})
	}
}

func TestAnswerNoticeSplitStream(t *testing.T) {
	attempt, recorder := newStreamTestAttempt(t, context.Background())
	attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
	attempt.outAdapter = outbound.Get(outbound.OutboundTypeOpenAIChat)
	var stream strings.Builder
	for _, text := range []string{testGoogleNotice[:20], testGoogleNotice[20:]} {
		payload, _ := jsonAPI.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}}}})
		stream.WriteString("data: " + string(payload) + "\n\n")
	}
	stream.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":7,\"total_tokens\":19}}\n\ndata: [DONE]\n\n")
	err := attempt.handleStreamResponse(context.Background(), &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream.String()))})
	termination, found := terminalCauseFromError(err)
	if !found || !termination.Cause.IsProviderRefusal() {
		t.Fatalf("missing block: %v", err)
	}
	if recorder.Body.Len() != 0 || attempt.streamOutputWasCommitted() {
		t.Fatalf("notice leaked: %s", recorder.Body.String())
	}
	if attempt.metrics.Stats.InputToken != 12 || attempt.metrics.Stats.OutputToken != 7 {
		t.Fatalf("usage lost: %+v", attempt.metrics.Stats)
	}
	writeClientTerminalError(attempt.c, attempt.channel, attempt.channel.Type, 200, err)
	if recorder.Code != 400 || !strings.Contains(recorder.Body.String(), "content_filter") {
		t.Fatalf("invalid terminal: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestAnswerNoticeBufferDeadline(t *testing.T) {
	attempt, _ := newStreamTestAttempt(t, context.Background())
	attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
	attempt.outAdapter = outbound.Get(outbound.OutboundTypeOpenAIChat)
	reader, writer := io.Pipe()
	defer writer.Close()
	finished := make(chan error, 1)
	go func() {
		finished <- attempt.handleStreamResponse(context.Background(), &http.Response{StatusCode: 200, Header: make(http.Header), Body: reader})
	}()
	writer.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"The prompt\"}}]}\n\n"))
	time.Sleep(1100 * time.Millisecond)
	writer.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\" is explained here.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not terminate")
	}
	if !attempt.streamOutputWasCommitted() {
		t.Fatal("prefix not released")
	}
}
