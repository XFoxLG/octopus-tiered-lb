package relay

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

// Some Chat-compatible bridges put finish_reason=stop on every text delta.
// The production regression was a fixed one-second tail starting at the first
// such delta, even though the provider was still sending the same choice.
func TestChatContentFinishHintsDoNotTruncateContinuedStream(t *testing.T) {
	for _, tc := range []struct {
		name    string
		inbound inbound.InboundType
		end     string
	}{
		{"chat", inbound.InboundTypeOpenAIChat, "data: [DONE]"},
		{"responses", inbound.InboundTypeOpenAIResponse, "response.completed"},
		{"messages", inbound.InboundTypeAnthropic, "event:message_stop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				attempt, recorder := newStreamTestAttempt(t, ctx)
				attempt.adapterType = outbound.OutboundTypeOpenAIChat
				attempt.outAdapter = outbound.Get(attempt.adapterType)
				attempt.inAdapter = inbound.Get(tc.inbound)
				attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
				reader, writer := io.Pipe()
				defer writer.Close()
				defer reader.Close()
				producerDone := make(chan struct{})
				defer func() {
					cancel()
					_ = reader.Close()
					_ = writer.Close()
					<-producerDone
				}()
				go func() {
					defer close(producerDone)
					if _, err := io.WriteString(writer, "data: {\"id\":\"hint-fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"first-part\"},\"finish_reason\":\"stop\"}]}\n\n"); err != nil {
						return
					}
					time.Sleep(500 * time.Millisecond)
					if _, err := io.WriteString(writer, "data: {\"id\":\"hint-fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"second-part\"},\"finish_reason\":\"stop\"}]}\n\n"); err != nil {
						return
					}
					time.Sleep(2 * time.Second)
					if _, err := io.WriteString(writer, "data: {\"id\":\"hint-fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"last-part\"},\"finish_reason\":\"stop\"}]}\n\n"); err != nil {
						return
					}
					time.Sleep(2 * time.Second)
					_, _ = io.WriteString(writer, "data: [DONE]\n\n")
				}()
				done := make(chan error, 1)
				go func() {
					done <- attempt.handleStreamResponse(ctx, &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader})
				}()
				time.Sleep(3 * time.Second)
				synctest.Wait()
				select {
				case err := <-done:
					t.Fatalf("content finish hint ended generation before its terminal: %v", err)
				default:
				}
				time.Sleep(2 * time.Second)
				synctest.Wait()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				default:
					t.Fatal("explicit DONE did not finish the stream")
				}
				body := recorder.Body.String()
				for _, text := range []string{"first-part", "second-part", "last-part"} {
					if !strings.Contains(body, text) {
						t.Fatalf("continued content %q lost: %s", text, body)
					}
				}
				if strings.Count(body, tc.end) != 1 {
					t.Fatalf("missing or duplicate downstream terminal: %s", body)
				}
			})
		})
	}
}
