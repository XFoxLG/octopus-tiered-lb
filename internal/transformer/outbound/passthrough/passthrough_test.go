package passthrough

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestTransformRequestOpenAIChatUsesRawBodyAndChatPath(t *testing.T) {
	raw := []byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}],"custom":true}`)
	stream := false
	req, err := (&Outbound{}).TransformRequest(context.Background(), &model.InternalLLMRequest{
		RawRequest:   raw,
		RawAPIFormat: model.APIFormatOpenAIChatCompletion,
		Stream:       &stream,
		Query:        mapQuery("beta", "1"),
	}, "https://example.com/api", "sk-test")
	if err != nil {
		t.Fatalf("TransformRequest error: %v", err)
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != string(raw) {
		t.Fatalf("body = %s, want raw %s", string(body), string(raw))
	}
	if req.URL.String() != "https://example.com/api/v1/chat/completions?beta=1" {
		t.Fatalf("url = %s", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := req.Header.Get("api-key"); got != "sk-test" {
		t.Fatalf("api-key = %q", got)
	}
}

func TestTransformRequestOpenAIResponsesUsesRawBodyAndResponsesPath(t *testing.T) {
	raw := []byte(`{"model":"client-model","input":"hello"}`)
	req, err := (&Outbound{}).TransformRequest(context.Background(), &model.InternalLLMRequest{
		RawRequest:   raw,
		RawAPIFormat: model.APIFormatOpenAIResponse,
	}, "https://example.com/v1", "sk-test")
	if err != nil {
		t.Fatalf("TransformRequest error: %v", err)
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != string(raw) {
		t.Fatalf("body = %s, want raw %s", string(body), string(raw))
	}
	if req.URL.String() != "https://example.com/v1/responses" {
		t.Fatalf("url = %s", req.URL.String())
	}
}

func TestTransformRequestAnthropicUsesRawBodyAndMessagesPath(t *testing.T) {
	raw := []byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}],"max_tokens":32}`)
	stream := true
	req, err := (&Outbound{}).TransformRequest(context.Background(), &model.InternalLLMRequest{
		RawRequest:   raw,
		RawAPIFormat: model.APIFormatAnthropicMessage,
		Stream:       &stream,
		Query:        mapQuery("trace", "1"),
	}, "https://anthropic.example.com/api", "sk-ant")
	if err != nil {
		t.Fatalf("TransformRequest error: %v", err)
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != string(raw) {
		t.Fatalf("body = %s, want raw %s", string(body), string(raw))
	}
	if req.URL.String() != "https://anthropic.example.com/api/messages?trace=1" {
		t.Fatalf("url = %s", req.URL.String())
	}
	if got := req.Header.Get("X-API-Key"); got != "sk-ant" {
		t.Fatalf("X-API-Key = %q", got)
	}
	if got := req.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("Accept = %q", got)
	}
}

func TestTransformRequestRewritesResolvedUpstreamModel(t *testing.T) {
	raw := []byte(`{"model":"group-name","messages":[{"role":"user","content":"hello"}],"max_tokens":32,"custom":true}`)
	stream := false
	req, err := (&Outbound{}).TransformRequest(context.Background(), &model.InternalLLMRequest{
		Model:        "claude-sonnet-4-20250514",
		RawRequest:   raw,
		RawAPIFormat: model.APIFormatAnthropicMessage,
		Stream:       &stream,
	}, "https://anthropic.example.com/api", "sk-ant")
	if err != nil {
		t.Fatalf("TransformRequest error: %v", err)
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if got["model"] != "claude-sonnet-4-20250514" {
		t.Fatalf("model = %#v, want rewritten upstream model", got["model"])
	}
	if got["custom"] != true {
		t.Fatalf("custom field should be preserved, got %#v", got["custom"])
	}
	if req.URL.String() != "https://anthropic.example.com/api/messages" {
		t.Fatalf("url = %s", req.URL.String())
	}
}

func TestTransformRequestKeepsBodyWhenModelUnchanged(t *testing.T) {
	raw := []byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}],"custom":true}`)
	req, err := (&Outbound{}).TransformRequest(context.Background(), &model.InternalLLMRequest{
		Model:        "client-model",
		RawRequest:   raw,
		RawAPIFormat: model.APIFormatOpenAIChatCompletion,
	}, "https://example.com/api", "sk-test")
	if err != nil {
		t.Fatalf("TransformRequest error: %v", err)
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != string(raw) {
		t.Fatalf("body should remain byte-identical when model is unchanged:\n got %s\nwant %s", string(body), string(raw))
	}
}

// rewritePassthroughModel 必须只替换顶层 "model" 值字节，其余字节（字段顺序、
// 空白、嵌套 model）原样保留，否则提示缓存因前缀非字节一致而失效。
func TestRewritePassthroughModelPreservesByteStability(t *testing.T) {
	raw := []byte("{\n  \"messages\": [{\"role\": \"user\", \"content\": \"hi\", \"model\": \"nested-keep\"}],\n  \"model\" :  \"group-name\" ,\n  \"custom\": true\n}")
	got, err := rewritePassthroughModel(raw, "upstream-model")
	if err != nil {
		t.Fatalf("rewritePassthroughModel error: %v", err)
	}
	want := "{\n  \"messages\": [{\"role\": \"user\", \"content\": \"hi\", \"model\": \"nested-keep\"}],\n  \"model\" :  \"upstream-model\" ,\n  \"custom\": true\n}"
	if string(got) != want {
		t.Fatalf("byte stability broken:\n got %s\nwant %s", string(got), want)
	}
}

// 缺顶层 model 时只在对象头部插入，其余字节不动；嵌套 model 不得被改写。
func TestRewritePassthroughModelInsertsWithoutTouchingRest(t *testing.T) {
	raw := []byte(`{"messages":[{"model":"nested-keep"}],"custom":1}`)
	got, err := rewritePassthroughModel(raw, "upstream-model")
	if err != nil {
		t.Fatalf("rewritePassthroughModel error: %v", err)
	}
	want := `{"model":"upstream-model","messages":[{"model":"nested-keep"}],"custom":1}`
	if string(got) != want {
		t.Fatalf("insert broken:\n got %s\nwant %s", string(got), want)
	}
}

// 转义引号与 unicode 转义的 model 值必须整体替换，不能截断。
func TestRewritePassthroughModelHandlesEscapedValue(t *testing.T) {
	raw := []byte(`{"model":"a\"b\\c","custom":true}`)
	got, err := rewritePassthroughModel(raw, "upstream-model")
	if err != nil {
		t.Fatalf("rewritePassthroughModel error: %v", err)
	}
	want := `{"model":"upstream-model","custom":true}`
	if string(got) != want {
		t.Fatalf("escaped value broken:\n got %s\nwant %s", string(got), want)
	}
}

// 非对象体（数组/标量/非法 JSON）保持原样，不报错。
func TestRewritePassthroughModelKeepsNonObjectBody(t *testing.T) {
	for _, raw := range []string{`[1,2]`, `"just-a-string"`, `not json`} {
		got, err := rewritePassthroughModel([]byte(raw), "upstream-model")
		if err != nil {
			t.Fatalf("rewritePassthroughModel(%q) error: %v", raw, err)
		}
		if string(got) != raw {
			t.Fatalf("non-object body changed: got %s want %s", string(got), raw)
		}
	}
}

func TestTransformRequestPreservePathUsesRawPath(t *testing.T) {
	raw := []byte(`{"model":"group-name","messages":[{"role":"user","content":"hello"}],"max_tokens":32,"custom":true}`)
	stream := true
	req, err := (&Outbound{PreservePath: true}).TransformRequest(context.Background(), &model.InternalLLMRequest{
		Model:        "claude-sonnet-4-20250514",
		RawRequest:   raw,
		RawAPIFormat: model.APIFormatAnthropicMessage,
		RawPath:      "/v1/messages",
		Stream:       &stream,
		Query:        mapQuery("trace", "1"),
	}, "https://anthropic.example.com", "sk-ant")
	if err != nil {
		t.Fatalf("TransformRequest error: %v", err)
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if got["model"] != "claude-sonnet-4-20250514" {
		t.Fatalf("model = %#v, want rewritten upstream model", got["model"])
	}
	if got["custom"] != true {
		t.Fatalf("custom field should be preserved, got %#v", got["custom"])
	}
	// 保留客户端原始路径 /v1/messages，而不是 Anthropic 固定端点 /messages
	if req.URL.String() != "https://anthropic.example.com/v1/messages?trace=1" {
		t.Fatalf("url = %s", req.URL.String())
	}
	if got := req.Header.Get("X-API-Key"); got != "sk-ant" {
		t.Fatalf("X-API-Key = %q", got)
	}
}

// Anthropic PreservePath 的 base URL 带前缀时必须原样拼接（不做 OpenAI 风格的
// /v1 去重），否则 raw 出站 + Anthropic 入站会得到 404 的错误 URL。
func TestTransformRequestPreservePathAnthropicKeepsBasePrefix(t *testing.T) {
	raw := []byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}],"max_tokens":32}`)
	stream := true
	req, err := (&Outbound{PreservePath: true}).TransformRequest(context.Background(), &model.InternalLLMRequest{
		Model:        "client-model",
		RawRequest:   raw,
		RawAPIFormat: model.APIFormatAnthropicMessage,
		RawPath:      "/v1/messages",
		Stream:       &stream,
		Query:        mapQuery("beta", "1"),
	}, "https://anthropic-proxy.example.com/api", "sk-ant")
	if err != nil {
		t.Fatalf("TransformRequest error: %v", err)
	}

	want := "https://anthropic-proxy.example.com/api/v1/messages?beta=1"
	if req.URL.String() != want {
		t.Fatalf("url = %s, want %s", req.URL.String(), want)
	}
	if got := req.Header.Get("X-API-Key"); got != "sk-ant" {
		t.Fatalf("X-API-Key = %q", got)
	}
	if got := req.Header.Get("Anthropic-Version"); got != "2023-06-01" {
		t.Fatalf("Anthropic-Version = %q", got)
	}
}

func TestTransformRequestPreservePathDeduplicatesVersionRoot(t *testing.T) {
	raw := []byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`)
	req, err := (&Outbound{PreservePath: true}).TransformRequest(context.Background(), &model.InternalLLMRequest{
		Model:        "client-model",
		RawRequest:   raw,
		RawAPIFormat: model.APIFormatOpenAIChatCompletion,
		RawPath:      "/v1/chat/completions",
	}, "https://example.com/v1", "sk-test")
	if err != nil {
		t.Fatalf("TransformRequest error: %v", err)
	}

	if req.URL.String() != "https://example.com/v1/chat/completions" {
		t.Fatalf("url = %s, want version root deduplicated", req.URL.String())
	}
}

func TestTransformRequestPreservePathAppendsToBasePrefix(t *testing.T) {
	raw := []byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}],"max_tokens":32}`)
	req, err := (&Outbound{PreservePath: true}).TransformRequest(context.Background(), &model.InternalLLMRequest{
		Model:        "client-model",
		RawRequest:   raw,
		RawAPIFormat: model.APIFormatAnthropicMessage,
		RawPath:      "/v1/messages",
	}, "https://proxy.example.com/anthropic", "sk-ant")
	if err != nil {
		t.Fatalf("TransformRequest error: %v", err)
	}

	if req.URL.String() != "https://proxy.example.com/anthropic/v1/messages" {
		t.Fatalf("url = %s, want raw path appended to base prefix", req.URL.String())
	}
}

func TestTransformRequestPreservePathFallsBackWhenRawPathEmpty(t *testing.T) {
	raw := []byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`)
	req, err := (&Outbound{PreservePath: true}).TransformRequest(context.Background(), &model.InternalLLMRequest{
		Model:        "client-model",
		RawRequest:   raw,
		RawAPIFormat: model.APIFormatOpenAIChatCompletion,
	}, "https://example.com/api", "sk-test")
	if err != nil {
		t.Fatalf("TransformRequest error: %v", err)
	}

	if req.URL.String() != "https://example.com/api/v1/chat/completions" {
		t.Fatalf("url = %s, want canonical endpoint fallback", req.URL.String())
	}
}

func TestTransformRequestRejectsUnsupportedFormat(t *testing.T) {
	_, err := (&Outbound{}).TransformRequest(context.Background(), &model.InternalLLMRequest{
		RawRequest:   []byte(`{"model":"x"}`),
		RawAPIFormat: model.APIFormatOpenAIEmbedding,
	}, "https://example.com", "sk-test")
	if err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("expected unsupported format error, got %v", err)
	}
}

func mapQuery(key, value string) map[string][]string {
	return map[string][]string{key: []string{value}}
}
