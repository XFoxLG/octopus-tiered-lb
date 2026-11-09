package store

import (
	"context"
	"github.com/lingyuins/octopus/internal/model"
	"testing"
)

func TestRedisNonChatMetricsRoundTrip(t *testing.T) {
	c, _ := newTestRedis(t)
	s := newRedisStats(c)
	ctx := context.Background()
	delta := model.StatsMetrics{InputToken: 451, NonChatMetrics: model.NonChatMetrics{UnattributedToken: 1000, BilledToken: 90, UsageReportedRequests: 1, UsagePartialRequests: 2, UsageBilledRequests: 3, UsageInvalidRequests: 4, EmbeddingInputItems: 8, EmbeddingInputObserved: 5, EmbeddingVectors: 8, EmbeddingVectorsObserved: 6, RerankDocuments: 30, RerankDocumentsObserved: 7, RerankResults: 10, RerankResultsObserved: 8}}
	var want model.StatsMetrics
	for n := 0; n < 2; n++ {
		if err := s.IncrMetrics(ctx, "total", "1", delta); err != nil {
			t.Fatal(err)
		}
		want.Add(delta)
	}
	got, err := s.GetMetrics(ctx, "total", "1")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("get=%+v want=%+v", got, want)
	}
	all, err := s.SnapshotAll(ctx, "total")
	if err != nil {
		t.Fatal(err)
	}
	if all["1"] != want {
		t.Fatalf("snapshot=%+v", all)
	}
	if err := c.HSet(ctx, statsKey("total", "legacy"), "input_token", 451).Err(); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.GetMetrics(ctx, "total", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if legacy.TotalTokens() != 451 || !legacy.NonChatMetrics.IsZero() {
		t.Fatalf("invented historical evidence: %+v", legacy)
	}
}
