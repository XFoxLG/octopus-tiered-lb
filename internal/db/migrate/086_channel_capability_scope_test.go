package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func TestChannelCapabilityScopeMigrationPreservesLegacyRowsAndReplacesIndex(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(t.TempDir()+"/capability-086.db"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	connection, _ := database.DB()
	defer connection.Close()

	if err := database.Exec(`CREATE TABLE channels(
		id INTEGER PRIMARY KEY,
		name TEXT,
		channel_uid TEXT
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO channels(id, name, channel_uid) VALUES(7, 'c', 'channel-uid-7')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`CREATE TABLE channel_model_capabilities(
		id INTEGER PRIMARY KEY,
		channel_id INTEGER NOT NULL,
		model_name TEXT NOT NULL,
		capability TEXT NOT NULL,
		supported BOOLEAN NOT NULL,
		source TEXT NOT NULL DEFAULT '',
		probe_key_id INTEGER NOT NULL DEFAULT 0,
		probed_at DATETIME,
		updated_at DATETIME
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`CREATE UNIQUE INDEX idx_channel_model_capability
		ON channel_model_capabilities(channel_id, model_name, capability)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO channel_model_capabilities
		(id, channel_id, model_name, capability, supported, source, probe_key_id)
		VALUES(1, 7, 'm', 'tool_calling', 1, 'probe', 3)`).Error; err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := migrateChannelCapabilityScope(database); err != nil {
			t.Fatalf("migration attempt %d: %v", attempt, err)
		}
	}

	var legacy model.ChannelModelCapability
	if err := database.First(&legacy, 1).Error; err != nil {
		t.Fatal(err)
	}
	if legacy.ChannelUID != "channel-uid-7" {
		t.Fatalf("channel uid = %q, want stable uid", legacy.ChannelUID)
	}
	if legacy.EndpointID != "" || legacy.Protocol != "" {
		t.Fatalf("legacy evidence gained an inferred scope: endpoint=%q protocol=%q", legacy.EndpointID, legacy.Protocol)
	}
	if legacy.Stale {
		t.Fatal("historical row was marked stale without a configuration change")
	}
	if database.Migrator().HasIndex(&model.ChannelModelCapability{}, channelCapabilityLegacyIndex) {
		t.Fatal("legacy unique index still exists")
	}
	if !database.Migrator().HasIndex(&model.ChannelModelCapability{}, channelCapabilityScopeIndex) {
		t.Fatal("scoped unique index was not created")
	}

	second := model.ChannelModelCapability{
		ChannelID:  7,
		ChannelUID: "channel-uid-7",
		EndpointID: "e1",
		Protocol:   "responses",
		ModelName:  "m",
		Capability: model.CapabilityToolCalling,
		ProbeKeyID: 4,
		Supported:  true,
	}
	if err := database.Create(&second).Error; err != nil {
		t.Fatalf("different scope was rejected: %v", err)
	}
	duplicate := second
	duplicate.ID = 0
	if err := database.Create(&duplicate).Error; err == nil {
		t.Fatal("duplicate scope was accepted")
	}

	otherChannel := second
	otherChannel.ID = 0
	otherChannel.ChannelID = 8
	otherChannel.ChannelUID = "channel-uid-8"
	if err := database.Create(&otherChannel).Error; err != nil {
		t.Fatalf("identical endpoint range on a different channel was rejected: %v", err)
	}
}
