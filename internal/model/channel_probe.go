package model

import "time"

// ProbeVerdict 是单项探测的判定结论。
//
// 只有三态 + unknown：
//   - Pass：明确证明可用（上游按预期响应）；
//   - Fail：明确证明不可用（上游明确拒绝，且不是限流/临时故障）；
//   - Unsupported：上游明确表示不支持该能力（404/405 等），属正常状态，
//     不应该当成故障去重试或熔断；
//   - Unknown：证据不足（限流、超时、5xx、网络错误）。**Unknown 绝不写入配置**，
//     否则上游一次抽风就会把可用的协议从渠道里删掉。
type ProbeVerdict string

const (
	ProbeVerdictPass        ProbeVerdict = "pass"
	ProbeVerdictFail        ProbeVerdict = "fail"
	ProbeVerdictUnsupported ProbeVerdict = "unsupported"
	ProbeVerdictUnknown     ProbeVerdict = "unknown"
)

// IsConclusive 报告该结论是否足以用来改写渠道配置。
func (v ProbeVerdict) IsConclusive() bool {
	return v == ProbeVerdictPass || v == ProbeVerdictUnsupported
}

// ProbeKind 区分一行的来源是协议层还是能力层。
type ProbeKind string

const (
	ProbeKindProtocol   ProbeKind = "protocol"
	ProbeKindCapability ProbeKind = "capability"
)

// ProbeItemProtocol 是协议层各行的标识（取值同 UpstreamProtocol）。
const (
	ProbeItemProtocolChat      = "protocol_chat"
	ProbeItemProtocolResponses = "protocol_responses"
	ProbeItemProtocolMessages  = "protocol_messages"
	ProbeItemProtocolGemini    = "protocol_gemini"
)

// ProbeItemCapability 是能力层各行的标识。
const (
	ProbeItemModels           = "models"
	ProbeItemTextGeneration   = "text_generation"
	ProbeItemToolCalling      = "tool_calling"
	ProbeItemStructuredOutput = "structured_output"
	ProbeItemWebSearch        = "web_search"
)

// CapabilityName 是渠道×模型能力表里的一项能力标识。
type CapabilityName string

const (
	CapabilityToolCalling CapabilityName = "tool_calling"
	CapabilityStructured  CapabilityName = "structured_output"
	CapabilityWebSearch   CapabilityName = "web_search"
	CapabilityStreaming   CapabilityName = "streaming"
)

// ChannelProbeRun 记录一次手动探测的元信息。
type ChannelProbeRun struct {
	ID        int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	ChannelID int       `json:"channel_id" gorm:"not null;index:idx_channel_probe_runs_channel_time,priority:1"`
	ModelName string    `json:"model_name" gorm:"not null;size:191"`
	KeyID     int       `json:"key_id" gorm:"not null;default:0"` // 探测使用的渠道 Key（审计用），0 = 未知
	StartedAt time.Time `json:"started_at" gorm:"not null"`
	EndedAt   time.Time `json:"ended_at"`
	// Summary 是一次探测的整体结论，仅供界面显示；不参与任何自动化决策。
	Summary string `json:"summary,omitempty" gorm:"size:512"`
	// Applied 记录用户是否把这次结果应用到了渠道配置（只加不减）。
	Applied   bool                 `json:"applied" gorm:"not null;default:false"`
	AppliedAt time.Time            `json:"applied_at,omitempty"`
	CreatedAt time.Time            `json:"created_at" gorm:"index:idx_channel_probe_runs_channel_time,priority:2"`
	Results   []ChannelProbeResult `json:"results,omitempty" gorm:"foreignKey:RunID"`
}

// TableName 明确表名，避免 GORM 复数化规则变化导致表名漂移。
func (ChannelProbeRun) TableName() string { return "channel_probe_runs" }

// ChannelProbeResult 是一次探测里的单行结果（一个协议或一项能力）。
type ChannelProbeResult struct {
	EndpointID string       `json:"endpoint_id,omitempty" gorm:"size:64"`
	ID         int64        `json:"id" gorm:"primaryKey;autoIncrement"`
	RunID      int64        `json:"run_id" gorm:"not null;index"`
	ChannelID  int          `json:"channel_id" gorm:"not null;index"`
	ModelName  string       `json:"model_name" gorm:"not null;size:191"`
	Kind       ProbeKind    `json:"kind" gorm:"not null;size:16"`
	Item       string       `json:"item" gorm:"not null;size:64"`
	Verdict    ProbeVerdict `json:"verdict" gorm:"not null;size:16"`
	StatusCode int          `json:"status_code,omitempty"`
	LatencyMS  int64        `json:"latency_ms,omitempty"`
	// Summary 是给界面看的短句（已脱敏）。
	Summary string `json:"summary,omitempty" gorm:"size:512"`
	// Detail 是截断后的上游回显片段，**必须**经脱敏函数处理，绝不包含密钥。
	Detail    string    `json:"detail,omitempty" gorm:"type:text"`
	CreatedAt time.Time `json:"created_at" gorm:"index"`
}

func (ChannelProbeResult) TableName() string { return "channel_probe_results" }

// ChannelModelCapability 是渠道×模型的能力结论（探测或人工确认的结果）。
//
// 相对旧的 GroupItem.SupportsTools* 三列，这张表把结论挂在"渠道×模型"上而不是
// "分组条目"上：同一个模型进多个分组时能力结论只有一份，不必重复探测，
// 也不会出现两个分组给出互相矛盾的结论。
type ChannelModelCapability struct {
	ID         int64          `json:"id" gorm:"primaryKey;autoIncrement"`
	ChannelID  int            `json:"channel_id" gorm:"not null;uniqueIndex:idx_channel_model_capability,priority:1"`
	ModelName  string         `json:"model_name" gorm:"not null;uniqueIndex:idx_channel_model_capability,priority:2;size:191"`
	Capability CapabilityName `json:"capability" gorm:"not null;uniqueIndex:idx_channel_model_capability,priority:3;size:32"`
	Supported  bool           `json:"supported" gorm:"not null"`
	// Source 记录结论来源：probe（自动探测）/ manual（人工确认）/ migrated（旧列平移）。
	Source string `json:"source" gorm:"not null;size:32;default:''"`
	// ProbeKeyID 是得出结论时使用的渠道 Key（多 Key 渠道审计用），0 = 未知。
	ProbeKeyID int       `json:"probe_key_id" gorm:"not null;default:0"`
	ProbedAt   time.Time `json:"probed_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (ChannelModelCapability) TableName() string { return "channel_model_capabilities" }

// ChannelProbeRequest 是手动探测请求。
type ChannelProbeRequest struct {
	ModelName string `json:"model_name" binding:"required"`
	// KeyIndex 指定用哪个 Key（下标）；负值 = 由后端挑一个可用 Key。
	KeyIndex int `json:"key_index"`
	// AllowSkipModelTest 显式确认"即使渠道标记了 skip_model_test 也要探测"。
	// 默认 false：禁止测活的渠道必须先经用户确认，避免误触上游封禁。
	AllowSkipModelTest bool `json:"allow_skip_model_test"`
}

// ChannelProbeApplyRequest 把某次探测的结论应用到渠道配置。
type ChannelProbeApplyRequest struct {
	RunID int64 `json:"run_id" binding:"required"`
}

// ChannelProbeApplyResult 描述应用结果，供前端回显改了哪些东西。
type ChannelProbeApplyResult struct {
	AddedProtocols []string `json:"added_protocols"`
	Capabilities   int      `json:"capabilities"`
}
