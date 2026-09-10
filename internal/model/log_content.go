package model

const (
	RelayLogContentStatePending     = "pending"
	RelayLogContentStateReady       = "ready"
	RelayLogContentStateExpired     = "expired"
	RelayLogContentStateUnavailable = "unavailable"
	RelayLogContentStateDisabled    = "disabled"
)

const (
	RelayLogGenerationSuccess    = "success"
	RelayLogGenerationFailed     = "failed"
	RelayLogGenerationNotStarted = "not_started"
	RelayLogGenerationCacheHit   = "cache_hit"
	RelayLogGenerationReplay     = "session_replay"
	RelayLogGenerationUnknown    = "unknown"
)

const (
	RelayLogUpstreamSuccess = "success"
	RelayLogUpstreamFailed  = "failed"
	RelayLogUpstreamNone    = "none"
	RelayLogUpstreamUnknown = "unknown"
)

const (
	RelayLogPersistencePending     = "pending"
	RelayLogPersistenceReady       = "ready"
	RelayLogPersistenceUnavailable = "unavailable"
)

const (
	RelayLogClientDeliveryAccepted   = "writer_accepted"
	RelayLogClientDeliveryPartial    = "partial"
	RelayLogClientDeliveryNotStarted = "not_started"
	RelayLogClientDeliveryUnknown    = "unknown"
)

const (
	RelayLogBoundaryClientIngress    = "client_ingress"
	RelayLogBoundaryUpstreamRequest  = "upstream_request"
	RelayLogBoundaryUpstreamResponse = "upstream_response"
	RelayLogBoundaryClientEgress     = "client_egress"
)

// RelayLogContentBlob stores one deduplicated request, response, or attachment
// payload. Payload is encoded according to Encoding and addressed by the
// SHA-256 digest of the original bytes.
type RelayLogContentBlob struct {
	Digest       string `json:"digest" gorm:"column:digest;primaryKey;size:64"`
	Encoding     string `json:"encoding" gorm:"column:encoding;size:16"`
	OriginalSize int64  `json:"original_size" gorm:"column:original_size"`
	StoredSize   int64  `json:"stored_size" gorm:"column:stored_size"`
	Payload      []byte `json:"-" gorm:"column:payload"`
	CreatedAt    int64  `json:"created_at" gorm:"column:created_at;index:idx_rlcb_created_at"`
}

func (RelayLogContentBlob) TableName() string { return "relay_log_content_blobs" }

// RelayLogContentRef associates a relay log boundary slot with a content blob.
// Unavailable slots intentionally have an empty BlobDigest so the UI can tell
// the difference between "not captured" and "captured but unavailable".
type RelayLogContentRef struct {
	ID            int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	RelayLogID    int64  `json:"relay_log_id" gorm:"column:relay_log_id;index:idx_rlcr_log_order,priority:1;uniqueIndex:uidx_rlcr_slot,priority:1"`
	AttemptNum    int    `json:"attempt_num" gorm:"column:attempt_num;index:idx_rlcr_log_order,priority:2;uniqueIndex:uidx_rlcr_slot,priority:2"`
	Boundary      string `json:"boundary" gorm:"column:boundary;size:32;index:idx_rlcr_log_order,priority:3;uniqueIndex:uidx_rlcr_slot,priority:3"`
	Slot          int    `json:"slot" gorm:"column:slot;index:idx_rlcr_log_order,priority:4;uniqueIndex:uidx_rlcr_slot,priority:4"`
	Kind          string `json:"kind" gorm:"column:kind;size:24;uniqueIndex:uidx_rlcr_slot,priority:5"`
	Protocol      string `json:"protocol,omitempty" gorm:"column:protocol;size:32"`
	FieldName     string `json:"field_name,omitempty" gorm:"column:field_name"`
	FileName      string `json:"file_name,omitempty" gorm:"column:file_name"`
	ContentType   string `json:"content_type,omitempty" gorm:"column:content_type"`
	HTTPStatus    int    `json:"http_status,omitempty" gorm:"column:http_status"`
	State         string `json:"state" gorm:"column:state;size:24"`
	Complete      bool   `json:"complete" gorm:"column:complete"`
	CapturedBytes int64  `json:"captured_bytes" gorm:"column:captured_bytes"`
	BlobDigest    string `json:"digest,omitempty" gorm:"column:blob_digest;size:64;index:idx_rlcr_blob"`
	Error         string `json:"error,omitempty" gorm:"column:error"`
	CreatedAt     int64  `json:"created_at" gorm:"column:created_at;index:idx_rlcr_created_at"`
}

func (RelayLogContentRef) TableName() string { return "relay_log_content_refs" }

// RelayLogCapturedContent is an in-memory boundary payload awaiting log flush.
// Data is deliberately excluded from JSON and GORM; detail responses expose
// textual content through Text and binary content through the authenticated
// content endpoint referenced by RelayLogContentRef.ID.
type RelayLogCapturedContent struct {
	RelayLogContentRef
	Data []byte `json:"-" gorm:"-"`
	Text string `json:"text,omitempty" gorm:"-"`
}

func (RelayLogCapturedContent) TableName() string { return "-" }

// RelayLogHealth exposes observable gaps without putting request or response
// payloads into monitoring responses.
type RelayLogHealth struct {
	PendingRecords            int    `json:"pending_records"`
	PendingContentBytes       int64  `json:"pending_content_bytes"`
	OldestPendingAgeSeconds   int64  `json:"oldest_pending_age_seconds"`
	DroppedRecords            int64  `json:"dropped_records"`
	DroppedNotifications      int64  `json:"dropped_notifications"`
	UnavailableContentBundles int64  `json:"unavailable_content_bundles"`
	PersistenceFailures       int64  `json:"persistence_failures"`
	LastPersistenceFailureAt  int64  `json:"last_persistence_failure_at,omitempty"`
	LastPersistenceError      string `json:"last_persistence_error,omitempty"`
	ContentLogicalBytes       int64  `json:"content_logical_bytes"`
	ContentPhysicalBytes      int64  `json:"content_physical_bytes,omitempty"`
	ContentPhysicalAvailable  bool   `json:"content_physical_available"`
	ContentBudgetBytes        int64  `json:"content_budget_bytes"`
}
