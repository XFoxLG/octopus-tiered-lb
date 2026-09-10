package relay

import (
	"context"
	"crypto/sha256"

	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	dbmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/utils/log"
	"github.com/lingyuins/octopus/internal/utils/semantic_cache"
)

const (
	semanticCacheNamespaceMetadataKey = "semantic_cache_namespace"
	semanticCacheTextMetadataKey      = "semantic_cache_text"
	semanticCacheHitMetadataKey       = "semantic_cache_hit"
)

func semanticCacheEndpointFamily(endpointType string, inboundType inbound.InboundType) string {
	switch {
	case endpointType == dbmodel.EndpointTypeChat && inboundType == inbound.InboundTypeOpenAIChat:
		return "chat"
	case endpointType == dbmodel.EndpointTypeResponses && inboundType == inbound.InboundTypeOpenAIResponse:
		return "responses"
	default:
		return ""
	}
}

func semanticLookupCacheKey(apiKeyID int, endpointFamily string, req *transmodel.InternalLLMRequest) string {
	if req == nil {
		return ""
	}
	return semantic_cache.BuildNamespace(apiKeyID, endpointFamily, req.Model)
}

func buildSemanticCacheLookupInput(apiKeyID int, endpointFamily string, req *transmodel.InternalLLMRequest) (string, string, bool) {
	if req == nil || apiKeyID <= 0 {
		semantic_cache.RecordBypass()
		return "", "", false
	}
	if strings.TrimSpace(endpointFamily) == "" || strings.TrimSpace(req.Model) == "" {
		semantic_cache.RecordBypass()
		return "", "", false
	}

	text, ok := semantic_cache.ExtractNormalizedText(req)
	if !ok {
		semantic_cache.RecordBypass()
		return "", "", false
	}

	return semantic_cache.BuildNamespace(apiKeyID, endpointFamily, req.Model), text, true
}

func getSemanticCacheLookupInput(req *relayRequest, endpointFamily string) (string, string, bool, bool) {
	if req == nil || req.internalRequest == nil {
		return "", "", false, false
	}
	cacheKey := semanticLookupCacheKey(req.apiKeyID, endpointFamily, req.internalRequest)
	if req.retryCache == nil || cacheKey == "" {
		namespace, text, ok := buildSemanticCacheLookupInput(req.apiKeyID, endpointFamily, req.internalRequest)
		return namespace, text, ok, false
	}
	return req.retryCache.getLookupInput(cacheKey, func() (string, string, bool) {
		return buildSemanticCacheLookupInput(req.apiKeyID, endpointFamily, req.internalRequest)
	})
}

func maybeServeSemanticCacheHit(c *gin.Context, req *relayRequest, endpointFamily string) (bool, []byte, error) {
	if c == nil || req == nil || req.internalRequest == nil {
		return false, nil, nil
	}
	if !canReuseSemanticAnswer(req.internalRequest, endpointFamily) ||
		strings.Contains(strings.ToLower(c.GetHeader("Cache-Control")), "no-cache") ||
		strings.Contains(strings.ToLower(c.GetHeader("Cache-Control")), "no-store") {
		semantic_cache.RecordBypass()
		return false, nil, nil
	}
	if _, enabled := semanticCacheRuntimeConfig(); !enabled {
		return false, nil, nil
	}

	namespace, text, ok, _ := getSemanticCacheLookupInput(req, endpointFamily)
	if !ok {
		return false, nil, nil
	}
	// Similarity may vary only the single question, never its sampling parameters
	// or current group policy. Conversation and protocol-bearing inputs bypass.
	parameters := *req.internalRequest
	parameters.Messages = nil
	identity, err := jsonAPI.Marshal(struct {
		Parameters *transmodel.InternalLLMRequest `json:"parameters"`
		Group      *dbmodel.Group                 `json:"group"`
	}{Parameters: &parameters, Group: req.group})
	if err != nil {
		return false, nil, nil
	}
	namespace = fmt.Sprintf("%s:%x", namespace, sha256.Sum256(identity))

	cfg, ok := semanticCacheRuntimeConfig()
	if !ok {
		semantic_cache.RecordBypass()
		return false, nil, nil
	}

	embedding, _, err := lookupSemanticEmbeddingWithCache(req.operationCtx, req, cfg, namespace, text)
	if err != nil {
		semantic_cache.RecordBypass()
		log.Warnf("semantic cache lookup bypassed: %v", err)
		return false, nil, nil
	}

	semantic_cache.RecordEvaluated()
	if payload, found := semantic_cache.Lookup(namespace, embedding); found {
		semantic_cache.RecordHit()
		if req.internalRequest.TransformerMetadata == nil {
			req.internalRequest.TransformerMetadata = make(map[string]string, 1)
		}
		req.internalRequest.TransformerMetadata[semanticCacheHitMetadataKey] = "true"

		isStream := req.internalRequest.Stream != nil && *req.internalRequest.Stream
		if isStream {
			if err := serveStreamingCacheHit(c, payload, req.internalRequest.Model); err != nil {
				return false, nil, err
			}
		} else {
			c.Data(http.StatusOK, "application/json", payload)
		}
		return true, payload, nil
	}
	semantic_cache.RecordMiss()

	if req.internalRequest.TransformerMetadata == nil {
		req.internalRequest.TransformerMetadata = make(map[string]string, 2)
	}
	req.internalRequest.TransformerMetadata[semanticCacheNamespaceMetadataKey] = namespace
	req.internalRequest.TransformerMetadata[semanticCacheTextMetadataKey] = text

	return false, nil, nil
}

func canReuseSemanticAnswer(request *transmodel.InternalLLMRequest, endpointFamily string) bool {
	if request == nil || endpointFamily != "chat" ||
		(request.Stream != nil && *request.Stream) || request.ConversationID != "" ||
		len(request.Messages) != 1 || request.Messages[0].Role != "user" ||
		request.Temperature == nil || *request.Temperature != 0 ||
		len(request.Tools) != 0 || request.ToolChoice != nil ||
		len(request.Messages[0].ToolCalls) != 0 ||
		request.ResponseFormat != nil || len(request.ExtraBody) != 0 ||
		request.Audio != nil || len(request.Modalities) != 0 {
		return false
	}
	for _, part := range request.Messages[0].Content.MultipleContent {
		if part.Type != "text" || part.Text == nil {
			return false
		}
	}
	return true
}

func serveStreamingCacheHit(c *gin.Context, payload []byte, model string) error {
	if c == nil || len(payload) == 0 {
		return nil
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	var response struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Index   int `json:"index"`
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage map[string]interface{} `json:"usage"`
	}

	if err := jsonAPI.Unmarshal(payload, &response); err != nil {
		return sendSSEChunk(c, payload)
	}

	if response.Model == "" {
		response.Model = model
	}

	for _, choice := range response.Choices {
		content := choice.Message.Content
		if content == "" {
			continue
		}

		// Use rune-based chunking to avoid splitting multi-byte UTF-8 characters
		runes := []rune(content)
		chunkSize := 20
		for i := 0; i < len(runes); i += chunkSize {
			end := i + chunkSize
			if end > len(runes) {
				end = len(runes)
			}

			chunk := map[string]interface{}{
				"id":      response.ID,
				"object":  "chat.completion.chunk",
				"created": response.Created,
				"model":   response.Model,
				"choices": []map[string]interface{}{
					{
						"index": choice.Index,
						"delta": map[string]interface{}{
							"content": string(runes[i:end]),
						},
						"finish_reason": nil,
					},
				},
			}

			chunkJSON, err := jsonAPI.Marshal(chunk)
			if err != nil {
				continue
			}

			if err := sendSSEChunk(c, chunkJSON); err != nil {
				return err
			}
		}

		finishChunk := map[string]interface{}{
			"id":      response.ID,
			"object":  "chat.completion.chunk",
			"created": response.Created,
			"model":   response.Model,
			"choices": []map[string]interface{}{
				{
					"index":         choice.Index,
					"delta":         map[string]interface{}{},
					"finish_reason": "stop",
				},
			},
		}

		if response.Usage != nil {
			finishChunk["usage"] = response.Usage
		}

		finishJSON, err := jsonAPI.Marshal(finishChunk)
		if err == nil {
			if err := sendSSEChunk(c, finishJSON); err != nil {
				return err
			}
		}
	}

	c.Writer.Write([]byte("data: [DONE]\n\n"))
	c.Writer.Flush()

	return nil
}

func sendSSEChunk(c *gin.Context, data []byte) error {
	_, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data)
	if err != nil {
		return err
	}
	c.Writer.Flush()
	return nil
}

func storeSemanticCacheResponse(ctx context.Context, req *transmodel.InternalLLMRequest, responseJSON []byte) {
	if !canReuseSemanticAnswer(req, "chat") || len(responseJSON) == 0 || !jsonAPI.Valid(responseJSON) {
		return
	}

	namespace, text, ok := semanticCacheStoreMetadata(req)
	if !ok {
		return
	}

	cfg, ok := semanticCacheRuntimeConfig()
	if !ok {
		return
	}

	embedding, err := semantic_cache.NewEmbeddingClient(cfg).CreateEmbedding(ctx, text)
	if err != nil {
		log.Warnf("semantic cache store bypassed: %v", err)
		return
	}

	semantic_cache.Store(namespace, text, responseJSON, embedding)
	semantic_cache.RecordStored()
}

func isSemanticCacheHitRequest(req *transmodel.InternalLLMRequest) bool {
	if req == nil || req.TransformerMetadata == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(req.TransformerMetadata[semanticCacheHitMetadataKey]), "true")
}

func semanticCacheHitPayload(responseJSON []byte, req *transmodel.InternalLLMRequest) []byte {
	if len(responseJSON) == 0 || !jsonAPI.Valid(responseJSON) {
		return responseJSON
	}

	var payload map[string]any
	if err := jsonAPI.Unmarshal(responseJSON, &payload); err != nil {
		return responseJSON
	}

	octopusValue, ok := payload["octopus"].(map[string]any)
	if !ok {
		octopusValue = map[string]any{}
		payload["octopus"] = octopusValue
	}
	octopusValue["semantic_cache"] = map[string]any{
		"hit": true,
	}

	if usageValue, ok := payload["usage"].(map[string]any); ok {
		delete(usageValue, "cached_tokens")
		delete(usageValue, "prompt_cache_hit_tokens")
		if promptDetails, ok := usageValue["input_token_details"].(map[string]any); ok {
			delete(promptDetails, "cached_tokens")
		}
		if promptDetails, ok := usageValue["prompt_tokens_details"].(map[string]any); ok {
			delete(promptDetails, "cached_tokens")
		}
		if inputDetails, ok := usageValue["input_tokens_details"].(map[string]any); ok {
			delete(inputDetails, "cached_tokens")
		}
	}

	normalized, err := jsonAPI.Marshal(payload)
	if err != nil {
		return responseJSON
	}
	return normalized
}

func buildSemanticCacheHitInternalResponse(req *transmodel.InternalLLMRequest, payload []byte) (*transmodel.InternalLLMResponse, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}

	internalResponse := &transmodel.InternalLLMResponse{}
	switch req.RawAPIFormat {
	case transmodel.APIFormatOpenAIResponse:
		var respPayload struct {
			ID        string `json:"id"`
			Object    string `json:"object"`
			CreatedAt int64  `json:"created_at"`
			Model     string `json:"model"`
			Usage     *struct {
				InputTokens       int64 `json:"input_tokens"`
				OutputTokens      int64 `json:"output_tokens"`
				TotalTokens       int64 `json:"total_tokens"`
				InputTokenDetails *struct {
					CachedTokens int64 `json:"cached_tokens"`
				} `json:"input_token_details"`
			} `json:"usage"`
		}
		if err := jsonAPI.Unmarshal(payload, &respPayload); err != nil {
			return nil, err
		}
		internalResponse.ID = respPayload.ID
		internalResponse.Object = "chat.completion"
		internalResponse.Created = respPayload.CreatedAt
		internalResponse.Model = respPayload.Model
		if respPayload.Usage != nil {
			internalResponse.Usage = &transmodel.Usage{
				PromptTokens:     respPayload.Usage.InputTokens,
				CompletionTokens: respPayload.Usage.OutputTokens,
				TotalTokens:      respPayload.Usage.TotalTokens,
			}
			if respPayload.Usage.InputTokenDetails != nil && respPayload.Usage.InputTokenDetails.CachedTokens > 0 {
				internalResponse.Usage.PromptTokensDetails = &transmodel.PromptTokensDetails{
					CachedTokens: respPayload.Usage.InputTokenDetails.CachedTokens,
				}
			}
		}
	default:
		if err := jsonAPI.Unmarshal(payload, internalResponse); err != nil {
			return nil, err
		}
	}
	return internalResponse, nil
}

func semanticCacheStoreMetadata(req *transmodel.InternalLLMRequest) (string, string, bool) {
	if req == nil || req.TransformerMetadata == nil {
		return "", "", false
	}

	namespace := strings.TrimSpace(req.TransformerMetadata[semanticCacheNamespaceMetadataKey])
	text := strings.TrimSpace(req.TransformerMetadata[semanticCacheTextMetadataKey])
	if namespace == "" || text == "" {
		return "", "", false
	}

	return namespace, text, true
}

// semanticCacheConfigCache 缓存基于设置派生的语义缓存运行时配置。每请求都会
// 调用 semanticCacheRuntimeConfig，但只有当设置代际（setting.Generation）发生
// 变化时才重新读取设置、重建配置并调用 ApplyRuntimeConfig。这样消除了热路径上
// 每请求多次 setting 读取以及对 globalCacheMu 写锁的反复争用。
var semanticCacheConfigCache struct {
	mu         sync.RWMutex
	generation uint64
	loaded     bool
	cfg        semantic_cache.RuntimeConfig
	ok         bool
}

// semanticCacheRuntimeConfig 返回当前语义缓存运行时配置，并确保全局缓存已按该
// 配置初始化。配置在设置代际不变时复用缓存值，仅在变更时重新加载并 Apply。
func semanticCacheRuntimeConfig() (semantic_cache.RuntimeConfig, bool) {
	gen := setting.Generation()

	semanticCacheConfigCache.mu.RLock()
	if semanticCacheConfigCache.loaded && semanticCacheConfigCache.generation == gen {
		cfg, ok := semanticCacheConfigCache.cfg, semanticCacheConfigCache.ok
		semanticCacheConfigCache.mu.RUnlock()
		return cfg, ok
	}
	semanticCacheConfigCache.mu.RUnlock()

	semanticCacheConfigCache.mu.Lock()
	defer semanticCacheConfigCache.mu.Unlock()
	// 双重检查：可能在等待写锁期间已被其他 goroutine 刷新。
	if semanticCacheConfigCache.loaded && semanticCacheConfigCache.generation == gen {
		return semanticCacheConfigCache.cfg, semanticCacheConfigCache.ok
	}

	cfg, ok := loadSemanticCacheRuntimeConfig()
	// ApplyRuntimeConfig 在 disabled 时 Reset，在参数不变时复用既有缓存，
	// 因此仅在代际变化时调用一次即可同步全局缓存状态。
	semantic_cache.ApplyRuntimeConfig(cfg)

	semanticCacheConfigCache.generation = gen
	semanticCacheConfigCache.loaded = true
	semanticCacheConfigCache.cfg = cfg
	semanticCacheConfigCache.ok = ok
	return cfg, ok
}

func loadSemanticCacheRuntimeConfig() (semantic_cache.RuntimeConfig, bool) {
	enabled, err := setting.GetBool(dbmodel.SettingKeySemanticCacheEnabled)
	if err != nil || !enabled {
		return semantic_cache.RuntimeConfig{}, false
	}

	ttl, _ := setting.GetInt(dbmodel.SettingKeySemanticCacheTTL)
	if ttl <= 0 {
		ttl = 3600
	}

	thresholdRaw, _ := setting.GetInt(dbmodel.SettingKeySemanticCacheThreshold)
	if thresholdRaw < 0 || thresholdRaw > 100 {
		thresholdRaw = 98
	}

	maxEntries, _ := setting.GetInt(dbmodel.SettingKeySemanticCacheMaxEntries)
	if maxEntries <= 0 {
		maxEntries = 1000
	}

	embeddingBaseURL, _ := setting.GetString(dbmodel.SettingKeySemanticCacheEmbeddingBaseURL)
	embeddingAPIKey, _ := setting.GetString(dbmodel.SettingKeySemanticCacheEmbeddingAPIKey)
	embeddingModel, _ := setting.GetString(dbmodel.SettingKeySemanticCacheEmbeddingModel)
	timeoutSeconds, _ := setting.GetInt(dbmodel.SettingKeySemanticCacheEmbeddingTimeoutSeconds)
	if timeoutSeconds <= 0 {
		timeoutSeconds = 15
	}

	return semantic_cache.RuntimeConfig{
		Enabled:          true,
		MaxEntries:       maxEntries,
		Threshold:        float64(thresholdRaw) / 100.0,
		TTL:              time.Duration(ttl) * time.Second,
		EmbeddingBaseURL: strings.TrimSpace(embeddingBaseURL),
		EmbeddingAPIKey:  strings.TrimSpace(embeddingAPIKey),
		EmbeddingModel:   strings.TrimSpace(embeddingModel),
		EmbeddingTimeout: time.Duration(timeoutSeconds) * time.Second,
	}, true
}
