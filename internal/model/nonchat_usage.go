package model

import transmodel "github.com/lingyuins/octopus/internal/transformer/model"

func NonChatAccounting(summary transmodel.NonChatUsageSummary) StatsMetrics {
	var m StatsMetrics
	if summary.InputTokens != nil {
		m.InputToken = *summary.InputTokens
	}
	if summary.OutputTokens != nil {
		m.OutputToken = *summary.OutputTokens
	}
	if summary.TotalTokens != nil {
		m.UnattributedToken = *summary.TotalTokens - m.InputToken - m.OutputToken
		m.UsageReportedRequests = 1
		if summary.Basis == "billed" {
			m.BilledToken = *summary.TotalTokens
			m.UsageBilledRequests = 1
		}
	}
	if summary.Completeness == "partial" {
		m.UsagePartialRequests = 1
	}
	if summary.Completeness == "invalid" {
		m.UsageInvalidRequests = 1
	}
	c := summary.Counts
	if c.EmbeddingInputItems != nil {
		m.EmbeddingInputItems = *c.EmbeddingInputItems
		m.EmbeddingInputObserved = 1
	}
	if c.EmbeddingVectors != nil {
		m.EmbeddingVectors = *c.EmbeddingVectors
		m.EmbeddingVectorsObserved = 1
	}
	if c.RerankDocuments != nil {
		m.RerankDocuments = *c.RerankDocuments
		m.RerankDocumentsObserved = 1
	}
	if c.RerankResults != nil {
		m.RerankResults = *c.RerankResults
		m.RerankResultsObserved = 1
	}
	return m
}

func (r *RelayLog) SetNonChatObservation(o *transmodel.NonChatObservation, request transmodel.NonChatCounts) {
	s := o.Summary
	s.Counts.EmbeddingInputItems = request.EmbeddingInputItems
	s.Counts.RerankDocuments = request.RerankDocuments
	s.Counts.RerankTopN = request.RerankTopN
	r.NonChatUsage = &s
	m := NonChatAccounting(s)
	r.InputTokens = m.InputToken
	r.OutputTokens = m.OutputToken
	r.NonChatMetrics = m.NonChatMetrics
	if len(o.Issues) > 0 {
		r.UsageInvalidRequests = 1
	}
	r.UsageState = RelayLogUsageNotReported
	if s.TotalTokens != nil {
		r.UsageState = RelayLogUsageReported
		if s.InputTokens != nil && s.OutputTokens != nil {
			r.UsageState = RelayLogUsageReportedFull
		} else if s.InputTokens != nil {
			r.UsageState = RelayLogUsageReportedInputOnly
		}
	}
	if r.Diagnostics == nil {
		r.Diagnostics = &RelayDiagnostics{Version: 1}
	}
	r.Diagnostics.NonChatUsage = o
}
