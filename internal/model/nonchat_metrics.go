package model

// NonChatMetrics stores evidence for requests that do not always provide
// prompt/completion token totals. BilledToken accumulates as classification
// evidence across requests; total-token consumers must not add it again on top
// of the legacy token totals.
type NonChatMetrics struct {
	UnattributedToken int64 `json:"unattributed_token" gorm:"bigint;not null;default:0"`
	BilledToken       int64 `json:"billed_token" gorm:"bigint;not null;default:0"`

	UsageReportedRequests int64 `json:"usage_reported_requests" gorm:"bigint;not null;default:0"`
	UsagePartialRequests  int64 `json:"usage_partial_requests" gorm:"bigint;not null;default:0"`
	UsageBilledRequests   int64 `json:"usage_billed_requests" gorm:"bigint;not null;default:0"`
	UsageInvalidRequests  int64 `json:"usage_invalid_requests" gorm:"bigint;not null;default:0"`

	EmbeddingInputItems      int64 `json:"embedding_input_items" gorm:"bigint;not null;default:0"`
	EmbeddingInputObserved   int64 `json:"embedding_input_observed" gorm:"bigint;not null;default:0"`
	EmbeddingVectors         int64 `json:"embedding_vectors" gorm:"bigint;not null;default:0"`
	EmbeddingVectorsObserved int64 `json:"embedding_vectors_observed" gorm:"bigint;not null;default:0"`

	RerankDocuments         int64 `json:"rerank_documents" gorm:"bigint;not null;default:0"`
	RerankDocumentsObserved int64 `json:"rerank_documents_observed" gorm:"bigint;not null;default:0"`
	RerankResults           int64 `json:"rerank_results" gorm:"bigint;not null;default:0"`
	RerankResultsObserved   int64 `json:"rerank_results_observed" gorm:"bigint;not null;default:0"`
}

// Add aggregates evidence, including the separately classified billed subset.
func (m *NonChatMetrics) Add(delta NonChatMetrics) {
	m.UnattributedToken += delta.UnattributedToken
	m.BilledToken += delta.BilledToken
	m.UsageReportedRequests += delta.UsageReportedRequests
	m.UsagePartialRequests += delta.UsagePartialRequests
	m.UsageBilledRequests += delta.UsageBilledRequests
	m.UsageInvalidRequests += delta.UsageInvalidRequests
	m.EmbeddingInputItems += delta.EmbeddingInputItems
	m.EmbeddingInputObserved += delta.EmbeddingInputObserved
	m.EmbeddingVectors += delta.EmbeddingVectors
	m.EmbeddingVectorsObserved += delta.EmbeddingVectorsObserved
	m.RerankDocuments += delta.RerankDocuments
	m.RerankDocumentsObserved += delta.RerankDocumentsObserved
	m.RerankResults += delta.RerankResults
	m.RerankResultsObserved += delta.RerankResultsObserved
}

func (m NonChatMetrics) IsZero() bool {
	return m.UnattributedToken == 0 &&
		m.BilledToken == 0 &&
		m.UsageReportedRequests == 0 &&
		m.UsagePartialRequests == 0 &&
		m.UsageBilledRequests == 0 &&
		m.UsageInvalidRequests == 0 &&
		m.EmbeddingInputItems == 0 &&
		m.EmbeddingInputObserved == 0 &&
		m.EmbeddingVectors == 0 &&
		m.EmbeddingVectorsObserved == 0 &&
		m.RerankDocuments == 0 &&
		m.RerankDocumentsObserved == 0 &&
		m.RerankResults == 0 &&
		m.RerankResultsObserved == 0
}
