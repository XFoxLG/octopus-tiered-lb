package model

type GroupMode int

const (
	GroupModeRoundRobin GroupMode = 1 // 轮询：依次循环选择渠道
	GroupModeRandom     GroupMode = 2 // 随机：每次随机选择一个渠道
	GroupModeFailover   GroupMode = 3 // 故障转移：按优先级选择，失败时降级到下一个
	GroupModeWeighted   GroupMode = 4 // 加权分配：按优权重分配流量
	GroupModeAuto       GroupMode = 5 // 自动：探索优先，基于成功率动态选择
)

type Group struct {
	ID                int       `json:"id" gorm:"primaryKey"`
	Name              string    `json:"name" gorm:"not null;size:191;uniqueIndex:idx_groups_endpoint_name,priority:2"`
	Category          string    `json:"category,omitempty" gorm:"not null;default:'';index;size:191"`
	EndpointType      string    `json:"endpoint_type" gorm:"not null;default:chat;index:idx_groups_endpoint_type;size:191;uniqueIndex:idx_groups_endpoint_name,priority:1"`
	EndpointProvider  string    `json:"endpoint_provider,omitempty" gorm:"not null;default:''"`
	OutboundFormat    string    `json:"outbound_format,omitempty" gorm:"not null;default:''"` // 出站格式: "" (auto), "chat", "responses", "messages", "chat_only", "responses_only", "messages_only", "passthrough", "raw" (原始穿透（信息体）: 保留原始请求体与请求路径，仅改写 model)
	Mode              GroupMode `json:"mode" gorm:"not null"`
	MatchRegex        string    `json:"match_regex"`
	// FirstTokenTimeOut is the maximum time to wait for the first visible
	// client-facing output in a streaming response. Metadata, role-only, and
	// reasoning-only frames do not satisfy this timer. Zero disables it.
	FirstTokenTimeOut int `json:"first_token_time_out"`
	// AttemptTimeOut 单次转发尝试的超时时间（秒），0 = 不启用（issue #122）。
	// 覆盖整个转发过程（HTTP 请求 + 响应读取），流式和非流式均生效。
	// 超时后自动视为错误，按现有重试策略切换到下一个渠道。
	AttemptTimeOut int `json:"attempt_time_out" gorm:"column:attempt_time_out;default:0"`
	// StreamIdleTimeout is the maximum allowed silence after visible streaming
	// output begins. Zero disables the watchdog for backwards compatibility.
	StreamIdleTimeout int `json:"stream_idle_timeout" gorm:"column:stream_idle_timeout;default:0"`
	SessionKeepTime int         `json:"session_keep_time"`   // 会话保持时间(秒) 0 为禁用
	Condition       string      `json:"condition,omitempty"` // 条件路由 JSON：[{"key":"model","op":"contains","value":"gpt-4"}]
	Items           []GroupItem `json:"items,omitempty" gorm:"foreignKey:GroupID"`
	// LastTestPassed 记录最近一次分组测试是否全部通过（issue #113）。
	// nil = 从未测试；true = 全部通过；false = 存在失败。测试完成时由
	// group_probe 回写，前端据此对失败分组做灰色化标记。
	LastTestPassed *bool `json:"last_test_passed,omitempty" gorm:"column:last_test_passed"`
	// LastTestAllFailed 区分"全部失败"与"部分失败"（issue #119）。
	// nil = 从未测试；true = 所有模型均失败；false = 至少一个模型通过。
	// 仅当 LastTestPassed=false 时有意义：前端据此决定是否对整张卡片
	// 灰色化（全部失败）还是仅标记部分失败（卡片边框保持正常）。
	LastTestAllFailed *bool `json:"last_test_all_failed,omitempty" gorm:"column:last_test_all_failed"`
	// LastTestAt 最近一次分组测试完成时间（unix 秒），0 = 从未测试。
	LastTestAt int64 `json:"last_test_at,omitempty" gorm:"column:last_test_at;default:0"`
	// ReasoningBufferStrategy 推理内容缓冲策略（issue #155 Cloudflare 超时问题）。
	// "" = 使用全局设置；"buffer" = 缓冲直到可见内容（安全重试但 CF 可能超时）；
	// "immediate" = 立即流式发送（实时体验但空输出不可重试）。
	ReasoningBufferStrategy string `json:"reasoning_buffer_strategy,omitempty" gorm:"column:reasoning_buffer_strategy;default:'';size:20"`
	// ParamOverride 分组级请求参数覆盖（JSON object 字符串，XyzenSun 移植）。
	// 与渠道级 ParamOverride 同语义（白名单字段、客户端优先）；优先级：客户端 > 渠道 > 分组。
	// nil/空串 = 未配置。
	ParamOverride *string `json:"param_override,omitempty"`
	// DefaultReasoningEffort 分组默认思考档位（Sub2API 参考，本仓库裁剪版）。
	// 非空时，客户端未表达任何思考意图的请求经 Octopus 补上该档位再转发；
	// 合法值 minimal/low/medium/high/xhigh/max，空串 = 关闭（不注入）。
	// 客户端显式发的档位一律尊重；显式关闭默认尊重，除非 ReasoningForceOverride。
	DefaultReasoningEffort string `json:"default_reasoning_effort,omitempty" gorm:"column:default_reasoning_effort;type:varchar(20);not null;default:''"`
	// ReasoningForceOverride 强制覆盖显式关闭（默认 false）。
	// true 时连客户端显式 none / thinking disabled 的请求也补上默认档位；
	// 对客户端已发具体档位的请求永不生效（改写档位不在本功能范围内）。
	ReasoningForceOverride bool `json:"reasoning_force_override,omitempty" gorm:"column:reasoning_force_override;not null;default:false"`
	// CustomHeader 分组级自定义请求头（XyzenSun 移植）。
	// 先于渠道级 CustomHeader 应用，同名时渠道覆盖分组。
	CustomHeader []CustomHeader `json:"custom_header,omitempty" gorm:"serializer:json"`
}

type GroupItem struct {
	ID        int    `json:"id" gorm:"primaryKey"`
	GroupID   int    `json:"group_id" gorm:"not null;index:idx_group_channel_model,unique"` // 创建时不携带此字段,更新时需要
	ChannelID int    `json:"channel_id" gorm:"not null;index:idx_group_channel_model,unique"`
	ModelName string `json:"model_name" gorm:"not null;index:idx_group_channel_model,unique;size:191"`
	Priority  int    `json:"priority"`
	Weight    int    `json:"weight"`
	// SupportsTools 渠道×模型 tools 支持结论（Seller 移植）。nil=未探测。
	SupportsTools *bool `json:"supports_tools,omitempty"`
	// SupportsToolsProbeKeyID 探测使用的 key ID（多 key 渠道审计用）。
	SupportsToolsProbeKeyID *int `json:"supports_tools_probe_key_id,omitempty"`
	// SupportsToolsProbedAt 最近探测/反馈时间。
	SupportsToolsProbedAt *int64 `json:"supports_tools_probed_at,omitempty"`
	// SupportsToolsSource 结论来源：probe/manual/manual-required-fallback。
	SupportsToolsSource string `json:"supports_tools_source,omitempty" gorm:"default:'';size:32"`
}

// GroupUpdateRequest 分组更新请求 - 仅包含变更的数据
type GroupUpdateRequest struct {
	ID                      int                      `json:"id" binding:"required"`
	Name                    *string                  `json:"name,omitempty"`                      // 仅在名称变更时发送
	Category                *string                  `json:"category,omitempty"`                  // 仅在分类变更时发送
	EndpointType            *string                  `json:"endpoint_type,omitempty"`             // 仅在 API 分类变更时发送
	EndpointProvider        *string                  `json:"endpoint_provider,omitempty"`         // 仅在端点提供方变更时发送
	OutboundFormat          *string                  `json:"outbound_format,omitempty"`           // 仅在出站格式变更时发送
	Mode                    *GroupMode               `json:"mode,omitempty"`                      // 仅在模式变更时发送
	MatchRegex              *string                  `json:"match_regex,omitempty"`               // 仅在匹配正则变更时发送
	Condition               *string                  `json:"condition,omitempty"`                 // 仅在条件变更时发送
	FirstTokenTimeOut       *int                     `json:"first_token_time_out,omitempty"`      // 仅在超时变更时发送(秒)
	AttemptTimeOut          *int                     `json:"attempt_time_out,omitempty"`          // 仅在转发超时变更时发送(秒)
	StreamIdleTimeout       *int                     `json:"stream_idle_timeout,omitempty"`       // 仅在流式空闲超时变更时发送(秒)
	SessionKeepTime         *int                     `json:"session_keep_time,omitempty"`         // 仅在会话保持时间变更时发送(秒)
	ReasoningBufferStrategy *string                  `json:"reasoning_buffer_strategy,omitempty"` // 仅在推理缓冲策略变更时发送
	ParamOverride           *string                  `json:"param_override,omitempty"`            // 仅在参数覆盖变更时发送（JSON object 字符串）
	DefaultReasoningEffort  *string                  `json:"default_reasoning_effort,omitempty"`  // 仅在默认思考档位变更时发送（空串=关闭注入）
	ReasoningForceOverride  *bool                    `json:"reasoning_force_override,omitempty"`  // 仅在强制覆盖显式关闭变更时发送
	CustomHeader            *[]CustomHeader          `json:"custom_header,omitempty"`             // 仅在自定义请求头变更时发送
	ItemsToAdd              []GroupItemAddRequest    `json:"items_to_add,omitempty"`              // 新增的 items
	ItemsToUpdate           []GroupItemUpdateRequest `json:"items_to_update,omitempty"`           // 更新的 items (priority 变更)
	ItemsToDelete           []int                    `json:"items_to_delete,omitempty"`           // 删除的 item IDs
}

// GroupItemAddRequest 新增 item 请求
type GroupItemAddRequest struct {
	ChannelID int    `json:"channel_id" binding:"required"`
	ModelName string `json:"model_name" binding:"required"`
	Priority  int    `json:"priority,omitempty"`
	Weight    int    `json:"weight,omitempty"`
}

// GroupItemUpdateRequest 更新 item 请求
type GroupItemUpdateRequest struct {
	ID       int `json:"id" binding:"required"`
	Priority int `json:"priority,omitempty"`
	Weight   int `json:"weight,omitempty"`
}

// GroupIDAndLLMName is a DTO for batch operations.
type GroupIDAndLLMName struct {
	ChannelID int
	ModelName string
}

// TableName explicitly returns "-" for DTO structs to prevent GORM auto-mapping.
func (GroupIDAndLLMName) TableName() string      { return "-" }
func (GroupUpdateRequest) TableName() string     { return "-" }
func (GroupItemAddRequest) TableName() string    { return "-" }
func (GroupItemUpdateRequest) TableName() string { return "-" }
