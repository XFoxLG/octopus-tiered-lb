package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func TestChannelProbeEvidenceMigrationPreservesHistory(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(t.TempDir()+"/probe084.db"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	connection, _ := database.DB()
	defer connection.Close()

	legacyTable := `CREATE TABLE channel_probe_results(
		id INTEGER PRIMARY KEY, run_id INTEGER, channel_id INTEGER, model_name TEXT,
		kind TEXT, item TEXT, verdict TEXT, status_code INTEGER, latency_ms INTEGER,
		protocol TEXT, endpoint_id TEXT, endpoint_url TEXT, key_scope TEXT,
		request_stream BOOLEAN, request_budget_ms INTEGER, request_fingerprint TEXT,
		summary TEXT, detail TEXT, created_at DATETIME
	)`
	if err := database.Exec(legacyTable).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO channel_probe_results
		(id, run_id, channel_id, model_name, kind, item, verdict, status_code, latency_ms, created_at)
		VALUES(1, 1, 1, 'm', 'capability', 'streaming', 'pass', 200, 10, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := addChannelProbeEvidence(database); err != nil {
			t.Fatalf("migration attempt %d: %v", attempt, err)
		}
	}
	var history model.ChannelProbeResult
	if err := database.First(&history, 1).Error; err != nil {
		t.Fatal(err)
	}
	if history.Evidence != nil {
		t.Fatalf("historical row gained inferred evidence: %+v", history.Evidence)
	}

	first := int64(12)
	completed := int64(18)
	fresh := model.ChannelProbeResult{
		RunID:     2,
		ChannelID: 1,
		ModelName: "m",
		Kind:      model.ProbeKindCapability,
		Item:      model.ProbeItemStreaming,
		Verdict:   model.ProbeVerdictPass,
		Evidence: &model.ChannelProbeEvidence{
			Source:                   "saved",
			ConfigurationFingerprint: "fingerprint",
			Transport:                "direct",
			Protocol:                 "chat",
			FirstBodyByteMS:          &first,
			TerminalMS:               &completed,
			TerminalObserved:         true,
			TerminalKind:             "finish_reason",
			GenerationState:          "completed",
			DoneObserved:             true,
			FinishReasons:            map[string]string{"0": "stop"},
		},
	}
	if err := database.Create(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	var back model.ChannelProbeResult
	if err := database.First(&back, fresh.ID).Error; err != nil {
		t.Fatal(err)
	}
	if back.Evidence == nil || back.Evidence.FirstBodyByteMS == nil || *back.Evidence.FirstBodyByteMS != 12 ||
		!back.Evidence.TerminalObserved || back.Evidence.FinishReasons["0"] != "stop" {
		t.Fatalf("structured evidence was not persisted: %+v", back.Evidence)
	}
}
