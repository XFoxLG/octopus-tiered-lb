package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
	tmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func TestRelayLogOutcomesRecognizesAcceptedTokenLimit(t *testing.T) {
	relayLog := appmodel.RelayLog{
		Error:            "accepted token-limit terminal",
		TerminationCause: string(tmodel.TerminationCauseTokenLimit),
		Attempts:         []appmodel.ChannelAttempt{{Status: appmodel.AttemptFailed, HTTPStatus: http.StatusOK}},
	}
	relayLog.TerminationCause = string(tmodel.TerminationCauseTokenLimit)

	generation, upstream := relayLogOutcomes(relayLog)
	if generation != appmodel.RelayLogGenerationFailed || upstream != appmodel.RelayLogUpstreamSuccess {
		t.Fatalf("accepted token-limit outcomes = (%q, %q), want (%q, %q)",
			generation, upstream, appmodel.RelayLogGenerationFailed, appmodel.RelayLogUpstreamSuccess)
	}
}

func TestRecordStreamTerminationKeepsTokenLimitOverLaterStop(t *testing.T) {
	limit := tmodel.TerminationMetadata{Cause: tmodel.TerminationCauseTokenLimit, ProviderReason: "max_tokens"}
	stop := tmodel.TerminationMetadata{Cause: tmodel.TerminationCauseComplete}
	attempt := &relayAttempt{}

	attempt.recordStreamTermination(&tmodel.InternalLLMResponse{Termination: limit, SawTerminalEvent: true})
	attempt.recordStreamTermination(&tmodel.InternalLLMResponse{Termination: stop, SawTerminalEvent: true})
	if attempt.streamTermination.Cause != tmodel.TerminationCauseTokenLimit {
		t.Fatalf("sticky token-limit result = %q", attempt.streamTermination.Cause)
	}
	if attempt.streamTermination.ProviderReason != "max_tokens" {
		t.Fatalf("provider reason = %q, want max_tokens", attempt.streamTermination.ProviderReason)
	}

	attempt = &relayAttempt{}
	limitReason, stopReason := "max_tokens", "stop"
	attempt.relayRequest = &relayRequest{internalRequest: &tmodel.InternalLLMRequest{RawRequest: []byte(`{"n":2}`)}}
	attempt.internalRequest = attempt.relayRequest.internalRequest
	attempt.recordStreamTermination(&tmodel.InternalLLMResponse{
		Choices: []tmodel.Choice{{Index: 0, FinishReason: &limitReason}},
	})
	if attempt.streamTermination.Cause != tmodel.TerminationCauseTokenLimit {
		t.Fatalf("per-choice token limit lost: %q", attempt.streamTermination.Cause)
	}
	if !attempt.hasUnfinishedObservedStreamChoices() {
		t.Fatal("one token-limit among two choices must not mark the whole stream complete")
	}
	attempt.recordStreamTermination(&tmodel.InternalLLMResponse{
		Choices: []tmodel.Choice{{Index: 1, FinishReason: &stopReason}},
	})
	if attempt.streamTermination.Cause != tmodel.TerminationCauseTokenLimit {
		t.Fatalf("per-choice token limit was overwritten by later stop: %q", attempt.streamTermination.Cause)
	}
}

func TestChatTokenLimitStreamIsAcceptedTerminal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempt, recorder := newStreamTestAttempt(t, context.Background())
		attempt.adapterType = outbound.OutboundTypeOpenAIChat
		attempt.outAdapter = outbound.Get(attempt.adapterType)
		attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
		attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
		reader, writer := io.Pipe()
		defer reader.Close()
		go func() {
			defer writer.Close()
			_, _ = io.WriteString(writer, `data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"partial answer"},"finish_reason":null}]}`+"\n\n")
			_, _ = io.WriteString(writer, `data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"max_tokens"}],"usage":{"prompt_tokens":2,"completion_tokens":8192,"total_tokens":8194}}`+"\n\n")
			_, _ = io.WriteString(writer, `data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"max_tokens"}]}`+"\n\n")
			_, _ = io.WriteString(writer, "data: [DONE]\n\n")
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
			if err == nil {
				t.Fatalf("accepted token-limit stream became success: %s", recorder.Body.String())
			}
			termination, ok := terminalCauseFromError(err)
			if !ok || termination.Cause != tmodel.TerminationCauseTokenLimit {
				t.Fatalf("token-limit terminal = (%#v, %t): %v", termination, ok, err)
			}
			if termination.ProviderReason != "max_tokens" {
				t.Fatalf("provider reason = %q, want max_tokens", termination.ProviderReason)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("token-limit stream did not finish")
		}

		body := recorder.Body.String()
		for _, want := range []string{"partial answer", `"finish_reason":"length"`, `"prompt_tokens":2`, `"completion_tokens":8192`, "data: [DONE]"} {
			if !strings.Contains(body, want) {
				t.Fatalf("body missing %q: %s", want, body)
			}
		}
		if strings.Contains(body, `"finish_reason":"max_tokens"`) {
			t.Fatalf("converted Chat finish_reason was not canonical: %s", body)
		}
		if strings.Contains(body, "event: error") || strings.Contains(body, "stream interrupted") {
			t.Fatalf("token-limit terminal became a generic interruption: %s", body)
		}
		if strings.Count(body, "data: [DONE]") != 1 {
			t.Fatalf("Chat DONE count = %d: %s", strings.Count(body, "data: [DONE]"), body)
		}
		if strings.Count(body, `"finish_reason":"length"`) != 1 {
			t.Fatalf("duplicate length terminal: %s", body)
		}
		if !attempt.streamOutputWasCommitted() {
			t.Fatal("accepted token-limit terminal was not committed")
		}
		if attempt.metrics.InternalResponse == nil || attempt.metrics.InternalResponse.Usage == nil ||
			attempt.metrics.InternalResponse.Usage.CompletionTokens != 8192 {
			t.Fatalf("accepted token-limit usage was lost: %#v", attempt.metrics.InternalResponse)
		}
	})
}

func TestChatZeroBodyTokenLimitDoesNotBecomeEmptyOutput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempt, recorder := newStreamTestAttempt(t, context.Background())
		attempt.adapterType = outbound.OutboundTypeOpenAIChat
		attempt.outAdapter = outbound.Get(attempt.adapterType)
		attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
		attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
		reader, writer := io.Pipe()
		defer reader.Close()
		go func() {
			defer writer.Close()
			_, _ = io.WriteString(writer, `data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"max_tokens"}]}`+"\n\n")
			_, _ = io.WriteString(writer, "data: [DONE]\n\n")
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
			if !ok || termination.Cause != tmodel.TerminationCauseTokenLimit {
				t.Fatalf("zero-body token limit = %v (%t): %s", err, ok, recorder.Body.String())
			}
			if errors.Is(err, errEmptyOutput) {
				t.Fatalf("token-limit terminal became empty-output retry: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("zero-body token-limit stream did not finish")
		}
		if strings.Contains(recorder.Body.String(), `"content":"`) {
			t.Fatalf("zero-body terminal had generated content: %s", recorder.Body.String())
		}
	})
}

func TestChatTokenLimitMultipleChoicesPreserveUsageTail(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempt, recorder := newStreamTestAttempt(t, context.Background())
		attempt.adapterType = outbound.OutboundTypeOpenAIChat
		attempt.outAdapter = outbound.Get(attempt.adapterType)
		attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
		attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
		attempt.internalRequest.RawRequest = []byte(`{"n":2}`)
		reader, writer := io.Pipe()
		defer reader.Close()
		go func() {
			defer writer.Close()
			_, _ = io.WriteString(writer, `data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"first"},"finish_reason":null}]}`+"\n\n")
			_, _ = io.WriteString(writer, `data: {"object":"chat.completion.chunk","choices":[{"index":1,"delta":{"content":"second"},"finish_reason":null}]}`+"\n\n")
			_, _ = io.WriteString(writer, `data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"max_tokens"}]}`+"\n\n")
			_, _ = io.WriteString(writer, `data: {"object":"chat.completion.chunk","choices":[{"index":1,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
			_, _ = io.WriteString(writer, `data: {"object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":32768,"total_tokens":32772}}`+"\n\n")
			_, _ = io.WriteString(writer, "data: [DONE]\n\n")
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
			if !ok || termination.Cause != tmodel.TerminationCauseTokenLimit || termination.ProviderReason != "max_tokens" {
				t.Fatalf("mixed-choice token limit = %v (%t): %s", err, ok, recorder.Body.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("mixed-choice token-limit stream did not finish")
		}

		body := recorder.Body.String()
		for _, want := range []string{"first", "second", `"completion_tokens":32768`} {
			if !strings.Contains(body, want) {
				t.Fatalf("mixed-choice body missing %q: %s", want, body)
			}
		}
		if strings.Count(body, `"finish_reason":"length"`) != 1 ||
			strings.Count(body, `"finish_reason":"stop"`) != 1 ||
			strings.Count(body, "data: [DONE]") != 1 {
			t.Fatalf("mixed-choice terminal fidelity changed: %s", body)
		}
		if attempt.metrics.InternalResponse == nil || attempt.metrics.InternalResponse.Usage == nil ||
			attempt.metrics.InternalResponse.Usage.CompletionTokens != 32768 {
			t.Fatalf("mixed-choice usage was lost: %#v", attempt.metrics.InternalResponse)
		}
	})
}

func TestChatTokenLimitFinishesAfterUsageTailWithoutEOF(t *testing.T) {
	attempt, recorder := newStreamTestAttempt(t, context.Background())
	attempt.adapterType = outbound.OutboundTypeOpenAIChat
	attempt.outAdapter = outbound.Get(attempt.adapterType)
	attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
	attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}

	done := runStreamWithUpstream(t, attempt, func(writer *io.PipeWriter) {
		_, _ = writer.Write([]byte(`data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":"max_tokens"}]}` + "\n\n"))
		_, _ = writer.Write([]byte(`data: {"object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":32768,"total_tokens":32770}}` + "\n\n"))
		_, _ = writer.Write([]byte("data: [DONE]\n\n"))
		// Deliberately keep the upstream pipe open after [DONE].
		<-time.After(10 * time.Second)
	})

	select {
	case err := <-done:
		termination, ok := terminalCauseFromError(err)
		if !ok || termination.Cause != tmodel.TerminationCauseTokenLimit {
			t.Fatalf("token-limit terminal after [DONE] = %v (%t): %s", err, ok, recorder.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("token-limit stream hung after [DONE]")
	}
	for _, want := range []string{"partial", `"completion_tokens":32768`, "data: [DONE]"} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Fatalf("token-limit [DONE] body missing %q: %s", want, recorder.Body.String())
		}
	}
}

func TestChatTokenLimitReplayFinishesWithoutGenericError(t *testing.T) {
	attempt, _ := newStreamTestAttempt(t, context.Background())
	attempt.adapterType = outbound.OutboundTypeOpenAIChat
	attempt.outAdapter = outbound.Get(attempt.adapterType)
	attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
	attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
	session := attachStreamSession(t, attempt)

	err := attempt.handleStreamResponse(context.Background(), &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			`data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":"max_tokens"}]}` + "\n\n" +
				"data: [DONE]\n\n",
		)),
	})
	termination, ok := terminalCauseFromError(err)
	if !ok || termination.Cause != tmodel.TerminationCauseTokenLimit {
		t.Fatalf("replayed token-limit outcome = %v (%t)", err, ok)
	}
	if !session.IsDone() {
		t.Fatal("replay session stayed open after accepted token-limit terminal")
	}

	var replay strings.Builder
	for _, event := range session.events {
		replay.Write(event.Payload)
	}
	payload := replay.String()
	for _, want := range []string{"partial", `"finish_reason":"length"`, "data: [DONE]"} {
		if !strings.Contains(payload, want) {
			t.Fatalf("replay missing %q: %s", want, payload)
		}
	}
	if strings.Contains(payload, "event: error") || strings.Count(payload, "data: [DONE]") != 1 {
		t.Fatalf("replay appended or duplicated a terminal: %s", payload)
	}
}

func TestChatTokenLimitLateReadErrorDoesNotOverrideTerminal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempt, recorder := newStreamTestAttempt(t, context.Background())
		attempt.adapterType = outbound.OutboundTypeOpenAIChat
		attempt.outAdapter = outbound.Get(attempt.adapterType)
		attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
		attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
		reader, writer := io.Pipe()
		defer reader.Close()
		go func() {
			_, _ = writer.Write([]byte(`data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":"max_tokens"}]}` + "\n\n"))
			_ = writer.CloseWithError(errors.New("late upstream read failure"))
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
			if !ok || termination.Cause != tmodel.TerminationCauseTokenLimit {
				t.Fatalf("late-read outcome = %v (%t): %s", err, ok, recorder.Body.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("accepted token-limit terminal did not finish before late read error")
		}
		if strings.Contains(recorder.Body.String(), "event: error") || strings.Contains(recorder.Body.String(), "stream interrupted") {
			t.Fatalf("late-read error became a second client error: %s", recorder.Body.String())
		}
	})
}

func TestHandleResponseReturnsAcceptedTokenLimitTypedFailure(t *testing.T) {
	attempt, recorder := newStreamTestAttempt(t, context.Background())
	attempt.adapterType = outbound.OutboundTypeOpenAIChat
	attempt.outAdapter = outbound.Get(attempt.adapterType)
	attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
	attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
	response := httptest.NewRecorder()
	response.Body.WriteString(`{"id":"resp","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"partial answer"},"finish_reason":"max_tokens"}],"usage":{"prompt_tokens":2,"completion_tokens":8192,"total_tokens":8194}}`)
	httpResponse := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(response.Body)}

	err := attempt.handleResponse(context.Background(), httpResponse)
	termination, ok := terminalCauseFromError(err)
	if !ok || termination.Cause != tmodel.TerminationCauseTokenLimit {
		t.Fatalf("nonstream token-limit outcome = %v (%t): %s", err, ok, recorder.Body.String())
	}
	if termination.ProviderReason != "max_tokens" {
		t.Fatalf("provider reason = %q, want max_tokens", termination.ProviderReason)
	}
	body := recorder.Body.String()
	for _, want := range []string{"partial answer", `"finish_reason":"length"`, `"completion_tokens":8192`} {
		if !strings.Contains(body, want) {
			t.Fatalf("nonstream body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `"finish_reason":"max_tokens"`) {
		t.Fatalf("converted nonstream Chat finish_reason was not canonical: %s", body)
	}
	if attempt.metrics.InternalResponse == nil || attempt.metrics.InternalResponse.Usage == nil ||
		attempt.metrics.InternalResponse.Usage.CompletionTokens != 8192 {
		t.Fatalf("nonstream token-limit usage was lost: %#v", attempt.metrics.InternalResponse)
	}
}

func TestTokenLimitAfterNormalChoiceIsNotLost(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "response", true: "stream"}[stream], func(t *testing.T) {
			attempt, recorder := newStreamTestAttempt(t, context.Background())
			attempt.adapterType = outbound.OutboundTypeOpenAIChat
			attempt.outAdapter = outbound.Get(attempt.adapterType)
			attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
			attempt.group = &appmodel.Group{ReasoningBufferStrategy: "buffer"}
			body := `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"complete"},"finish_reason":"stop"},{"index":1,"message":{"role":"assistant","content":"partial"},"finish_reason":"max_tokens"}]}`
			var err error
			if stream {
				body = "data: " + strings.ReplaceAll(strings.ReplaceAll(body, "chat.completion", "chat.completion.chunk"), `"message":`, `"delta":`) + "\n\ndata: [DONE]\n\n"
				err = attempt.handleStreamResponse(context.Background(), &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))})
			} else {
				err = attempt.handleResponse(context.Background(), &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))})
			}
			termination, ok := terminalCauseFromError(err)
			if !ok || termination.Cause != tmodel.TerminationCauseTokenLimit || termination.ProviderReason != "max_tokens" {
				t.Fatalf("mixed outcome = %#v, %v; body=%s", termination, err, recorder.Body.String())
			}
		})
	}
}

type tokenLimitFailingWriter struct{ gin.ResponseWriter }

func (w tokenLimitFailingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestBufferedTokenLimitHonorsDeliveryFailure(t *testing.T) {
	for _, failWrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "delivered", true: "writer_failed"}[failWrite], func(t *testing.T) {
			attempt, recorder := newStreamTestAttempt(t, context.Background())
			attempt.adapterType = outbound.OutboundTypeOpenAIChat
			attempt.outAdapter = outbound.Get(attempt.adapterType)
			attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
			attempt.group = &appmodel.Group{ReasoningBufferStrategy: "buffer"}
			if failWrite {
				attempt.c.Writer = tokenLimitFailingWriter{attempt.c.Writer}
			}
			body := "data: " + `{"choices":[{"index":0,"delta":{"reasoning_content":"thinking"},"finish_reason":"max_tokens"}]}` + "\n\ndata: [DONE]\n\n"
			err := attempt.handleStreamResponse(context.Background(), &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))})
			if failWrite {
				if !errors.Is(err, errClientDisconnected) {
					t.Fatalf("writer failure became %v", err)
				}
				return
			}
			termination, ok := terminalCauseFromError(err)
			if !ok || termination.Cause != tmodel.TerminationCauseTokenLimit || strings.Count(recorder.Body.String(), "[DONE]") != 1 {
				t.Fatalf("buffered outcome = %v; body=%s", err, recorder.Body.String())
			}
			if !attempt.hedgeTerminalChunk() || !hedgeMustStop(attempt, 200, err) {
				t.Fatal("accepted empty token limit must select and stop its hedge lane")
			}
		})
	}
}

func TestTokenLimitRequiresAllChoicesEvenWithDone(t *testing.T) {
	attempt, recorder := newStreamTestAttempt(t, context.Background())
	attempt.adapterType = outbound.OutboundTypeOpenAIChat
	attempt.outAdapter = outbound.Get(attempt.adapterType)
	attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
	attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
	attempt.internalRequest.RawRequest = []byte(`{"n":2}`)
	body := "data: " + `{"choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":"max_tokens"}]}` + "\n\ndata: [DONE]\n\n"
	err := attempt.handleStreamResponse(context.Background(), &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))})
	if !errors.Is(err, errMissingStreamTerminal) || strings.Contains(recorder.Body.String(), "[DONE]") {
		t.Fatalf("unfinished choice accepted DONE: %v; body=%s", err, recorder.Body.String())
	}
}

func TestTokenLimitTailWaitIsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempt, recorder := newStreamTestAttempt(t, context.Background())
		attempt.adapterType = outbound.OutboundTypeOpenAIChat
		attempt.outAdapter = outbound.Get(attempt.adapterType)
		attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
		attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
		reader, writer := io.Pipe()
		defer writer.Close()
		go func() {
			_, _ = io.WriteString(writer, "data: "+`{"choices":[{"index":0,"delta":{"content":"last text"},"finish_reason":"max_tokens"}]}`+"\n\n")
		}()
		start := time.Now()
		err := attempt.handleStreamResponse(context.Background(), &http.Response{StatusCode: 200, Body: reader})
		termination, ok := terminalCauseFromError(err)
		if !ok || termination.Cause != tmodel.TerminationCauseTokenLimit || time.Since(start) > 2*time.Second {
			t.Fatalf("tail outcome = %v, duration=%s", err, time.Since(start))
		}
		body := recorder.Body.String()
		if strings.Count(body, "last text") != 1 || strings.Count(body, `"finish_reason":"length"`) != 1 || strings.Count(body, "[DONE]") != 1 {
			t.Fatalf("tail duplicated or lost output: %s", body)
		}
	})
}
