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

	"github.com/gin-gonic/gin"
	dbmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/model"
)

type terminationFidelityTestOutbound struct {
	internalStream *model.InternalLLMResponse
}

func (adapter *terminationFidelityTestOutbound) TransformRequest(context.Context, *model.InternalLLMRequest, string, string) (*http.Request, error) {
	return nil, nil
}

func (adapter *terminationFidelityTestOutbound) TransformResponse(context.Context, *http.Response) (*model.InternalLLMResponse, error) {
	return nil, nil
}

func (adapter *terminationFidelityTestOutbound) TransformStream(context.Context, []byte) (*model.InternalLLMResponse, error) {
	return adapter.internalStream, nil
}

type terminationFidelityTestInbound struct {
	streamPayload []byte
}

func (adapter *terminationFidelityTestInbound) TransformRequest(context.Context, []byte) (*model.InternalLLMRequest, error) {
	return nil, nil
}

func (adapter *terminationFidelityTestInbound) TransformResponse(context.Context, *model.InternalLLMResponse) ([]byte, error) {
	return nil, nil
}

func (adapter *terminationFidelityTestInbound) TransformStream(context.Context, *model.InternalLLMResponse) ([]byte, error) {
	return adapter.streamPayload, nil
}

func (adapter *terminationFidelityTestInbound) GetInternalResponse(context.Context) (*model.InternalLLMResponse, error) {
	return nil, nil
}

type terminationFidelityErrorReadCloser struct {
	reader        *strings.Reader
	terminalError error
	returnedError bool
}

func (reader *terminationFidelityErrorReadCloser) Read(buffer []byte) (int, error) {
	if reader.reader != nil && reader.reader.Len() > 0 {
		return reader.reader.Read(buffer)
	}
	if !reader.returnedError {
		reader.returnedError = true
		return 0, reader.terminalError
	}
	return 0, io.EOF
}

func (reader *terminationFidelityErrorReadCloser) Close() error {
	return nil
}

func newTerminationFidelityRelayAttempt(recorder *httptest.ResponseRecorder, internalStream *model.InternalLLMResponse, streamPayload []byte) *relayAttempt {
	gin.SetMode(gin.TestMode)
	ginContext, _ := gin.CreateTestContext(recorder)
	clientContext := context.Background()
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(clientContext)

	stream := true
	return &relayAttempt{
		relayRequest: &relayRequest{
			c:               ginContext,
			clientCtx:       clientContext,
			operationCtx:    context.Background(),
			inAdapter:       &terminationFidelityTestInbound{streamPayload: streamPayload},
			internalRequest: &model.InternalLLMRequest{Stream: &stream},
			metrics:         &RelayMetrics{},
		},
		outAdapter: &terminationFidelityTestOutbound{
			internalStream: internalStream,
		},
		channel: &dbmodel.Channel{Name: "termination-test"},
	}
}

func TestClassifyRelayErrorHandlesMissingStreamTerminal(t *testing.T) {
	beforeWrite := ClassifyRelayError(http.StatusOK, errMissingStreamTerminal, false)
	if beforeWrite.Scope != ScopeSameChannel {
		t.Fatalf("pre-write scope = %s, want %s", beforeWrite.Scope, ScopeSameChannel)
	}

	afterWrite := ClassifyRelayError(http.StatusOK, errMissingStreamTerminal, true)
	if afterWrite.Scope != ScopeAbortAll {
		t.Fatalf("post-write scope = %s, want %s", afterWrite.Scope, ScopeAbortAll)
	}
}

func TestReplaySessionOutputPreventsMissingTerminalRetry(t *testing.T) {
	attempt := &relayAttempt{streamOutputCommitted: true}
	decision := ClassifyRelayError(http.StatusOK, errMissingStreamTerminal, attempt.streamOutputWasCommitted())
	if decision.Scope != ScopeAbortAll {
		t.Fatalf("replay-session output decision = %s, want %s", decision.Scope, ScopeAbortAll)
	}
}

func TestRecordStreamTerminationRequiresAllObservedChoicesToFinish(t *testing.T) {
	finishReason := "stop"
	attempt := &relayAttempt{}

	attempt.recordStreamTermination(&model.InternalLLMResponse{
		Choices: []model.Choice{
			{Index: 0, FinishReason: &finishReason},
			{Index: 1},
		},
	})

	if !attempt.streamSawTerminalEvent {
		t.Fatal("finished choice must be recorded as a terminal candidate")
	}
	if !attempt.hasUnfinishedObservedStreamChoices() {
		t.Fatal("unfinished observed choice must keep EOF on the missing-terminal path")
	}

	attempt.recordStreamTermination(&model.InternalLLMResponse{
		Choices: []model.Choice{{
			Index:        1,
			FinishReason: &finishReason,
		}},
	})
	if attempt.hasUnfinishedObservedStreamChoices() {
		t.Fatal("all observed choices finished, but stream remains incomplete")
	}

	attempt.recordStreamTermination(&model.InternalLLMResponse{
		Object:           "[DONE]",
		SawTerminalEvent: true,
	})
	if attempt.hasUnfinishedObservedStreamChoices() {
		t.Fatal("an explicit stream-wide terminal marker must supersede choice tracking")
	}
}

func TestIsEmptyOutputResponseDoesNotRetryProviderRefusal(t *testing.T) {
	response := &model.InternalLLMResponse{
		Choices: []model.Choice{{
			Termination: model.TerminationMetadata{
				Cause: model.TerminationCauseContentFilter,
			},
		}},
	}
	if isEmptyOutputResponse(response) {
		t.Fatal("content-filtered empty response must not be retried as empty output")
	}
}

func TestIsEmptyOutputResponseDoesNotRetryProviderFailure(t *testing.T) {
	for _, cause := range []model.TerminationCause{
		model.TerminationCauseError,
		model.TerminationCauseUnknown,
	} {
		response := &model.InternalLLMResponse{
			Choices: []model.Choice{{
				Termination: model.TerminationMetadata{Cause: cause},
			}},
		}
		if isEmptyOutputResponse(response) {
			t.Fatalf("%q provider terminal was retried as an empty response", cause)
		}
	}
}

func TestResponseHasNonCacheableTerminationRejectsPromptBlock(t *testing.T) {
	promptBlockedResponse := &model.InternalLLMResponse{
		Termination: model.TerminationMetadata{
			Cause: model.TerminationCausePromptBlocked,
		},
	}
	if !responseHasNonCacheableTermination(promptBlockedResponse) {
		t.Fatal("prompt-blocked response must not enter the semantic cache")
	}

	completedResponse := &model.InternalLLMResponse{
		Termination: model.TerminationMetadata{
			Cause: model.TerminationCauseComplete,
		},
	}
	if responseHasNonCacheableTermination(completedResponse) {
		t.Fatal("completed response must remain eligible for semantic caching")
	}
}

func TestResponseProviderFailureDistinguishesFailureFromRefusal(t *testing.T) {
	providerFailure := &model.InternalLLMResponse{
		Termination: model.TerminationMetadata{
			Cause:          model.TerminationCauseError,
			ProviderReason: "response.failed",
		},
	}
	termination, found := responseProviderFailure(providerFailure)
	if !found || termination.Cause != model.TerminationCauseError {
		t.Fatalf("responseProviderFailure() = (%#v, %t), want error termination", termination, found)
	}

	modelRefusal := &model.InternalLLMResponse{
		Termination: model.TerminationMetadata{
			Cause: model.TerminationCauseRefusal,
		},
	}
	if termination, found := responseProviderFailure(modelRefusal); found {
		t.Fatalf("responseProviderFailure() = (%#v, true), refusal must remain a model outcome", termination)
	}
}

func TestIsTruncatedOutputUsesCanonicalTermination(t *testing.T) {
	response := &model.InternalLLMResponse{
		Choices: []model.Choice{{
			Termination: model.TerminationMetadata{
				Cause: model.TerminationCauseTokenLimit,
			},
		}},
	}
	if !isTruncatedOutput(response) {
		t.Fatal("token-limit termination must be detected as truncated output")
	}
}

func TestHandleStreamResponseReportsMissingTerminalAfterPartialOutput(t *testing.T) {
	visibleText := "partial output"
	internalStream := &model.InternalLLMResponse{
		Object: "chat.completion.chunk",
		Choices: []model.Choice{{
			Index: 0,
			Delta: &model.Message{
				Role: "assistant",
				Content: model.MessageContent{
					Content: &visibleText,
				},
			},
		}},
	}
	recorder := httptest.NewRecorder()
	attempt := newTerminationFidelityRelayAttempt(recorder, internalStream, []byte("data: partial output\n\n"))

	err := attempt.handleStreamResponse(context.Background(), &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("data: upstream partial\n\n")),
	})
	if !errors.Is(err, errMissingStreamTerminal) {
		t.Fatalf("handleStreamResponse() error = %v, want missing terminal", err)
	}

	responseBody := recorder.Body.String()
	if !strings.Contains(responseBody, "data: partial output") {
		t.Fatalf("response body lacks partial output: %s", responseBody)
	}
	if !strings.Contains(responseBody, "event: error") {
		t.Fatalf("response body lacks SSE error event: %s", responseBody)
	}
}

func TestHandleStreamResponseKeepsNoOutputMissingTerminalRetryable(t *testing.T) {
	internalStream := &model.InternalLLMResponse{
		Object: "chat.completion.chunk",
	}
	recorder := httptest.NewRecorder()
	attempt := newTerminationFidelityRelayAttempt(recorder, internalStream, []byte("data: metadata\n\n"))

	err := attempt.handleStreamResponse(context.Background(), &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("data: upstream metadata\n\n")),
	})
	if !errors.Is(err, errMissingStreamTerminal) {
		t.Fatalf("handleStreamResponse() error = %v, want missing terminal", err)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("no-output missing terminal wrote downstream data: %s", recorder.Body.String())
	}
}

func TestHandleStreamResponseKeepsSessionOpenForPreOutputReadError(t *testing.T) {
	sessionStore := &relayStreamSessionStore{
		byKey:                make(map[string]*relayStreamSession),
		activeByConversation: make(map[string]string),
	}
	session := &relayStreamSession{
		store:             sessionStore,
		key:               "termination-pre-output",
		conversationScope: "1:termination-pre-output",
		subscribers:       make(map[chan struct{}]struct{}),
	}

	recorder := httptest.NewRecorder()
	attempt := newTerminationFidelityRelayAttempt(recorder, nil, nil)
	attempt.streamSession = session
	readError := errors.New("upstream read failed before output")

	err := attempt.handleStreamResponse(context.Background(), &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: &terminationFidelityErrorReadCloser{
			terminalError: readError,
		},
	})
	if !errors.Is(err, readError) {
		t.Fatalf("handleStreamResponse() error = %v, want wrapped %v", err, readError)
	}
	if session.IsDone() {
		t.Fatal("pre-output read failure prematurely finished the replay session")
	}
	if attempt.streamOutputWasCommitted() {
		t.Fatal("pre-output read failure incorrectly marked stream output committed")
	}
}

func TestHandleStreamResponseReportsPostOutputReadError(t *testing.T) {
	visibleText := "visible output"
	internalStream := &model.InternalLLMResponse{
		Object: "chat.completion.chunk",
		Choices: []model.Choice{{
			Index: 0,
			Delta: &model.Message{
				Role: "assistant",
				Content: model.MessageContent{
					Content: &visibleText,
				},
			},
		}},
	}
	recorder := httptest.NewRecorder()
	attempt := newTerminationFidelityRelayAttempt(recorder, internalStream, []byte("data: visible output\n\n"))
	readError := errors.New("upstream read failed after output")

	err := attempt.handleStreamResponse(context.Background(), &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: &terminationFidelityErrorReadCloser{
			reader:        strings.NewReader("data: upstream visible output\n\n"),
			terminalError: readError,
		},
	})
	if !errors.Is(err, readError) {
		t.Fatalf("handleStreamResponse() error = %v, want wrapped %v", err, readError)
	}
	if !attempt.streamOutputWasCommitted() {
		t.Fatal("post-output read failure did not mark stream output committed")
	}

	responseBody := recorder.Body.String()
	if !strings.Contains(responseBody, "data: visible output") {
		t.Fatalf("response body lacks visible output: %s", responseBody)
	}
	if !strings.Contains(responseBody, "event: error") {
		t.Fatalf("response body lacks SSE interruption event: %s", responseBody)
	}
	if !strings.Contains(responseBody, `"type":"api_error"`) || !strings.Contains(responseBody, `"code":"stream_interrupted"`) {
		t.Fatalf("response body lacks OpenAI-compatible interruption envelope: %s", responseBody)
	}
}

func TestHandleStreamResponseIgnoresReadErrorAfterAcceptedTerminal(t *testing.T) {
	finishReason := "stop"
	visibleText := "complete output"
	internalStream := &model.InternalLLMResponse{
		Object: "chat.completion.chunk",
		Choices: []model.Choice{{
			Index:        0,
			FinishReason: &finishReason,
			Delta: &model.Message{
				Role: "assistant",
				Content: model.MessageContent{
					Content: &visibleText,
				},
			},
		}},
	}
	recorder := httptest.NewRecorder()
	attempt := newTerminationFidelityRelayAttempt(recorder, internalStream, []byte("data: complete output\n\n"))
	readError := errors.New("socket closed after terminal event")

	err := attempt.handleStreamResponse(context.Background(), &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: &terminationFidelityErrorReadCloser{
			reader:        strings.NewReader("data: upstream terminal\n\n"),
			terminalError: readError,
		},
	})
	if err != nil {
		t.Fatalf("handleStreamResponse() error = %v, want successful terminal completion", err)
	}
	if strings.Contains(recorder.Body.String(), "event: error") {
		t.Fatalf("accepted terminal received a second error event: %s", recorder.Body.String())
	}
}

func TestHandleStreamResponseBuffersOneInterruptionForReplaySession(t *testing.T) {
	visibleText := "visible output"
	internalStream := &model.InternalLLMResponse{
		Object: "chat.completion.chunk",
		Choices: []model.Choice{{
			Index: 0,
			Delta: &model.Message{
				Role: "assistant",
				Content: model.MessageContent{
					Content: &visibleText,
				},
			},
		}},
	}
	sessionStore := &relayStreamSessionStore{
		byKey:                make(map[string]*relayStreamSession),
		activeByConversation: make(map[string]string),
	}
	session := &relayStreamSession{
		store:             sessionStore,
		key:               "termination-replay",
		conversationScope: "1:termination-replay",
		subscribers:       make(map[chan struct{}]struct{}),
	}
	recorder := httptest.NewRecorder()
	attempt := newTerminationFidelityRelayAttempt(recorder, internalStream, []byte("data: visible output\n\n"))
	attempt.streamSession = session
	readError := errors.New("upstream read failed after replayable output")

	err := attempt.handleStreamResponse(context.Background(), &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: &terminationFidelityErrorReadCloser{
			reader:        strings.NewReader("data: upstream visible output\n\n"),
			terminalError: readError,
		},
	})
	if !errors.Is(err, readError) {
		t.Fatalf("handleStreamResponse() error = %v, want wrapped %v", err, readError)
	}
	if !session.IsDone() {
		t.Fatal("post-output interruption must finish the replay session")
	}

	events, done, sessionErr := session.Snapshot(0)
	if !done || sessionErr != nil {
		t.Fatalf("session Snapshot() = (%#v, done=%t, err=%v), want done with buffered native error", events, done, sessionErr)
	}
	if len(events) != 2 {
		t.Fatalf("replay event count = %d, want visible payload plus one interruption", len(events))
	}
	if !strings.Contains(string(events[1].Payload), "event: error") {
		t.Fatalf("replay terminal event = %s, want error event", events[1].Payload)
	}

	replayRecorder := httptest.NewRecorder()
	replayContext, _ := gin.CreateTestContext(replayRecorder)
	replayContext.Request = httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	serveRelayStreamSession(replayContext, &relayRequest{
		streamSession: session,
		internalRequest: &model.InternalLLMRequest{
			ResumeFromEventID: 0,
		},
	})
	if count := strings.Count(replayRecorder.Body.String(), "event: error"); count != 1 {
		t.Fatalf("replay error event count = %d, want exactly 1; body=%s", count, replayRecorder.Body.String())
	}
}

func TestHandleStreamResponseFailsProviderTerminalError(t *testing.T) {
	internalStream := &model.InternalLLMResponse{
		Object: "chat.completion.chunk",
		Termination: model.TerminationMetadata{
			Cause:          model.TerminationCauseError,
			ProviderReason: "response.failed",
			Detail:         "upstream reported a failure",
		},
	}
	recorder := httptest.NewRecorder()
	attempt := newTerminationFidelityRelayAttempt(recorder, internalStream, []byte("data: provider terminal failure\n\n"))

	err := attempt.handleStreamResponse(context.Background(), &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("data: upstream failure terminal\n\n")),
	})
	if !errors.Is(err, errProviderTerminalFailure) {
		t.Fatalf("handleStreamResponse() error = %v, want provider terminal failure", err)
	}
	if !attempt.streamOutputWasCommitted() {
		t.Fatal("provider terminal failure did not mark its terminal payload committed")
	}
	if strings.Contains(recorder.Body.String(), "event: error") {
		t.Fatalf("provider-native terminal failure received a duplicate generic SSE error: %s", recorder.Body.String())
	}
}

func TestHandleStreamResponseWaitsForVisibleOutputBeforeFirstTimeout(t *testing.T) {
	internalStream := &model.InternalLLMResponse{Object: "chat.completion.chunk"}
	recorder := httptest.NewRecorder()
	attempt := newTerminationFidelityRelayAttempt(recorder, internalStream, []byte("data: metadata\n\n"))
	attempt.firstTokenTimeOutSec = 1
	attempt.group = &dbmodel.Group{ReasoningBufferStrategy: "buffer"}

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	go func() {
		_, _ = writer.Write([]byte("data: upstream metadata\n\n"))
	}()

	result := make(chan error, 1)
	go func() {
		result <- attempt.handleStreamResponse(context.Background(), &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       reader,
		})
	}()

	select {
	case err := <-result:
		if !errors.Is(err, errFirstVisibleOutputTimeout) {
			t.Fatalf("handleStreamResponse() error = %v, want first visible output timeout", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("metadata-only stream incorrectly disabled the first visible output timeout")
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("metadata-only stream wrote downstream data: %s", recorder.Body.String())
	}
}

func TestHandleStreamResponseAbortsIdleVisibleStream(t *testing.T) {
	visibleText := "visible output"
	internalStream := &model.InternalLLMResponse{
		Object: "chat.completion.chunk",
		Choices: []model.Choice{{
			Index: 0,
			Delta: &model.Message{
				Role: "assistant",
				Content: model.MessageContent{
					Content: &visibleText,
				},
			},
		}},
	}
	recorder := httptest.NewRecorder()
	attempt := newTerminationFidelityRelayAttempt(recorder, internalStream, []byte("data: visible output\n\n"))
	attempt.group = &dbmodel.Group{StreamIdleTimeout: 1}

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	go func() {
		_, _ = writer.Write([]byte("data: upstream visible output\n\n"))
	}()

	result := make(chan error, 1)
	go func() {
		result <- attempt.handleStreamResponse(context.Background(), &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       reader,
		})
	}()

	select {
	case err := <-result:
		if !errors.Is(err, errStreamIdleTimeout) {
			t.Fatalf("handleStreamResponse() error = %v, want stream idle timeout", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("visible stream did not abort after idle timeout")
	}

	responseBody := recorder.Body.String()
	if !strings.Contains(responseBody, "data: visible output") {
		t.Fatalf("idle stream response lacks visible output: %s", responseBody)
	}
	if !strings.Contains(responseBody, "event: error") {
		t.Fatalf("idle stream response lacks SSE error event: %s", responseBody)
	}
}
