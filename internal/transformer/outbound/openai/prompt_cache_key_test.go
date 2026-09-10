package openai

import (
	"context"
	"io"
	"strings"
	"testing"

	inboundopenai "github.com/lingyuins/octopus/internal/transformer/inbound/openai"
)

func TestChatPromptCacheKeySurvivesInboundAndOutbound(t *testing.T) {
	body := []byte(`{"model":"chat-model","messages":[{"role":"user","content":"continue this chapter"}],"prompt_cache_key":"character-session-prefix","temperature":0.8,"max_tokens":8192}`)
	request, err := (&inboundopenai.ChatInbound{}).TransformRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("string prompt_cache_key must be accepted: %v", err)
	}
	if request.PromptCacheKey == nil || *request.PromptCacheKey != "character-session-prefix" {
		t.Fatal("provider prompt-cache identity was lost")
	}
	httpRequest, err := (&ChatOutbound{}).TransformRequest(context.Background(), request, "https://provider.example.test/v1", "synthetic-key")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := io.ReadAll(httpRequest.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"prompt_cache_key":"character-session-prefix"`, `"temperature":0.8`, `"max_tokens":8192`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("client generation parameter was not preserved: %s", field)
		}
	}
}
