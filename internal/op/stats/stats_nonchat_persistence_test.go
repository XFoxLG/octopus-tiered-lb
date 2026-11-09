package stats

import (
	"context"
	"testing"

	"github.com/lingyuins/octopus/internal/model"
)

func TestNonChatStatsSaveReloadAndDailyDimensions(t *testing.T) {
	databaseConnection := initializeStatsPersistenceTest(t, true)
	ctx := context.Background()

	metrics := model.StatsMetrics{
		InputToken:  100,
		OutputToken: 40,
		NonChatMetrics: model.NonChatMetrics{
			UnattributedToken:        7,
			BilledToken:              90,
			UsageReportedRequests:    3,
			UsagePartialRequests:     2,
			UsageBilledRequests:      1,
			UsageInvalidRequests:     1,
			EmbeddingInputItems:      5,
			EmbeddingInputObserved:   4,
			EmbeddingVectors:         3,
			EmbeddingVectorsObserved: 2,
			RerankDocuments:          9,
			RerankDocumentsObserved:  8,
			RerankResults:            4,
			RerankResultsObserved:    3,
		},
	}
	recordSnapshotMetricsForTest(t, metrics)
	if err := DailyDimensionChannelUpdate(ctx, isolationChannelID, " channel ", metrics); err != nil {
		t.Fatalf("daily channel update: %v", err)
	}
	if err := DailyDimensionModelUpdate(ctx, isolationModelName, metrics); err != nil {
		t.Fatalf("daily model update: %v", err)
	}
	if err := DailyDimensionAPIKeyUpdate(ctx, isolationAPIKeyID, " key ", metrics); err != nil {
		t.Fatalf("daily API key update: %v", err)
	}
	if err := DailyDimensionChannelModelUpdate(ctx, isolationChannelID, " channel ", isolationModelName, metrics); err != nil {
		t.Fatalf("daily channel-model update: %v", err)
	}

	usePostgresDialectForTest(t, databaseConnection)
	if err := SaveDB(ctx); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	ClearAllCachesForTest()
	if err := RefreshCache(ctx); err != nil {
		t.Fatalf("reload snapshot: %v", err)
	}

	assertSnapshotMetricsForTest(t, metrics)
	var dailyChannel model.StatsDailyChannel
	if err := databaseConnection.Where("channel_id = ?", isolationChannelID).First(&dailyChannel).Error; err != nil {
		t.Fatalf("reload daily channel: %v", err)
	}
	var dailyModel model.StatsDailyModel
	if err := databaseConnection.Where("model_name = ?", isolationModelName).First(&dailyModel).Error; err != nil {
		t.Fatalf("reload daily model: %v", err)
	}
	var dailyAPIKey model.StatsDailyAPIKey
	if err := databaseConnection.Where("api_key_id = ?", isolationAPIKeyID).First(&dailyAPIKey).Error; err != nil {
		t.Fatalf("reload daily API key: %v", err)
	}
	var dailyChannelModel model.StatsDailyChannelModel
	if err := databaseConnection.Where("channel_id = ? AND model_name = ?", isolationChannelID, isolationModelName).First(&dailyChannelModel).Error; err != nil {
		t.Fatalf("reload daily channel-model: %v", err)
	}
	for name, row := range map[string]model.StatsDailyChannel{
		"channel":       dailyChannel,
		"model":         {StatsMetrics: dailyModel.StatsMetrics},
		"apikey":        {StatsMetrics: dailyAPIKey.StatsMetrics},
		"channel_model": {StatsMetrics: dailyChannelModel.StatsMetrics},
	} {
		if row.StatsMetrics != metrics {
			t.Errorf("daily %s metrics = %+v, want %+v", name, row.StatsMetrics, metrics)
		}
	}
}
