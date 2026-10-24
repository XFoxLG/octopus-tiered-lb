package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/apikey"
	"github.com/lingyuins/octopus/internal/op/group"
	st "github.com/lingyuins/octopus/internal/op/stats"
	"github.com/lingyuins/octopus/internal/server/auth"
	"github.com/lingyuins/octopus/internal/server/middleware"
	"github.com/lingyuins/octopus/internal/server/resp"
	"github.com/lingyuins/octopus/internal/server/router"
	"github.com/samber/lo"
)

// errInvalidRequestTimeoutPayload 表示 request_timeout_seconds 字段虽然存在，
// 但不是合法的 JSON 数字。ShouldBindJSON 对 int 字段的类型不匹配会直接报错，
// 但本字段以 RawMessage 绑定（用于区分「缺失」与「显式 0」），所以在此单独判定。
var errInvalidRequestTimeoutPayload = errors.New("invalid request_timeout_seconds payload")

func init() {
	router.NewGroupRouter("/api/v1/apikey").
		Use(middleware.Auth()).
		Use(middleware.RequirePermission(auth.PermAPIKeysRead)).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermAPIKeysWrite)).
				Handle(createAPIKey),
		).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listAPIKey),
		).
		AddRoute(
			router.NewRoute("/update", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermAPIKeysWrite)).
				Handle(updateAPIKey),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Use(middleware.RequirePermission(auth.PermAPIKeysWrite)).
				Handle(deleteAPIKey),
		)
	router.NewGroupRouter("/api/v1/apikey").
		Use(middleware.APIKeyAuth()).
		AddRoute(
			router.NewRoute("/stats", http.MethodGet).
				Handle(getStatsAPIKeyById),
		).
		AddRoute(
			router.NewRoute("/login", http.MethodGet).
				Handle(loginAPIKey),
		)
}

func createAPIKey(c *gin.Context) {
	var req apiKeyRequestPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if _, err := req.requestTimeoutSeconds(); err != nil {
		resp.Error(c, http.StatusBadRequest, "request_timeout_seconds must be an integer")
		return
	}
	apiKey := req.toModel()
	apiKey.ID = 0
	// Use custom key if provided; otherwise auto-generate.
	if strings.TrimSpace(apiKey.APIKey) == "" {
		key, err := auth.GenerateAPIKey()
		if err != nil {
			resp.InternalError(c)
			return
		}
		apiKey.APIKey = key
	} else {
		apiKey.APIKey = normalizeAPIKeyPrefix(apiKey.APIKey)
	}
	if err := apikey.Create(&apiKey, c.Request.Context()); err != nil {
		if status, msg, ok := classifyAPIKeyMutationError(err); ok {
			resp.Error(c, status, msg)
			return
		}
		resp.InternalError(c)
		return
	}
	resp.Success(c, apiKey)
}

func listAPIKey(c *gin.Context) {
	apiKeys, err := apikey.List(c.Request.Context())
	if err != nil {
		resp.InternalError(c)
		return
	}
	if !auth.HasPermission(c.GetString("user_role"), auth.PermAPIKeysWrite) {
		apiKeys = maskAPIKeys(apiKeys)
	}
	resp.Success(c, apiKeys)
}

func updateAPIKey(c *gin.Context) {
	var req apiKeyRequestPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	apiKey := req.toModel()
	// request_timeout_seconds 采用三态语义：缺失保留旧值、显式 0 清空预算、
	// 1..86400 覆盖为新预算。Update 走 Save，若不在此处补齐旧值，
	// 缺失字段绑定的零值会把 Key 已配置的预算悄悄清掉。
	timeout, err := req.resolveRequestTimeoutSeconds(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "request_timeout_seconds must be an integer")
		return
	}
	apiKey.RequestTimeoutSeconds = timeout
	// Normalize custom key prefix if a new key value is provided.
	if strings.TrimSpace(apiKey.APIKey) != "" {
		apiKey.APIKey = normalizeAPIKeyPrefix(apiKey.APIKey)
	}
	if err := apikey.Update(&apiKey, c.Request.Context()); err != nil {
		if status, msg, ok := classifyAPIKeyMutationError(err); ok {
			resp.Error(c, status, msg)
			return
		}
		resp.InternalError(c)
		return
	}
	apiKey.APIKey = maskAPIKeyValue(apiKey.APIKey)
	resp.Success(c, apiKey)
}

func deleteAPIKey(c *gin.Context) {
	id := c.Param("id")
	idNum, err := strconv.Atoi(id)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	if err := apikey.Delete(idNum, c.Request.Context()); err != nil {
		if status, msg, ok := classifyAPIKeyMutationError(err); ok {
			resp.Error(c, status, msg)
			return
		}
		resp.InternalError(c)
		return
	}
	resp.Success(c, nil)
}

func getStatsAPIKeyById(c *gin.Context) {
	id := c.GetInt("api_key_id")
	stats := st.APIKeyGet(id)
	info, err := apikey.Get(id, c.Request.Context())
	if err != nil {
		resp.InternalError(c)
		return
	}
	models, err := group.GroupListModel(c.Request.Context())
	if err != nil {
		resp.InternalError(c)
		return
	}
	var modelsString string
	if info.SupportedModels == "" {
		modelsString = strings.Join(models, ", ")
	} else {
		supportedModels := lo.Map(strings.Split(info.SupportedModels, ","), func(s string, _ int) string {
			return strings.TrimSpace(s)
		})
		models = lo.Filter(models, func(m string, _ int) bool {
			return lo.Contains(supportedModels, m)
		})
		modelsString = strings.Join(models, ", ")
	}
	info.SupportedModels = modelsString
	resp.Success(c, map[string]any{
		"stats": stats,
		"info":  info,
	})
}

func loginAPIKey(c *gin.Context) {
	resp.Success(c, nil)
}

func maskAPIKeys(keys []model.APIKey) []model.APIKey {
	if len(keys) == 0 {
		return make([]model.APIKey, 0)
	}

	masked := make([]model.APIKey, len(keys))
	for i, key := range keys {
		key.APIKey = maskAPIKeyValue(key.APIKey)
		masked[i] = key
	}
	return masked
}

func maskAPIKeyValue(raw string) string {
	return maskSecretValue(raw)
}

type apiKeyRequestPayload struct {
	ID                     int     `json:"id"`
	Name                   string  `json:"name"`
	APIKey                 string  `json:"api_key,omitempty"`
	Enabled                bool    `json:"enabled"`
	ExpireAt               int64   `json:"expire_at,omitempty"`
	MaxCost                float64 `json:"max_cost,omitempty"`
	MaxTokens              int64   `json:"max_tokens,omitempty"`
	SupportedModels        string  `json:"supported_models,omitempty"`
	AllowedGroupCategories string  `json:"allowed_group_categories,omitempty"`
	RateLimitRPM           int     `json:"rate_limit_rpm,omitempty"`
	RateLimitTPM           int     `json:"rate_limit_tpm,omitempty"`
	// RequestTimeoutSecondsRaw 用 json.RawMessage 绑定，仅为在 Update 路径区分
	// 「字段缺失」与「显式 0」；解析出的整数值通过 toModel 落到 model.APIKey。
	RequestTimeoutSecondsRaw json.RawMessage `json:"request_timeout_seconds"`
	PerModelQuotaJSON        string          `json:"per_model_quota_json,omitempty"`
	AllowedIPs               string          `json:"allowed_ips,omitempty"`
	Tags                     string          `json:"tags,omitempty"`
	ExcludedChannels         string          `json:"excluded_channels,omitempty"`
}

func (p apiKeyRequestPayload) toModel() model.APIKey {
	// 调用方（createAPIKey/updateAPIKey）已先行校验该字段，这里忽略错误即可；
	// 缺失值落到 0，Update 路径随后用 resolveRequestTimeoutSeconds 补齐旧值。
	timeout, _ := p.requestTimeoutSeconds()
	return model.APIKey{
		ID:                     p.ID,
		Name:                   p.Name,
		APIKey:                 p.APIKey,
		Enabled:                p.Enabled,
		ExpireAt:               p.ExpireAt,
		MaxCost:                p.MaxCost,
		MaxTokens:              p.MaxTokens,
		SupportedModels:        p.SupportedModels,
		AllowedGroupCategories: p.AllowedGroupCategories,
		RateLimitRPM:           p.RateLimitRPM,
		RateLimitTPM:           p.RateLimitTPM,
		RequestTimeoutSeconds:  timeout,
		PerModelQuotaJSON:      p.PerModelQuotaJSON,
		AllowedIPs:             p.AllowedIPs,
		Tags:                   p.Tags,
		ExcludedChannels:       p.ExcludedChannels,
	}
}

// requestTimeoutSeconds 解析请求体里的 request_timeout_seconds。
// 字段缺失时返回 0（Create 的默认预算；Update 会另行补齐旧值），
// 字段存在但不是合法整数时返回 errInvalidRequestTimeoutPayload。
func (p apiKeyRequestPayload) requestTimeoutSeconds() (int, error) {
	if len(p.RequestTimeoutSecondsRaw) == 0 {
		return 0, nil
	}
	if strings.TrimSpace(string(p.RequestTimeoutSecondsRaw)) == "null" {
		return 0, errInvalidRequestTimeoutPayload
	}
	var value int
	if err := json.Unmarshal(p.RequestTimeoutSecondsRaw, &value); err != nil {
		return 0, errInvalidRequestTimeoutPayload
	}
	return value, nil
}

func (p apiKeyRequestPayload) hasRequestTimeoutSeconds() bool {
	return len(p.RequestTimeoutSecondsRaw) > 0
}

// resolveRequestTimeoutSeconds 返回 Update 路径应写入的预算值：
// 字段缺失时读取旧 Key 的预算（读取失败沿用 0，最终由 apikey.Update 返回 not found），
// 字段存在时优先使用请求值，保证显式 0 可以清空预算。
func (p apiKeyRequestPayload) resolveRequestTimeoutSeconds(ctx context.Context) (int, error) {
	if p.hasRequestTimeoutSeconds() {
		return p.requestTimeoutSeconds()
	}
	if existing, err := apikey.Get(p.ID, ctx); err == nil {
		return existing.RequestTimeoutSeconds, nil
	}
	return 0, nil
}

func classifyAPIKeyMutationError(err error) (int, string, bool) {
	if err == nil {
		return 0, "", false
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "request timeout must"):
		return http.StatusBadRequest, err.Error(), true
	case strings.Contains(msg, "api key not found"):
		return http.StatusNotFound, "API key not found", true
	case strings.Contains(msg, "unique constraint failed") ||
		strings.Contains(msg, "duplicate entry") ||
		strings.Contains(msg, "duplicate key"):
		return http.StatusConflict, "API key already exists", true
	default:
		return 0, "", false
	}
}

// apiKeyPrefix is the mandatory prefix for all API keys.
var apiKeyPrefix = "sk-" + conf.APP_NAME + "-"

// normalizeAPIKeyPrefix ensures the key has the sk-octopus- prefix.
// If the user provides only a suffix (e.g. "my-key"), the prefix is prepended.
func normalizeAPIKeyPrefix(key string) string {
	key = strings.TrimSpace(key)
	if strings.HasPrefix(key, apiKeyPrefix) {
		return key
	}
	return apiKeyPrefix + key
}
