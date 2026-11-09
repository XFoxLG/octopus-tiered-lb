package model

import "testing"

func TestNonChatMetricsAddSumsBilledClassification(t *testing.T) {
	first := NonChatMetrics{
		UnattributedToken:        3,
		BilledToken:              90,
		UsageReportedRequests:    1,
		UsageBilledRequests:      1,
		EmbeddingInputItems:      2,
		EmbeddingInputObserved:   2,
		EmbeddingVectors:         2,
		EmbeddingVectorsObserved: 2,
		RerankDocuments:          4,
		RerankResults:            3,
	}
	second := NonChatMetrics{
		BilledToken:              50,
		UsagePartialRequests:     1,
		UsageBilledRequests:      1,
		EmbeddingInputItems:      5,
		EmbeddingInputObserved:   4,
		EmbeddingVectors:         3,
		EmbeddingVectorsObserved: 3,
		RerankDocumentsObserved:  7,
		RerankResultsObserved:    3,
	}

	first.Add(second)
	if first.BilledToken != 140 {
		t.Fatalf("BilledToken = %d, want 140", first.BilledToken)
	}
	if first.UsageReportedRequests != 1 ||
		first.UsagePartialRequests != 1 ||
		first.UsageBilledRequests != 2 ||
		first.EmbeddingInputItems != 7 ||
		first.EmbeddingInputObserved != 6 ||
		first.EmbeddingVectors != 5 ||
		first.EmbeddingVectorsObserved != 5 ||
		first.RerankDocuments != 4 ||
		first.RerankDocumentsObserved != 7 ||
		first.RerankResults != 3 ||
		first.RerankResultsObserved != 3 {
		t.Fatalf("unexpected aggregated evidence: %+v", first)
	}
}

func TestStatsMetricsNonChatEvidenceAndTotalTokens(t *testing.T) {
	stats := StatsMetrics{
		InputToken:  100,
		OutputToken: 70,
		NonChatMetrics: NonChatMetrics{
			UnattributedToken: 12,
			BilledToken:       90,
		},
	}
	if got := stats.TotalTokens(); got != 182 {
		t.Fatalf("TotalTokens = %d, want 182", got)
	}

	stats.Add(StatsMetrics{
		InputToken: 10,
		NonChatMetrics: NonChatMetrics{
			UnattributedToken:    4,
			BilledToken:          50,
			UsageInvalidRequests: 1,
		},
	})
	if stats.InputToken != 110 || stats.UnattributedToken != 16 || stats.BilledToken != 140 || stats.UsageInvalidRequests != 1 {
		t.Fatalf("unexpected stats after add: %+v", stats)
	}
	if stats.IsZero() {
		t.Fatal("non-zero non-chat evidence was treated as zero")
	}

	explicitZero := StatsMetrics{NonChatMetrics: NonChatMetrics{UsageInvalidRequests: 1}}
	if explicitZero.IsZero() {
		t.Fatal("explicit non-chat coverage must not be treated as zero")
	}
}

func TestRelayLogCopiesNonChatMetricsToListItem(t *testing.T) {
	log := RelayLog{
		InputTokens:  100,
		OutputTokens: 70,
		NonChatMetrics: NonChatMetrics{
			UnattributedToken: 12,
			BilledToken:       90,
		},
	}
	item := log.ToListItem()
	if item.NonChatMetrics != log.NonChatMetrics {
		t.Fatalf("list item evidence = %+v, want %+v", item.NonChatMetrics, log.NonChatMetrics)
	}
	if got := log.TotalTokens(); got != 182 {
		t.Fatalf("RelayLog.TotalTokens = %d, want 182", got)
	}
}
