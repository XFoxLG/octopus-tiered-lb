package backup

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func TestTaskConnectionAndProbeEvidenceBackupV5RoundTrip(t *testing.T) {
	source := initBackupTestDB(t, "tasks-source.db")
	if err := db.InitLogDB("", "", false); err != nil {
		t.Fatal(err)
	}
	if err := source.AutoMigrate(&model.ChannelProbeRun{}, &model.ChannelProbeResult{}); err != nil {
		t.Fatal(err)
	}
	config := &model.ConnectionConfig{Version: 2, Selection: "configured", Catalog: model.ModelCatalog{Format: "manual"}, Endpoints: []model.ChannelEndpoint{{ID: "task-chat", Protocol: "chat", URL: "https://fixture.example/v1", URLMode: "base", Auth: "bearer", ForwardMode: "convert", Transport: "catie_tasks", TaskAPIURL: "https://fixture.example/v1/openai/tasks", Headers: []model.CustomHeader{{HeaderKey: "X-Fixture", HeaderValue: "preserved"}}}}}
	ch := model.Channel{ID: 8, Name: "synthetic-tasks", Type: outbound.OutboundTypeOpenAIChat, ConnectionConfig: config}
	if err := source.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	run := model.ChannelProbeRun{ID: 31, ChannelID: 8, ModelName: "full-prefixed-model"}
	if err := source.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	evidence := &model.ChannelProbeEvidence{Source: "draft", Transport: "catie_tasks", EndpointID: "task-chat", Protocol: "chat", Model: "full-prefixed-model", KeyID: 61, TerminalObserved: true, GenerationState: "completed", Network: &model.RelayNetworkDiagnostics{TimingSource: "task_submission", PreparationAt: 1000, DispatchAt: 1001, ResponseHeadersAt: 1002, FirstBodyByteAt: 1050}, Task: &model.RelayTaskDiagnostics{Transport: "catie_tasks", Status: "completed", Done: true, Cursor: 2, PollCount: 1, CancelState: "not_requested", DeadlineAt: 1801000}}
	probe := model.ChannelProbeResult{ID: 32, RunID: 31, ChannelID: 8, ModelName: "full-prefixed-model", Kind: model.ProbeKindCapability, Item: model.ProbeItemStreaming, Verdict: model.ProbeVerdictPass, Evidence: evidence}
	if err := source.Create(&probe).Error; err != nil {
		t.Fatal(err)
	}
	entry := model.RelayLog{ID: 91, Diagnostics: &model.RelayDiagnostics{Version: 3, SelectedAttempt: 1, Task: evidence.Task, Network: evidence.Network, Probe: evidence}}
	if err := source.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	dump, err := ExportAll(context.Background(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if dump.Version != dbDumpVersion {
		t.Fatalf("backup version = %d, want %d", dump.Version, dbDumpVersion)
	}
	if len(dump.RelayLogs) != 1 || dump.RelayLogs[0].ID != entry.ID {
		t.Fatalf("exported log diagnostics missing: %+v", dump.RelayLogs)
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	var restored model.DBDump
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{model.ImportModeIncremental, model.ImportModeFull} {
		t.Run(mode, func(t *testing.T) {
			target := backupReliabilityTarget(t, "tasks-target.db")
			if err := target.AutoMigrate(&model.ChannelProbeRun{}, &model.ChannelProbeResult{}); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if _, err := ImportWithModeToDB(context.Background(), target, &restored, mode); err != nil {
					t.Fatal(err)
				}
			}
			var ch model.Channel
			var probe model.ChannelProbeResult
			var log model.RelayLog
			if err := target.First(&ch, 8).Error; err != nil {
				t.Fatal(err)
			}
			if err := target.First(&probe, 32).Error; err != nil {
				t.Fatal(err)
			}
			if err := target.First(&log, 91).Error; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(ch.ConnectionConfig, config) || !reflect.DeepEqual(probe.Evidence, evidence) || !reflect.DeepEqual(log.Diagnostics, entry.Diagnostics) {
				t.Fatalf("configuration/observations changed: %+v %+v %+v", ch.ConnectionConfig, probe.Evidence, log.Diagnostics)
			}
			for _, row := range []any{&model.Channel{}, &model.ChannelProbeResult{}, &model.RelayLog{}} {
				var count int64
				target.Model(row).Count(&count)
				if count != 1 {
					t.Fatalf("%T count=%d", row, count)
				}
			}
		})
	}
}
