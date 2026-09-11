package helper

import (
	"context"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func TestSkipModelTestBlocksEveryModelProbeEntry(testContext *testing.T) {
	// No URL or key is provided: a skipped channel must be rejected before
	// credential selection, request construction, or any network operation.
	channel := &appmodel.Channel{Type: outbound.OutboundTypeOpenAIChat, SkipModelTest: true}
	testContext.Run("shared probe transport", func(testContext *testing.T) {
		statusCode, responseText, response, err := sendGroupProbeRequest(context.Background(), outbound.Get(channel.Type), channel, "", appmodel.EndpointTypeChat, "fixture")
		if err == nil || !strings.Contains(err.Error(), "skipped model test") {
			testContext.Fatalf("expected skip before constructing probe, got %v", err)
		}
		if statusCode != 0 || responseText != "" || response != nil {
			testContext.Fatal("skipped probe must not invent an upstream response")
		}
	})
	testContext.Run("health probe", func(testContext *testing.T) {
		result := RunGroupHealthCandidate(context.Background(), channel, appmodel.ChannelKey{}, "fixture", appmodel.EndpointTypeChat)
		if result.Success || result.HTTPStatus != 0 || !strings.Contains(result.ErrorMessage, "skipped model test") {
			testContext.Fatalf("health probe must be skipped: %+v", result)
		}
	})
	testContext.Run("tools probe", func(testContext *testing.T) {
		for _, toolChoice := range []string{"", "required"} {
			_, err := TestToolsSupport(context.Background(), channel, "fixture", toolChoice)
			if err == nil || !strings.Contains(err.Error(), "skipped model test") {
				testContext.Fatalf("tools probe %q must be skipped before key lookup: %v", toolChoice, err)
			}
		}
	})
}

func TestRunGroupHealthRecordsProhibitedProbeAsSkipped(testContext *testing.T) {
	setupHelperDB(testContext)
	// Migration registrations are consumed by the first database in a process.
	// This fixture must also work after another test has initialized a database.
	if err := db.GetDB().AutoMigrate(&appmodel.GroupHealthSnapshot{}, &appmodel.GroupHealthAttempt{}); err != nil {
		testContext.Fatal(err)
	}
	const fixtureID = 991103
	channel.GetCache().Set(fixtureID, appmodel.Channel{
		ID: fixtureID, Name: "no-probing-fixture", Type: outbound.OutboundTypeOpenAIChat,
		Enabled: true, SkipModelTest: true,
	})
	group.GetCache().Set(fixtureID, appmodel.Group{
		ID: fixtureID, Name: "no-probing-fixture", Mode: appmodel.GroupModeFailover,
		EndpointType: appmodel.EndpointTypeChat,
		Items:        []appmodel.GroupItem{{ID: fixtureID, ChannelID: fixtureID, ModelName: "fixture", Priority: 1}},
	})
	testContext.Cleanup(func() {
		channel.GetCache().Del(fixtureID)
		group.GetCache().Del(fixtureID)
	})
	if err := RunGroupHealth(context.Background(), fixtureID); err != nil {
		testContext.Fatal(err)
	}
	snapshot, err := group.NewGroupHealthRepository().GetLatestSnapshotByGroupID(context.Background(), fixtureID)
	if err != nil {
		testContext.Fatal(err)
	}
	if len(snapshot.Attempts) != 1 || snapshot.Attempts[0].Status != appmodel.GroupHealthAttemptStatusSkipped {
		testContext.Fatalf("prohibited probe must be recorded as skipped, not failed: %+v", snapshot.Attempts)
	}
	if snapshot.Attempts[0].HTTPStatus != 0 || snapshot.Attempts[0].ChannelKeyID != 0 {
		testContext.Fatalf("skipped probe invented an HTTP result or selected a key: %+v", snapshot.Attempts[0])
	}
	if snapshot.Status != appmodel.GroupHealthStatusPartial || !strings.Contains(snapshot.Message, "availability was not checked") {
		testContext.Fatalf("all-skipped snapshot misrepresented availability: %+v", snapshot)
	}
}
