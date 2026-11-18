package backup

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	internaldb "github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"gorm.io/gorm"
)

func openChannelUIDBackupTarget(t *testing.T, name string) *gorm.DB {
	t.Helper()
	target, err := internaldb.OpenStandalone("sqlite", filepath.Join(t.TempDir(), name), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if connection, err := target.DB(); err == nil {
			_ = connection.Close()
		}
	})
	if err := internaldb.Migrate(target); err != nil {
		t.Fatal(err)
	}
	var defaultGroupCount int64
	if err := target.Model(&model.ChannelGroup{}).Where("is_default = ?", true).Count(&defaultGroupCount).Error; err != nil {
		t.Fatal(err)
	}
	if defaultGroupCount == 0 {
		if err := target.Create(&model.ChannelGroup{
			Name: model.DefaultChannelGroupName, IsDefault: true,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return target
}

func TestHistoricalBackupChannelUIDImportAndRoundTrip(t *testing.T) {
	t.Run("missing uid is filled and used for insert", func(t *testing.T) {
		target := openChannelUIDBackupTarget(t, "uid-missing.db")
		dump := &model.DBDump{
			Version: 5,
			Channels: []model.Channel{{
				ID:   11,
				Name: "legacy-channel",
				Type: outbound.OutboundTypeOpenAIChat,
			}},
		}
		if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental); err != nil {
			t.Fatal(err)
		}
		if _, err := uuid.Parse(dump.Channels[0].ChannelUID); err != nil {
			t.Fatalf("dump did not receive a UUID: %q", dump.Channels[0].ChannelUID)
		}
		var stored model.Channel
		if err := target.First(&stored, 11).Error; err != nil {
			t.Fatal(err)
		}
		if stored.ChannelUID != dump.Channels[0].ChannelUID {
			t.Fatalf("stored uid %q != dump uid %q", stored.ChannelUID, dump.Channels[0].ChannelUID)
		}
	})

	t.Run("full restore keeps source uid", func(t *testing.T) {
		target := openChannelUIDBackupTarget(t, "uid-full.db")
		sourceUID := uuid.NewString()
		dump := &model.DBDump{
			Version: 5,
			Channels: []model.Channel{{
				ID:         12,
				Name:       "source-channel",
				Type:       outbound.OutboundTypeOpenAIChat,
				ChannelUID: sourceUID,
			}},
		}
		if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull); err != nil {
			t.Fatal(err)
		}
		var stored model.Channel
		if err := target.First(&stored, 12).Error; err != nil {
			t.Fatal(err)
		}
		if stored.ChannelUID != sourceUID {
			t.Fatalf("full restore uid = %q, want source %q", stored.ChannelUID, sourceUID)
		}
	})

	t.Run("incremental conflict keeps target uid", func(t *testing.T) {
		target := openChannelUIDBackupTarget(t, "uid-conflict.db")
		existing := model.Channel{ID: 13, Name: "target-channel", Type: outbound.OutboundTypeOpenAIChat}
		if err := target.Create(&existing).Error; err != nil {
			t.Fatal(err)
		}
		if existing.ChannelUID == "" {
			t.Fatal("seed channel has no uid")
		}
		dump := &model.DBDump{
			Version: 5,
			Channels: []model.Channel{{
				ID:         13,
				Name:       "source-channel",
				Type:       outbound.OutboundTypeOpenAIChat,
				ChannelUID: uuid.NewString(),
			}},
		}
		if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental); err != nil {
			t.Fatal(err)
		}
		var stored model.Channel
		if err := target.First(&stored, 13).Error; err != nil {
			t.Fatal(err)
		}
		if stored.ChannelUID != existing.ChannelUID {
			t.Fatalf("incremental conflict replaced target uid: got %q, want %q", stored.ChannelUID, existing.ChannelUID)
		}
		if stored.Name != existing.Name {
			t.Fatalf("incremental conflict replaced target row: name %q", stored.Name)
		}
	})

	t.Run("duplicate uid in backup fails before mutation", func(t *testing.T) {
		target := openChannelUIDBackupTarget(t, "uid-duplicate.db")
		duplicate := uuid.NewString()
		dump := &model.DBDump{
			Version: 5,
			Channels: []model.Channel{
				{ID: 21, Name: "a", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: duplicate},
				{ID: 22, Name: "b", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: duplicate},
			},
		}
		_, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental)
		if err == nil || !strings.Contains(err.Error(), "channel_uid collision") {
			t.Fatalf("duplicate uid import error = %v", err)
		}
		var count int64
		if err := target.Model(&model.Channel{}).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("rejected import inserted %d channels", count)
		}
	})

	t.Run("incremental uid owned by another target id is rejected", func(t *testing.T) {
		target := openChannelUIDBackupTarget(t, "uid-owned-elsewhere.db")
		existing := model.Channel{ID: 30, Name: "occupied-source", Type: outbound.OutboundTypeOpenAIChat}
		if err := target.Create(&existing).Error; err != nil {
			t.Fatal(err)
		}
		dump := &model.DBDump{
			Version: 5,
			Channels: []model.Channel{{
				ID:         31,
				Name:       "source-with-shared-uid",
				Type:       outbound.OutboundTypeOpenAIChat,
				ChannelUID: existing.ChannelUID,
			}},
		}
		_, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental)
		if err == nil || !strings.Contains(err.Error(), "already uses it") {
			t.Fatalf("conflicting uid import error = %v", err)
		}
		var count int64
		if err := target.Model(&model.Channel{}).Where("id = ?", 31).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("rejected conflicting import inserted a channel")
		}
	})
}

func TestChannelUIDTravelsThroughExportJSON(t *testing.T) {
	if err := internaldb.InitDB("sqlite", filepath.Join(t.TempDir(), "uid-export.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = internaldb.Close() })
	channel := model.Channel{Name: "export-uid", Type: outbound.OutboundTypeOpenAIChat}
	if err := internaldb.GetDB().Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	dump, err := ExportAll(context.Background(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"channel_uid"`) {
		t.Fatal("export JSON does not carry channel_uid")
	}
	var restored model.DBDump
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ch := range restored.Channels {
		if ch.Name == "export-uid" {
			found = true
			if ch.ChannelUID != channel.ChannelUID {
				t.Fatalf("round-trip uid = %q, want %q", ch.ChannelUID, channel.ChannelUID)
			}
		}
	}
	if !found {
		t.Fatal("exported channel missing")
	}
}
