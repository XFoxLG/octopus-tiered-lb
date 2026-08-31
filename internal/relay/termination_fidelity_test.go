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
