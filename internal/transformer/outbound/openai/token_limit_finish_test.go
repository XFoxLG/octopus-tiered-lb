package openai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestChatOutboundNormalizesTokenLimitFinishReasons(t *testing.T) {
	for _, tc := range []struct {
		name         string
		finishReason string
		wantFinish   string
	}{
		{"standard_length", "length", "length"},
		{"provider_alias", "max_tokens", "length"},
		{"case_insensitive_alias", "MAX_TOKENS", "length"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"id":"resp","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"partial"},"finish_reason":"` + tc.finishReason + `"}],"usage":{"prompt_tokens":1,"completion_tokens":32768,"total_tokens":32769}}`
			response, err := (&ChatOutbound{}).TransformResponse(context.Background(), &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			})
			if err != nil {
				t.Fatalf("TransformResponse error = %v", err)
			}
			if response.Termination.Cause == model.TerminationCauseTokenLimit {
				t.Fatal("response-level termination should remain per-choice for legacy Chat bodies")
			}
			choice := response.Choices[0]
			if choice.Termination.Cause != model.TerminationCauseTokenLimit || choice.Termination.ProviderReason != tc.finishReason {
				t.Fatalf("response termination = %#v, want token_limit/%s", choice.Termination, tc.finishReason)
			}
			if got := *choice.FinishReason; got != tc.wantFinish {
				t.Fatalf("response finish_reason = %q, want %q", got, tc.wantFinish)
			}

			streamBody := `{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":"` + tc.finishReason + `"}]}`
			stream, err := (&ChatOutbound{}).TransformStream(context.Background(), []byte(streamBody))
			if err != nil {
				t.Fatalf("TransformStream error = %v", err)
			}
			streamChoice := stream.Choices[0]
			if streamChoice.Termination.Cause != model.TerminationCauseTokenLimit || streamChoice.Termination.ProviderReason != tc.finishReason {
				t.Fatalf("stream termination = %#v, want token_limit/%s", streamChoice.Termination, tc.finishReason)
			}
			if got := *streamChoice.FinishReason; got != tc.wantFinish {
				t.Fatalf("stream finish_reason = %q, want %q", got, tc.wantFinish)
			}
		})
	}
}
