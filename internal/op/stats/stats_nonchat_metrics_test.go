package stats

import (
	"context"
	"testing"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/store"
)

func TestStatsRedisNonChatMetricsPersistAndSum(t *testing.T) {
	newStatsTestRedis(t)
	ss := store.GetStats()
	ctx := context.Background()

	first := model.StatsMetrics{
		InputToken:  100,
		OutputToken: 40,
		NonChatMetrics: model.NonChatMetrics{
			BilledToken:              90,
			UsageReportedRequests:    1,
			UsagePartialRequests:     1,
			UsageBilledRequests:      1,
			UsageInvalidRequests:     0,
			EmbeddingInputItems:      2,
			EmbeddingInputObserved:   2,
			EmbeddingVectors:         2,
			EmbeddingVectorsObserved: 2,
			RerankDocuments:          4,
			RerankDocumentsObserved:  3,
			RerankResults:            2,
			RerankResultsObserved:    2,
		},
	}
	second := model.StatsMetrics{
		InputToken: 20,
		NonChatMetrics: model.NonChatMetrics{
			UnattributedToken:        7,
			BilledToken:              50,
			UsageBilledRequests:      1,
			UsageInvalidRequests:     1,
			EmbeddingInputItems:      3,
			EmbeddingInputObserved:   0,
			EmbeddingVectors:         1,
			EmbeddingVectorsObserved: 1,
			RerankDocuments:          0,
			RerankDocumentsObserved:  1,
			RerankResults:            1,
			RerankResultsObserved:    1,
		},
	}
	for _, metrics := range []model.StatsMetrics{first, second} {
		if err := ss.IncrMetrics(ctx, statsScopeAPIKey, "9", metrics); err != nil {
			t.Fatalf("IncrMetrics: %v", err)
		}
	}

	got, err := ss.GetMetrics(ctx, statsScopeAPIKey, "9")
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if got.TotalTokens() != 167 {
		t.Fatalf("TotalTokens = %d, want 167", got.TotalTokens())
	}
	if got.BilledToken != 140 {
		t.Fatalf("BilledToken = %d, want 140", got.BilledToken)
	}
	if got.UnattributedToken != 7 ||
		got.UsageReportedRequests != 1 ||
		got.UsagePartialRequests != 1 ||
		got.UsageBilledRequests != 2 ||
		got.UsageInvalidRequests != 1 ||
		got.EmbeddingInputItems != 5 ||
		got.EmbeddingInputObserved != 2 ||
		got.EmbeddingVectors != 3 ||
		got.EmbeddingVectorsObserved != 3 ||
		got.RerankDocuments != 4 ||
		got.RerankDocumentsObserved != 4 ||
		got.RerankResults != 3 ||
		got.RerankResultsObserved != 3 {
		t.Fatalf("unexpected Redis evidence: %+v", got.NonChatMetrics)
	}
}
