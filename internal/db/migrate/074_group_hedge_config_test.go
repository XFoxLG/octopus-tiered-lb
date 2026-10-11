package migrate

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"testing"
)

func TestGroupHedgeConfigMigrationDefaultOffAndIdempotent(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`CREATE TABLE groups (id INTEGER PRIMARY KEY, name TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO groups (id, name) VALUES (1, 'legacy')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := addGroupHedgeConfig(database); err != nil {
		t.Fatal(err)
	}
	var row struct {
		Name         string
		HedgeEnabled bool
		HedgeDelayMs int
	}
	if err := database.Table("groups").Where("id = 1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Name != "legacy" || row.HedgeEnabled || row.HedgeDelayMs != 300 {
		t.Fatalf("unexpected defaults: %+v", row)
	}
	if err := database.Exec(`UPDATE groups SET hedge_enabled = true, hedge_delay_ms = 0 WHERE id = 1`).Error; err != nil {
		t.Fatal(err)
	}
	if err := addGroupHedgeConfig(database); err != nil {
		t.Fatal(err)
	}
	if err := database.Table("groups").Where("id = 1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if !row.HedgeEnabled || row.HedgeDelayMs != 0 {
		t.Fatalf("rerun overwrote configuration: %+v", row)
	}
}
