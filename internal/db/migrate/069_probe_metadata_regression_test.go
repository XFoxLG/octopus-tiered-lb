package migrate

import (
	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestReviewMigration69PreservesLatestTimestampAndKey(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/review69.db"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	if err := db.Exec(`CREATE TABLE group_items(id INTEGER PRIMARY KEY,group_id INTEGER,channel_id INTEGER,model_name TEXT,supports_tools INTEGER,supports_tools_probe_key_id INTEGER,supports_tools_probed_at INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO group_items VALUES(1,1,1,'model-a',1,101,2000),(2,2,1,'model-a',0,102,1000)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.ChannelModelCapability{}); err != nil {
		t.Fatal(err)
	}
	if err := migrateChannelCapabilityProbe(db); err != nil {
		t.Fatal(err)
	}
	var got model.ChannelModelCapability
	if err := db.First(&got).Error; err != nil {
		t.Fatal(err)
	}
	t.Logf("supported=%v key=%d probed_at=%d", got.Supported, got.ProbeKeyID, got.ProbedAt.Unix())
	if !got.Supported || got.ProbeKeyID != 101 || got.ProbedAt.Unix() != 2000 {
		t.Errorf("migration lost latest verdict/key/timestamp")
	}
}

func TestRepair71DoesNotOverwriteManualOrProbeResults(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(t.TempDir()+"/repair71.db"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	connection, _ := database.DB()
	defer connection.Close()
	if err := database.Exec("CREATE TABLE group_items(id INTEGER PRIMARY KEY,channel_id INTEGER,model_name TEXT,supports_tools INTEGER,supports_tools_probe_key_id INTEGER,supports_tools_probed_at INTEGER)").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&model.ChannelModelCapability{}); err != nil {
		t.Fatal(err)
	}
	for index, source := range []string{"migrated", "manual", "probe"} {
		if err := database.Exec("INSERT INTO group_items VALUES(?,1,?,1,101,2000)", index+1, source).Error; err != nil {
			t.Fatal(err)
		}
		if err := database.Create(&model.ChannelModelCapability{ChannelID: 1, ModelName: source, Capability: model.CapabilityToolCalling, Source: source, Supported: false, ProbeKeyID: 0, ProbedAt: time.Unix(3000, 0)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := repairCapabilityMetadata(database); err != nil {
			t.Fatal(err)
		}
	}
	var rows []model.ChannelModelCapability
	if err := database.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Source == "migrated" {
			if !row.Supported || row.ProbeKeyID != 101 || row.ProbedAt.Unix() != 2000 {
				t.Fatalf("old migration not repaired: %+v", row)
			}
		} else if row.Supported || row.ProbeKeyID != 0 || row.ProbedAt.Unix() != 3000 {
			t.Fatalf("new evidence overwritten: %+v", row)
		}
	}
}
