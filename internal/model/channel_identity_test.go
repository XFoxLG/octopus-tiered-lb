package model

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func openChannelIdentityTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "channel-uid.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&Channel{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if connection, err := database.DB(); err == nil {
			_ = connection.Close()
		}
	})
	return database
}

func TestChannelCreateAssignsStableUID(t *testing.T) {
	database := openChannelIdentityTestDB(t)
	channel := Channel{Name: "uid-channel"}
	if err := database.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(channel.ChannelUID); err != nil {
		t.Fatalf("created channel uid %q is not a UUID: %v", channel.ChannelUID, err)
	}

	var stored Channel
	if err := database.First(&stored, channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ChannelUID != channel.ChannelUID {
		t.Fatalf("persisted uid %q != in-memory uid %q", stored.ChannelUID, channel.ChannelUID)
	}

	// A struct Save must not be able to blank or replace the create-only column.
	stored.ChannelUID = ""
	if err := database.Save(&stored).Error; err != nil {
		t.Fatal(err)
	}
	var reloaded Channel
	if err := database.First(&reloaded, channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.ChannelUID != channel.ChannelUID {
		t.Fatalf("channel_uid changed through Save: got %q, want %q", reloaded.ChannelUID, channel.ChannelUID)
	}
}
