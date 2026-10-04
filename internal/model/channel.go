package model

import (
	"encoding/json"
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

// UpstreamProtocol 是渠道声明的上游协议名。与分组 outbound_format 的历史取值
// 保持同一套词汇，便于旧值平移；渠道字段为空时完全沿用分组值。
type UpstreamProtocol string

const (
	// UpstreamProtocolChat 优先 Chat Completions，可回退 Responses。
	UpstreamProtocolChat UpstreamProtocol = "chat"
	// UpstreamProtocolResponses 优先 Responses，可回退 Chat Completions。
	UpstreamProtocolResponses UpstreamProtocol = "responses"
	// UpstreamProtocolMessages 优先 Anthropic Messages，可回退 Chat → Responses。
	UpstreamProtocolMessages UpstreamProtocol = "messages"
	// UpstreamProtocolChatOnly 只走 Chat Completions，禁用跨格式回退。
	UpstreamProtocolChatOnly UpstreamProtocol = "chat_only"
	// UpstreamProtocolResponsesOnly 只走 Responses，禁用跨格式回退。
	UpstreamProtocolResponsesOnly UpstreamProtocol = "responses_only"
	// UpstreamProtocolMessagesOnly 只走 Anthropic Messages，禁用跨格式回退。
	UpstreamProtocolMessagesOnly UpstreamProtocol = "messages_only"
	// UpstreamProtocolPassthrough 原样转发入站 JSON 体，禁用 adapter 回退。
	UpstreamProtocolPassthrough UpstreamProtocol = "passthrough"
	// UpstreamProtocolRaw 与 passthrough 相同，但额外保留客户端原始请求路径。
	UpstreamProtocolRaw UpstreamProtocol = "raw"
)

// upstreamProtocolOrder 列出全部合法协议名。仅用于取值校验，不用于排序：
// 用户在界面上拖出来的顺序就是运行时优先级，排序会把这个信息抹掉。
var upstreamProtocolOrder = []UpstreamProtocol{
	UpstreamProtocolChat,
	UpstreamProtocolResponses,
	UpstreamProtocolMessages,
	UpstreamProtocolChatOnly,
	UpstreamProtocolResponsesOnly,
	UpstreamProtocolMessagesOnly,
	UpstreamProtocolPassthrough,
	UpstreamProtocolRaw,
}

// IsValidUpstreamProtocol 报告给定协议名是否为受支持的取值（空串合法 = 跟随分组）。
func IsValidUpstreamProtocol(value string) bool {
	normalized := UpstreamProtocol(strings.ToLower(strings.TrimSpace(value)))
	if normalized == "" {
		return true
	}
	for _, candidate := range upstreamProtocolOrder {
		if normalized == candidate {
			return true
		}
	}
	return false
}

// NormalizeUpstreamProtocols 校验并规范化渠道协议声明列表：逐项小写去空白、
// 拒绝未知取值、去重，并**保留用户声明的先后顺序**（顺序即尝试优先级）。
// 空输入返回 nil（= 跟随分组）。
//
// 同时做互斥校验：passthrough/raw 是「整体透传」，不能与其它协议共存；
// 带 _only 的严格模式与同协议的非严格模式（如 chat_only + chat）互相矛盾，
// 一并拒绝，避免运行时无法判断该用哪个。
func NormalizeUpstreamProtocols(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	seen := make(map[UpstreamProtocol]bool, len(values))
	ordered := make([]string, 0, len(values))
	for _, raw := range values {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		normalized := UpstreamProtocol(strings.ToLower(trimmed))
		if !IsValidUpstreamProtocol(string(normalized)) || normalized == "" {
			return nil, fmt.Errorf("unsupported upstream protocol: %s", raw)
		}
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		ordered = append(ordered, string(normalized))
	}
	if len(ordered) == 0 {
		return nil, nil
	}
	if len(seen) > 1 {
		for _, passthroughOnly := range []UpstreamProtocol{UpstreamProtocolPassthrough, UpstreamProtocolRaw} {
			if seen[passthroughOnly] {
				return nil, fmt.Errorf("upstream protocol %s cannot be combined with other protocols", passthroughOnly)
			}
		}
	}
	for _, strictPair := range [][2]UpstreamProtocol{
		{UpstreamProtocolChatOnly, UpstreamProtocolChat},
		{UpstreamProtocolResponsesOnly, UpstreamProtocolResponses},
		{UpstreamProtocolMessagesOnly, UpstreamProtocolMessages},
	} {
		if seen[strictPair[0]] && seen[strictPair[1]] {
			return nil, fmt.Errorf("upstream protocols %s and %s are mutually exclusive", strictPair[0], strictPair[1])
		}
	}
	return ordered, nil
}

// MarshalUpstreamProtocols 把已规范化的协议列表编码成入库用的 JSON 文本。
// 空列表返回空串，与「未声明」的列默认值保持一致（避免写入 "null" / "[]"
// 两种等价格式让「是否已声明」的判断变复杂）。
func MarshalUpstreamProtocols(protocols []string) (string, error) {
	if len(protocols) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(protocols)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

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
	ID            int                   `json:"id" gorm:"primaryKey"`
	Name          string                `json:"name" gorm:"unique;not null"`
	GroupID       int                   `json:"group_id" gorm:"not null;default:0;index"`
	Type          outbound.OutboundType `json:"type"`
	Enabled       bool                  `json:"enabled"`
	BaseUrls      []BaseUrl             `json:"base_urls" gorm:"serializer:json"`
	Keys          []ChannelKey          `json:"keys" gorm:"foreignKey:ChannelID"`
	Model         string                `json:"model"`
	CustomModel   string                `json:"custom_model"`
	ProxyMode     ProxyUsageMode        `json:"proxy_mode" gorm:"type:varchar(16);not null;default:'direct'"`
	ProxyConfigID *int                  `json:"proxy_config_id"`
	Proxy         bool                  `json:"proxy" gorm:"default:false"`
	AutoSync      bool                  `json:"auto_sync" gorm:"default:false"`
	// AutoSyncKeyModels 开启后，模型自动同步任务会逐个 key 抓取上游模型列表，
	// 并把结果回填到每个 key 的 SupportedModels（按 key 隔离模型权限）。
	// 抓取失败的 key 保留旧值，绝不清空。默认关闭：关闭时同步任务只抓成本最低
	// 的一个 key，把结果写进渠道级 Model（保持原有行为不变）。
	AutoSyncKeyModels    bool           `json:"auto_sync_key_models" gorm:"column:auto_sync_key_models;default:false"`
	AutoGroup            AutoGroupType  `json:"auto_group" gorm:"default:0"`
	SkipModelTest        bool           `json:"skip_model_test" gorm:"default:false"`
	Disposable           bool           `json:"disposable" gorm:"default:false"`
	ExpireAt             *time.Time     `json:"expire_at,omitempty" gorm:"index"`
	KeySelectionStrategy string         `json:"key_selection_strategy" gorm:"type:varchar(16);not null;default:''"`
	CustomHeader         []CustomHeader `json:"custom_header" gorm:"serializer:json"`
	ParamOverride        *string        `json:"param_override"`
	// OutboundFormatOverride 渠道级出站协议覆盖（issue: 只支持单一协议的公益站）。
	// 空 = 跟随分组 outbound_format；合法值 chat_only / responses_only，渠道值优先。
	OutboundFormatOverride string                `json:"outbound_format_override,omitempty" gorm:"column:outbound_format_override;type:varchar(20);not null;default:''"`
	ChannelProxy           *string               `json:"channel_proxy,omitempty" gorm:"column:channel_proxy"`
	RequestRewrite         *RequestRewriteConfig `json:"request_rewrite" gorm:"serializer:json"`
	// RelayLogRawSSEUntil enables bounded provider-native SSE capture until the
	// Unix timestamp. Zero or an expired value keeps the normal semantic-only
	// stream log path and avoids permanently retaining every raw event frame.
	RelayLogRawSSEUntil int64         `json:"relay_log_raw_sse_until,omitempty" gorm:"column:relay_log_raw_sse_until;default:0"`
	Stats               *StatsChannel `json:"stats,omitempty" gorm:"foreignKey:ChannelID"`
	MatchRegex          *string       `json:"match_regex"`
	// KeyHealthPassed 记录最近一次定时 Key 巡检是否全部通过（issue #142）。
	// nil = 从未巡检；true = 全部通过；false = 存在失败。前端据此对失败渠道标灰。
	KeyHealthPassed *bool `json:"key_health_passed,omitempty" gorm:"column:key_health_passed"`
	// KeyHealthAllFailed 区分"全部 Key 失败"与"部分失败"。
	// nil = 从未巡检；true = 所有 Key 均不可用；false = 至少一个 Key 可用。
	// 仅当 KeyHealthPassed=false 时有意义：前端据此决定整张卡片灰色化还是仅标记部分失败。
	KeyHealthAllFailed *bool `json:"key_health_all_failed,omitempty" gorm:"column:key_health_all_failed"`
	// KeyHealthAt 最近一次定时 Key 巡检完成时间（unix 秒），0 = 从未巡检。
	KeyHealthAt int64 `json:"key_health_at,omitempty" gorm:"column:key_health_at;default:0"`
	// MaxConcurrency 渠道级最大并发（同时在途的 upstream 请求数上限）。0 = 不限制。
	// 保护脆弱上游：超过上限的候选在候选级被 Skip，attempt 级原子占用兜底竞态。
	MaxConcurrency int `json:"max_concurrency,omitempty" gorm:"column:max_concurrency;not null;default:0"`
	// RPMLimit 渠道级每分钟请求数上限（按候选选中计，1 次选中消耗 1 个 token）。
	// 0 = 不限制。与 API key 级 RPM 语义一致：计数请求路由而非 upstream 尝试数。
	RPMLimit int `json:"rpm_limit,omitempty" gorm:"column:rpm_limit;not null;default:0"`
	// RelayRetryCountOverride 渠道级 Key 重试次数覆盖（阶段2）。-1 = 跟随分组/全局；
	// 0 = 该渠道不重试（只尝试 1 次）；>0 = 该渠道最多重试 N 次（共 N+1 次尝试）。
	// 覆盖优先级：渠道 > 分组 > 全局设置。
	RelayRetryCountOverride int `json:"relay_retry_count_override,omitempty" gorm:"column:relay_retry_count_override;not null;default:-1"`
	// CircuitBreakerThreshold 渠道级熔断阈值覆盖（阶段3）：连续失败 N 次触发熔断。
	// 0 = 跟随全局设置。内存构造的结构体零值即"跟随全局"，无 -1 哨兵的零值陷阱。
	CircuitBreakerThreshold int `json:"circuit_breaker_threshold,omitempty" gorm:"column:circuit_breaker_threshold;not null;default:0"`
	// CircuitBreakerCooldown 渠道级熔断基础冷却（秒）覆盖。0 = 跟随全局设置。
	// 指数退避基于该值：cooldown = base * 2^(tripCount-1)，上限见 MaxCooldown。
	CircuitBreakerCooldown int `json:"circuit_breaker_cooldown,omitempty" gorm:"column:circuit_breaker_cooldown;not null;default:0"`
	// CircuitBreakerMaxCooldown 渠道级熔断最大冷却（秒）覆盖。0 = 跟随全局设置。
	CircuitBreakerMaxCooldown int `json:"circuit_breaker_max_cooldown,omitempty" gorm:"column:circuit_breaker_max_cooldown;not null;default:0"`
	// KeyCooldownRatelimit 渠道级 429 冷却（秒）覆盖。0 = 跟随全局设置。
	KeyCooldownRatelimit int `json:"key_cooldown_ratelimit,omitempty" gorm:"column:key_cooldown_ratelimit;not null;default:0"`
	// KeyCooldownAuthError 渠道级 401/403 冷却（秒）覆盖。0 = 跟随全局设置。
	KeyCooldownAuthError int `json:"key_cooldown_auth_error,omitempty" gorm:"column:key_cooldown_auth_error;not null;default:0"`
	// KeyCooldownServerError 渠道级 5xx/408 冷却（秒）覆盖。0 = 跟随全局设置。
	KeyCooldownServerError int `json:"key_cooldown_server_error,omitempty" gorm:"column:key_cooldown_server_error;not null;default:0"`
	// RetryableStatusCodes 渠道级可重试状态码(逗号分隔,100-599)。命中则强制进入
	// 换 Key/换渠道重试。空 = 不启用渠道级白名单(仍受内置默认规则与全局设置影响)。
	// 上游是公益站/私有网关时,同一语义的错误码各不相同,渠道级配置比全局规则更准。
	RetryableStatusCodes string `json:"retryable_status_codes,omitempty" gorm:"column:retryable_status_codes;type:varchar(255);not null;default:''"`
	// RetryableKeywords 渠道级可重试关键词(逗号分隔)。与 RetryableStatusCodes 是 OR
	// 关系:任一命中即重试。匹配上游错误文本,大小写不敏感子串。空 = 不启用。
	RetryableKeywords string `json:"retryable_keywords,omitempty" gorm:"column:retryable_keywords;type:varchar(512);not null;default:''"`
	// NonRetryableStatusCodes 渠道级不可重试状态码(逗号分隔)。命中则强制不重试,
	// 优先级高于 RetryableStatusCodes/RetryableKeywords。空 = 不启用。
	NonRetryableStatusCodes string `json:"non_retryable_status_codes,omitempty" gorm:"column:non_retryable_status_codes;type:varchar(255);not null;default:''"`
	// ErrorMessageTemplate 渠道级错误文案模板,支持 {upstream} 占位符嵌入上游原文。
	// 仅改写最终呈现文案,不改重试决策、不伪造成功。空 = 不启用。
	ErrorMessageTemplate string `json:"error_message_template,omitempty" gorm:"column:error_message_template;type:varchar(512);not null;default:''"`
	// UpstreamProtocols 渠道级上游协议声明（JSON 有序数组），取值见 UpstreamProtocol。
	// 空数组/nil = 完全沿用分组 outbound_format（升级零行为变化）；非空时按顺序
	// 决定出站 adapter 的尝试顺序，取代分组的协议选择。
	//
	// 这是渠道自己的属性而非分组的属性：同一个分组里可能同时有只支持
	// Chat Completions 的公益站和原生 Gemini 渠道，协议声明必须在渠道层表达。
	UpstreamProtocols []string `json:"upstream_protocols,omitempty" gorm:"column:upstream_protocols;serializer:json;type:text"`
	// FirstTokenTimeOut 渠道级首字超时覆盖（秒）。
	// 0 = 跟随分组（也是 Go 零值，内存里临时构造的渠道对象不会误关看门狗）；
	// -1 = 显式关闭该看门狗；>0 = 秒数。
	FirstTokenTimeOut int `json:"first_token_time_out,omitempty" gorm:"column:first_token_time_out;not null;default:0"`
	// AttemptTimeOut 渠道级单次转发尝试超时覆盖（秒）。语义同 FirstTokenTimeOut：
	// 0 = 跟随分组，-1 = 显式关闭，>0 = 秒数。
	AttemptTimeOut int `json:"attempt_time_out,omitempty" gorm:"column:attempt_time_out;not null;default:0"`
	// StreamIdleTimeout 渠道级流式空闲超时覆盖（秒）。语义同 FirstTokenTimeOut。
	StreamIdleTimeout int `json:"stream_idle_timeout,omitempty" gorm:"column:stream_idle_timeout;not null;default:0"`
	// ReasoningBufferStrategy 渠道级推理缓冲策略覆盖：空 = 跟随分组，
	// buffer / immediate = 显式指定。
	ReasoningBufferStrategy string `json:"reasoning_buffer_strategy,omitempty" gorm:"column:reasoning_buffer_strategy;type:varchar(20);not null;default:''"`
}

type BaseUrl struct {
	URL        string `json:"url"`
	Delay      int    `json:"delay"`
	SuffixMode string `json:"suffix_mode,omitempty"`
	// Protocol 声明这条 base URL 服务于哪种上游协议（取值同 UpstreamProtocol，
	// 通常为 chat / responses / messages）。空 = 通用地址。
	//
	// 一个渠道同时支持多协议、且各协议地址不同（如火山方舟的 OpenAI 兼容在
	// /api/v3、Anthropic 兼容在 /api/compatible）时，靠这个字段把地址和协议绑起来；
	// 只有一条地址时不填即可，行为与从前一致。
	Protocol string `json:"protocol,omitempty"`
}

type CustomHeader struct {
	HeaderKey   string `json:"header_key"`
	HeaderValue string `json:"header_value"`
}

// NormalizeOutboundFormatOverride 校验并规范化渠道级出站协议覆盖。
// 空值合法（= 跟随分组）；仅接受 chat_only / responses_only；其他值报错，
// 避免 UI/API 写入脏值后运行时静默忽略造成"设置了但不生效"的困惑。
func NormalizeOutboundFormatOverride(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "", "chat_only", "responses_only":
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported outbound format override: %s (allowed: chat_only, responses_only)", value)
	}
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
	ID            int                    `json:"id" binding:"required"`
	Name          *string                `json:"name,omitempty"`
	GroupID       *int                   `json:"group_id,omitempty"`
	Type          *outbound.OutboundType `json:"type,omitempty"`
	Enabled       *bool                  `json:"enabled,omitempty"`
	BaseUrls      *[]BaseUrl             `json:"base_urls,omitempty"`
	Model         *string                `json:"model,omitempty"`
	CustomModel   *string                `json:"custom_model,omitempty"`
	ProxyMode     *ProxyUsageMode        `json:"proxy_mode,omitempty"`
	ProxyConfigID *int                   `json:"proxy_config_id,omitempty"`
	Proxy         *bool                  `json:"proxy,omitempty"`
	AutoSync      *bool                  `json:"auto_sync,omitempty"`
	// AutoSyncKeyModels 为 true 时，模型自动同步任务逐个 key 抓取并回填每个 key 的
	// SupportedModels；nil 表示本次请求不修改该开关（白名单补丁语义）。
	AutoSyncKeyModels       *bool                 `json:"auto_sync_key_models,omitempty"`
	SkipModelTest           *bool                 `json:"skip_model_test,omitempty"`
	Disposable              *bool                 `json:"disposable,omitempty"`
	ExpireAt                *time.Time            `json:"expire_at,omitempty"`
	KeySelectionStrategy    *string               `json:"key_selection_strategy,omitempty"`
	AutoGroup               *AutoGroupType        `json:"auto_group,omitempty"`
	CustomHeader            *[]CustomHeader       `json:"custom_header,omitempty"`
	ChannelProxy            *string               `json:"channel_proxy,omitempty"`
	ParamOverride           *string               `json:"param_override,omitempty"`
	OutboundFormatOverride  *string               `json:"outbound_format_override,omitempty"`
	UpstreamProtocols       *[]string             `json:"upstream_protocols,omitempty"`
	FirstTokenTimeOut       *int                  `json:"first_token_time_out,omitempty"`
	AttemptTimeOut          *int                  `json:"attempt_time_out,omitempty"`
	StreamIdleTimeout       *int                  `json:"stream_idle_timeout,omitempty"`
	ReasoningBufferStrategy *string               `json:"reasoning_buffer_strategy,omitempty"`
	RequestRewrite          *RequestRewriteConfig `json:"request_rewrite,omitempty"`
	RelayLogRawSSEUntil     *int64                `json:"relay_log_raw_sse_until,omitempty"`
	MatchRegex              *string               `json:"match_regex,omitempty"`
	MaxConcurrency          *int                  `json:"max_concurrency,omitempty"`
	RPMLimit                *int                  `json:"rpm_limit,omitempty"`
	RetryableStatusCodes    *string               `json:"retryable_status_codes,omitempty"`
	RetryableKeywords       *string               `json:"retryable_keywords,omitempty"`
	NonRetryableStatusCodes *string               `json:"non_retryable_status_codes,omitempty"`
	ErrorMessageTemplate    *string               `json:"error_message_template,omitempty"`

	KeysToAdd    []ChannelKeyAddRequest    `json:"keys_to_add,omitempty"`
	KeysToUpdate []ChannelKeyUpdateRequest `json:"keys_to_update,omitempty"`
	KeysToDelete []int                     `json:"keys_to_delete,omitempty"`
}

type ChannelKeyAddRequest struct {
	Enabled    bool   `json:"enabled"`
	ChannelKey string `json:"channel_key" binding:"required"`
	Priority   int    `json:"priority"`
	Remark     string `json:"remark"`
	// SupportedModels 逗号分隔的模型列表，限定该 key 只能用于这些模型（空=不限）。
	SupportedModels string `json:"supported_models,omitempty"`
}

type ChannelKeyUpdateRequest struct {
	ID         int     `json:"id" binding:"required"`
	Enabled    *bool   `json:"enabled,omitempty"`
	ChannelKey *string `json:"channel_key,omitempty"`
	Priority   *int    `json:"priority,omitempty"`
	Remark     *string `json:"remark,omitempty"`
	// SupportedModels 逗号分隔的模型列表，限定该 key 只能用于这些模型（空=不限）。
	SupportedModels *string `json:"supported_models,omitempty"`
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

// GetBaseUrlForProtocol 选择服务于指定上游协议的 base URL。
//
// 匹配规则（按优先级）：
//  1. Protocol 与请求协议精确相等的条目里，取 Delay 最小的；
//  2. Protocol 为空的通用条目里，取 Delay 最小的（兼容未绑定协议的老配置）；
//  3. 都没有时回落到 GetBaseUrl()（整体延迟最小），保证老行为不变。
//
// protocol 为空串时直接走第 3 条，与改动前逐字节一致。
func (c *Channel) GetBaseUrlForProtocol(protocol string) string {
	if c == nil || len(c.BaseUrls) == 0 {
		return ""
	}
	normalized := strings.ToLower(strings.TrimSpace(protocol))
	if normalized == "" {
		return c.GetBaseUrl()
	}

	boundURL, boundSet := "", false
	boundDelay := 0
	genericURL, genericSet := "", false
	genericDelay := 0
	for _, bu := range c.BaseUrls {
		url := strings.TrimSpace(bu.URL)
		if url == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(bu.Protocol)) {
		case normalized:
			if !boundSet || bu.Delay < boundDelay {
				boundURL, boundDelay, boundSet = url, bu.Delay, true
			}
		case "":
			if !genericSet || bu.Delay < genericDelay {
				genericURL, genericDelay, genericSet = url, bu.Delay, true
			}
		}
	}
	if boundSet {
		return boundURL
	}
	if genericSet {
		return genericURL
	}
	return c.GetBaseUrl()
}

func (c *Channel) GetNormalizedBaseUrl() string {
	if c == nil {
		return ""
	}

	rawURL := c.GetBaseUrl()
	return normalizeChannelBaseURL(rawURL, c.Type, c.getBaseURLSuffixMode(rawURL))
}

// GetNormalizedBaseUrlForProtocol 是 GetBaseUrlForProtocol 的规范化版本：
// 在选中地址后套用与 GetNormalizedBaseUrl 相同的路径补全规则（按渠道类型补
// /v1、/v1beta、/api/v3 等，或按该条地址自己的 suffix_mode 处理）。
func (c *Channel) GetNormalizedBaseUrlForProtocol(protocol string) string {
	if c == nil {
		return ""
	}

	rawURL := c.GetBaseUrlForProtocol(protocol)
	return normalizeChannelBaseURL(rawURL, c.Type, c.getBaseURLSuffixMode(rawURL))
}

// ResolveTimeoutOverride 按「渠道覆盖 > 分组」解析一个超时项。
//
// channelValue 语义：0 = 未覆盖（跟随分组）；-1 = 显式关闭；>0 = 秒数。
//
// 这里刻意让「未覆盖」= Go 零值，而不是用 -1 之类的哨兵：渠道对象既从数据库
// 反序列化、也在内存里临时构造（探测、测试、缓存重建），哨兵方案会让任何一处
// 忘了显式赋值的构造点静默改变转发行为。0 作为「跟随分组」时，未配置的渠道与
// 改动前的运行结果逐字节一致。
func ResolveTimeoutOverride(channelValue, groupValue int) int {
	if channelValue == 0 {
		return groupValue
	}
	if channelValue < 0 {
		return 0
	}
	return channelValue
}

// EffectiveFirstTokenTimeOut 返回渠道与分组叠加后的首字超时秒数。
func (c *Channel) EffectiveFirstTokenTimeOut(groupValue int) int {
	if c == nil {
		return groupValue
	}
	return ResolveTimeoutOverride(c.FirstTokenTimeOut, groupValue)
}

// EffectiveAttemptTimeOut 返回渠道与分组叠加后的单次尝试超时秒数。
func (c *Channel) EffectiveAttemptTimeOut(groupValue int) int {
	if c == nil {
		return groupValue
	}
	return ResolveTimeoutOverride(c.AttemptTimeOut, groupValue)
}

// EffectiveStreamIdleTimeout 返回渠道与分组叠加后的流式空闲超时秒数。
func (c *Channel) EffectiveStreamIdleTimeout(groupValue int) int {
	if c == nil {
		return groupValue
	}
	return ResolveTimeoutOverride(c.StreamIdleTimeout, groupValue)
}

// EffectiveReasoningBufferStrategy 返回渠道级推理缓冲策略；空串表示未覆盖
// （由调用方继续回落到分组/全局）。
func (c *Channel) EffectiveReasoningBufferStrategy() string {
	if c == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(c.ReasoningBufferStrategy)) {
	case "buffer", "immediate":
		return strings.ToLower(strings.TrimSpace(c.ReasoningBufferStrategy))
	default:
		return ""
	}
}

// EffectiveUpstreamProtocols 返回渠道声明的协议列表。返回 nil 表示渠道未声明
// （调用方必须回落到分组 outbound_format）。
func (c *Channel) EffectiveUpstreamProtocols() []string {
	if c == nil || len(c.UpstreamProtocols) == 0 {
		return nil
	}
	normalized, err := NormalizeUpstreamProtocols(c.UpstreamProtocols)
	if err != nil || len(normalized) == 0 {
		return nil
	}
	return normalized
}

// PrimaryUpstreamProtocol 返回渠道声明的第一个协议，用于选择 base URL 绑定与
// 探测时的首选协议。未声明时返回空串。
func (c *Channel) PrimaryUpstreamProtocol() string {
	protocols := c.EffectiveUpstreamProtocols()
	if len(protocols) == 0 {
		return ""
	}
	return protocols[0]
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

// ModelMatches 判断逗号分隔的模型列表是否包含目标模型。
// modelsCSV 为空表示不限制（返回 true）；匹配采用 trim 后精确比较，不做模糊匹配。
func ModelMatches(modelsCSV, model string) bool {
	if modelsCSV == "" {
		return true
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return true
	}
	for _, candidate := range strings.Split(modelsCSV, ",") {
		if strings.TrimSpace(candidate) == model {
			return true
		}
	}
	return false
}
