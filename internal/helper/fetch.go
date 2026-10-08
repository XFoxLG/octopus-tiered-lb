package helper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dlclark/regexp2"
	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func FetchModels(ctx context.Context, request model.Channel) ([]string, error) {
	if conf.IsDevMockSuccess() {
		return filterDevMockModels(request)
	}

	client, err := ChannelHttpClient(&request)
	if err != nil {
		return nil, err
	}
	return fetchModelsWithClient(client, ctx, request)
}

// FetchModelsShortTimeout 使用短超时(30s) HTTP 客户端获取模型列表
// 用于后台同步任务，避免不可达 endpoint 长时间占用连接
func FetchModelsShortTimeout(ctx context.Context, request model.Channel) ([]string, error) {
	if conf.IsDevMockSuccess() {
		return filterDevMockModels(request)
	}

	client, err := ChannelShortTimeoutHttpClient(&request)
	if err != nil {
		return nil, err
	}
	return fetchModelsWithClient(client, ctx, request)
}

func fetchModelsWithClient(client *http.Client, ctx context.Context, request model.Channel) ([]string, error) {
	fetchModel := make([]string, 0)
	var err error
	if request.ConnectionConfig != nil {
		fetchModel, err = fetchConnectionModels(client, ctx, request)
	} else {
		switch request.Type {
		case outbound.OutboundTypeAnthropic:
			fetchModel, err = fetchAnthropicModels(client, ctx, request)
		case outbound.OutboundTypeGemini:
			fetchModel, err = fetchGeminiModels(client, ctx, request)
		default:
			fetchModel, err = fetchOpenAIModels(client, ctx, request)
		}
	}
	if err != nil {
		return nil, err
	}
	if request.MatchRegex != nil && *request.MatchRegex != "" {
		matchModel := make([]string, 0)
		re, err := regexp2.Compile(*request.MatchRegex, regexp2.ECMAScript)
		if err != nil {
			return nil, err
		}
		for _, model := range fetchModel {
			matched, err := re.MatchString(model)
			if err != nil {
				return nil, err
			}
			if matched {
				matchModel = append(matchModel, model)
			}
		}
		return matchModel, nil
	}
	return fetchModel, nil
}

func filterDevMockModels(request model.Channel) ([]string, error) {
	models := []string{
		"gpt-4o",
		"gpt-4.1",
		"text-embedding-3-small",
		"claude-3-7-sonnet",
		"gemini-2.5-pro",
		"mimo-v2.5",
	}
	if request.MatchRegex == nil || strings.TrimSpace(*request.MatchRegex) == "" {
		return models, nil
	}

	re, err := regexp2.Compile(*request.MatchRegex, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	filtered := make([]string, 0, len(models))
	for _, item := range models {
		matched, err := re.MatchString(item)
		if err != nil {
			return nil, err
		}
		if matched {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

// refer: https://platform.openai.com/docs/api-reference/models/list
func fetchOpenAIModels(client *http.Client, ctx context.Context, request model.Channel) ([]string, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		request.GetNormalizedBaseUrl()+"/models",
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+request.GetChannelKey().ChannelKey)
	for _, header := range request.CustomHeader {
		if header.HeaderKey != "" {
			req.Header.Set(header.HeaderKey, header.HeaderValue)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return nil, fmt.Errorf("fetch models failed: status %d: %s", resp.StatusCode, message)
	}

	var result model.OpenAIModelList

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, m.ID)
	}
	return models, nil
}

// refer: https://ai.google.dev/api/models
func fetchGeminiModels(client *http.Client, ctx context.Context, request model.Channel) ([]string, error) {
	var allModels []string
	pageToken := ""

	for {
		err := func() error {
			req, err := http.NewRequestWithContext(
				ctx,
				http.MethodGet,
				request.GetNormalizedBaseUrl()+"/models",
				nil,
			)
			if err != nil {
				return fmt.Errorf("create request: %w", err)
			}
			req.Header.Set("X-Goog-Api-Key", request.GetChannelKey().ChannelKey)
			for _, header := range request.CustomHeader {
				if header.HeaderKey != "" {
					req.Header.Set(header.HeaderKey, header.HeaderValue)
				}
			}
			if pageToken != "" {
				q := req.URL.Query()
				q.Add("pageToken", pageToken)
				req.URL.RawQuery = q.Encode()
			}

			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			var result model.GeminiModelList

			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return err
			}

			for _, m := range result.Models {
				name := strings.TrimPrefix(m.Name, "models/")
				allModels = append(allModels, name)
			}

			if result.NextPageToken == "" {
				pageToken = ""
				return nil
			}
			pageToken = result.NextPageToken
			return nil
		}()
		if err != nil {
			return nil, err
		}
		if pageToken == "" {
			break
		}
	}
	if len(allModels) == 0 {
		return fetchOpenAIModels(client, ctx, request)
	}
	return allModels, nil
}

// refer: https://platform.claude.com/docs
func fetchAnthropicModels(client *http.Client, ctx context.Context, request model.Channel) ([]string, error) {

	var allModels []string
	var afterID string
	for {

		err := func() error {
			req, err := http.NewRequestWithContext(
				ctx,
				http.MethodGet,
				request.GetNormalizedBaseUrl()+"/models",
				nil,
			)
			if err != nil {
				return fmt.Errorf("create request: %w", err)
			}
			req.Header.Set("X-Api-Key", request.GetChannelKey().ChannelKey)
			req.Header.Set("Anthropic-Version", "2023-06-01")
			for _, header := range request.CustomHeader {
				if header.HeaderKey != "" {
					req.Header.Set(header.HeaderKey, header.HeaderValue)
				}
			}
			// 设置多页参数
			q := req.URL.Query()

			if afterID != "" {
				q.Set("after_id", afterID)
			}
			req.URL.RawQuery = q.Encode()

			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			var result model.AnthropicModelList

			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return err
			}

			for _, m := range result.Data {
				allModels = append(allModels, m.ID)
			}

			if !result.HasMore {
				afterID = ""
				return nil
			}

			afterID = result.LastID
			return nil
		}()
		if err != nil {
			return nil, err
		}
		if afterID == "" {
			break
		}
	}
	if len(allModels) == 0 {
		return fetchOpenAIModels(client, ctx, request)
	}
	return allModels, nil
}

// KeyModelResult 单个 key 拉取模型的结果
type KeyModelResult struct {
	// KeyID 对应 channel_keys.id。逐 key 回填 SupportedModels 时必须靠它定位行；
	// 诊断端点也用它把结果映射回具体 key（新增字段，向后兼容）。
	KeyID      int      `json:"key_id"`
	KeyRemark  string   `json:"key_remark,omitempty"`
	KeyMasked  string   `json:"key_masked,omitempty"`
	Models     []string `json:"models"`
	StatusCode int      `json:"status_code"`
	Passed     bool     `json:"passed"`
	Message    string   `json:"message,omitempty"`
}

// FetchModelsPerKeyResult 按 key 拉取模型的汇总结果
type FetchModelsPerKeyResult struct {
	Results   []KeyModelResult `json:"results"`
	AllModels []string         `json:"all_models"`
}

// FetchModelsPerKey 逐个 key 拉取模型列表，返回每个 key 的模型列表和并集。
// 用于诊断同一个 channel 内不同 key 是否拥有不同的模型访问权限。
// 使用常规（长超时）HTTP 客户端：这是用户在前端主动触发的诊断操作，
// 宁可等久一点也要拿到结果。
func FetchModelsPerKey(ctx context.Context, request model.Channel) (*FetchModelsPerKeyResult, error) {
	if conf.IsDevMockSuccess() {
		models, _ := filterDevMockModels(request)
		return mockFetchModelsPerKey(request, models), nil
	}

	client, err := ChannelHttpClient(&request)
	if err != nil {
		return nil, err
	}
	return fetchModelsPerKeyWithClient(ctx, client, request)
}

// FetchModelsPerKeyShortTimeout 与 FetchModelsPerKey 相同，但使用短超时(30s)
// HTTP 客户端。供后台的模型自动同步任务（task.SyncModelsTask）使用：与
// FetchModelsShortTimeout 的超时策略一致，避免不可达 endpoint 长时间占用连接、
// 让 goroutine 堆积。
func FetchModelsPerKeyShortTimeout(ctx context.Context, request model.Channel) (*FetchModelsPerKeyResult, error) {
	if conf.IsDevMockSuccess() {
		models, _ := filterDevMockModels(request)
		return mockFetchModelsPerKey(request, models), nil
	}

	client, err := ChannelShortTimeoutHttpClient(&request)
	if err != nil {
		return nil, err
	}
	return fetchModelsPerKeyWithClient(ctx, client, request)
}

// fetchModelsPerKeyWithClient 是逐 key 抓取的共同实现：对每个启用且非空的 key
// 构造只含该 key 的临时 channel 副本单独请求，因此结果里的 KeyID 与入参
// request.Keys 的 ID 一一对应。单个 key 抓取失败不中断整体，只在结果里标
// Passed=false + Message（调用方据此决定是否跳过回填）。
func fetchModelsPerKeyWithClient(ctx context.Context, client *http.Client, request model.Channel) (*FetchModelsPerKeyResult, error) {
	// 只取启用的非空 key
	enabledKeys := make([]model.ChannelKey, 0)
	for _, k := range request.Keys {
		if k.Enabled && strings.TrimSpace(k.ChannelKey) != "" {
			enabledKeys = append(enabledKeys, k)
		}
	}

	results := make([]KeyModelResult, 0, len(enabledKeys))
	allModelsSet := make(map[string]struct{})

	for _, key := range enabledKeys {
		// 构造一个只包含当前 key 的临时 channel 副本
		ch := request
		ch.Keys = []model.ChannelKey{key}

		fetchModel, err := fetchModelsWithClient(client, ctx, ch)
		result := KeyModelResult{
			KeyID:     key.ID,
			KeyRemark: key.Remark,
			KeyMasked: maskSecret(key.ChannelKey),
		}
		if err != nil {
			result.Passed = false
			result.Message = err.Error()
		} else {
			result.Passed = true
			result.Models = fetchModel
			for _, m := range fetchModel {
				allModelsSet[m] = struct{}{}
			}
		}
		results = append(results, result)
	}

	allModels := make([]string, 0, len(allModelsSet))
	for m := range allModelsSet {
		allModels = append(allModels, m)
	}

	return &FetchModelsPerKeyResult{
		Results:   results,
		AllModels: allModels,
	}, nil
}

// maskSecret 对 key 做脱敏展示：保留前 4 位 + "..." + 后 4 位
// （定义在 channel_probe.go 中，此处复用）

func mockFetchModelsPerKey(request model.Channel, models []string) *FetchModelsPerKeyResult {
	enabledKeys := make([]model.ChannelKey, 0)
	for _, k := range request.Keys {
		if k.Enabled && strings.TrimSpace(k.ChannelKey) != "" {
			enabledKeys = append(enabledKeys, k)
		}
	}
	results := make([]KeyModelResult, 0, len(enabledKeys))
	for _, key := range enabledKeys {
		results = append(results, KeyModelResult{
			KeyID:     key.ID,
			KeyRemark: key.Remark,
			KeyMasked: maskSecret(key.ChannelKey),
			Models:    models,
			Passed:    true,
		})
	}
	return &FetchModelsPerKeyResult{
		Results:   results,
		AllModels: models,
	}
}
