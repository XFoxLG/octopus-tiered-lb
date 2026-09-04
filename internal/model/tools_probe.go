package model

// ToolsProbeState 渠道×模型 tools 探测状态（Seller 移植：判别矩阵输出）。
type ToolsProbeState string

const (
	// ToolsProbeStateAccepted auto/降级 2xx：协议层接受 tools 参数（弱证据）。
	ToolsProbeStateAccepted ToolsProbeState = "accepted"
	// ToolsProbeStateExecuted required 逼出 tool_call：执行确认（强证据）。
	ToolsProbeStateExecuted ToolsProbeState = "executed"
	// ToolsProbeStateRequiredUnsupported required 4xx → auto 2xx：支持 tools 但 required 不可用。
	ToolsProbeStateRequiredUnsupported ToolsProbeState = "required_unsupported"
	// ToolsProbeStateRequiredIgnored required 2xx 无 tool_call：模型不服从 required 或网关静默剥参。
	ToolsProbeStateRequiredIgnored ToolsProbeState = "required_ignored"
	// ToolsProbeStateUnsupported 白名单 ≥2 确认：不支持 tools。
	ToolsProbeStateUnsupported ToolsProbeState = "unsupported"
	// ToolsProbeStatePending 白名单第 1 次命中：待确认。
	ToolsProbeStatePending ToolsProbeState = "pending"
	// ToolsProbeStateUnknown 非白名单错误 / 5xx / 超时：不判定。
	ToolsProbeStateUnknown ToolsProbeState = "unknown"
)

// ToolsProbeResult 探测结果（判别矩阵输出）。error 表示完全无法探测，不写列。
type ToolsProbeResult struct {
	State    ToolsProbeState
	Supports bool   // accepted/executed/required_unsupported 时 true；unsupported 时 false
	Source   string // probe / manual / manual-required-fallback
}
