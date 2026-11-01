package model

// RelayDiagnostics records observations made by this gateway. It does not
// claim that a downstream browser rendered bytes accepted by the HTTP writer.
type RelayDiagnostics struct {
	Version                  int                       `json:"version"`
	RequestID                string                    `json:"request_id,omitempty"`
	InstanceID               string                    `json:"instance_id,omitempty"`
	ReceivedAt               int64                     `json:"received_at,omitempty"`
	DeadlineAt               int64                     `json:"deadline_at,omitempty"`
	DeadlineSource           string                    `json:"deadline_source,omitempty"`
	CancelledAt              int64                     `json:"cancelled_at,omitempty"`
	CancelReason             string                    `json:"cancel_reason,omitempty"`
	TerminalAt               int64                     `json:"terminal_at,omitempty"`
	TerminalSource           string                    `json:"terminal_source,omitempty"`
	WriterAcceptedBytes      int64                     `json:"writer_accepted_bytes,omitempty"`
	SemanticBytes            int64                     `json:"semantic_bytes,omitempty"`
	ControlBytes             int64                     `json:"control_bytes,omitempty"`
	ReasoningBufferStrategy  string                    `json:"reasoning_buffer_strategy,omitempty"`
	ReasoningBufferApplied   bool                      `json:"reasoning_buffer_applied,omitempty"`
	ReasoningChunksReceived  int64                     `json:"reasoning_chunks_received,omitempty"`
	ReasoningContentBytes    int64                     `json:"reasoning_content_bytes,omitempty"`
	ReasoningChunksBuffered  int64                     `json:"reasoning_chunks_buffered,omitempty"`
	ReasoningFrameBytes      int64                     `json:"reasoning_frame_bytes,omitempty"`
	ReasoningChunksFlushed   int64                     `json:"reasoning_chunks_flushed,omitempty"`
	ReasoningBufferOverflows int64                     `json:"reasoning_buffer_overflows,omitempty"`
	ReasoningBufferDropped   int64                     `json:"reasoning_buffer_dropped_bytes,omitempty"`
	FirstReasoningAt         int64                     `json:"first_reasoning_at,omitempty"`
	LastReasoningAt          int64                     `json:"last_reasoning_at,omitempty"`
	ReasoningBufferFlushAt   int64                     `json:"reasoning_buffer_flush_at,omitempty"`
	VisibleChunks            int64                     `json:"visible_chunks,omitempty"`
	VisibleContentBytes      int64                     `json:"visible_content_bytes,omitempty"`
	ToolDeltaCount           int64                     `json:"tool_delta_count,omitempty"`
	PersistenceState         string                    `json:"persistence_state,omitempty"`
	RoutingConflicts         []string                  `json:"routing_conflicts,omitempty"`
	Attempts                 []RelayAttemptDiagnostics `json:"attempts,omitempty"`
}

type RelayAttemptDiagnostics struct {
	Attempt        int    `json:"attempt"`
	ChannelID      int    `json:"channel_id,omitempty"`
	Protocol       string `json:"protocol,omitempty"`
	StartedAt      int64  `json:"started_at,omitempty"`
	EndedAt        int64  `json:"ended_at,omitempty"`
	DeadlineAt     int64  `json:"deadline_at,omitempty"`
	TerminalSource string `json:"terminal_source,omitempty"`
	CancelReason   string `json:"cancel_reason,omitempty"`
}
