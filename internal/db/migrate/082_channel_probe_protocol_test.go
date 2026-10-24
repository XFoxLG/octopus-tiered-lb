package migrate

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

// TestChannelProbeProtocolMigrationKeepsHistoryUnknown 验证迁移 082：
//   - 069 时期的旧表能补上协议元数据列；
//   - 存量行补列后这些列保持 NULL（不回填猜测），UI 才有「未记录」可显示；
//   - 重复执行幂等，且不会在重跑时回填历史行；
//   - 新行能正常写入协议、地址、Key 范围、stream、预算与请求指纹。
func TestChannelProbeProtocolMigrationKeepsHistoryUnknown(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(t.TempDir()+"/probe082.db"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	connection, _ := database.DB()
	defer connection.Close()

	legacyTable := `CREATE TABLE channel_probe_results(id INTEGER PRIMARY KEY, run_id INTEGER, channel_id INTEGER, model_name TEXT, kind TEXT, item TEXT, verdict TEXT, status_code INTEGER, latency_ms INTEGER, summary TEXT, detail TEXT, endpoint_id TEXT, created_at DATETIME)`
	if err := database.Exec(legacyTable).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO channel_probe_results VALUES(1,1,1,'m','protocol','protocol_chat','pass',200,10,'200 OK','','chat-one',CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatal(err)
	}

	if err := migrateChannelProbeProtocolMetadata(database); err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	for _, column := range []string{"protocol", "endpoint_url", "key_scope", "request_stream", "request_budget_ms", "request_fingerprint"} {
		if !database.Migrator().HasColumn(&model.ChannelProbeResult{}, column) {
			t.Fatalf("column %s was not added", column)
		}
	}

	type protocolMetadata struct {
		Protocol           *string `gorm:"column:protocol"`
		EndpointURL        *string `gorm:"column:endpoint_url"`
		KeyScope           *string `gorm:"column:key_scope"`
		RequestStream      *bool   `gorm:"column:request_stream"`
		RequestBudgetMS    *int64  `gorm:"column:request_budget_ms"`
		RequestFingerprint *string `gorm:"column:request_fingerprint"`
	}
	history := protocolMetadata{}
	if err := database.Table("channel_probe_results").Where("id = ?", 1).Take(&history).Error; err != nil {
		t.Fatal(err)
	}
	if history.Protocol != nil || history.EndpointURL != nil || history.KeyScope != nil ||
		history.RequestStream != nil || history.RequestBudgetMS != nil || history.RequestFingerprint != nil {
		t.Fatalf("historical row gained inferred metadata: %+v", history)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := migrateChannelProbeProtocolMetadata(database); err != nil {
			t.Fatalf("idempotent run %d failed: %v", attempt, err)
		}
	}
	after := protocolMetadata{}
	if err := database.Table("channel_probe_results").Where("id = ?", 1).Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.Protocol != nil || after.KeyScope != nil {
		t.Fatalf("rerun backfilled history: %+v", after)
	}

	stream := false
	budget := int64(25000)
	fresh := model.ChannelProbeResult{
		RunID:              2,
		ChannelID:          1,
		ModelName:          "m",
		Kind:               model.ProbeKindProtocol,
		Item:               model.ProbeItemProtocolResponses,
		Verdict:            model.ProbeVerdictPass,
		Protocol:           "responses",
		EndpointID:         "ep-1",
		EndpointURL:        "https://example.test/v1/responses",
		KeyScope:           "key_index:0,key_id:9",
		RequestStream:      &stream,
		RequestBudgetMS:    &budget,
		RequestFingerprint: strings.Repeat("a", 64),
	}
	if err := database.Create(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	var back model.ChannelProbeResult
	if err := database.Where("id = ?", fresh.ID).Take(&back).Error; err != nil {
		t.Fatal(err)
	}
	if back.Protocol != "responses" || back.EndpointID != "ep-1" || back.EndpointURL != "https://example.test/v1/responses" ||
		back.KeyScope != "key_index:0,key_id:9" || back.RequestStream == nil || *back.RequestStream ||
		back.RequestBudgetMS == nil || *back.RequestBudgetMS != 25000 || back.RequestFingerprint != strings.Repeat("a", 64) {
		t.Fatalf("new probe metadata was not persisted: %+v", back)
	}
}
