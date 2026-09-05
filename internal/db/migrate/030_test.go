package migrate

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func TestMigrateGroupEndpointNameUniqueIndexAllowsSameNameAcrossEndpoints(t *testing.T) {
	db := openLegacyGroupDB(t, "legacy-groups.db")
	createLegacyGroupsTable(t, db, "")
	if err := db.Exec(`CREATE UNIQUE INDEX idx_groups_name ON groups(name)`).Error; err != nil {
		t.Fatalf("create legacy name index: %v", err)
	}
	seedLegacySharedModelGroup(t, db)

	assertMigrateGroupEndpointNameUniqueIndexAllowsSameNameAcrossEndpoints(t, db)
}

func TestMigrateGroupEndpointNameUniqueIndexRebuildsSQLiteInlineUniqueName(t *testing.T) {
	db := openLegacyGroupDB(t, "legacy-groups-inline-unique.db")
	createLegacyGroupsTable(t, db, "UNIQUE")
	seedLegacySharedModelGroup(t, db)

	assertMigrateGroupEndpointNameUniqueIndexAllowsSameNameAcrossEndpoints(t, db)
}

func openLegacyGroupDB(t *testing.T, filename string) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), filename)
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func createLegacyGroupsTable(t *testing.T, db *gorm.DB, nameConstraint string) {
	t.Helper()
	nameColumn := "name TEXT NOT NULL"
	if nameConstraint != "" {
		nameColumn += " " + nameConstraint
	}
	if err := db.Exec(`
		CREATE TABLE groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			` + nameColumn + `,
			category TEXT NOT NULL DEFAULT '',
			endpoint_type TEXT NOT NULL DEFAULT '*',
			endpoint_provider TEXT NOT NULL DEFAULT '',
			outbound_format TEXT NOT NULL DEFAULT '',
			mode INTEGER NOT NULL,
			match_regex TEXT,
			first_token_time_out INTEGER,
			attempt_time_out INTEGER DEFAULT 0,
			session_keep_time INTEGER,
			condition TEXT,
			last_test_passed INTEGER,
			last_test_all_failed INTEGER,
			last_test_at INTEGER DEFAULT 0
		)
	`).Error; err != nil {
		t.Fatalf("create legacy groups table: %v", err)
	}
}

func seedLegacySharedModelGroup(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec(`
		INSERT INTO groups (name, endpoint_type, mode, match_regex, first_token_time_out, session_keep_time)
		VALUES ('shared-model', 'chat', 1, '', 0, 0)
	`).Error; err != nil {
		t.Fatalf("insert legacy group: %v", err)
	}
}

func assertMigrateGroupEndpointNameUniqueIndexAllowsSameNameAcrossEndpoints(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := migrateGroupEndpointNameUniqueIndex(db); err != nil {
		t.Fatalf("migrateGroupEndpointNameUniqueIndex: %v", err)
	}
	// Run migration 036 to add reasoning_buffer_strategy column
	// (test uses latest model.Group which includes this field)
	if err := addReasoningBufferStrategyToGroups(db); err != nil {
		t.Fatalf("addReasoningBufferStrategyColumn: %v", err)
	}
	// Run migration 053 to add stream_idle_timeout column
	// (test uses latest model.Group which includes this field)
	if err := addStreamIdleTimeoutToGroups(db); err != nil {
		t.Fatalf("addStreamIdleTimeoutColumn: %v", err)
	}
	// Run migration 054 to add param_override/custom_header columns
	// (test uses latest model.Group which includes these fields)
	if err := addGroupRequestOptions(db); err != nil {
		t.Fatalf("addGroupRequestOptions: %v", err)
	}
	// Run migration 058 to add default reasoning policy columns
	// (test uses latest model.Group which includes these fields)
	if err := addGroupReasoningPolicyColumns(db); err != nil {
		t.Fatalf("addGroupReasoningPolicyColumns: %v", err)
	}

	if err := db.Create(&model.Group{Name: "shared-model", EndpointType: model.EndpointTypeEmbeddings, Mode: model.GroupModeRoundRobin}).Error; err != nil {
		t.Fatalf("create same-name different endpoint group after migration: %v", err)
	}
	if err := db.Create(&model.Group{Name: "shared-model", EndpointType: model.EndpointTypeChat, Mode: model.GroupModeRoundRobin}).Error; err == nil {
		t.Fatal("create duplicate same endpoint group error = nil, want unique constraint")
	}

	var idxName string
	if err := db.Raw(`
		SELECT name
		FROM sqlite_master
		WHERE type = 'index' AND tbl_name = 'groups' AND name = 'idx_groups_endpoint_name'
		LIMIT 1
	`).Scan(&idxName).Error; err != nil {
		t.Fatalf("read endpoint/name index: %v", err)
	}
	if idxName != "idx_groups_endpoint_name" {
		t.Fatalf("expected endpoint/name index to exist, got %q", idxName)
	}
}
