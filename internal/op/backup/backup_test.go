package backup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	internaldb "github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func loadBackupSource(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	src, err := os.ReadFile(filepath.Clean(filepath.Join(filepath.Dir(file), "backup.go")))
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	return string(src)
}

func TestFullImportDeleteOrderUsesChannelGroupsTable(t *testing.T) {
	text := loadBackupSource(t)
	if strings.Contains(text, `"group_items", "group_channel_items", "groups"`) {
		t.Fatal("delete order still references legacy group_channel_items table")
	}
	if !strings.Contains(text, `"group_items", "channel_groups", "groups"`) {
		t.Fatal("delete order does not include channel_groups between group_items and groups")
	}
}

func TestBackupIncludesCircuitBreakerStates(t *testing.T) {
	text := loadBackupSource(t)
	if !strings.Contains(text, `Find(&d.CircuitBreakerStates)`) {
		t.Fatal("ExportAll does not export circuit_breaker_states")
	}
	if !strings.Contains(text, `"audit_logs", "auto_strategy_states", "circuit_breaker_states"`) {
		t.Fatal("full import delete order does not clear runtime or circuit_breaker_states")
	}
	if !strings.Contains(text, `doNothing("circuit_breaker_states", &dump.CircuitBreakerStates, len(dump.CircuitBreakerStates))`) {
		t.Fatal("ImportWithMode does not restore circuit_breaker_states")
	}
}

func TestBackupIncludesAPICredentialProfiles(t *testing.T) {
	text := loadBackupSource(t)
	if !strings.Contains(text, "Find(&d.APICredentialProfiles)") {
		t.Fatal("ExportAll does not export api_credential_profiles")
	}
	if !strings.Contains(text, "api_credential_profiles") {
		t.Fatal("full import does not handle api_credential_profiles")
	}
}

func TestImportWithModeFullClearsExistingRowsUsingActualTableNames(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "backup.db")
	if err := internaldb.InitDB("sqlite", dbPath, false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = internaldb.Close()
	})

	dbConn := internaldb.GetDB()
	legacyChannel := model.Channel{ID: 1, Name: "legacy-channel", Type: outbound.OutboundTypeOpenAIChat, BaseUrls: []model.BaseUrl{{URL: "https://legacy.example.com"}}}
	legacyGroup := model.Group{ID: 1, Name: "legacy-group", Mode: model.GroupModeRoundRobin, EndpointType: model.EndpointTypeChat}
	legacyRuntime := model.AutoStrategyState{Key: "legacy", ChannelID: 1, ModelName: "gpt-4o", UpdatedAt: 1}
	legacyStats := model.StatsTotal{ID: 1}

	for _, row := range []any{&legacyChannel, &legacyGroup, &legacyRuntime, &legacyStats} {
		if err := dbConn.Create(row).Error; err != nil {
			t.Fatalf("seed legacy row: %v", err)
		}
	}

	dump := &model.DBDump{
		Version:       1,
		Channels:      []model.Channel{{ID: 2, Name: "new-channel", Type: outbound.OutboundTypeOpenAIChat, BaseUrls: []model.BaseUrl{{URL: "https://new.example.com"}}}},
		Groups:        []model.Group{{ID: 2, Name: "new-group", Mode: model.GroupModeRandom, EndpointType: model.EndpointTypeChat}},
		RuntimeStates: []model.AutoStrategyState{{Key: "new", ChannelID: 2, ModelName: "gpt-4.1", UpdatedAt: 2}},
		IncludeStats:  true,
		StatsTotal:    []model.StatsTotal{{ID: 2}},
	}

	if _, err := ImportWithMode(context.Background(), dump, model.ImportModeFull); err != nil {
		t.Fatalf("full import: %v", err)
	}

	assertCount := func(modelValue any, expected int64, where string, args ...any) {
		t.Helper()
		var count int64
		query := dbConn.Model(modelValue)
		if where != "" {
			query = query.Where(where, args...)
		}
		if err := query.Count(&count).Error; err != nil {
			t.Fatalf("count %T: %v", modelValue, err)
		}
		if count != expected {
			t.Fatalf("count %T = %d, want %d", modelValue, count, expected)
		}
	}

	assertCount(&model.Channel{}, 0, "id = ?", 1)
	assertCount(&model.Channel{}, 1, "id = ?", 2)
	assertCount(&model.Group{}, 0, "id = ?", 1)
	assertCount(&model.Group{}, 1, "id = ?", 2)
	assertCount(&model.AutoStrategyState{}, 0, "key = ?", "legacy")
	assertCount(&model.AutoStrategyState{}, 1, "key = ?", "new")
	assertCount(&model.StatsTotal{}, 0, "id = ?", 1)
	assertCount(&model.StatsTotal{}, 1, "id = ?", 2)
}

func TestExportImportSeparateLogDBRoundTrip(t *testing.T) {
	mainPath := filepath.Join(t.TempDir(), "main.db")
	if err := internaldb.InitDB("sqlite", mainPath, false); err != nil {
		t.Fatalf("init main db: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "logs.db")
	if err := internaldb.InitLogDB("sqlite", logPath, false); err != nil {
		t.Fatalf("init log db: %v", err)
	}
	t.Cleanup(func() { _ = internaldb.Close() })

	// Seed relay logs into the separate log DB (not the main DB).
	logConn := internaldb.GetLogDB()
	seed := []model.RelayLog{
		{ID: 1, Time: 1, RequestModelName: "m1"},
		{ID: 2, Time: 2, RequestModelName: "m2"},
	}
	if err := logConn.Create(&seed).Error; err != nil {
		t.Fatalf("seed log db: %v", err)
	}

	// Export must read relay_logs from the log DB.
	dump, err := ExportAll(context.Background(), true, false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(dump.RelayLogs) != 2 {
		t.Fatalf("exported relay logs = %d, want 2 (must read from log DB)", len(dump.RelayLogs))
	}

	// Clear the log DB, then full-import: logs must be force-written back to log DB.
	if err := logConn.Where("1 = 1").Delete(&model.RelayLog{}).Error; err != nil {
		t.Fatalf("clear log db: %v", err)
	}
	if _, err := ImportWithMode(context.Background(), dump, model.ImportModeFull); err != nil {
		t.Fatalf("full import: %v", err)
	}

	var logCount int64
	if err := internaldb.GetLogDB().Model(&model.RelayLog{}).Count(&logCount).Error; err != nil {
		t.Fatalf("count log db after import: %v", err)
	}
	if logCount != 2 {
		t.Fatalf("log DB relay log count after import = %d, want 2", logCount)
	}

	// Logs must NOT have leaked into the main DB.
	var mainCount int64
	if err := internaldb.GetDB().Model(&model.RelayLog{}).Count(&mainCount).Error; err != nil {
		t.Fatalf("count main db: %v", err)
	}
	if mainCount != 0 {
		t.Fatalf("main DB relay log count = %d, want 0 (logs must stay in log DB)", mainCount)
	}
}

func TestImportForceReopensClosedLogDB(t *testing.T) {
	mainPath := filepath.Join(t.TempDir(), "main.db")
	if err := internaldb.InitDB("sqlite", mainPath, false); err != nil {
		t.Fatalf("init main db: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "logs.db")
	if err := internaldb.InitLogDB("sqlite", logPath, false); err != nil {
		t.Fatalf("init log db: %v", err)
	}
	t.Cleanup(func() { _ = internaldb.Close() })

	// Simulate logs disabled: log DB disconnected.
	if err := internaldb.CloseLogDB(); err != nil {
		t.Fatalf("close log db: %v", err)
	}
	if internaldb.GetLogDB() != nil {
		t.Fatalf("precondition: log DB should be disconnected")
	}

	dump := &model.DBDump{
		Version:     1,
		IncludeLogs: true,
		RelayLogs:   []model.RelayLog{{ID: 9, Time: 9, RequestModelName: "forced"}},
	}
	if _, err := ImportWithMode(context.Background(), dump, model.ImportModeFull); err != nil {
		t.Fatalf("full import: %v", err)
	}

	// Import must have force-reopened the log DB and written the row.
	logConn := internaldb.GetLogDB()
	if logConn == nil {
		t.Fatalf("log DB should be reconnected after import")
	}
	var count int64
	if err := logConn.Model(&model.RelayLog{}).Where("id = ?", 9).Count(&count).Error; err != nil {
		t.Fatalf("count log db: %v", err)
	}
	if count != 1 {
		t.Fatalf("forced relay log count = %d, want 1", count)
	}
}

// TestFullImportPreservesUsers verifies that a full-mode restore does NOT
// delete the users table. User.Password is json:"-", so backups carry no
// password hashes — deleting users would leave an empty table and lock out
// the admin. The users table is auth infrastructure and must survive.
func TestFullImportPreservesUsers(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "users.db")
	if err := internaldb.InitDB("sqlite", dbPath, false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = internaldb.Close() })

	dbConn := internaldb.GetDB()

	// Seed an admin user (password hash is irrelevant — we only check survival).
	admin := model.User{ID: 1, Username: "admin", Password: "$2a$10$hash", Role: model.UserRoleAdmin}
	if err := dbConn.Create(&admin).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	// Full import with an empty dump (simulates a backup that excluded users).
	emptyDump := &model.DBDump{Version: 1}
	if _, err := ImportWithMode(context.Background(), emptyDump, model.ImportModeFull); err != nil {
		t.Fatalf("full import: %v", err)
	}

	var count int64
	if err := dbConn.Model(&model.User{}).Count(&count).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Fatalf("users count = %d, want 1 (full restore must not wipe users)", count)
	}

	var got model.User
	if err := dbConn.First(&got, 1).Error; err != nil {
		t.Fatalf("fetch admin: %v", err)
	}
	if got.Username != "admin" {
		t.Fatalf("admin username = %q, want admin", got.Username)
	}
}

// TestProxyFieldsExportRoundTrip verifies that custom proxy addresses survive
// a JSON export → import cycle. The field was previously json:"-", so backup
// files silently dropped custom proxy configurations (including credentials
// like socks5://user:pass@host).
func TestProxyFieldsExportRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "proxy.db")
	if err := internaldb.InitDB("sqlite", dbPath, false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = internaldb.Close() })

	dbConn := internaldb.GetDB()

	channelProxy := "socks5://user:pass@proxy.example.com:1080"

	// Seed a channel with a custom proxy address.
	ch := model.Channel{ID: 1, Name: "proxy-channel", Type: outbound.OutboundTypeOpenAIChat, BaseUrls: []model.BaseUrl{{URL: "https://api.example.com"}}, ChannelProxy: &channelProxy}
	if err := dbConn.Create(&ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	// Export and JSON round-trip (simulates backup file serialization).
	dump, err := ExportAll(context.Background(), false, false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	jsonBytes, err := json.Marshal(dump)
	if err != nil {
		t.Fatalf("marshal dump: %v", err)
	}
	var roundTripped model.DBDump
	if err := json.Unmarshal(jsonBytes, &roundTripped); err != nil {
		t.Fatalf("unmarshal dump: %v", err)
	}

	// Verify proxy fields survived the JSON round-trip.
	if len(roundTripped.Channels) != 1 || roundTripped.Channels[0].ChannelProxy == nil || *roundTripped.Channels[0].ChannelProxy != channelProxy {
		t.Fatalf("channel proxy lost in export: %+v", roundTripped.Channels)
	}
}
