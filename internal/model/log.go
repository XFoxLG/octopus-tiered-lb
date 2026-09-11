package model

// AttemptStatus 尝试状态
type AttemptStatus string

const (
	AttemptSuccess      AttemptStatus = "success"       // 转发成功
	AttemptFailed       AttemptStatus = "failed"        // 转发失败
	AttemptCircuitBreak AttemptStatus = "circuit_break" // 熔断跳过
	AttemptSkipped      AttemptStatus = "skipped"       // 其他原因跳过（禁用、无Key、类型不兼容等）
)

// ChannelAttempt 记录单次渠道尝试的决策和结果
type ChannelAttempt struct {
	ChannelID        int           `json:"channel_id"`
	ChannelKeyID     int           `json:"channel_key_id,omitempty"`
	ChannelName      string        `json:"channel_name"`
	ModelName        string        `json:"model_name"`
	AdapterType      string        `json:"adapter_type,omitempty"` // 适配器类型: response, chat, anthropic, gemini 等
	AttemptNum       int           `json:"attempt_num"`
	Status           AttemptStatus `json:"status"`
	HTTPStatus       int           `json:"http_status,omitempty"`
	RequestPrepared  bool          `json:"request_prepared,omitempty"`
	SendStarted      bool          `json:"send_started,omitempty"`
	RequestBytes     int64         `json:"request_bytes,omitempty"`
	RequestComplete  bool          `json:"request_complete,omitempty"`
	ResponseReceived bool          `json:"response_received,omitempty"`
	ResponseBytes    int64         `json:"response_bytes,omitempty"`
	ResponseComplete bool          `json:"response_complete,omitempty"`
	Duration         int           `json:"duration"`
	Sticky           bool          `json:"sticky,omitempty"`
	Msg              string        `json:"msg,omitempty"`
}

type RelayLog struct {
	ID                int64  `json:"id" gorm:"primaryKey;autoIncrement:false"`                                                                                                   // Snowflake ID
	TraceID           string `json:"trace_id,omitempty" gorm:"column:trace_id;size:36;index:idx_relay_logs_trace_id"`                                                            // 请求级追踪 ID
	Time              int64  `json:"time" gorm:"column:time;index:idx_relay_logs_time;index:idx_relay_logs_channel_time,priority:2;index:idx_relay_logs_apikey_time,priority:2"` // 时间戳（秒）
	RequestModelName  string `json:"request_model_name" gorm:"column:request_model_name"`                                                                                        // 请求模型名称
	RequestAPIKeyID   int    `json:"request_api_key_id" gorm:"column:request_api_key_id;index:idx_relay_logs_apikey_time,priority:1"`                                            // 请求使用的 API Key ID
	RequestAPIKeyName string `json:"request_api_key_name" gorm:"column:request_api_key_name"`                                                                                    // 请求使用的 API Key 名称
	ClientIP          string `json:"client_ip" gorm:"column:client_ip"`                                                                                                          // 客户端 IP
	// 展示轨来源 IP：按转发头解析（CF-Connecting-IP 优先、XFF 右起首个公网回退），
	// 仅用于日志展示；client_ip 保持 Gin 安全解析语义（限流/白名单）。见迁移 061。
	ReportedClientIP        string             `json:"reported_client_ip,omitempty" gorm:"column:reported_client_ip"`
	ReportedClientIPSource  string             `json:"reported_client_ip_source,omitempty" gorm:"column:reported_client_ip_source;size:24"`
	UserAgent         string `json:"user_agent,omitempty" gorm:"column:user_agent"`                                                                                              // 客户端自报 User-Agent（详情页展示来源）
	EndpointType      string `json:"endpoint_type" gorm:"column:endpoint_type"`                                                                                                  // 命中的端点分类
	ChannelId         int    `json:"channel" gorm:"column:channel_id;index:idx_relay_logs_channel_time,priority:1"`                                                              // 实际使用的渠道ID
	ChannelName       string `json:"channel_name" gorm:"column:channel_name"`                                                                                                    // 渠道名称
	ActualModelName   string `json:"actual_model_name" gorm:"column:actual_model_name"`                                                                                          // 实际使用模型名称
	InputTokens       int    `json:"input_tokens" gorm:"column:input_tokens"`                                                                                                    // 输入Token
	OutputTokens      int    `json:"output_tokens" gorm:"column:output_tokens"`                                                                                                  // 输出 Token
	SemanticCacheHit  bool   `json:"semantic_cache_hit" gorm:"column:semantic_cache_hit"`                                                                                        // 语义缓存命中（写入时落库，避免列表查询重解析大字段）
	CacheReadTokens   int    `json:"cache_read_tokens" gorm:"column:cache_read_tokens"`                                                                                          // 提供方提示缓存命中 Token（写入时落库）
	ReasoningEffort   string `json:"reasoning_effort" gorm:"column:reasoning_effort"`                                                                                            // 出站最终思考强度（effective）
	ReasoningTokens   int    `json:"reasoning_tokens" gorm:"column:reasoning_tokens"`                                                                                            // 上游返回的思考 Token（usage，确定性）
	ReasoningChars    int    `json:"reasoning_chars" gorm:"column:reasoning_chars"`                                                                                              // 思考文本字符数（无官方 token 时的估算回退，UTF-8 rune 数）

	Ftut                      int                       `json:"ftut" gorm:"column:ftut"`                         // 首字时间(毫秒)
	UseTime                   int                       `json:"use_time" gorm:"column:use_time"`                 // 总用时(毫秒)
	Cost                      float64                   `json:"cost" gorm:"column:cost"`                         // 消耗费用
	BillingWindow             string                    `json:"billing_window" gorm:"column:billing_window"`     // 计费窗口（DeepSeek 峰谷: peak/offpeak，其余为空）
	RequestContent            string                    `json:"request_content" gorm:"column:request_content"`   // 兼容旧版详情的规范化请求内容
	ResponseContent           string                    `json:"response_content" gorm:"column:response_content"` // 兼容旧版详情的规范化响应内容
	HTTPStatus                int                       `json:"http_status,omitempty" gorm:"column:http_status"` // Octopus 向客户端写出的 HTTP 状态
	GenerationOutcome         string                    `json:"generation_outcome,omitempty" gorm:"column:generation_outcome;size:24"`
	UpstreamOutcome           string                    `json:"upstream_outcome,omitempty" gorm:"column:upstream_outcome;size:24"`
	PersistenceState          string                    `json:"persistence_state,omitempty" gorm:"column:persistence_state;size:24"`
	ClientDeliveryState       string                    `json:"client_delivery_state,omitempty" gorm:"column:client_delivery_state;size:24"`
	ClientWriteError          string                    `json:"client_write_error,omitempty" gorm:"column:client_write_error"`
	TerminationCause          string                    `json:"termination_cause,omitempty" gorm:"column:termination_cause;size:32"`
	ProviderTerminationReason string                    `json:"provider_termination_reason,omitempty" gorm:"column:provider_termination_reason"`
	ClientWriteBytes          int64                     `json:"client_write_bytes,omitempty" gorm:"column:client_write_bytes"` // 服务端 writer 实际接受的字节数
	ClientWriteComplete       bool                      `json:"client_write_complete" gorm:"column:client_write_complete"`     // handler 正常返回且 writer 未报告错误
	ContentState              string                    `json:"content_state,omitempty" gorm:"column:content_state;size:24"`   // pending/ready/expired/unavailable/disabled
	ContentUnavailableError   string                    `json:"content_unavailable_error,omitempty" gorm:"column:content_unavailable_error"`
	Contents                  []RelayLogCapturedContent `json:"contents,omitempty" gorm:"-"`                     // 四边界正文/附件，详情按需水合
	Error                     string                    `json:"error" gorm:"column:error"`                       // 错误信息
	Attempts                  []ChannelAttempt          `json:"attempts" gorm:"column:attempts;serializer:json"` // 所有尝试记录
	TotalAttempts             int                       `json:"total_attempts" gorm:"column:total_attempts"`     // 总尝试次数
	IsTest                    bool                      `json:"is_test" gorm:"column:is_test;default:false"`     // 是否为测试请求日志（issue #82）
	QueueSequence             uint64                    `json:"-" gorm:"-"`                                      // 仅用于维护屏障区分恢复前后的待写记录
}

// RelayLogListItem 日志列表轻量条目，排除了 RequestContent 和 ResponseContent 大字段
type RelayLogListItem struct {
	ID                        int64            `json:"id" gorm:"column:id"`
	TraceID                   string           `json:"trace_id,omitempty" gorm:"column:trace_id"`
	Time                      int64            `json:"time" gorm:"column:time;index:idx_relay_logs_time"`
	RequestModelName          string           `json:"request_model_name" gorm:"column:request_model_name"`
	RequestAPIKeyID           int              `json:"request_api_key_id" gorm:"column:request_api_key_id"`
	RequestAPIKeyName         string           `json:"request_api_key_name" gorm:"column:request_api_key_name"`
	ClientIP                  string           `json:"client_ip" gorm:"column:client_ip"`
	ReportedClientIP          string           `json:"reported_client_ip,omitempty" gorm:"column:reported_client_ip"`
	ReportedClientIPSource    string           `json:"reported_client_ip_source,omitempty" gorm:"column:reported_client_ip_source;size:24"`
	EndpointType              string           `json:"endpoint_type" gorm:"column:endpoint_type"`
	ChannelId                 int              `json:"channel" gorm:"column:channel_id"`
	ChannelName               string           `json:"channel_name" gorm:"column:channel_name"`
	ActualModelName           string           `json:"actual_model_name" gorm:"column:actual_model_name"`
	InputTokens               int              `json:"input_tokens" gorm:"column:input_tokens"`
	OutputTokens              int              `json:"output_tokens" gorm:"column:output_tokens"`
	SemanticCacheHit          bool             `json:"semantic_cache_hit" gorm:"column:semantic_cache_hit"`
	CacheReadTokens           int              `json:"cache_read_tokens" gorm:"column:cache_read_tokens"`
	ReasoningEffort           string           `json:"reasoning_effort" gorm:"column:reasoning_effort"`
	ReasoningTokens           int              `json:"reasoning_tokens" gorm:"column:reasoning_tokens"`
	ReasoningChars            int              `json:"reasoning_chars" gorm:"column:reasoning_chars"`
	Ftut                      int              `json:"ftut" gorm:"column:ftut"`
	UseTime                   int              `json:"use_time" gorm:"column:use_time"`
	Cost                      float64          `json:"cost" gorm:"column:cost"`
	BillingWindow             string           `json:"billing_window" gorm:"column:billing_window"`
	HTTPStatus                int              `json:"http_status,omitempty" gorm:"column:http_status"`
	GenerationOutcome         string           `json:"generation_outcome,omitempty" gorm:"column:generation_outcome"`
	UpstreamOutcome           string           `json:"upstream_outcome,omitempty" gorm:"column:upstream_outcome"`
	PersistenceState          string           `json:"persistence_state,omitempty" gorm:"column:persistence_state"`
	ClientDeliveryState       string           `json:"client_delivery_state,omitempty" gorm:"column:client_delivery_state"`
	TerminationCause          string           `json:"termination_cause,omitempty" gorm:"column:termination_cause"`
	ProviderTerminationReason string           `json:"provider_termination_reason,omitempty" gorm:"column:provider_termination_reason"`
	ClientWriteBytes          int64            `json:"client_write_bytes,omitempty" gorm:"column:client_write_bytes"`
	ClientWriteComplete       bool             `json:"client_write_complete" gorm:"column:client_write_complete"`
	ContentState              string           `json:"content_state,omitempty" gorm:"column:content_state"`
	Error                     string           `json:"error" gorm:"column:error"`
	Attempts                  []ChannelAttempt `json:"attempts" gorm:"column:attempts;serializer:json"`
	TotalAttempts             int              `json:"total_attempts" gorm:"column:total_attempts"`
	IsTest                    bool             `json:"is_test" gorm:"column:is_test;default:false"` // 是否为测试请求日志（issue #82）
}

// RelayLogAttempt 是 relay_log_attempts 关联表的一行，把单次渠道尝试从 RelayLog.Attempts
// JSON 数组中抽出为可索引行。这样"渠道A 失败 → 重试到渠道B 成功"的请求中，渠道A 的失败
// 才能被按 channel_id 过滤/聚合（issue #67）。Time 取自所属 RelayLog 的完成时间，
// 用于窗口聚合。仅对修复部署后的新请求生效，不回填历史日志。
type RelayLogAttempt struct {
	ID               int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	RelayLogID       int64  `json:"relay_log_id" gorm:"column:relay_log_id;index:idx_rla_log;uniqueIndex:uidx_rla_log_attempt,priority:1"`
	AttemptNum       int    `json:"attempt_num" gorm:"column:attempt_num;uniqueIndex:uidx_rla_log_attempt,priority:2"`
	ChannelID        int    `json:"channel_id" gorm:"column:channel_id;index:idx_rla_channel;index:idx_rla_chan_model_time,priority:1"`
	ChannelKeyID     int    `json:"channel_key_id,omitempty" gorm:"column:channel_key_id"`
	ChannelName      string `json:"channel_name" gorm:"column:channel_name"`
	ModelName        string `json:"model_name" gorm:"column:model_name;index:idx_rla_chan_model_time,priority:2;size:191"`
	AdapterType      string `json:"adapter_type,omitempty" gorm:"column:adapter_type;size:32"`
	Status           string `json:"status" gorm:"column:status"` // success | failed | circuit_break | skipped
	HTTPStatus       int    `json:"http_status,omitempty" gorm:"column:http_status"`
	RequestPrepared  bool   `json:"request_prepared,omitempty" gorm:"column:request_prepared"`
	SendStarted      bool   `json:"send_started,omitempty" gorm:"column:send_started"`
	RequestBytes     int64  `json:"request_bytes,omitempty" gorm:"column:request_bytes"`
	RequestComplete  bool   `json:"request_complete,omitempty" gorm:"column:request_complete"`
	ResponseReceived bool   `json:"response_received,omitempty" gorm:"column:response_received"`
	ResponseBytes    int64  `json:"response_bytes,omitempty" gorm:"column:response_bytes"`
	ResponseComplete bool   `json:"response_complete,omitempty" gorm:"column:response_complete"`
	Duration         int    `json:"duration" gorm:"column:duration"`
	Sticky           bool   `json:"sticky,omitempty" gorm:"column:sticky"`
	Msg              string `json:"msg,omitempty" gorm:"column:msg"`
	Time             int64  `json:"time" gorm:"column:time;index:idx_rla_time;index:idx_rla_chan_model_time,priority:3"`
}

func (RelayLogAttempt) TableName() string { return "relay_log_attempts" }

// TableName explicitly returns "-" for DTO structs to prevent GORM auto-mapping.
func (ChannelAttempt) TableName() string { return "-" }

// TableName 指定 RelayLogListItem 使用与 RelayLog 相同的数据库表
func (RelayLogListItem) TableName() string { return "relay_logs" }

// ToListItem 将完整的 RelayLog 转换为轻量的列表条目
func (r *RelayLog) ToListItem() RelayLogListItem {
	return RelayLogListItem{
		ID:                        r.ID,
		TraceID:                   r.TraceID,
		Time:                      r.Time,
		RequestModelName:          r.RequestModelName,
		RequestAPIKeyID:           r.RequestAPIKeyID,
		RequestAPIKeyName:         r.RequestAPIKeyName,
		ClientIP:                  r.ClientIP,
		ReportedClientIP:          r.ReportedClientIP,
		ReportedClientIPSource:    r.ReportedClientIPSource,
		EndpointType:              r.EndpointType,
		ChannelId:                 r.ChannelId,
		ChannelName:               r.ChannelName,
		ActualModelName:           r.ActualModelName,
		InputTokens:               r.InputTokens,
		OutputTokens:              r.OutputTokens,
		SemanticCacheHit:          r.SemanticCacheHit,
		CacheReadTokens:           r.CacheReadTokens,
		ReasoningEffort:           r.ReasoningEffort,
		ReasoningTokens:           r.ReasoningTokens,
		ReasoningChars:            r.ReasoningChars,
		Ftut:                      r.Ftut,
		UseTime:                   r.UseTime,
		Cost:                      r.Cost,
		BillingWindow:             r.BillingWindow,
		HTTPStatus:                r.HTTPStatus,
		GenerationOutcome:         r.GenerationOutcome,
		UpstreamOutcome:           r.UpstreamOutcome,
		PersistenceState:          r.PersistenceState,
		ClientDeliveryState:       r.ClientDeliveryState,
		TerminationCause:          r.TerminationCause,
		ProviderTerminationReason: r.ProviderTerminationReason,
		ClientWriteBytes:          r.ClientWriteBytes,
		ClientWriteComplete:       r.ClientWriteComplete,
		ContentState:              r.ContentState,
		Error:                     r.Error,
		Attempts:                  r.Attempts,
		TotalAttempts:             r.TotalAttempts,
		IsTest:                    r.IsTest,
	}
}
