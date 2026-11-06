package gemini

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestMessagesOutboundTransformStreamPreservesFlexibleErrorFrames(testContext *testing.T) {
	testCases := []struct {
		name               string
		payload            string
		expectedCode       string
		expectedReason     string
		expectedMessage    string
		expectedHTTPStatus int
	}{
		{
			name:               "symbolic proxy code inside HTTP 200 SSE",
			payload:            `{"error":{"message":"fixture credential unavailable","type":"pggo_error","code":"no_credential","param":null,"http_status":503}}`,
			expectedCode:       "no_credential",
			expectedReason:     "pggo_error",
			expectedMessage:    "fixture credential unavailable",
			expectedHTTPStatus: 503,
		},
		{
			name:            "native numeric code inside HTTP 200 SSE",
			payload:         `{"error":{"code":503,"message":"fixture upstream unavailable","status":"UNAVAILABLE"}}`,
			expectedCode:    "503",
			expectedReason:  "UNAVAILABLE",
			expectedMessage: "fixture upstream unavailable",
		},
		{
			name:            "missing code",
			payload:         `{"error":{"message":"fixture failed","status":"INVALID_ARGUMENT"}}`,
			expectedCode:    "",
			expectedReason:  "INVALID_ARGUMENT",
			expectedMessage: "fixture failed",
		},
		{
			name:            "future code shape",
			payload:         `{"error":{"code":{"detail":"fixture"},"message":"fixture failed","type":"pggo_error"}}`,
			expectedCode:    `{"detail":"fixture"}`,
			expectedReason:  "pggo_error",
			expectedMessage: "fixture failed",
		},
	}

	outbound := &MessagesOutbound{}
	for _, testCase := range testCases {
		testContext.Run(testCase.name, func(testContext *testing.T) {
			response, err := outbound.TransformStream(context.Background(), []byte(testCase.payload))
			if err != nil {
				testContext.Fatalf("transform flexible error frame: %v", err)
			}
			if response == nil || !response.SawTerminalEvent {
				testContext.Fatalf("HTTP 200 error envelope became a non-terminal response: response=%#v err=%v", response, err)
			}
			if response.Termination.Cause != model.TerminationCauseError {
				testContext.Fatalf("termination cause = %q, want error", response.Termination.Cause)
			}
			if response.Termination.ProviderReason != testCase.expectedReason {
				testContext.Fatalf("provider reason = %q, want %q", response.Termination.ProviderReason, testCase.expectedReason)
			}
			if response.Termination.Detail != testCase.expectedMessage {
				testContext.Fatalf("termination detail = %q, want %q", response.Termination.Detail, testCase.expectedMessage)
			}
			if response.Error == nil {
				testContext.Fatal("typed provider error was lost")
			}
			if response.Error.StatusCode != testCase.expectedHTTPStatus {
				testContext.Fatalf("error status = %d, want %d", response.Error.StatusCode, testCase.expectedHTTPStatus)
			}
			if response.Error.Detail.Code != testCase.expectedCode {
				testContext.Fatalf("error code = %q, want %q", response.Error.Detail.Code, testCase.expectedCode)
			}
			if response.Error.Detail.Message != testCase.expectedMessage {
				testContext.Fatalf("error message = %q, want %q", response.Error.Detail.Message, testCase.expectedMessage)
			}
			if response.Error.Detail.Type != testCase.expectedReason {
				testContext.Fatalf("error type = %q, want %q", response.Error.Detail.Type, testCase.expectedReason)
			}
		})
	}
}

func TestMessagesOutboundTransformStreamRejectsMalformedJSON(testContext *testing.T) {
	response, err := (&MessagesOutbound{}).TransformStream(context.Background(), []byte(`{"error":{"code":`))
	if err == nil || response != nil {
		testContext.Fatalf("malformed JSON became a response: response=%#v err=%v", response, err)
	}
	if !strings.Contains(err.Error(), "failed to unmarshal gemini stream chunk") {
		testContext.Fatalf("decode failure lost its cause: %v", err)
	}
}

func TestMessagesOutboundTransformResponsePreservesProviderError(testContext *testing.T) {
	testCases := []struct {
		name           string
		httpStatus     int
		payload        string
		terminal       bool
		expectedCode   string
		expectedReason string
		expectedStatus int
	}{
		{
			name:           "symbolic code on HTTP error",
			httpStatus:     http.StatusServiceUnavailable,
			payload:        `{"error":{"message":"fixture credential unavailable","type":"pggo_error","code":"no_credential"}}`,
			expectedCode:   "no_credential",
			expectedReason: "pggo_error",
			expectedStatus: http.StatusServiceUnavailable,
		},
		{
			name:           "native code on HTTP error",
			httpStatus:     http.StatusServiceUnavailable,
			payload:        `{"error":{"code":503,"message":"fixture upstream unavailable","status":"UNAVAILABLE"}}`,
			expectedCode:   "503",
			expectedReason: "UNAVAILABLE",
			expectedStatus: http.StatusServiceUnavailable,
		},
		{
			name:           "HTTP 200 symbolic error remains a failure",
			httpStatus:     http.StatusOK,
			payload:        `{"error":{"message":"fixture credential unavailable","type":"pggo_error","code":"no_credential","http_status":503}}`,
			terminal:       true,
			expectedCode:   "no_credential",
			expectedReason: "pggo_error",
			expectedStatus: http.StatusServiceUnavailable,
		},
	}

	outbound := &MessagesOutbound{}
	for _, testCase := range testCases {
		testContext.Run(testCase.name, func(testContext *testing.T) {
			response, err := outbound.TransformResponse(context.Background(), &http.Response{
				StatusCode: testCase.httpStatus,
				Body:       io.NopCloser(strings.NewReader(testCase.payload)),
			})
			if testCase.terminal {
				if err != nil || response == nil || !response.SawTerminalEvent || response.Error == nil {
					testContext.Fatalf("HTTP 200 error envelope response=%#v err=%v", response, err)
				}
			} else {
				if response != nil || err == nil {
					testContext.Fatalf("HTTP error response=%#v err=%v", response, err)
				}
				var responseError *model.ResponseError
				if !errors.As(err, &responseError) {
					testContext.Fatalf("error type = %T, want *model.ResponseError: %v", err, err)
				}
				response = &model.InternalLLMResponse{Error: responseError}
			}

			if response.Error.StatusCode != testCase.expectedStatus {
				testContext.Fatalf("error status = %d, want %d", response.Error.StatusCode, testCase.expectedStatus)
			}
			if response.Error.Detail.Code != testCase.expectedCode {
				testContext.Fatalf("error code = %q, want %q", response.Error.Detail.Code, testCase.expectedCode)
			}
			if response.Error.Detail.Type != testCase.expectedReason {
				testContext.Fatalf("error type = %q, want %q", response.Error.Detail.Type, testCase.expectedReason)
			}
			if err != nil && !strings.Contains(err.Error(), "fixture credential unavailable") && !strings.Contains(err.Error(), "fixture upstream unavailable") {
				testContext.Fatalf("provider cause was masked: %v", err)
			}
		})
	}
}
