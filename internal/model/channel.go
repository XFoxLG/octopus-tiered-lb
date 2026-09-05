package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

type AutoGroupType int

const (
	AutoGroupTypeNone  AutoGroupType = 0 //不自动分组
	AutoGroupTypeFuzzy AutoGroupType = 1 //模糊匹配
	AutoGroupTypeExact AutoGroupType = 2 //准确匹配
	AutoGroupTypeRegex AutoGroupType = 3 //正则匹配
)

func (t AutoGroupType) Valid() bool {
	switch t {
	case AutoGroupTypeNone, AutoGroupTypeFuzzy, AutoGroupTypeExact, AutoGroupTypeRegex:
		return true
	default:
		return false
	}
}

func ParseAutoGroupSettingValue(value string) (AutoGroupType, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "false":
		return AutoGroupTypeNone, true
	case "true":
		return AutoGroupTypeFuzzy, true
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return AutoGroupTypeNone, false
	}
	mode := AutoGroupType(parsed)
	return mode, mode.Valid()
}

type RequestRewriteProfile string

const (
	RequestRewriteProfilePreserve         RequestRewriteProfile = "preserve"
	RequestRewriteProfileOpenAIChatCompat RequestRewriteProfile = "openai_chat_compat"
	RequestRewriteProfileCodexHeaders     RequestRewriteProfile = "codex"
)

type ToolRoleStrategy string

const (
	ToolRoleStrategyKeep            ToolRoleStrategy = "keep"
	ToolRoleStrategyStringifyToUser ToolRoleStrategy = "stringify_to_user"
)

type SystemMessageStrategy string

const (
	SystemMessageStrategyKeep  SystemMessageStrategy = "keep"
	SystemMessageStrategyMerge SystemMessageStrategy = "merge"
)

type RequestRewriteConfig struct {
	Enabled               bool                  `json:"enabled"`
	Profile               RequestRewriteProfile `json:"profile,omitempty"`
	ToolRoleStrategy      ToolRoleStrategy      `json:"tool_role_strategy,omitempty"`
	SystemMessageStrategy SystemMessageStrategy `json:"system_message_strategy,omitempty"`
	HeaderProfile         string                `json:"header_profile,omitempty"`
}

type Channel struct {
	ID                   int                   `json:"id" gorm:"primaryKey"`
	Name                 string                `json:"name" gorm:"unique;not null"`
	GroupID              int                   `json:"group_id" gorm:"not null;default:0;index"`
	Type                 outbound.OutboundType `json:"type"`
	Enabled              bool                  `json:"enabled"`
	BaseUrls             []BaseUrl             `json:"base_urls" gorm:"serializer:json"`
	Keys                 []ChannelKey          `json:"keys" gorm:"foreignKey:ChannelID"`
	Model                string                `json:"model"`
	CustomModel          string                `json:"custom_model"`
	ProxyMode            ProxyUsageMode        `json:"proxy_mode" gorm:"type:varchar(16);not null;default:'direct'"`
	ProxyConfigID        *int                  `json:"proxy_config_id"`
	Proxy                bool                  `json:"proxy" gorm:"default:false"`
	AutoSync             bool                  `json:"auto_sync" gorm:"default:false"`
	AutoGroup            AutoGroupType         `json:"auto_group" gorm:"default:0"`
	SkipModelTest        bool                  `json:"skip_model_test" gorm:"default:false"`
	Disposable           bool                  `json:"disposable" gorm:"default:false"`
	ExpireAt             *time.Time            `json:"expire_at,omitempty" gorm:"index"`
	KeySelectionStrategy string                `json:"key_selection_strategy" gorm:"type:varchar(16);not null;default:''"`
	CustomHeader         []CustomHeader        `json:"custom_header" gorm:"serializer:json"`
	ParamOverride        *string               `json:"param_override"`
	ChannelProxy         *string               `json:"channel_proxy,omitempty" gorm:"column:channel_proxy"`
	RequestRewrite       *RequestRewriteConfig `json:"request_rewrite" gorm:"serializer:json"`
	Stats                *StatsChannel         `json:"stats,omitempty" gorm:"foreignKey:ChannelID"`
	MatchRegex           *string               `json:"match_regex"`
	// IsReserve 备用渠道标记（Seller 移植）。排序策略 non_relay_* 把备用渠道排后：
	// 非备用按余额/倍率正常排，备用统一沉底。false=常规，true=备用。
	IsReserve bool `json:"is_reserve" gorm:"default:false"`
	// KeyHealthPassed 记录最近一次定时 Key 巡检是否全部通过（issue #142）。
	// nil = 从未巡检；true = 全部通过；false = 存在失败。前端据此对失败渠道标灰。
	KeyHealthPassed *bool `json:"key_health_passed,omitempty" gorm:"column:key_health_passed"`
	// KeyHealthAllFailed 区分"全部 Key 失败"与"部分失败"。
	// nil = 从未巡检；true = 所有 Key 均不可用；false = 至少一个 Key 可用。
	// 仅当 KeyHealthPassed=false 时有意义：前端据此决定整张卡片灰色化还是仅标记部分失败。
	KeyHealthAllFailed *bool `json:"key_health_all_failed,omitempty" gorm:"column:key_health_all_failed"`
	// KeyHealthAt 最近一次定时 Key 巡检完成时间（unix 秒），0 = 从未巡检。
	KeyHealthAt int64 `json:"key_health_at,omitempty" gorm:"column:key_health_at;default:0"`
	// PoolID 关联号池。0 = 使用 inline keys，>0 = 从号池调度器选账号。
	PoolID        int                   `json:"pool_id" gorm:"default:0;index"`
	Managed       bool                  `json:"managed" gorm:"-"`
	ManagedSource *ManagedChannelSource `json:"managed_source,omitempty" gorm:"-"`
}

type BaseUrl struct {
	URL        string `json:"url"`
	Delay      int    `json:"delay"`
	SuffixMode string `json:"suffix_mode,omitempty"`
}

type CustomHeader struct {
	HeaderKey   string `json:"header_key"`
	HeaderValue string `json:"header_value"`
}

type ChannelKey struct {
	ID               int     `json:"id" gorm:"primaryKey"`
	ChannelID        int     `json:"channel_id"`
	Enabled          bool    `json:"enabled"`
	ChannelKey       string  `json:"channel_key"`
	StatusCode       int     `json:"status_code"`
	LastUseTimeStamp int64   `json:"last_use_time_stamp"`
	TotalCost        float64 `json:"total_cost"`
	Priority         int     `json:"priority" gorm:"default:0"`
	Remark           string  `json:"remark"`
	// SupportedModels 逗号分隔的模型列表，限定该 key 只能用于这些模型。
	// 空表示不限制（兼容存量 key）。key 选择时用 ModelMatches 过滤，
	// 避免把不支持当前模型的 key 发给上游（如上游中转站某 token 无某模型权限）。
	SupportedModels string `json:"supported_models,omitempty" gorm:"column:supported_models;type:varchar(512)"`
	// Managed 标记该 key 是否由 site 同步投影自动生成。
	// site 同步 diff 时只删除 Managed=true 的 key，
	// 保留用户手动添加的（Managed=false）key 不被清除。
	Managed bool `json:"managed" gorm:"default:false"`
}

// KeyCooldownFunc 由 balancer 包在启动时注入，用于查询某 (channelID, keyID, modelName)
// 是否处于按模型粒度的冷却期。返回 true 表示该 key 对当前 model 应被跳过。
// model 包不能导入 internal/relay/balancer（会形成循环依赖），故用函数变量解耦。
// 冷却时长由 balancer 内部从 SettingKeyRatelimitCooldown 读取，与熔断器一致。
// nil 表示冷却机制未启用（一律放行），后台任务场景也会传空 modelName 跳过。
// KeyAvailabilityScoreFunc 由 balancer 包在启动时注入，用于查询某 (channelID, keyID,
// modelName) 的可用度分数。返回 0~100，分数越高优先级越高。未注入时返回满分
// （不参与可用度评分，保持后台任务等场景可用）。
var KeyAvailabilityScoreFunc func(channelID, keyID int, modelName string) float64

// KeySpeedTPSFunc 由 balancer 包在启动时注入，用于查询某 (channelID, keyID,
// modelName) 的 EMA 平滑 TPS（tokens/sec）。返回 0 表示无数据（冷启动）。
// 未注入时返回 0，speed 策略回退 cost（不参与速度评分，保持后台任务等场景可用）。
var KeySpeedTPSFunc func(channelID, keyID int, modelName string) float64

// GlobalKeySelectionStrategyFunc 由 op/setting 包在启动时注入，用于读取全局 key 选择
// 策略（"cost"、"availability"、"speed" 或 "priority"）。model 包不能导入 internal/op（循环依赖），故用
// 函数变量解耦。未注入时返回 "cost"（默认策略）。
var GlobalKeySelectionStrategyFunc func() string
var KeyCooldownFunc func(channelID, keyID int, modelName string) bool

// ChannelUpdateRequest 渠道更新请求 - 仅包含变更的数据
type ChannelUpdateRequest struct {
	ID                   int                    `json:"id" binding:"required"`
	Name                 *string                `json:"name,omitempty"`
	GroupID              *int                   `json:"group_id,omitempty"`
	Type                 *outbound.OutboundType `json:"type,omitempty"`
	Enabled              *bool                  `json:"enabled,omitempty"`
	BaseUrls             *[]BaseUrl             `json:"base_urls,omitempty"`
	Model                *string                `json:"model,omitempty"`
	CustomModel          *string                `json:"custom_model,omitempty"`
	ProxyMode            *ProxyUsageMode        `json:"proxy_mode,omitempty"`
	ProxyConfigID        *int                   `json:"proxy_config_id,omitempty"`
	Proxy                *bool                  `json:"proxy,omitempty"`
	AutoSync             *bool                  `json:"auto_sync,omitempty"`
	SkipModelTest        *bool                  `json:"skip_model_test,omitempty"`
	Disposable           *bool                  `json:"disposable,omitempty"`
	ExpireAt             *time.Time             `json:"expire_at,omitempty"`
	KeySelectionStrategy *string                `json:"key_selection_strategy,omitempty"`
	AutoGroup            *AutoGroupType         `json:"auto_group,omitempty"`
	CustomHeader         *[]CustomHeader        `json:"custom_header,omitempty"`
	ChannelProxy         *string                `json:"channel_proxy,omitempty"`
	ParamOverride        *string                `json:"param_override,omitempty"`
	RequestRewrite       *RequestRewriteConfig  `json:"request_rewrite,omitempty"`
	MatchRegex           *string                `json:"match_regex,omitempty"`
	PoolID               *int                   `json:"pool_id,omitempty"`
	IsReserve            *bool                  `json:"is_reserve,omitempty"`

	KeysToAdd    []ChannelKeyAddRequest    `json:"keys_to_add,omitempty"`
	KeysToUpdate []ChannelKeyUpdateRequest `json:"keys_to_update,omitempty"`
	KeysToDelete []int                     `json:"keys_to_delete,omitempty"`

	BypassManagedCheck bool `json:"-"` // 内部使用：允许投影逻辑更新 managed channel
}

type ChannelKeyAddRequest struct {
	Enabled    bool   `json:"enabled"`
	ChannelKey string `json:"channel_key" binding:"required"`
	Priority   int    `json:"priority"`
	Remark     string `json:"remark"`
	// SupportedModels 逗号分隔的模型列表，限定该 key 只能用于这些模型（空=不限）。
	SupportedModels string `json:"supported_models,omitempty"`
	// Managed 标记是否由 site 同步投影自动生成，仅投影逻辑透传。
	Managed bool `json:"managed,omitempty"`
}

type ChannelKeyUpdateRequest struct {
	ID         int     `json:"id" binding:"required"`
	Enabled    *bool   `json:"enabled,omitempty"`
	ChannelKey *string `json:"channel_key,omitempty"`
	Priority   *int    `json:"priority,omitempty"`
	Remark     *string `json:"remark,omitempty"`
	// SupportedModels 逗号分隔的模型列表，限定该 key 只能用于这些模型（空=不限）。
	SupportedModels *string `json:"supported_models,omitempty"`
	// Managed 标记是否由 site 同步投影自动生成，仅投影逻辑透传。
	Managed *bool `json:"managed,omitempty"`
}

// ChannelBatchGroupRequest 批量设置渠道分组请求
type ChannelBatchGroupRequest struct {
	IDs     []int `json:"ids" binding:"required"`
	GroupID int   `json:"group_id"` // 0 表示默认分组
}

// ChannelBatchGroupResult 批量设置渠道分组结果
type ChannelBatchGroupResult struct {
	SuccessIDs  []int                      `json:"success_ids"`
	FailedItems []ChannelBatchGroupFailure `json:"failed_items"`
}

type ChannelBatchGroupFailure struct {
	ID      int    `json:"id"`
	Message string `json:"message"`
}

// ChannelFetchModelRequest is used by /channel/fetch-model (not persisted).
type ChannelFetchModelRequest struct {
	Type    outbound.OutboundType `json:"type" binding:"required"`
	BaseURL string                `json:"base_url" binding:"required"`
	Key     string                `json:"key" binding:"required"`
	Proxy   bool                  `json:"proxy"`
}

// TableName explicitly returns "-" for DTO structs to prevent GORM auto-mapping.
func (ChannelUpdateRequest) TableName() string     { return "-" }
func (ChannelKeyAddRequest) TableName() string     { return "-" }
func (ChannelKeyUpdateRequest) TableName() string  { return "-" }
func (ChannelFetchModelRequest) TableName() string { return "-" }

func (c *RequestRewriteConfig) Validate(channelType outbound.OutboundType) error {
	if c == nil || !c.Enabled {
		return nil
	}

	if c.Profile == "" {
		return fmt.Errorf("request rewrite profile is required when enabled")
	}

	switch c.Profile {
	case RequestRewriteProfilePreserve:
		// preserve means no body rewrite
	case RequestRewriteProfileOpenAIChatCompat:
		if channelType != outbound.OutboundTypeOpenAIChat && channelType != outbound.OutboundTypeOpenAIResponse && channelType != outbound.OutboundTypeMimo {
			return fmt.Errorf("request rewrite profile %s is not supported for channel type %d", c.Profile, channelType)
		}
	case RequestRewriteProfileCodexHeaders:
		// codex profile currently affects header shaping only and is allowed for enabled rewrite configs.
	default:
		return fmt.Errorf("unsupported request rewrite profile: %s", c.Profile)
	}

	switch c.ToolRoleStrategy {
	case "", ToolRoleStrategyKeep, ToolRoleStrategyStringifyToUser:
	default:
		return fmt.Errorf("unsupported tool role strategy: %s", c.ToolRoleStrategy)
	}

	switch c.SystemMessageStrategy {
	case "", SystemMessageStrategyKeep, SystemMessageStrategyMerge:
	default:
		return fmt.Errorf("unsupported system message strategy: %s", c.SystemMessageStrategy)
	}

	return nil
}

func (c *Channel) GetBaseUrl() string {
	if c == nil || len(c.BaseUrls) == 0 {
		return ""
	}

	bestURL := ""
	bestDelay := 0
	bestSet := false

	for _, bu := range c.BaseUrls {
		if bu.URL == "" {
			continue
		}
		if !bestSet || bu.Delay < bestDelay {
			bestURL = bu.URL
			bestDelay = bu.Delay
			bestSet = true
		}
	}

	return bestURL
}

func (c *Channel) GetNormalizedBaseUrl() string {
	if c == nil {
		return ""
	}

	rawURL := c.GetBaseUrl()
	return normalizeChannelBaseURL(rawURL, c.Type, c.getBaseURLSuffixMode(rawURL))
}

func (c *Channel) GetNormalizedBaseUrlFor(rawURL string) string {
	if c == nil {
		return strings.TrimRight(strings.TrimSpace(rawURL), "/")
	}

	return normalizeChannelBaseURL(rawURL, c.Type, c.getBaseURLSuffixMode(rawURL))
}

func (c *Channel) getBaseURLSuffixMode(rawURL string) string {
	trimmedURL := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	for _, baseURL := range c.BaseUrls {
		if strings.TrimRight(strings.TrimSpace(baseURL.URL), "/") == trimmedURL {
			return baseURL.SuffixMode
		}
	}
	return ""
}

func normalizeChannelBaseURL(rawURL string, channelType outbound.OutboundType, suffixMode string) string {
	trimmedURL := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if trimmedURL == "" {
		return ""
	}

	switch strings.ToLower(strings.TrimSpace(suffixMode)) {
	case "":
		return appendBaseURLPathByChannel(trimmedURL, channelType)
	case "custom":
		return normalizeCustomBaseURL(trimmedURL, channelType)
	case "openai_compat", "openai":
		return appendBaseURLPathIfMissing(trimmedURL, strings.ToLower(trimmedURL), "/v1")
	case "anthropic":
		return appendBaseURLPathIfMissing(trimmedURL, strings.ToLower(trimmedURL), "/v1")
	case "gemini":
		return appendBaseURLPathIfMissing(trimmedURL, strings.ToLower(trimmedURL), "/v1beta")
	case "volcengine":
		return appendBaseURLPathIfMissing(trimmedURL, strings.ToLower(trimmedURL), "/api/v3")
	default:
		return appendBaseURLPathByChannel(trimmedURL, channelType)
	}
}

func normalizeCustomBaseURL(rawURL string, channelType outbound.OutboundType) string {
	switch channelType {
	case outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeOpenAIResponse, outbound.OutboundTypeOpenAIEmbedding, outbound.OutboundTypeMimo:
		return trimKnownOpenAIEndpointPath(rawURL)
	default:
		return rawURL
	}
}

func trimKnownOpenAIEndpointPath(rawURL string) string {
	lowerURL := strings.ToLower(strings.TrimSpace(rawURL))
	for _, suffix := range []string{"/v1/chat/completions", "/chat/completions", "/v1/responses", "/responses", "/v1/embeddings", "/embeddings"} {
		if strings.HasSuffix(lowerURL, suffix) {
			return strings.TrimRight(rawURL[:len(rawURL)-len(suffix)], "/")
		}
	}
	return rawURL
}

func appendBaseURLPathByChannel(rawURL string, channelType outbound.OutboundType) string {
	lowerURL := strings.ToLower(rawURL)
	switch channelType {
	case outbound.OutboundTypeAnthropic:
		return appendBaseURLPathIfMissing(rawURL, lowerURL, "/v1")
	case outbound.OutboundTypeGemini:
		return appendBaseURLPathIfMissing(rawURL, lowerURL, "/v1beta")
	case outbound.OutboundTypeVolcengine:
		return appendBaseURLPathIfMissing(rawURL, lowerURL, "/api/v3")
	case outbound.OutboundTypeCloudflare:
		// Cloudflare Workers AI 的路径（/ai/run/@cf/{model}）由 adapter 拼接，
		// base_url 保持账户根路径（含 /client/v4/accounts/{id}）原样，不加默认前缀。
		return rawURL
	default:
		return appendBaseURLPathIfMissing(rawURL, lowerURL, "/v1")
	}
}

func appendBaseURLPathIfMissing(rawURL, lowerURL, suffix string) string {
	if strings.HasSuffix(lowerURL, strings.ToLower(suffix)) {
		return rawURL
	}
	return rawURL + suffix
}

// GetChannelKey 选择一个可用的渠道 Key，用于后台探测/拉模型等不涉及冷却的场景。
// 这些场景的 model 维度冷却语义较弱，故传空 model 跳过按模型冷却（仅按成本排序）。
func (c *Channel) GetChannelKey() ChannelKey {
	return c.GetChannelKeyWithCooldown("", 300)
}

// EnabledKeyCount returns the number of enabled keys with non-empty ChannelKey.
func (c *Channel) EnabledKeyCount() int {
	if c == nil {
		return 0
	}
	count := 0
	for _, k := range c.Keys {
		if k.Enabled && k.ChannelKey != "" {
			count++
		}
	}
	return count
}

// GetChannelKeyExcluding 选择一个未被排除的可用 Key，用于后台任务（不涉及冷却）。
func (c *Channel) GetChannelKeyExcluding(excludeKeyIDs []int) ChannelKey {
	return c.GetChannelKeyExcludingWithCooldown(excludeKeyIDs, "", 300)
}

// GetChannelKeyWithCooldown 选择一个可用 Key，按模型粒度冷却过滤。
// modelName 为空时跳过冷却判断（后台探测/拉模型等场景），仅按成本排序。
func (c *Channel) GetChannelKeyWithCooldown(modelName string, ratelimitCooldownSec int) ChannelKey {
	return c.GetChannelKeyExcludingWithCooldown(nil, modelName, ratelimitCooldownSec)
}

// GetChannelKeyExcludingWithCooldown 选择一个未被排除的可用 Key，按模型粒度冷却过滤。
// key 冷却改为按 (channelID, keyID, modelName) 维度记录在内存中（见 balancer.RecordKeyCooldown），
// 避免某 key 对一个模型触发 ≥400 错误后，连带冷却该 key 对其他所有模型的使用——
// 公益站「部分模型出问题、其他模型没问题」的常见场景下，旧逻辑会浪费可用 Key。
// KeyCooldownFunc 由 relay/balancer 包在 init 时注入，用于按 (channelID, keyID, modelName)
// 维度查询某个 Key 是否处于冷却期。model 包不能反向依赖 balancer（存在循环导入），
// 故通过函数变量解耦。未注入时返回 false（不冷却），保持后台任务等场景可用。
//
// Key 选择策略（key_selection_strategy）：
//   - "cost"（默认）：选 TotalCost 最低的 key
//   - "availability"：选可用度分数最高的 key（满分 100，出错衰减、成功/时间恢复）；
//     同分按 Keys 数组顺序取第一个（初始全满分 → 用第一个 key）；全部分数 ≤ 0 时
//     回退 cost 策略防卡死。可用度是软优先级，冷却/熔断/失败提示硬隔离仍生效。
//   - "speed"：选 EMA 平滑 TPS（tokens/sec）最高的 key；仅记录成功请求的 TPS
//     （output_tokens / attempt_duration_seconds），反映上游真实生成速度。
//     同 TPS 按候选顺序取第一个；所有候选均无 TPS 数据时回退 cost 策略防卡死。
//     速度是软优先级，冷却/熔断/失败提示硬隔离仍生效（issue #140）。
//   - "priority"：选 Priority 数字最大的 key；同优先级选 TotalCost 更低者，仍相同则
//     按 Keys 数组顺序取第一个。
//
// 渠道 KeySelectionStrategy 为空时继承全局策略（GlobalKeySelectionStrategyFunc）。
func (c *Channel) GetChannelKeyExcludingWithCooldown(excludeKeyIDs []int, modelName string, ratelimitCooldownSec int) ChannelKey {
	if c == nil || len(c.Keys) == 0 {
		return ChannelKey{}
	}

	excludeSet := make(map[int]struct{}, len(excludeKeyIDs))
	for _, id := range excludeKeyIDs {
		excludeSet[id] = struct{}{}
	}

	// 冷却时长由 balancer 层从 SettingKeyRatelimitCooldown 读取（见 IsKeyOnCooldown），
	// ratelimitCooldownSec 参数保留以兼容调用方，不再在此处直接使用。
	_ = ratelimitCooldownSec
	modelName = strings.TrimSpace(modelName)

	// 先收集通过 Enabled/排除/冷却三关的候选。
	candidates := make([]ChannelKey, 0, len(c.Keys))
	for _, k := range c.Keys {
		if !k.Enabled {
			continue
		}
		if _, excluded := excludeSet[k.ID]; excluded {
			continue
		}
		// 按模型过滤：key 的 SupportedModels 非空时，只选支持当前模型的 key。
		// 空表示不限制（兼容存量 key）。用 ModelMatches 做 trim 后精确比较。
		// modelName 为空（后台任务）时不跳过。
		if modelName != "" && !ModelMatches(k.SupportedModels, modelName) {
			continue
		}
		// 按模型粒度冷却：仅当该 (channelID, keyID, modelName) 处于冷却期时跳过。
		// modelName 为空（后台任务）时不跳过。通过 KeyCooldownFunc 间接调用 balancer，
		// 避免 model → balancer 循环依赖；冷却时长由 balancer 内部读取设置决定。
		if modelName != "" && KeyCooldownFunc != nil && KeyCooldownFunc(c.ID, k.ID, modelName) {
			continue
		}
		candidates = append(candidates, k)
	}
	if len(candidates) == 0 {
		return ChannelKey{}
	}

	strategy := c.effectiveKeySelectionStrategy()
	if strategy == "availability" && KeyAvailabilityScoreFunc != nil && modelName != "" {
		return c.selectKeyByAvailability(candidates, modelName)
	}
	if strategy == "speed" && KeySpeedTPSFunc != nil && modelName != "" {
		return c.selectKeyBySpeed(candidates, modelName)
	}
	if strategy == "priority" {
		return selectKeyByPriority(candidates)
	}
	return selectKeyByCost(candidates)
}

// effectiveKeySelectionStrategy 返回生效的 key 选择策略：渠道优先，空则取全局。
func (c *Channel) effectiveKeySelectionStrategy() string {
	if c.KeySelectionStrategy != "" {
		return c.KeySelectionStrategy
	}
	if GlobalKeySelectionStrategyFunc != nil {
		if s := GlobalKeySelectionStrategyFunc(); s != "" {
			return s
		}
	}
	return "cost"
}

// selectKeyByCost 选成本最低的 key（原有逻辑）。
func selectKeyByCost(candidates []ChannelKey) ChannelKey {
	best := candidates[0]
	for _, k := range candidates[1:] {
		if k.TotalCost < best.TotalCost {
			best = k
		}
	}
	return best
}

// selectKeyByPriority 选优先级数字最大的 key；同优先级选成本更低者，仍相同则保持候选顺序。
func selectKeyByPriority(candidates []ChannelKey) ChannelKey {
	best := candidates[0]
	for _, k := range candidates[1:] {
		if k.Priority > best.Priority || (k.Priority == best.Priority && k.TotalCost < best.TotalCost) {
			best = k
		}
	}
	return best
}

// selectKeyByAvailability 选可用度分数最高的 key。
// 分数 > 0 的候选中选最高分；同分按候选顺序取第一个（即 Keys 数组顺序）。
// 若所有候选分数 ≤ 0，回退成本最低（防卡死兜底）。
func (c *Channel) selectKeyByAvailability(candidates []ChannelKey, modelName string) ChannelKey {
	best := ChannelKey{}
	bestScore := 0.0
	bestSet := false
	for _, k := range candidates {
		score := KeyAvailabilityScoreFunc(c.ID, k.ID, modelName)
		if score <= 0 {
			continue
		}
		if !bestSet || score > bestScore {
			best = k
			bestScore = score
			bestSet = true
		}
	}
	if !bestSet {
		// 所有候选分数 ≤ 0，回退成本最低。
		return selectKeyByCost(candidates)
	}
	return best
}

// selectKeyBySpeed 选 EMA 平滑 TPS 最高的 key。
// 仅考虑有 TPS 数据（>0）的候选；同 TPS 按候选顺序取第一个（即 Keys 数组顺序）。
// 若所有候选均无 TPS 数据（冷启动），回退成本最低（防卡死兜底）。
func (c *Channel) selectKeyBySpeed(candidates []ChannelKey, modelName string) ChannelKey {
	best := ChannelKey{}
	bestTPS := 0.0
	bestSet := false
	for _, k := range candidates {
		tps := KeySpeedTPSFunc(c.ID, k.ID, modelName)
		if tps <= 0 {
			continue
		}
		if !bestSet || tps > bestTPS {
			best = k
			bestTPS = tps
			bestSet = true
		}
	}
	if !bestSet {
		// 所有候选均无 TPS 数据，回退成本最低。
		return selectKeyByCost(candidates)
	}
	return best
}
