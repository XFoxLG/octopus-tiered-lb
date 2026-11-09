package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

// TestNonChatAccountingMigrationIsAdditiveAndIdempotent covers the shared
// migration function on a legacy table. A separate log DB reaches the same
// result because MigrateLogDB AutoMigrates the current RelayLog model.
func TestNonChatAccountingMigrationIsAdditiveAndIdempotent(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(t.TempDir()+"/nonchat083.db"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	connection, _ := database.DB()
	defer connection.Close()

	legacyTable := `CREATE TABLE relay_logs (
		id INTEGER PRIMARY KEY,
		input_tokens INTEGER,
		output_tokens INTEGER
	)`
	if err := database.Exec(legacyTable).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO relay_logs (id, input_tokens, output_tokens) VALUES (1, 1200, 34)`).Error; err != nil {
		t.Fatal(err)
	}

	for run := 0; run < 2; run++ {
		if err := addNonChatAccountingColumns(database); err != nil {
			t.Fatalf("migration run %d: %v", run, err)
		}
		for _, field := range nonChatMetricFields {
			if !database.Migrator().HasColumn(&model.RelayLog{}, field) {
				t.Fatalf("column %s was not added", field)
			}
		}
	}

	type legacyEvidence struct {
		UnattributedToken int64
		BilledToken       int64
	}
	history := legacyEvidence{}
	if err := database.Table("relay_logs").Where("id = 1").Take(&history).Error; err != nil {
		t.Fatal(err)
	}
	if history.UnattributedToken != 0 || history.BilledToken != 0 {
		t.Fatalf("historical evidence was backfilled: %+v", history)
	}

	if err := database.Exec(`UPDATE relay_logs SET unattributed_token = 12, billed_token = 34 WHERE id = 1`).Error; err != nil {
		t.Fatal(err)
	}
	if err := addNonChatAccountingColumns(database); err != nil {
		t.Fatal(err)
	}
	after := legacyEvidence{}
	if err := database.Table("relay_logs").Where("id = 1").Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.UnattributedToken != 12 || after.BilledToken != 34 {
		t.Fatalf("idempotent rerun changed evidence: %+v", after)
	}
}
