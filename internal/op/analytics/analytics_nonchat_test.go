package analytics

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/relaylog"
)

func TestAnalyticsProviderFallbackIncludesNonChatEvidence(t *testing.T) {
	setupSeparateLogDB(t)

	stored := model.RelayLog{
		ID:               1,
		Time:             time.Now().Unix(),
		ChannelId:        5,
		ChannelName:      "providerA",
		RequestModelName: "m",
		ActualModelName:  "m",
		NonChatMetrics: model.NonChatMetrics{
			BilledToken:              90,
			UsageReportedRequests:    1,
			UsageBilledRequests:      1,
			EmbeddingInputItems:      2,
			EmbeddingInputObserved:   1,
			EmbeddingVectors:         3,
			EmbeddingVectorsObserved: 2,
			RerankDocuments:          4,
			RerankDocumentsObserved:  3,
			RerankResults:            2,
			RerankResultsObserved:    1,
		},
	}
	if err := db.GetLogDB().Create(&stored).Error; err != nil {
		t.Fatalf("seed stored relay log: %v", err)
	}

	cached := model.RelayLog{
		ID:               2,
		Time:             time.Now().Unix(),
		ChannelId:        5,
		ChannelName:      "providerA",
		RequestModelName: "m",
		ActualModelName:  "m",
		NonChatMetrics: model.NonChatMetrics{
			UnattributedToken:        7,
			BilledToken:              50,
			UsagePartialRequests:     1,
			UsageBilledRequests:      1,
			UsageInvalidRequests:     1,
			EmbeddingInputItems:      3,
			EmbeddingInputObserved:   3,
			EmbeddingVectors:         2,
			EmbeddingVectorsObserved: 2,
			RerankDocuments:          1,
			RerankDocumentsObserved:  1,
			RerankResults:            1,
			RerankResultsObserved:    0,
		},
	}
	cleanup := relaylog.SetCacheForTest([]model.RelayLog{cached})
	defer cleanup()

	items, err := AnalyticsProviderBreakdownGet(context.Background(), model.AnalyticsRangeAll)
	if err != nil {
		t.Fatalf("provider breakdown: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("provider breakdown returned %d items, want 1", len(items))
	}
	got := items[0].AnalyticsMetrics
	want := model.NonChatMetrics{
		UnattributedToken:        7,
		BilledToken:              140,
		UsageReportedRequests:    1,
		UsagePartialRequests:     1,
		UsageBilledRequests:      2,
		UsageInvalidRequests:     1,
		EmbeddingInputItems:      5,
		EmbeddingInputObserved:   4,
		EmbeddingVectors:         5,
		EmbeddingVectorsObserved: 4,
		RerankDocuments:          5,
		RerankDocumentsObserved:  4,
		RerankResults:            3,
		RerankResultsObserved:    1,
	}
	if got.NonChatMetrics != want {
		t.Fatalf("non-chat evidence = %+v, want %+v", got.NonChatMetrics, want)
	}
	if got.TotalTokens != 7 {
		t.Fatalf("TotalTokens = %d, want 7 (BilledToken is classification, not an extra total)", got.TotalTokens)
	}
}

func TestNonChatChannelModelAmountsCountOnceAcrossRetryAndCancel(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		for _, storage := range []string{"cache", "attempt_table", "attempt_json"} {
			t.Run(fmt.Sprintf("%s/cancel=%t", storage, cancelled), func(t *testing.T) {
				setupSeparateLogDB(t)
				finalStatus := model.AttemptSuccess
				errText := ""
				wantSuccess := int64(1)
				if cancelled {
					finalStatus = model.AttemptCancelled
					errText = "client disconnected"
					wantSuccess = 0
				}
				entry := model.RelayLog{ID: 31, Time: time.Now().Unix(), ChannelId: 5, ChannelName: "provider", ActualModelName: "rerank", RequestModelName: "virtual", EndpointType: model.EndpointTypeRerank, InputTokens: 700, OutputTokens: 2, Error: errText,
					NonChatMetrics: model.NonChatMetrics{UnattributedToken: 298, UsageReportedRequests: 1, RerankDocuments: 30, RerankDocumentsObserved: 1, RerankResults: 10, RerankResultsObserved: 1},
					Attempts:       []model.ChannelAttempt{{ChannelID: 5, ChannelName: "provider", ModelName: "rerank", AttemptNum: 1, Status: model.AttemptFailed}, {ChannelID: 5, ChannelName: "provider", ModelName: "rerank", AttemptNum: 2, Status: finalStatus}}}
				rows := map[string]*analyticsChannelModelAggregateRow{}
				scope := func(int, string) bool { return true }
				switch storage {
				case "cache":
					mergeRelayLogIntoChannelModelRows(rows, &entry, scope)
				case "attempt_table":
					if err := db.GetLogDB().AutoMigrate(&model.RelayLogAttempt{}); err != nil {
						t.Fatal(err)
					}
					if err := db.GetLogDB().Create(&entry).Error; err != nil {
						t.Fatal(err)
					}
					for _, a := range entry.Attempts {
						r := model.RelayLogAttempt{RelayLogID: entry.ID, ChannelID: 5, ChannelName: "provider", ModelName: "rerank", Time: entry.Time, AttemptNum: a.AttemptNum, Status: string(a.Status)}
						if err := db.GetLogDB().Create(&r).Error; err != nil {
							t.Fatal(err)
						}
					}
					if err := loadChannelModelRowsFromAttemptsTable(context.Background(), db.GetLogDB(), nil, rows, scope); err != nil {
						t.Fatal(err)
					}
				case "attempt_json":
					if err := db.GetLogDB().Create(&entry).Error; err != nil {
						t.Fatal(err)
					}
					if err := loadChannelModelRowsFromAttemptsJSONBatched(context.Background(), db.GetLogDB(), nil, rows, scope, false); err != nil {
						t.Fatal(err)
					}
				}
				got := rows["5\x00rerank"]
				if got == nil {
					t.Fatalf("missing aggregate: %+v", rows)
				}
				if got.NonChatMetrics != entry.NonChatMetrics || got.InputTokens != 700 || got.OutputTokens != 2 || got.RequestFailed != 1 || got.RequestSuccess != wantSuccess {
					t.Fatalf("amounts duplicated/lost or health changed: %+v", got)
				}
			})
		}
	}
}
