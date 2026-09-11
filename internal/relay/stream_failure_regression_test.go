package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func TestStreamTransformFailureCannotBecomeSuccess(testContext *testing.T) {
	for _, visibleOutput := range []bool{false, true} {
		for _, malformedEvent := range []bool{false, true} {
			name := "provider_error"
			if malformedEvent {
				name = "malformed_event"
			}
			if visibleOutput {
				name += "_after_output"
			}
			testContext.Run(name, func(testContext *testing.T) {
				recorder := httptest.NewRecorder()
				attempt := newTerminationFidelityRelayAttempt(recorder, nil, nil)
				attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
				attempt.outAdapter = outbound.Get(outbound.OutboundTypeOpenAIChat)
				attempt.group = &appmodel.Group{ReasoningBufferStrategy: "buffer"}
				var upstreamStream strings.Builder
				if visibleOutput {
					upstreamStream.WriteString("data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial answer\"}}]}\n\n")
				}
				if malformedEvent {
					upstreamStream.WriteString("data: {invalid-json}\n\n")
				} else {
					upstreamStream.WriteString("data: {\"error\":{\"message\":\"fixture provider failure\",\"type\":\"api_error\"}}\n\n")
				}
				upstreamStream.WriteString("data: [DONE]\n\n")
				streamErr := attempt.handleStreamResponse(context.Background(), &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader(upstreamStream.String())),
				})
				if streamErr == nil || errors.Is(streamErr, errEmptyOutput) {
					testContext.Fatalf("stream failure lost its cause: %v; body=%s", streamErr, recorder.Body.String())
				}
				if !malformedEvent && !strings.Contains(streamErr.Error(), "fixture provider failure") {
					testContext.Fatalf("provider failure was replaced: %v", streamErr)
				}
				if !visibleOutput && (recorder.Body.Len() != 0 || attempt.streamOutputWasCommitted()) {
					testContext.Fatalf("pre-output failure was committed instead of remaining retryable: %s", recorder.Body.String())
				}
				if visibleOutput {
					if strings.Count(recorder.Body.String(), "event: error\n") != 1 || strings.Contains(recorder.Body.String(), "[DONE]") {
						testContext.Fatalf("partial failure needs exactly one error and no success marker: %s", recorder.Body.String())
					}
					decision := ClassifyRelayError(http.StatusOK, streamErr, attempt.streamOutputWasCommitted())
					if decision.Scope != ScopeAbortAll {
						testContext.Fatalf("partial answer must not be replayed: %+v", decision)
					}
				}
			})
		}
	}
}

func TestStreamOperationCancellationInterruptsBlockedReader(testContext *testing.T) {
	for _, visibleOutput := range []bool{false, true} {
		name := "before_output"
		if visibleOutput {
			name = "after_output"
		}
		testContext.Run(name, func(testContext *testing.T) {
			recorder := httptest.NewRecorder()
			attempt := newTerminationFidelityRelayAttempt(recorder, nil, nil)
			attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
			attempt.outAdapter = outbound.Get(outbound.OutboundTypeOpenAIChat)
			attempt.group = &appmodel.Group{ReasoningBufferStrategy: "buffer"}
			operationContext, cancelOperation := context.WithCancel(context.Background())
			defer cancelOperation()
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			finished := make(chan error, 1)
			go func() {
				finished <- attempt.handleStreamResponse(operationContext, &http.Response{
					StatusCode: http.StatusOK, Header: make(http.Header), Body: reader,
				})
			}()
			if visibleOutput {
				// Three frames fill the reader channel and force the first visible
				// frame through the relay before cancellation, without a sleep race.
				if _, err := writer.Write([]byte("data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial answer\"}}]}\n\n")); err != nil {
					testContext.Fatal(err)
				}
				for frameNumber := 0; frameNumber < 3; frameNumber++ {
					if _, err := writer.Write([]byte("data: {\"choices\":[]}\n\n")); err != nil {
						testContext.Fatal(err)
					}
				}
			}
			cancelOperation()
			select {
			case streamErr := <-finished:
				if !errors.Is(streamErr, context.Canceled) {
					testContext.Fatalf("cancellation cause was lost: %v", streamErr)
				}
			case <-time.After(time.Second):
				_ = reader.Close()
				<-finished
				testContext.Fatal("operation cancellation did not stop a blocked stream reader")
			}
			if visibleOutput && strings.Count(recorder.Body.String(), "event: error\n") != 1 {
				testContext.Fatalf("partial answer lacks one terminal error: %s", recorder.Body.String())
			}
			if !visibleOutput && recorder.Body.Len() != 0 {
				testContext.Fatalf("pre-output cancellation must not commit SSE: %s", recorder.Body.String())
			}
		})
	}
}

func TestImmediateReasoningTimeoutEmitsTerminalError(testContext *testing.T) {
	recorder := httptest.NewRecorder()
	attempt := newTerminationFidelityRelayAttempt(recorder, nil, nil)
	attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
	attempt.outAdapter = outbound.Get(outbound.OutboundTypeOpenAIChat)
	attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
	attempt.firstTokenTimeOutSec = 1
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	finished := make(chan error, 1)
	go func() {
		finished <- attempt.handleStreamResponse(context.Background(), &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header), Body: reader,
		})
	}()
	if _, err := writer.Write([]byte("data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"still thinking\"}}]}\n\n")); err != nil {
		testContext.Fatal(err)
	}
	select {
	case streamErr := <-finished:
		if !errors.Is(streamErr, errFirstVisibleOutputTimeout) {
			testContext.Fatalf("expected visible-output timeout, got %v", streamErr)
		}
	case <-time.After(3 * time.Second):
		_ = reader.Close()
		<-finished
		testContext.Fatal("reasoning-only stream did not time out")
	}
	if strings.Count(recorder.Body.String(), "event: error\n") != 1 || !attempt.streamOutputWasCommitted() {
		testContext.Fatalf("committed reasoning ended silently: %s", recorder.Body.String())
	}
}
