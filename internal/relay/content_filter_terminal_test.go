package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
	tmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func TestChatContentFilterTerminalPreservesFrameAndReturnsTypedFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		frames []string
		want   string
	}{
		{
			name: "body_with_filter",
			frames: []string{
				`data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"partial answer"},"finish_reason":"content_filter"}]}`,
				`data: [DONE]`,
			},
			want: "partial answer",
		},
		{
			name: "zero_body_filter",
			frames: []string{
				`data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}]}`,
				`data: [DONE]`,
			},
		},
		{
			name: "reasoning_only_filter",
			frames: []string{
				`data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"thinking"},"finish_reason":"content_filter"}]}`,
				`data: [DONE]`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var streamErr error
				attempt, recorder := newStreamTestAttempt(t, context.Background())
				attempt.adapterType = outbound.OutboundTypeOpenAIChat
				attempt.outAdapter = outbound.Get(attempt.adapterType)
				attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
				attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
				reader, writer := io.Pipe()
				defer reader.Close()
				go func() {
					for _, frame := range tc.frames {
						_, _ = io.WriteString(writer, frame+"\n\n")
					}
					_ = writer.Close()
				}()
				done := make(chan error, 1)
				go func() {
					done <- attempt.handleStreamResponse(context.Background(), &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
						Body:       reader,
					})
				}()
				select {
				case streamErr = <-done:
					if streamErr == nil {
						t.Fatalf("content_filter became success: %s", recorder.Body.String())
					}
					if !strings.Contains(streamErr.Error(), "content_filter") {
						t.Fatalf("typed filter failure was lost: %v", streamErr)
					}
					if errors.Is(streamErr, errEmptyOutput) || errors.Is(streamErr, context.Canceled) {
						t.Fatalf("filter terminal changed semantics: %v", streamErr)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("content-filter stream did not finish")
				}
				body := recorder.Body.String()
				if tc.want != "" && !strings.Contains(body, tc.want) {
					t.Fatalf("converted body was dropped: %s", body)
				}
				if !strings.Contains(body, `"finish_reason":"content_filter"`) {
					t.Fatalf("filter terminal frame was dropped: %s", body)
				}
				if strings.Contains(body, "event: error") || strings.Contains(body, "stream interrupted") {
					t.Fatalf("filter terminal became a generic interruption: %s", body)
				}
				if strings.Count(body, "data: [DONE]") != 1 {
					t.Fatalf("Chat DONE count = %d: %s", strings.Count(body, "data: [DONE]"), body)
				}
				if !attempt.streamOutputWasCommitted() {
					t.Fatal("delivered protocol terminal was not committed")
				}
				termination, ok := terminalCauseFromError(streamErr)
				if !ok || termination.Cause != tmodel.TerminationCauseContentFilter {
					t.Fatalf("terminal cause = (%#v, %t)", termination, ok)
				}
			})
		})
	}
}

func TestResponsesContentFilterTerminalKeepsBodyAndIncompleteReason(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempt, recorder := newStreamTestAttempt(t, context.Background())
		attempt.adapterType = outbound.OutboundTypeOpenAIResponse
		attempt.outAdapter = outbound.Get(attempt.adapterType)
		attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIResponse)
		attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
		reader, writer := io.Pipe()
		defer reader.Close()
		go func() {
			_, _ = io.WriteString(writer, `data: {"type":"response.output_text.delta","delta":"partial answer","output_index":0,"content_index":0}`+"\n\n")
			_, _ = io.WriteString(writer, `data: {"type":"response.incomplete","response":{"id":"r","status":"incomplete","incomplete_details":{"reason":"content_filter"},"usage":{"input_tokens":2,"output_tokens":3}}}`+"\n\n")
			_ = writer.Close()
		}()
		done := make(chan error, 1)
		go func() {
			done <- attempt.handleStreamResponse(context.Background(), &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       reader,
			})
		}()
		select {
		case err := <-done:
			termination, ok := terminalCauseFromError(err)
			if !ok || termination.Cause != tmodel.TerminationCauseContentFilter {
				t.Fatalf("Responses filter terminal = %v (%t): %s", err, ok, recorder.Body.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Responses content-filter stream did not finish")
		}
		body := recorder.Body.String()
		for _, want := range []string{"partial answer", `"type":"response.incomplete"`, `"reason":"content_filter"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("missing %q: %s", want, body)
			}
		}
		if strings.Contains(body, "event: error") || strings.Contains(body, "data: [DONE]") {
			t.Fatalf("Responses terminal was cross-protocol rewritten: %s", body)
		}
	})
}

func TestContentFilterReplayStoresProtocolTerminalWithoutGenericError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempt, _ := newStreamTestAttempt(t, context.Background())
		attempt.adapterType = outbound.OutboundTypeOpenAIChat
		attempt.outAdapter = outbound.Get(attempt.adapterType)
		attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
		attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
		session := attachStreamSession(t, attempt)
		reader, writer := io.Pipe()
		defer reader.Close()
		go func() {
			_, _ = io.WriteString(writer, `data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"partial answer"},"finish_reason":"content_filter"}]}`+"\n\n")
			_, _ = io.WriteString(writer, "data: [DONE]\n\n")
			_ = writer.Close()
		}()
		done := make(chan error, 1)
		go func() {
			done <- attempt.handleStreamResponse(context.Background(), &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       reader,
			})
		}()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "content_filter") {
				t.Fatalf("request result must remain a typed filter failure: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("replayed content-filter stream did not finish")
		}
		if !session.IsDone() {
			t.Fatal("transport replay session stayed open after protocol terminal")
		}
		var replay strings.Builder
		for _, event := range session.events {
			replay.Write(event.Payload)
		}
		payload := replay.String()
		for _, want := range []string{"partial answer", `"finish_reason":"content_filter"`, "data: [DONE]"} {
			if !strings.Contains(payload, want) {
				t.Fatalf("replay missing %q: %s", want, payload)
			}
		}
		if strings.Contains(payload, "event: error") {
			t.Fatalf("replay appended a generic error after protocol terminal: %s", payload)
		}
	})
}

func TestMessagesContentFilterKeepsNativeRefusalAndMessageStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempt, recorder := newStreamTestAttempt(t, context.Background())
		attempt.adapterType = outbound.OutboundTypeOpenAIChat
		attempt.outAdapter = outbound.Get(attempt.adapterType)
		attempt.inAdapter = inbound.Get(inbound.InboundTypeAnthropic)
		attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
		reader, writer := io.Pipe()
		defer reader.Close()
		go func() {
			_, _ = io.WriteString(writer, `data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"partial answer"},"finish_reason":"content_filter"}]}`+"\n\n")
			_, _ = io.WriteString(writer, "data: [DONE]\n\n")
			_ = writer.Close()
		}()
		done := make(chan error, 1)
		go func() {
			done <- attempt.handleStreamResponse(context.Background(), &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       reader,
			})
		}()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "content_filter") {
				t.Fatalf("Messages filter terminal lost typed failure: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Messages content-filter stream did not finish")
		}
		body := recorder.Body.String()
		for _, want := range []string{"partial answer", "refusal", "message_stop"} {
			if !strings.Contains(body, want) {
				t.Fatalf("missing %q: %s", want, body)
			}
		}
		if strings.Contains(body, "data: [DONE]") || strings.Contains(body, "event: error") {
			t.Fatalf("Messages protocol terminal was cross-protocol rewritten: %s", body)
		}
	})
}

func TestStickyContentFilterCannotBeOverwrittenByLaterStop(t *testing.T) {
	filter := tmodel.TerminationMetadata{Cause: tmodel.TerminationCauseContentFilter}
	stop := tmodel.TerminationMetadata{Cause: tmodel.TerminationCauseComplete}
	attempt := &relayAttempt{}
	attempt.recordStreamTermination(&tmodel.InternalLLMResponse{Termination: filter, SawTerminalEvent: true})
	attempt.recordStreamTermination(&tmodel.InternalLLMResponse{Termination: stop, SawTerminalEvent: true})
	if attempt.streamTermination.Cause != tmodel.TerminationCauseContentFilter {
		t.Fatalf("sticky filter result = %q", attempt.streamTermination.Cause)
	}

	attempt = &relayAttempt{}
	filterReason := "content_filter"
	stopReason := "stop"
	attempt.relayRequest = &relayRequest{internalRequest: &tmodel.InternalLLMRequest{RawRequest: []byte(`{"n":2}`)}}
	attempt.internalRequest = attempt.relayRequest.internalRequest
	attempt.recordStreamTermination(&tmodel.InternalLLMResponse{
		Choices: []tmodel.Choice{{Index: 0, FinishReason: &filterReason}},
	})
	if attempt.streamTermination.Cause != tmodel.TerminationCauseContentFilter {
		t.Fatalf("per-choice filter lost: %q", attempt.streamTermination.Cause)
	}
	if !attempt.hasUnfinishedObservedStreamChoices() {
		t.Fatal("one filter among two choices must not mark the whole stream complete")
	}
	attempt.recordStreamTermination(&tmodel.InternalLLMResponse{
		Choices: []tmodel.Choice{{Index: 1, FinishReason: &stopReason}},
	})
	if attempt.streamTermination.Cause != tmodel.TerminationCauseContentFilter {
		t.Fatalf("per-choice filter was overwritten by later stop: %q", attempt.streamTermination.Cause)
	}
}

func TestChatContentFilterMultipleChoicesPreservesEveryChoice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempt, recorder := newStreamTestAttempt(t, context.Background())
		attempt.adapterType = outbound.OutboundTypeOpenAIChat
		attempt.outAdapter = outbound.Get(attempt.adapterType)
		attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
		attempt.group = &appmodel.Group{ReasoningBufferStrategy: "buffer"}
		attempt.internalRequest.RawRequest = []byte(`{"n":2}`)
		reader, writer := io.Pipe()
		defer reader.Close()
		go func() {
			defer writer.Close()
			_, _ = io.WriteString(writer, "data: "+`{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"first"},"finish_reason":null}]}`+"\n\n")
			_, _ = io.WriteString(writer, "data: "+`{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
			time.Sleep(3 * time.Second)
			_, _ = io.WriteString(writer, "data: "+`{"object":"chat.completion.chunk","choices":[{"index":1,"delta":{"content":"last"},"finish_reason":"content_filter"}]}`+"\n\n")
			_, _ = io.WriteString(writer, "data: [DONE]\n\n")
		}()
		err := attempt.handleStreamResponse(context.Background(), &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader})
		termination, ok := terminalCauseFromError(err)
		body := recorder.Body.String()
		if !ok || termination.Cause != tmodel.TerminationCauseContentFilter || !strings.Contains(body, "first") || !strings.Contains(body, "last") || strings.Count(body, `"finish_reason":"content_filter"`) != 1 || strings.Count(body, "data: [DONE]") != 1 {
			t.Fatalf("multi-choice filter changed outcome: %v %s", err, body)
		}
	})
}

func TestStreamChunkVisibleBytesCountsTextOnly(t *testing.T) {
	content := "abc"
	outputText := "de"
	reasoning := "reasoning"
	ignoredText := "ignored"
	response := &tmodel.InternalLLMResponse{Choices: []tmodel.Choice{{
		Delta: &tmodel.Message{
			Content: tmodel.MessageContent{
				Content: &content,
				MultipleContent: []tmodel.MessageContentPart{
					{Type: "output_text", Text: &outputText},
					{Type: "image_url", Text: &ignoredText},
				},
			},
			ReasoningContent: &reasoning,
			ToolCalls:        []tmodel.ToolCall{{}},
		},
	}}}
	if got := streamChunkVisibleBytes(response); got != len(content)+len(outputText) {
		t.Fatalf("visible bytes = %d, want %d", got, len(content)+len(outputText))
	}
}

func TestDiagnosticsKeepPerAttemptOutputTextIsolated(t *testing.T) {
	request, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", http.NoBody)
	trace := newRelayRequestTrace(request, false)
	t.Cleanup(trace.close)
	trace.startDiagnosticAttempt(1, 11, "chat", context.Background())
	trace.startDiagnosticAttempt(2, 22, "chat", context.Background())
	trace.recordStreamChunk(1, 0, true, 7, 0)
	trace.recordStreamChunk(2, 3, false, 0, 0)
	trueValue := true
	falseValue := false
	trace.endDiagnosticAttempt(1, context.Background(), true, &trueValue)
	trace.endDiagnosticAttempt(2, context.Background(), true, &falseValue)
	trace.markSelectedAttemptOutputTextPresent([]appmodel.ChannelAttempt{
		{AttemptNum: 1, Status: appmodel.AttemptFailed},
		{AttemptNum: 2, Status: appmodel.AttemptSuccess},
	}, true)
	diagnostics := trace.diagnosticsSnapshot()
	if diagnostics == nil || diagnostics.OutputTextPresent == nil || *diagnostics.OutputTextPresent {
		t.Fatalf("selected-attempt output state = %#v", diagnostics)
	}
	if len(diagnostics.Attempts) != 2 {
		t.Fatalf("attempt count = %d", len(diagnostics.Attempts))
	}
	if diagnostics.Attempts[0].OutputTextPresent == nil || !*diagnostics.Attempts[0].OutputTextPresent {
		t.Fatalf("failed attempt output state = %#v", diagnostics.Attempts[0])
	}
	if diagnostics.Attempts[1].OutputTextPresent == nil || *diagnostics.Attempts[1].OutputTextPresent {
		t.Fatalf("successful no-body attempt output state = %#v", diagnostics.Attempts[1])
	}

	trueValue = true
	trace.markAttemptOutputTextPresent(7, &trueValue, 33, "chat")
	diagnostics = trace.diagnosticsSnapshot()
	var hedgeAttempt *appmodel.RelayAttemptDiagnostics
	for index := range diagnostics.Attempts {
		if diagnostics.Attempts[index].Attempt == 7 {
			hedgeAttempt = &diagnostics.Attempts[index]
		}
	}
	if hedgeAttempt == nil || hedgeAttempt.ChannelID != 33 || hedgeAttempt.Protocol != "chat" ||
		hedgeAttempt.OutputTextPresent == nil || !*hedgeAttempt.OutputTextPresent {
		t.Fatalf("selected hedge attempt state = %#v", hedgeAttempt)
	}
}

func TestRelayMetricsTerminalSourceSeparatesFilterAndWriterFailures(t *testing.T) {
	filterErr := providerTerminalFailureError(tmodel.TerminationMetadata{
		Cause: tmodel.TerminationCauseContentFilter,
	})
	if got := relayMetricsTerminalSource(nil, false, filterErr); got != "upstream_terminal" {
		t.Fatalf("filter terminal source = %q", got)
	}
	if got := relayMetricsTerminalSource(nil, false, errors.New("upstream unavailable")); got != "gateway_error" {
		t.Fatalf("generic source = %q", got)
	}
	if got := relayMetricsTerminalSource(nil, false, errClientDisconnected); got != "cancelled" {
		t.Fatalf("cancel source = %q", got)
	}
	if got := relayMetricsTerminalSource(nil, true, nil); got != "upstream_terminal" {
		t.Fatalf("success source = %q", got)
	}

	request, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", http.NoBody)
	writerMetrics := &RelayMetrics{RequestTrace: newRelayRequestTrace(request, false)}
	writerMetrics.RequestTrace.recordClientWrite([]byte("payload"), 0, io.ErrShortWrite)
	if got := relayMetricsTerminalSource(writerMetrics, false, errors.New("late upstream failure")); got != "delivery_error" {
		t.Fatalf("writer-failure source = %q", got)
	}
}
