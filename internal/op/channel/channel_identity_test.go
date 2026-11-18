package channel

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
)

func TestRefreshCacheRepairsMissingChannelUID(t *testing.T) {
	setupBatchGroupTest(t)
	seedChannel(t, 1, 1)

	before, err := Get(1, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before.ChannelUID == "" {
		t.Fatal("seed channel has no uid")
	}

	// Simulate an old binary writing NULL after migration 085 already ran.
	if err := db.GetDB().Exec(`UPDATE channels SET channel_uid = NULL WHERE id = 1`).Error; err != nil {
		t.Fatal(err)
	}
	chCache.Clear()
	if err := RefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	repaired, err := Get(1, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(repaired.ChannelUID); err != nil {
		t.Fatalf("repaired uid %q is not a UUID: %v", repaired.ChannelUID, err)
	}
	var stored model.Channel
	if err := db.GetDB().First(&stored, 1).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ChannelUID != repaired.ChannelUID {
		t.Fatalf("cache uid %q != persisted uid %q", repaired.ChannelUID, stored.ChannelUID)
	}
}

func TestChannelUIDSurvivesRenameAndEndpointEdits(t *testing.T) {
	setupBatchGroupTest(t)
	seedChannel(t, 1, 1)
	original, err := Get(1, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if original.ChannelUID == "" {
		t.Fatal("seed channel has no uid")
	}

	cfg := &model.ConnectionConfig{
		Version:   1,
		Selection: "same_protocol",
		Endpoints: []model.ChannelEndpoint{{
			ID:       "chat-1",
			Protocol: "chat",
			URL:      "https://example.com/v1",
			URLMode:  "base",
			Auth:     "default",
		}},
		Catalog: model.ModelCatalog{Format: "manual"},
	}
	renamed := "renamed-channel"
	if _, err := Update(&model.ChannelUpdateRequest{ID: 1, Name: &renamed, ConnectionConfig: cfg}, context.Background()); err != nil {
		t.Fatal(err)
	}
	withTwoEndpoints := *cfg
	withTwoEndpoints.Endpoints = append(append([]model.ChannelEndpoint(nil), cfg.Endpoints...), model.ChannelEndpoint{
		ID:       "responses-1",
		Protocol: "responses",
		URL:      "https://example.com/v1/responses",
		URLMode:  "full",
		Auth:     "default",
	})
	if _, err := Update(&model.ChannelUpdateRequest{ID: 1, ConnectionConfig: &withTwoEndpoints}, context.Background()); err != nil {
		t.Fatal(err)
	}
	withOneEndpoint := *cfg
	withOneEndpoint.Endpoints = append([]model.ChannelEndpoint(nil), cfg.Endpoints...)
	if _, err := Update(&model.ChannelUpdateRequest{ID: 1, ConnectionConfig: &withOneEndpoint}, context.Background()); err != nil {
		t.Fatal(err)
	}

	after, err := Get(1, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.ChannelUID != original.ChannelUID {
		t.Fatalf("channel_uid changed across edits: got %q, want %q", after.ChannelUID, original.ChannelUID)
	}
	if after.Name != renamed {
		t.Fatalf("rename did not apply: got %q", after.Name)
	}
	var stored model.Channel
	if err := db.GetDB().First(&stored, 1).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ChannelUID != original.ChannelUID {
		t.Fatalf("persisted channel_uid changed: got %q, want %q", stored.ChannelUID, original.ChannelUID)
	}
}
