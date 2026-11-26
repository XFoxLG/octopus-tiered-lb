package model

import transmodel "github.com/lingyuins/octopus/internal/transformer/model"

const RelayDiagnosticsVersion = 4

// RelayDiagnostics records observations made by this gateway. It does not
// claim that a downstream browser rendered bytes accepted by the HTTP writer.
type RelayDiagnostics struct {
	NonChatUsage              *transmodel.NonChatObservation `json:"non_chat_usage,omitempty"`
	Version                   int                            `json:"version"`
	RequestID                 string                         `json:"request_id,omitempty"`
	InstanceID                string                         `json:"instance_id,omitempty"`
	ReceivedAt                int64                          `json:"received_at,omitempty"`
	DeadlineAt                int64                          `json:"deadline_at,omitempty"`
	DeadlineSource            string                         `json:"deadline_source,omitempty"`
	CancelledAt               int64                          `json:"cancelled_at,omitempty"`
	CancelReason              string                         `json:"cancel_reason,omitempty"`
	TerminalAt                int64                          `json:"terminal_at,omitempty"`
	TerminalSource            string                         `json:"terminal_source,omitempty"`
	OutboundMaxTokens         *int64                         `json:"outbound_max_tokens,omitempty"`
	OutboundMaxTokensSource   string                         `json:"outbound_max_tokens_source,omitempty"`
	OutboundMaxTokensField    string                         `json:"outbound_max_tokens_field,omitempty"`
	OutboundMaxTokensAdjusted bool                           `json:"outbound_max_tokens_adjusted,omitempty"`
	OutputTextPresent         *bool                          `json:"output_text_present,omitempty"`
	RobinEnvelopeKind         string                         `json:"robin_envelope_kind,omitempty"`
	RobinEnvelopeReason       string                         `json:"robin_envelope_reason,omitempty"`
	WriterAcceptedBytes       int64                          `json:"writer_accepted_bytes,omitempty"`
	SemanticBytes             int64                          `json:"semantic_bytes,omitempty"`
	ControlBytes              int64                          `json:"control_bytes,omitempty"`
	KeepalivePings            int64                          `json:"keepalive_pings,omitempty"`
	FirstKeepaliveAt          int64                          `json:"first_keepalive_at,omitempty"`
	LastKeepaliveAt           int64                          `json:"last_keepalive_at,omitempty"`
	LastKeepalivePhase        string                         `json:"last_keepalive_phase,omitempty"`
	ReasoningBufferStrategy   string                         `json:"reasoning_buffer_strategy,omitempty"`
	ReasoningBufferApplied    bool                           `json:"reasoning_buffer_applied,omitempty"`
	ReasoningChunksReceived   int64                          `json:"reasoning_chunks_received,omitempty"`
	ReasoningContentBytes     int64                          `json:"reasoning_content_bytes,omitempty"`
	ReasoningChunksBuffered   int64                          `json:"reasoning_chunks_buffered,omitempty"`
	ReasoningFrameBytes       int64                          `json:"reasoning_frame_bytes,omitempty"`
	ReasoningChunksFlushed    int64                          `json:"reasoning_chunks_flushed,omitempty"`
	ReasoningBufferOverflows  int64                          `json:"reasoning_buffer_overflows,omitempty"`
	ReasoningBufferDropped    int64                          `json:"reasoning_buffer_dropped_bytes,omitempty"`
	FirstReasoningAt          int64                          `json:"first_reasoning_at,omitempty"`
	LastReasoningAt           int64                          `json:"last_reasoning_at,omitempty"`
	ReasoningBufferFlushAt    int64                          `json:"reasoning_buffer_flush_at,omitempty"`
	VisibleChunks             int64                          `json:"visible_chunks,omitempty"`
	VisibleContentBytes       int64                          `json:"visible_content_bytes,omitempty"`
	ToolDeltaCount            int64                          `json:"tool_delta_count,omitempty"`
	PersistenceState          string                         `json:"persistence_state,omitempty"`
	RoutingConflicts          []string                       `json:"routing_conflicts,omitempty"`
	OutboundBudgetState       string                         `json:"outbound_budget_state,omitempty"`
	Task                      *RelayTaskDiagnostics          `json:"task,omitempty"`
	Probe                     *ChannelProbeEvidence          `json:"probe,omitempty"`
	SelectedAttempt           int                            `json:"selected_attempt,omitempty"`
	Network                   *RelayNetworkDiagnostics       `json:"network,omitempty"`
	FirstWriteAt              int64                          `json:"first_write_at,omitempty"`
	Attempts                  []RelayAttemptDiagnostics      `json:"attempts,omitempty"`
}

type RelayAttemptDiagnostics struct {
	Attempt        int                      `json:"attempt"`
	ChannelID      int                      `json:"channel_id,omitempty"`
	Protocol       string                   `json:"protocol,omitempty"`
	EndpointID     string                   `json:"endpoint_id,omitempty"`
	Model          string                   `json:"model,omitempty"`
	KeyID          int                      `json:"key_id,omitempty"`
	HedgeLane      string                   `json:"hedge_lane,omitempty"`
	Network        *RelayNetworkDiagnostics `json:"network,omitempty"`
	StartedAt      int64                    `json:"started_at,omitempty"`
	EndedAt        int64                    `json:"ended_at,omitempty"`
	DeadlineAt     int64                    `json:"deadline_at,omitempty"`
	TerminalSource string                   `json:"terminal_source,omitempty"`
	CancelReason   string                   `json:"cancel_reason,omitempty"`
	UpstreamError  *RelayUpstreamError      `json:"upstream_error,omitempty"`
	// OutboundMaxTokens is the final limit observed on the wire for this
	// attempt. Source identifies client, override, or adapter default.
	OutboundMaxTokens         *int64                  `json:"outbound_max_tokens,omitempty"`
	OutboundMaxTokensSource   string                  `json:"outbound_max_tokens_source,omitempty"`
	OutboundMaxTokensField    string                  `json:"outbound_max_tokens_field,omitempty"`
	OutboundMaxTokensAdjusted bool                    `json:"outbound_max_tokens_adjusted,omitempty"`
	OutboundBudgetState       string                  `json:"outbound_budget_state,omitempty"`
	OutputTextPresent         *bool                   `json:"output_text_present,omitempty"`
	Stream                    *RelayStreamDiagnostics `json:"stream,omitempty"`
	// RobinEnvelopeReason is a bounded classification, never tool arguments or
	// generated content.
	RobinEnvelopeKind   string                `json:"robin_envelope_kind,omitempty"`
	RobinEnvelopeReason string                `json:"robin_envelope_reason,omitempty"`
	Task                *RelayTaskDiagnostics `json:"task,omitempty"`
}

// RelayStreamDiagnostics contains bounded framing and failure evidence. It
// never stores event payloads or upstream response text.
type RelayStreamDiagnostics struct {
	FailureStage         string `json:"failure_stage,omitempty"`
	FailureKind          string `json:"failure_kind,omitempty"`
	FailureEventIndex    int64  `json:"failure_event_index,omitempty"`
	FailureEventBytes    int64  `json:"failure_event_bytes,omitempty"`
	IncompleteEventIndex int64  `json:"incomplete_event_index,omitempty"`
	IncompleteEventBytes int64  `json:"incomplete_event_bytes,omitempty"`
}

type RelayTaskDiagnostics struct {
	Transport            string `json:"transport"`
	SubmissionState      string `json:"submission_state,omitempty"`
	Status               string `json:"status,omitempty"`
	Done                 bool   `json:"done"`
	Cursor               int    `json:"cursor,omitempty"`
	PollCount            int    `json:"poll_count,omitempty"`
	QueryRetryCount      int    `json:"query_retry_count,omitempty"`
	CancelState          string `json:"cancel_state,omitempty"`
	RequestID            string `json:"request_id,omitempty"`
	CFRay                string `json:"cf_ray,omitempty"`
	RndrID               string `json:"rndr_id,omitempty"`
	StartedAt            int64  `json:"started_at,omitempty"`
	DeadlineAt           int64  `json:"deadline_at,omitempty"`
	SubmissionDispatchAt int64  `json:"submission_dispatch_at,omitempty"`
	SubmissionHeadersAt  int64  `json:"submission_headers_at,omitempty"`
	FirstChunkAt         int64  `json:"first_chunk_at,omitempty"`
	LastObservedAt       int64  `json:"last_observed_at,omitempty"`
	CancelledAt          int64  `json:"cancelled_at,omitempty"`
	TimedOut             bool   `json:"timed_out,omitempty"`
	TerminalReported     bool   `json:"terminal_reported,omitempty"`
}

// Unix milliseconds recorded at the actual network/read/parser boundaries.
// Absent phases remain unknown, including DNS/TLS on a reused connection.
type RelayNetworkDiagnostics struct {
	TimingSource           string `json:"timing_source,omitempty"`
	PreparationAt          int64  `json:"preparation_at,omitempty"`
	DispatchAt             int64  `json:"dispatch_at,omitempty"`
	DNSStartAt             int64  `json:"dns_start_at,omitempty"`
	DNSEndAt               int64  `json:"dns_end_at,omitempty"`
	DialStartAt            int64  `json:"dial_start_at,omitempty"`
	DialEndAt              int64  `json:"dial_end_at,omitempty"`
	TLSStartAt             int64  `json:"tls_start_at,omitempty"`
	TLSEndAt               int64  `json:"tls_end_at,omitempty"`
	ConnectionReused       *bool  `json:"connection_reused,omitempty"`
	HeaderFirstByteAt      int64  `json:"header_first_byte_at,omitempty"`
	ResponseHeadersAt      int64  `json:"response_headers_at,omitempty"`
	FirstBodyByteAt        int64  `json:"first_body_byte_at,omitempty"`
	FirstReasoningAt       int64  `json:"first_reasoning_at,omitempty"`
	FirstEffectiveOutputAt int64  `json:"first_effective_output_at,omitempty"`
	TerminalAt             int64  `json:"terminal_at,omitempty"`
	CancelledAt            int64  `json:"cancelled_at,omitempty"`
	RequestID              string `json:"request_id,omitempty"`
	CFRay                  string `json:"cf_ray,omitempty"`
	RndrID                 string `json:"rndr_id,omitempty"`
}

// RelayUpstreamError contains protocol evidence only, never response messages,
// request content, headers, or credentials.
type RelayUpstreamError struct {
	StatusCode int    `json:"status_code"`
	Code       string `json:"code,omitempty"`
	Type       string `json:"type,omitempty"`
	Category   string `json:"category,omitempty"`
	Source     string `json:"source"`
	RecoveryAt int64  `json:"recovery_at,omitempty"`
}
