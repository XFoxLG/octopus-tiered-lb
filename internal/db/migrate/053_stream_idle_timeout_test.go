package migrate

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func TestAddStreamIdleTimeoutToGroups(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "legacy-groups.db")
	database, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	sqlDatabase, err := database.DB()
	if err != nil {
		t.Fatalf("get sql database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDatabase.Close() })

	if err := database.Exec(`
		CREATE TABLE groups (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			endpoint_type TEXT NOT NULL,
			mode INTEGER NOT NULL
		)
	`).Error; err != nil {
		t.Fatalf("create legacy groups table: %v", err)
	}
	if err := database.Exec(`
		INSERT INTO groups (id, name, endpoint_type, mode)
		VALUES (1, 'legacy-model', 'chat', 1)
	`).Error; err != nil {
		t.Fatalf("insert legacy group: %v", err)
	}

	if err := addStreamIdleTimeoutToGroups(database); err != nil {
		t.Fatalf("addStreamIdleTimeoutToGroups: %v", err)
	}
	if !database.Migrator().HasColumn(&model.Group{}, "StreamIdleTimeout") {
		t.Fatal("groups table is missing stream_idle_timeout after migration")
	}

	var migratedGroup struct {
		StreamIdleTimeout int `gorm:"column:stream_idle_timeout"`
	}
	if err := database.Table("groups").Where("id = ?", 1).First(&migratedGroup).Error; err != nil {
		t.Fatalf("load migrated group: %v", err)
	}
	if migratedGroup.StreamIdleTimeout != 0 {
		t.Fatalf("legacy stream_idle_timeout = %d, want 0 (disabled)", migratedGroup.StreamIdleTimeout)
	}

	if err := addStreamIdleTimeoutToGroups(database); err != nil {
		t.Fatalf("re-run addStreamIdleTimeoutToGroups: %v", err)
	}
}
