package migrate

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"testing"
)

func TestRelayFirstVisibleMilestonesPreserveLegacyRows(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`CREATE TABLE relay_logs (id INTEGER PRIMARY KEY, ftut INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO relay_logs (id, ftut) VALUES (1, 42)`).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := addRelayFirstVisibleMilestones(database); err != nil {
			t.Fatal(err)
		}
	}
	var row struct {
		Ftut                   int
		UpstreamFirstVisibleAt int64
		ClientFirstFlushAt     int64
	}
	if err := database.Table("relay_logs").Where("id = 1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Ftut != 42 || row.UpstreamFirstVisibleAt != 0 || row.ClientFirstFlushAt != 0 {
		t.Fatalf("legacy record changed: %+v", row)
	}
	if err := database.Exec(`UPDATE relay_logs SET upstream_first_visible_at = 1000, client_first_flush_at = 1020 WHERE id = 1`).Error; err != nil {
		t.Fatal(err)
	}
	if err := addRelayFirstVisibleMilestones(database); err != nil {
		t.Fatal(err)
	}
	if err := database.Table("relay_logs").Where("id = 1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.UpstreamFirstVisibleAt != 1000 || row.ClientFirstFlushAt != 1020 {
		t.Fatalf("rerun changed milestones: %+v", row)
	}
}
