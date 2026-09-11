package openai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestChatOutboundRejectsErrorEnvelopesInsideSuccessfulHTTP(testContext *testing.T) {
	for _, responseBody := range []string{
		`{"error":{"message":"fixture upstream failure","type":"api_error"}}`,
		`{"error":{"message":"fixture upstream failure","code":500}}`,
		`{"error":"fixture upstream failure"}`,
	} {
		testContext.Run(responseBody, func(testContext *testing.T) {
			adapter := &ChatOutbound{}
			response, err := adapter.TransformResponse(context.Background(), &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(responseBody)),
			})
			if err == nil || !strings.Contains(err.Error(), "fixture upstream failure") || response != nil {
				testContext.Fatalf("error envelope became an empty answer: response=%+v err=%v", response, err)
			}
			chunk, err := adapter.TransformStream(context.Background(), []byte(responseBody))
			if err == nil || !strings.Contains(err.Error(), "fixture upstream failure") || chunk != nil {
				testContext.Fatalf("stream error envelope lost its cause: response=%+v err=%v", chunk, err)
			}
		})
	}
}

func TestChatOutboundRequiresChoicesOnlyForCompleteResponses(testContext *testing.T) {
	adapter := &ChatOutbound{}
	for _, responseBody := range []string{`{}`, `null`, `{"error":null}`, `{"choices":[]}`} {
		response, err := adapter.TransformResponse(context.Background(), &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(responseBody)),
		})
		if err == nil || response != nil {
			testContext.Errorf("missing chat choices became an answer: payload=%s response=%+v err=%v", responseBody, response, err)
		}
	}

	// Usage-only stream frames are valid even though they contain no choices.
	chunk, err := adapter.TransformStream(context.Background(), []byte(`{"choices":[],"error":null,"usage":{"prompt_tokens":12,"completion_tokens":3}}`))
	if err != nil || chunk == nil || chunk.Usage == nil || chunk.Usage.CompletionTokens != 3 {
		testContext.Fatalf("usage-only frame was rejected: response=%+v err=%v", chunk, err)
	}
	response, err := adapter.TransformResponse(context.Background(), &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(`{"error":null,"choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"fixture-call","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)),
	})
	if err != nil || response == nil || len(response.Choices) != 1 || len(response.Choices[0].Message.ToolCalls) != 1 {
		testContext.Fatalf("tool-only answer was rejected: response=%+v err=%v", response, err)
	}
}
