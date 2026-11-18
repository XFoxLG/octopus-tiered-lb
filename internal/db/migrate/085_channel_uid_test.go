package migrate

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func openChannelUIDMigrationDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), name)), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if connection, err := database.DB(); err == nil {
			_ = connection.Close()
		}
	})
	return database
}

func TestChannelUIDMigrationBackfillsAndRepairsIdempotently(t *testing.T) {
	database := openChannelUIDMigrationDB(t, "channel-uid-085.db")
	if err := database.Exec(`CREATE TABLE channels(
		id INTEGER PRIMARY KEY,
		name TEXT,
		channel_uid TEXT
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO channels(id, name) VALUES (1, 'legacy-1'), (2, 'legacy-2')`).Error; err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := addChannelUID(database); err != nil {
			t.Fatalf("migration attempt %d: %v", attempt, err)
		}
	}

	var rows []model.Channel
	if err := database.Select("id", "channel_uid").Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("channel count = %d, want 2", len(rows))
	}
	seen := map[string]struct{}{}
	for _, row := range rows {
		if _, err := uuid.Parse(row.ChannelUID); err != nil {
			t.Fatalf("channel %d uid %q is not a UUID: %v", row.ID, row.ChannelUID, err)
		}
		if _, ok := seen[row.ChannelUID]; ok {
			t.Fatalf("duplicate generated uid %q", row.ChannelUID)
		}
		seen[row.ChannelUID] = struct{}{}
	}

	original := rows[0].ChannelUID
	if err := database.Exec(`UPDATE channels SET channel_uid = NULL WHERE id = 2`).Error; err != nil {
		t.Fatal(err)
	}
	if err := addChannelUID(database); err != nil {
		t.Fatal(err)
	}
	var repaired []model.Channel
	if err := database.Select("id", "channel_uid").Order("id").Find(&repaired).Error; err != nil {
		t.Fatal(err)
	}
	if repaired[0].ChannelUID != original {
		t.Fatalf("existing uid was rewritten: got %q, want %q", repaired[0].ChannelUID, original)
	}
	if repaired[1].ChannelUID == "" {
		t.Fatal("NULL uid was not repaired")
	}
	if !database.Migrator().HasIndex(&model.Channel{}, channelUIDUniqueIndex) {
		t.Fatalf("%s was not created", channelUIDUniqueIndex)
	}
}

func TestChannelUIDMigrationRejectsDuplicateExistingUID(t *testing.T) {
	database := openChannelUIDMigrationDB(t, "channel-uid-duplicate.db")
	if err := database.Exec(`CREATE TABLE channels(
		id INTEGER PRIMARY KEY,
		name TEXT,
		channel_uid TEXT
	)`).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := uuid.NewString()
	if err := database.Exec(`INSERT INTO channels(id, name, channel_uid) VALUES (1, 'a', ?), (2, 'b', ?)`, duplicate, duplicate).Error; err != nil {
		t.Fatal(err)
	}
	if err := addChannelUID(database); err == nil {
		t.Fatal("duplicate non-empty channel_uid did not fail the unique index")
	}
	var count int64
	if err := database.Model(&model.Channel{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("migration changed duplicate rows: count = %d, want 2", count)
	}
}
