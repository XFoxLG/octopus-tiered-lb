package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	internaldb "github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func backupReliabilityTarget(t *testing.T, name string) *gorm.DB {
	t.Helper()
	target, err := internaldb.OpenStandalone("sqlite", filepath.Join(t.TempDir(), name), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := target.DB()
		_ = sqlDB.Close()
	})
	if err := internaldb.Migrate(target); err != nil {
		t.Fatal(err)
	}
	// These evidence tables are created by after-migrations in production. The
	// process-global migration registry is intentionally one-shot, so a standalone
	// test target opened after the source may not receive them from Migrate.
	if err := target.AutoMigrate(
		&model.GroupHealthSnapshot{},
		&model.GroupHealthAttempt{},
	); err != nil {
		t.Fatal(err)
	}
	return target
}

func seedBackupReliabilityRows(t *testing.T, conn *gorm.DB) {
	t.Helper()
	if err := conn.AutoMigrate(
		&model.GroupHealthSnapshot{},
		&model.GroupHealthAttempt{},
		&model.AIRouteTask{},
	); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 28, 1, 2, 3, 0, time.UTC)
	snapshot := model.GroupHealthSnapshot{
		ID:                  41,
		GroupID:             7,
		GroupName:           "rp",
		GroupMode:           model.GroupModeFailover,
		ProbeMode:           model.GroupHealthProbeModeFull,
		RequestModel:        "client-model",
		Status:              model.GroupHealthStatusPartial,
		StartedAt:           started,
		DurationMS:          0,
		SuccessfulChannelID: nil,
		Message:             "retained",
	}
	attempt := model.GroupHealthAttempt{
		ID:           51,
		SnapshotID:   41,
		GroupItemID:  8,
		ChannelID:    9,
		ChannelName:  "provider",
		ChannelKeyID: 0,
		KeyRemark:    "",
		ModelName:    "provider-model",
		Priority:     0,
		Weight:       0,
		Status:       model.GroupHealthAttemptStatusSkipped,
		HTTPStatus:   0,
		DurationMS:   0,
	}
	task := model.AIRouteTask{
		ID:               "task-v5",
		Scope:            model.AIRouteScopeGroup,
		GroupID:          7,
		Status:           "completed",
		CurrentStep:      "done",
		ProgressPercent:  100,
		TotalBatches:     1,
		CompletedBatches: 1,
		Done:             false,
		ResultReady:      false,
		MessageArgs:      map[string]any{"count": 0, "ok": false},
		ErrorReasonArgs:  map[string]any{"retry": 0},
		StartedAt:        &started,
		EventSequence:    0,
		Summary:          &model.GenerateAIRouteProgressSummary{},
	}
	for _, row := range []any{&snapshot, &attempt, &task} {
		if err := conn.Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}
}

func TestBackupV5GroupHealthAndRouteTasksRoundTrip(t *testing.T) {
	source := initBackupTestDB(t, "v5-source.db")
	seedBackupReliabilityRows(t, source)

	dump, err := ExportAll(context.Background(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if dump.Version != 5 {
		t.Fatalf("dump version = %d, want 5", dump.Version)
	}
	if len(dump.GroupHealthSnapshots) != 1 ||
		len(dump.GroupHealthAttempts) != 1 ||
		len(dump.AIRouteTasks) != 1 {
		t.Fatalf(
			"v5 counts snapshots=%d attempts=%d tasks=%d",
			len(dump.GroupHealthSnapshots),
			len(dump.GroupHealthAttempts),
			len(dump.AIRouteTasks),
		)
	}

	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	var restored model.DBDump
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	target := backupReliabilityTarget(t, "v5-target.db")
	result, err := ImportWithModeToDB(
		context.Background(),
		target,
		&restored,
		model.ImportModeFull,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"group_health_snapshots",
		"group_health_attempts",
		"ai_route_tasks",
	} {
		if result.RowsAffected[table] != 1 {
			t.Fatalf("%s rows affected = %d, want 1", table, result.RowsAffected[table])
		}
	}

	var gotSnapshot model.GroupHealthSnapshot
	var gotAttempt model.GroupHealthAttempt
	var gotTask model.AIRouteTask
	if err := target.First(&gotSnapshot, 41).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.First(&gotAttempt, 51).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.First(&gotTask, "id = ?", "task-v5").Error; err != nil {
		t.Fatal(err)
	}
	if gotSnapshot.DurationMS != 0 ||
		gotSnapshot.SuccessfulChannelID != nil ||
		gotSnapshot.Message != "retained" {
		t.Fatalf("snapshot changed: %#v", gotSnapshot)
	}
	if gotAttempt.ChannelKeyID != 0 ||
		gotAttempt.Priority != 0 ||
		gotAttempt.Weight != 0 ||
		gotAttempt.HTTPStatus != 0 ||
		gotAttempt.DurationMS != 0 {
		t.Fatalf("attempt default-zero values changed: %#v", gotAttempt)
	}
	if gotTask.Done ||
		gotTask.ResultReady ||
		gotTask.EventSequence != 0 ||
		gotTask.MessageArgs["count"].(float64) != 0 ||
		gotTask.MessageArgs["ok"].(bool) ||
		gotTask.ErrorReasonArgs["retry"].(float64) != 0 {
		t.Fatalf("route task zero/false values changed: %#v", gotTask)
	}
}

func TestHistoricalFullRestorePreservesV5OnlyTables(t *testing.T) {
	target := backupReliabilityTarget(t, "historical-target.db")
	seedBackupReliabilityRows(t, target)

	for _, version := range []int{3, 4} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			dump := &model.DBDump{
				Version:      version,
				IncludeLogs:  false,
				IncludeStats: false,
			}
			if _, err := ImportWithModeToDB(
				context.Background(),
				target,
				dump,
				model.ImportModeFull,
			); err != nil {
				t.Fatal(err)
			}
			for _, row := range []struct {
				name  string
				model any
				where string
				args  []any
			}{
				{"snapshots", &model.GroupHealthSnapshot{}, "id = ?", []any{41}},
				{"attempts", &model.GroupHealthAttempt{}, "id = ?", []any{51}},
				{"tasks", &model.AIRouteTask{}, "id = ?", []any{"task-v5"}},
			} {
				var count int64
				if err := target.Model(row.model).Where(row.where, row.args...).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Fatalf("v%d unexpectedly cleared %s: count=%d", version, row.name, count)
				}
			}
		})
	}
}

func TestV5FullRestoreTreatsEmptyEvidenceTablesAsAuthoritative(t *testing.T) {
	target := backupReliabilityTarget(t, "v5-empty-target.db")
	seedBackupReliabilityRows(t, target)

	dump := &model.DBDump{Version: 5}
	if _, err := ImportWithModeToDB(
		context.Background(),
		target,
		dump,
		model.ImportModeFull,
	); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.GroupHealthSnapshot{},
		&model.GroupHealthAttempt{},
		&model.AIRouteTask{},
	} {
		var count int64
		if err := target.Model(row).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("v5 empty full restore left %d rows in %T", count, row)
		}
	}
}

func TestV5FullRestoreSkipsUnavailableOptionalEvidenceTable(t *testing.T) {
	target := backupReliabilityTarget(t, "v5-partial-schema.db")
	seedBackupReliabilityRows(t, target)
	if err := target.Migrator().DropTable(&model.AIRouteTask{}); err != nil {
		t.Fatal(err)
	}

	started := time.Date(2026, 10, 28, 2, 3, 4, 0, time.UTC)
	dump := &model.DBDump{
		Version: 5,
		GroupHealthSnapshots: []model.GroupHealthSnapshot{{
			ID:           42,
			GroupID:      7,
			GroupName:    "partial-schema",
			GroupMode:    model.GroupModeFailover,
			ProbeMode:    model.GroupHealthProbeModeFull,
			RequestModel: "client-model",
			Status:       model.GroupHealthStatusSuccess,
			StartedAt:    started,
		}},
		AIRouteTasks: []model.AIRouteTask{{ID: "ignored-without-table"}},
	}
	if _, err := ImportWithModeToDB(
		context.Background(),
		target,
		dump,
		model.ImportModeFull,
	); err != nil {
		t.Fatal(err)
	}

	var snapshot model.GroupHealthSnapshot
	if err := target.First(&snapshot, 42).Error; err != nil {
		t.Fatal(err)
	}
	if snapshot.GroupName != "partial-schema" {
		t.Fatalf("snapshot group name = %q", snapshot.GroupName)
	}
}

func TestSeparateLogDBRestoreReportsAndRetainsRows(t *testing.T) {
	mainPath := filepath.Join(t.TempDir(), "sequence-main.db")
	logPath := filepath.Join(t.TempDir(), "sequence-log.db")
	if err := internaldb.InitDB("sqlite", mainPath, false); err != nil {
		t.Fatal(err)
	}
	if err := internaldb.InitLogDB("sqlite", logPath, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = internaldb.Close() })

	dump := &model.DBDump{
		Version:     5,
		IncludeLogs: true,
		RelayLogs: []model.RelayLog{
			{ID: 900, Time: 1, RequestModelName: "model-900"},
			{ID: 901, Time: 2, RequestModelName: "model-901"},
		},
	}
	result, err := ImportWithMode(
		context.Background(),
		dump,
		model.ImportModeFull,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.RowsAffected["relay_logs"] != 2 {
		t.Fatalf("separate log DB rows affected = %d, want 2", result.RowsAffected["relay_logs"])
	}
	var count int64
	if err := internaldb.GetLogDB().Model(&model.RelayLog{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("separate log DB relay log count = %d, want 2", count)
	}
}

func TestSynchronizeImportedSequencesPostgres(t *testing.T) {
	dsn := os.Getenv("OCTOPUS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL test database not configured")
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" {
		t.Fatal("test PostgreSQL must be local, never a production endpoint")
	}
	conn, err := internaldb.OpenStandalone("postgres", dsn, false)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	schema := fmt.Sprintf("backup_sequence_%d", time.Now().UnixNano())
	if err := conn.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Exec("DROP SCHEMA " + schema + " CASCADE").Error })
	if err := conn.Exec("SET search_path TO " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(`
CREATE TABLE backup_sequence_probe (
	id BIGSERIAL PRIMARY KEY,
	value TEXT NOT NULL
)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(`
INSERT INTO backup_sequence_probe (id, value)
VALUES (10, 'explicit-10'), (25, 'explicit-25')`).Error; err != nil {
		t.Fatal(err)
	}

	cfg := &importConfig{
		conn: conn,
		res: &model.DBImportResult{
			Progress: []model.DBImportStep{{
				Table:        "backup_sequence_probe",
				Mode:         "insert",
				RowsAffected: 2,
				OK:           true,
			}},
		},
	}
	if err := synchronizeImportedSequences(cfg); err != nil {
		t.Fatal(err)
	}

	var nextID int64
	if err := conn.Raw(
		"INSERT INTO backup_sequence_probe (value) VALUES ('after-restore') RETURNING id",
	).Scan(&nextID).Error; err != nil {
		t.Fatal(err)
	}
	if nextID != 26 {
		t.Fatalf("next explicit-id sequence value = %d, want 26", nextID)
	}
}
