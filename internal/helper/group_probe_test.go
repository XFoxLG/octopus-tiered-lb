package helper

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

func TestBuildGroupProbeRequest_ConversationEndpointsUseMessages(t *testing.T) {
	for _, endpointType := range []string{
		appmodel.EndpointTypeAll,
		appmodel.EndpointTypeChat,
		appmodel.EndpointTypeDeepSeek,
		appmodel.EndpointTypeResponses,
		appmodel.EndpointTypeMessages,
	} {
		req, err := buildGroupProbeRequest(endpointType, "gpt-4o-mini")
		if err != nil {
			t.Fatalf("buildGroupProbeRequest(%q) error = %v", endpointType, err)
		}
		if req.EmbeddingInput != nil {
			t.Fatalf("buildGroupProbeRequest(%q) embedding_input = %#v, want nil", endpointType, req.EmbeddingInput)
		}
		if len(req.Messages) != 1 {
			t.Fatalf("buildGroupProbeRequest(%q) messages len = %d, want 1", endpointType, len(req.Messages))
		}
		if req.Messages[0].Content.Content == nil || *req.Messages[0].Content.Content != "hi" {
			t.Fatalf("buildGroupProbeRequest(%q) message content = %#v, want hi", endpointType, req.Messages[0].Content.Content)
		}
		if req.Stream == nil || *req.Stream {
			t.Fatalf("buildGroupProbeRequest(%q) stream = %#v, want false", endpointType, req.Stream)
		}
	}
}

func TestBuildGroupProbeRequest_EmbeddingsUseInput(t *testing.T) {
	req, err := buildGroupProbeRequest(appmodel.EndpointTypeEmbeddings, "text-embedding-3-small")
	if err != nil {
		t.Fatalf("buildGroupProbeRequest() error = %v", err)
	}
	if len(req.Messages) != 0 {
		t.Fatalf("buildGroupProbeRequest() messages len = %d, want 0", len(req.Messages))
	}
	if req.EmbeddingInput == nil || req.EmbeddingInput.Single == nil || *req.EmbeddingInput.Single != "hi" {
		t.Fatalf("buildGroupProbeRequest() embedding_input = %#v, want single hi", req.EmbeddingInput)
	}
}

func TestBuildGroupProbeRequest_RejectsUnsupportedEndpoint(t *testing.T) {
	if _, err := buildGroupProbeRequest(appmodel.EndpointTypeImageGeneration, "gpt-image-1"); err == nil {
		t.Fatal("buildGroupProbeRequest() error = nil, want unsupported endpoint error")
	}
}

func TestValidateGroupProbeChannelType_RejectsMismatchedEmbeddingChannel(t *testing.T) {
	err := validateGroupProbeChannelType(appmodel.EndpointTypeEmbeddings, outbound.OutboundTypeOpenAIChat)
	if err == nil {
		t.Fatal("validateGroupProbeChannelType() error = nil, want mismatch error")
	}
}

func TestValidateGroupProbeChannelType_AllAcceptsChatChannel(t *testing.T) {
	err := validateGroupProbeChannelType(appmodel.EndpointTypeAll, outbound.OutboundTypeOpenAIChat)
	if err != nil {
		t.Fatalf("validateGroupProbeChannelType() error = %v, want nil", err)
	}
}

func TestSendGroupProbeRequest_EmbeddingsUseEmbeddingPayload(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	type outboundRequest struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("request path = %q, want /v1/embeddings", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("Authorization = %q, want Bearer sk-test", got)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}

		var got outboundRequest
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal request body: %v", err)
		}
		if got.Input != "hi" {
			t.Fatalf("input = %q, want hi", got.Input)
		}
		if got.Model != "text-embedding-3-small" {
			t.Fatalf("model = %q, want text-embedding-3-small", got.Model)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"emb-1","object":"list","created":1,"model":"text-embedding-3-small","data":[{"object":"embedding","index":0,"embedding":[0.1]}]}`))
	}))
	defer server.Close()

	channel := &appmodel.Channel{
		Type:     outbound.OutboundTypeOpenAIEmbedding,
		BaseUrls: []appmodel.BaseUrl{{URL: server.URL}},
	}

	statusCode, responseText, internalResp, err := sendGroupProbeRequest(
		context.Background(),
		outbound.Get(outbound.OutboundTypeOpenAIEmbedding),
		outbound.OutboundTypeOpenAIEmbedding,
		channel,
		"sk-test",
		appmodel.EndpointTypeEmbeddings,
		"text-embedding-3-small",
	)
	if err != nil {
		t.Fatalf("sendGroupProbeRequest() error = %v", err)
	}
	if statusCode != http.StatusOK {
		t.Fatalf("sendGroupProbeRequest() status = %d, want 200", statusCode)
	}
	if responseText == "" {
		t.Fatal("sendGroupProbeRequest() responseText = empty, want upstream payload")
	}
	if internalResp == nil {
		t.Fatal("sendGroupProbeRequest() internalResp = nil, want parsed response (issue #90)")
	}
}

func TestSendGroupProbeRequest_EmbeddingVectorLargerThanPreviewLimit(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	vector := make([]float64, 4096)
	for i := range vector {
		vector[i] = 0.123456
	}
	body, err := json.Marshal(map[string]any{
		"object": "list", "model": "Qwen/Qwen3-Embedding-8B",
		"data":  []any{map[string]any{"object": "embedding", "index": 0, "embedding": vector}},
		"usage": map[string]int{"prompt_tokens": 1, "total_tokens": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) <= 4096 {
		t.Fatal("fixture must exceed the old preview limit")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	channel := &appmodel.Channel{Type: outbound.OutboundTypeOpenAIEmbedding, BaseUrls: []appmodel.BaseUrl{{URL: server.URL}}}
	status, text, response, err := sendGroupProbeRequest(context.Background(), outbound.Get(channel.Type), channel.Type,
		channel, "fixture-key", appmodel.EndpointTypeEmbeddings, "Qwen/Qwen3-Embedding-8B")
	if err != nil {
		t.Fatalf("valid large embedding response rejected: %v", err)
	}
	if status != http.StatusOK || text != string(body) {
		t.Fatalf("response truncated: status=%d bytes=%d want=%d", status, len(text), len(body))
	}
	if response == nil || len(response.EmbeddingData) != 1 {
		t.Fatalf("missing embedding response: %#v", response)
	}
}

func TestSendGroupProbeRequestCatieTasksUsesOneSubmission(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })

	var submissions, queries atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/openai/tasks":
			submissions.Add(1)
			_, _ = io.WriteString(w, `{"id":"probe-task"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/openai/tasks/probe-task":
			queries.Add(1)
			_, _ = io.WriteString(w, `{"chunks":[],"next_after":0,"done":true,"status":"completed","result":{"id":"chatcmpl-task","object":"chat.completion","created":1,"model":"gpt-task","choices":[{"index":0,"message":{"role":"assistant","content":"task ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	channel := &appmodel.Channel{
		Type: outbound.OutboundTypeOpenAIChat,
		ConnectionConfig: &appmodel.ConnectionConfig{
			Version:   appmodel.ConnectionConfigVersionTasks,
			Selection: "same_protocol",
			Endpoints: []appmodel.ChannelEndpoint{{
				ID:          "chat",
				Protocol:    "chat",
				URL:         server.URL + "/v1",
				URLMode:     "base",
				Auth:        "bearer",
				ForwardMode: "convert",
				Transport:   appmodel.EndpointTransportCatieTasks,
				TaskAPIURL:  server.URL + "/v1/openai/tasks",
			}},
			Catalog: appmodel.ModelCatalog{Format: "manual"},
		},
	}

	status, _, response, err := sendGroupProbeRequest(
		context.Background(),
		outbound.Get(outbound.OutboundTypeOpenAIChat),
		outbound.OutboundTypeOpenAIChat,
		channel,
		"fixture-key",
		appmodel.EndpointTypeChat,
		"gpt-task",
	)
	if err != nil {
		t.Fatalf("Catie probe failed: %v", err)
	}
	if status != http.StatusOK || response == nil || response.ID != "chatcmpl-task" {
		t.Fatalf("Catie probe response = status %d, response %#v", status, response)
	}
	if submissions.Load() != 1 || queries.Load() != 1 {
		t.Fatalf("task submission/query count = %d/%d", submissions.Load(), queries.Load())
	}
}

// TestSendGroupProbeRequest_ChatResponseUsageParsed 验证 issue #90 的修复：
// chat 探测成功后，上游返回的 usage 必须被解析到 InternalLLMResponse.Usage，
// 使 recordTestLog 能回填 InputTokens/OutputTokens/Cost（不再显示为「未知」）。
func TestSendGroupProbeRequest_ChatResponseUsageParsed(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// OpenAI chat completion 响应，含 usage。
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`))
	}))
	defer server.Close()

	channel := &appmodel.Channel{
		Type:     outbound.OutboundTypeOpenAIChat,
		BaseUrls: []appmodel.BaseUrl{{URL: server.URL}},
	}

	statusCode, _, internalResp, err := sendGroupProbeRequest(
		context.Background(),
		outbound.Get(outbound.OutboundTypeOpenAIChat),
		outbound.OutboundTypeOpenAIChat,
		channel,
		"sk-test",
		appmodel.EndpointTypeChat,
		"gpt-4o-mini",
	)
	if err != nil {
		t.Fatalf("sendGroupProbeRequest() error = %v", err)
	}
	if statusCode != http.StatusOK {
		t.Fatalf("sendGroupProbeRequest() status = %d, want 200", statusCode)
	}
	if internalResp == nil || internalResp.Usage == nil {
		t.Fatal("sendGroupProbeRequest() usage not parsed, want non-nil Usage (issue #90)")
	}
	if internalResp.Usage.PromptTokens != 5 {
		t.Fatalf("prompt_tokens = %d, want 5", internalResp.Usage.PromptTokens)
	}
	if internalResp.Usage.CompletionTokens != 2 {
		t.Fatalf("completion_tokens = %d, want 2", internalResp.Usage.CompletionTokens)
	}
}

func TestGetGroupModelTestProgress_RemovesExpiredEntry(t *testing.T) {
	restoreTTL := groupProbeProgressTTL
	groupProbeProgressTTL = time.Minute
	defer func() {
		groupProbeProgressTTL = restoreTTL
		clearGroupProbeProgressStore()
	}()
	clearGroupProbeProgressStore()

	storeGroupModelProgressAt(&GroupModelTestProgress{ID: "expired"}, time.Now().Add(-2*time.Minute))

	got, ok := GetGroupModelTestProgress("expired")
	if ok || got != nil {
		t.Fatalf("GetGroupModelTestProgress() = (%#v, %t), want (nil, false)", got, ok)
	}
	if _, stillExists := groupProbeProgress.Load("expired"); stillExists {
		t.Fatal("expired progress entry still exists after lookup cleanup")
	}
}

func TestStoreGroupModelProgress_CleansExpiredEntries(t *testing.T) {
	restoreTTL := groupProbeProgressTTL
	groupProbeProgressTTL = time.Minute
	defer func() {
		groupProbeProgressTTL = restoreTTL
		clearGroupProbeProgressStore()
	}()
	clearGroupProbeProgressStore()

	storeGroupModelProgressAt(&GroupModelTestProgress{ID: "expired"}, time.Now().Add(-2*time.Minute))
	storeGroupModelProgressAt(&GroupModelTestProgress{ID: "active", Total: 1}, time.Now())

	if _, stillExists := groupProbeProgress.Load("expired"); stillExists {
		t.Fatal("expired progress entry still exists after store cleanup")
	}
	got, ok := GetGroupModelTestProgress("active")
	if !ok || got == nil || got.ID != "active" {
		t.Fatalf("GetGroupModelTestProgress(active) = (%#v, %t), want active entry", got, ok)
	}
}

func clearGroupProbeProgressStore() {
	groupProbeProgress.Range(func(key, _ any) bool {
		groupProbeProgress.Delete(key)
		return true
	})
}
