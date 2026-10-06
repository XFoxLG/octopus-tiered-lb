package channel

import (
	"context"
	"errors"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
	"testing"
)

func TestReviewFailedHistoryWritePreservesResults(t *testing.T) {
	setupBatchGroupTest(t)
	database := db.GetDB()
	err := database.Callback().Create().Before("gorm:create").Register("review_simulated_result_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "channel_probe_results" {
			tx.AddError(errors.New("simulated result persistence failure"))
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Callback().Create().Remove("review_simulated_result_failure")
	run := &model.ChannelProbeRun{ChannelID: 1, ModelName: "test-model", Results: []model.ChannelProbeResult{{Kind: model.ProbeKindProtocol, Item: model.ProbeItemProtocolChat, Verdict: model.ProbeVerdictPass}}}
	err = SaveProbeRun(context.Background(), run)
	t.Logf("error=%v remaining_results=%d run_id=%d", err, len(run.Results), run.ID)
	if err == nil {
		t.Fatal("fixture must fail saving results")
	}
	if len(run.Results) != 1 {
		t.Error("failed history persistence discarded the already-completed probe results")
	}
}
