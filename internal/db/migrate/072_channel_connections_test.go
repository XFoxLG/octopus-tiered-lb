package migrate

import (
	"encoding/json"
	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"gorm.io/gorm"
	"testing"
)

func TestChannelConnectionsMigrationFromLegacySchema(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`CREATE TABLE channels (id INTEGER PRIMARY KEY, type INTEGER, base_urls TEXT, upstream_protocols TEXT, outbound_format_override TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`CREATE TABLE channel_probe_results (id INTEGER PRIMARY KEY)`).Error; err != nil {
		t.Fatal(err)
	}
	oldURLs := `[{"url":"https://example.com","delay":3,"suffix_mode":"auto"}]`
	for id, override := range map[int]string{1: "chat_only", 2: ""} {
		if err := database.Exec(`INSERT INTO channels (id,type,base_urls,upstream_protocols,outbound_format_override) VALUES (?,?,?,?,?)`, id, int(outbound.OutboundTypeOpenAIChat), oldURLs, `[]`, override).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := addChannelConnections(database); err != nil {
		t.Fatal(err)
	}
	var first, second model.Channel
	if err := database.First(&first, 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.First(&second, 2).Error; err != nil {
		t.Fatal(err)
	}
	if first.ConnectionConfig == nil || first.ConnectionConfig.Endpoints[0].URL != "https://example.com/v1/chat/completions" {
		t.Fatalf("missing equivalent migration %+v", first.ConnectionConfig)
	}
	if second.ConnectionConfig != nil {
		t.Fatal("inherited group policy migrated")
	}
	var raw string
	database.Raw(`SELECT base_urls FROM channels WHERE id=1`).Scan(&raw)
	if raw != oldURLs {
		t.Fatal("rollback snapshot changed")
	}
	first.ConnectionConfig.Endpoints[0].URL = "https://manual.test/v1/chat/completions"
	payload, _ := json.Marshal(first.ConnectionConfig)
	database.Exec(`UPDATE channels SET connection_config=? WHERE id=1`, string(payload))
	if err := addChannelConnections(database); err != nil {
		t.Fatal(err)
	}
	database.First(&first, 1)
	if first.ConnectionConfig.Endpoints[0].URL != "https://manual.test/v1/chat/completions" {
		t.Fatal("migration overwrote manual config")
	}
	if !database.Migrator().HasColumn(&model.ChannelProbeResult{}, "EndpointID") {
		t.Fatal("missing endpoint evidence column")
	}
}
