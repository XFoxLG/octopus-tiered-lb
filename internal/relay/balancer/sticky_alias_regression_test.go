package balancer

import (
	"testing"
	"time"
)

func TestReviewStickyEscapeUsesResolvedModel(t *testing.T) {
	clearAutoStatsForTest()
	defer clearAutoStatsForTest()
	const channelID = 988
	const requestModel = "tavern-roleplay-alias"
	const actualModel = "claude-upstream-model"
	recordOutcome(channelID, actualModel, false, getMinSamples()+2)
	if !ShouldEvictSticky(982, actualModel, channelID) {
		t.Fatal("fixture should have enough failure samples")
	}
	SetSticky(982, requestModel, channelID, 1, actualModel)
	defer RemoveSticky(982, requestModel)
	got := GetSticky(982, requestModel, time.Hour)
	if got != nil {
		t.Errorf("failed upstream model stays sticky when request uses an alias: %+v", got)
	}
}
