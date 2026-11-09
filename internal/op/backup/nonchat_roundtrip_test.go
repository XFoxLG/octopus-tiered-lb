package backup

import (
	"context"
	"encoding/json"
	"github.com/lingyuins/octopus/internal/model"
	trans "github.com/lingyuins/octopus/internal/transformer/model"
	"reflect"
	"testing"
)

func TestNonChatBackupV5RoundTrip(t *testing.T) {
	source := initBackupTestDB(t, "nonchat-source.db")
	for _, parent := range []any{&model.Channel{ID: 8, Name: "synthetic"}, &model.APIKey{ID: 5, Name: "synthetic", APIKey: "synthetic-local-test-only"}} {
		if err := source.Create(parent).Error; err != nil {
			t.Fatal(err)
		}
	}
	metrics := model.StatsMetrics{InputToken: 700, NonChatMetrics: model.NonChatMetrics{UnattributedToken: 300, BilledToken: 90, UsageReportedRequests: 1, UsagePartialRequests: 2, UsageBilledRequests: 3, UsageInvalidRequests: 4, EmbeddingInputItems: 8, EmbeddingInputObserved: 5, EmbeddingVectors: 8, EmbeddingVectorsObserved: 6, RerankDocuments: 30, RerankDocumentsObserved: 7, RerankResults: 10, RerankResultsObserved: 8}}
	rows := []any{
		&model.StatsTotal{ID: 1, StatsMetrics: metrics}, &model.StatsDaily{Date: "20261109", StatsMetrics: metrics},
		&model.StatsHourly{Date: "20261109", Hour: 3, StatsMetrics: metrics}, &model.StatsModel{Name: "embedding", ChannelID: 8, StatsMetrics: metrics},
		&model.StatsChannel{ChannelID: 8, StatsMetrics: metrics}, &model.StatsAPIKey{APIKeyID: 5, StatsMetrics: metrics},
		&model.StatsDailyChannel{Date: "20261109", ChannelID: 8, StatsMetrics: metrics}, &model.StatsDailyModel{Date: "20261109", ModelName: "embedding", StatsMetrics: metrics},
		&model.StatsDailyAPIKey{Date: "20261109", APIKeyID: 5, StatsMetrics: metrics}, &model.StatsDailyChannelModel{Date: "20261109", ChannelID: 8, ModelName: "embedding", StatsMetrics: metrics},
	}
	for _, row := range rows {
		if err := source.Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}
	zero, total := int64(0), int64(1000)
	entry := model.RelayLog{ID: 81, EndpointType: model.EndpointTypeRerank, NonChatMetrics: metrics.NonChatMetrics, NonChatUsage: &trans.NonChatUsageSummary{Basis: "processing", Completeness: "reported", NonChatCounters: trans.NonChatCounters{TotalTokens: &total}, Counts: trans.NonChatCounts{RerankResults: &zero}}, Diagnostics: &model.RelayDiagnostics{Version: 1, NonChatUsage: &trans.NonChatObservation{Processing: trans.NonChatCounters{TotalTokens: &total}, Units: trans.NonChatUnits{SearchUnits: new(float64)}}}}
	if err := source.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	dump, err := ExportAll(context.Background(), true, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	var restored model.DBDump
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{model.ImportModeIncremental, model.ImportModeFull} {
		t.Run(string(mode), func(t *testing.T) {
			target := backupReliabilityTarget(t, "nonchat-target.db")
			for n := 0; n < 2; n++ {
				if _, err := ImportWithModeToDB(context.Background(), target, &restored, mode); err != nil {
					t.Fatal(err)
				}
			}
			for _, row := range rows {
				got := reflect.New(reflect.TypeOf(row).Elem()).Interface()
				var count int64
				if err := target.Model(row).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Fatalf("%T duplicate count %d", row, count)
				}
				if err := target.First(got).Error; err != nil {
					t.Fatal(err)
				}
				if actual := reflect.ValueOf(got).Elem().FieldByName("StatsMetrics").Interface(); !reflect.DeepEqual(actual, metrics) {
					t.Fatalf("%T lost metrics: %+v", row, actual)
				}
			}
			var got model.RelayLog
			if err := target.First(&got, 81).Error; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.NonChatUsage, entry.NonChatUsage) || !reflect.DeepEqual(got.Diagnostics, entry.Diagnostics) || got.NonChatMetrics != entry.NonChatMetrics {
				t.Fatalf("log evidence changed: %+v", got)
			}
		})
	}
}
