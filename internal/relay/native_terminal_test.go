package relay

import (
	"context"
	"errors"
	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
	tm "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestNativeTerminalEndsWithoutSocketEOF(t *testing.T) {
	cases := []struct {
		name    string
		adapter outbound.OutboundType
		frames  string
		failure bool
	}{
		{"responses_complete", outbound.OutboundTypeOpenAIResponse, `{"type":"response.output_text.delta","delta":"answer","output_index":0,"content_index":0}` + "\n" + `{"type":"response.completed","response":{"id":"r","status":"completed","usage":{"input_tokens":2,"output_tokens":3}}}`, false},
		{"responses_length", outbound.OutboundTypeOpenAIResponse, `{"type":"response.output_text.delta","delta":"partial","output_index":0,"content_index":0}` + "\n" + `{"type":"response.incomplete","response":{"id":"r","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":2,"output_tokens":3}}}`, false},
		{"responses_failed", outbound.OutboundTypeOpenAIResponse, `{"type":"response.failed","response":{"id":"r","status":"failed","error":{"type":"server_error","message":"fixture failed"}}}`, true},
		{"messages", outbound.OutboundTypeAnthropic, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"answer"}}` + "\n" + `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}` + "\n" + `{"type":"message_stop"}`, false},
		{"gemini", outbound.OutboundTypeGemini, `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}}`, false},
		{"chat_usage_tail", outbound.OutboundTypeOpenAIChat, `{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":"stop"}]}` + "\n" + `{"object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				attempt, recorder := newStreamTestAttempt(t, context.Background())
				attempt.adapterType = tc.adapter
				attempt.outAdapter = outbound.Get(tc.adapter)
				attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
				attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
				reader, writer := io.Pipe()
				defer writer.Close()
				go func() {
					for _, frame := range strings.Split(tc.frames, "\n") {
						_, _ = io.WriteString(writer, "data: "+frame+"\n\n")
					}
				}()
				done := make(chan error, 1)
				go func() {
					done <- attempt.handleStreamResponse(context.Background(), &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader})
				}()
				select {
				case err := <-done:
					if tc.failure != (err != nil) {
						t.Fatalf("terminal outcome: %v body=%s", err, recorder.Body.String())
					}
				case <-time.After(2 * time.Second):
					t.Fatal("native terminal waited for EOF")
				}
				if !tc.failure && strings.Count(recorder.Body.String(), "data: [DONE]") != 1 {
					t.Fatalf("Chat conversion missing/duplicate DONE: %s", recorder.Body.String())
				}
				if tc.failure && strings.Contains(recorder.Body.String(), "[DONE]") {
					t.Fatal("failure became success")
				}
			})
		})
	}
}

func TestMessagesDeltaWithoutStopRemainsMissingTerminal(t *testing.T) {
	attempt, recorder := newStreamTestAttempt(t, context.Background())
	attempt.adapterType = outbound.OutboundTypeAnthropic
	attempt.outAdapter = outbound.Get(attempt.adapterType)
	attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
	attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
	err := attempt.handleStreamResponse(context.Background(), &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n"))})
	if !errors.Is(err, errMissingStreamTerminal) || strings.Contains(recorder.Body.String(), "[DONE]") {
		t.Fatalf("message_delta accepted as terminal: %v %s", err, recorder.Body.String())
	}
}

func TestChatFinishTailBoundedAndMultipleChoices(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempt, recorder := newStreamTestAttempt(t, context.Background())
		attempt.adapterType = outbound.OutboundTypeOpenAIChat
		attempt.outAdapter = outbound.Get(attempt.adapterType)
		attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
		attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
		attempt.internalRequest.RawRequest = []byte(`{"n":2}`)
		attempt.internalRequest.RawAPIFormat = tm.APIFormatOpenAIChatCompletion
		reader, writer := io.Pipe()
		defer writer.Close()
		go func() {
			_, _ = io.WriteString(writer, "data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"one\"},\"finish_reason\":\"stop\"}]}\n\n")
			time.Sleep(2 * time.Second)
			_, _ = io.WriteString(writer, "data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":1,\"delta\":{\"content\":\"two\"},\"finish_reason\":\"stop\"}]}\n\n")
		}()
		done := make(chan error, 1)
		go func() {
			done <- attempt.handleStreamResponse(context.Background(), &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader})
		}()
		time.Sleep(time.Second)
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("first choice ended all choices")
		default:
		}
		time.Sleep(3 * time.Second)
		synctest.Wait()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("finish tail wait was unbounded")
		}
		if !strings.Contains(recorder.Body.String(), "two") || strings.Count(recorder.Body.String(), "[DONE]") != 1 {
			t.Fatal("multiple choices lost or duplicate terminal")
		}
	})
}
