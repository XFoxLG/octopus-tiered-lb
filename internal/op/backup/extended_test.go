package backup

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
)

func TestBackupExtendedConfigurationRoundTrip(t *testing.T) {
	conn := initBackupTestDB(t, "extended-source.db")
	now := time.Now().UTC().Truncate(time.Millisecond)
	stream := false
	budget := int64(0)
	rows := []any{
		&model.ProxyConfiguration{ID: 12, Name: "proxy", URL: "http://example.invalid:8080", Enabled: true},
		&model.ModelPriceSchedule{ID: 10, Name: "schedule", RuleValue: "gemini", Enabled: true},
		&model.ModelPriceCategory{ID: 11, Name: "category", RuleValue: "gemini", Enabled: false},
		&model.ModelMapping{ID: 13, Name: "mapping", Pattern: "client", TargetModel: "provider", MatchType: model.MatchExact},
		&model.Channel{ID: 20, Name: "proxy-channel", ProxyMode: model.ProxyUsageModePool, ProxyConfigID: func() *int { n := 12; return &n }()},
		&model.Group{ID: 21, Name: "rp", RequestTimeoutSeconds: 900, StreamKeepaliveEnabled: true, AssistantPrefillCompat: true, ForwardMode: "convert"},
		&model.ChannelModelLimit{ID: 22, ChannelID: 20, ModelName: "gemini", MaxContextTokens: 32768, Source: "manual"},
		&model.ChannelModelCapability{ID: 23, ChannelID: 20, ModelName: "gemini", Capability: model.CapabilityToolCalling, Supported: false, Source: "manual"},
		&model.ChannelProbeRun{ID: 24, ChannelID: 20, ModelName: "gemini", StartedAt: now},
		&model.ChannelProbeResult{ID: 25, RunID: 24, ChannelID: 20, ModelName: "gemini", Kind: model.ProbeKindProtocol, Item: model.ProbeItemProtocolChat, Verdict: model.ProbeVerdictPass, Protocol: "chat", RequestStream: &stream, RequestBudgetMS: &budget, RequestFingerprint: "fingerprint", CreatedAt: now},
		&model.StatsDailyChannel{Date: "20261025", ChannelID: 20, StatsMetrics: model.StatsMetrics{RequestSuccess: 3}},
		&model.StatsDailyModel{Date: "20261025", ModelName: "gemini", StatsMetrics: model.StatsMetrics{OutputToken: 19}},
		&model.StatsDailyAPIKey{Date: "20261025", APIKeyID: 1, StatsMetrics: model.StatsMetrics{InputToken: 17}},
		&model.StatsDailyChannelModel{Date: "20261025", ChannelID: 20, ModelName: "gemini", StatsMetrics: model.StatsMetrics{InputCost: 0.2}},
		&model.ErrorLog{ID: 26, Time: now.Unix(), Message: "synthetic error"},
	}
	for _, row := range rows {
		if err := conn.Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}
	if err := conn.Model(&model.ProxyConfiguration{}).Where("id = ?", 12).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Model(&model.ModelPriceSchedule{}).Where("id = ?", 10).Updates(map[string]any{"off_peak_mul": 0, "window1_start": 0, "window1_end": 0, "window2_start": 0, "window2_end": 0}).Error; err != nil {
		t.Fatal(err)
	}
	dump, err := ExportAll(context.Background(), true, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	var restored model.DBDump
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.ProxyConfigurations) != 1 || len(restored.ModelPriceSchedules) != 1 || len(restored.ChannelProbeRuns) != 1 || len(restored.ChannelProbeResults) != 1 || len(restored.ChannelModelLimits) != 1 || len(restored.ErrorLogs) != 1 {
		t.Fatalf("backup counts: proxy=%d schedule=%d runs=%d results=%d limits=%d errors=%d", len(restored.ProxyConfigurations), len(restored.ModelPriceSchedules), len(restored.ChannelProbeRuns), len(restored.ChannelProbeResults), len(restored.ChannelModelLimits), len(restored.ErrorLogs))
	}
	target, err := db.OpenStandalone("sqlite", filepath.Join(t.TempDir(), "extended-target.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := target.DB(); _ = sqlDB.Close() })
	if err := db.Migrate(target); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportWithModeToDB(context.Background(), target, &restored, model.ImportModeFull); err != nil {
		t.Fatal(err)
	}
	var proxy model.ProxyConfiguration
	var schedule model.ModelPriceSchedule
	var group model.Group
	var result model.ChannelProbeResult
	for _, row := range []struct {
		dest any
		id   int
	}{{&proxy, 12}, {&schedule, 10}, {&group, 21}, {&result, 25}} {
		if err := target.First(row.dest, row.id).Error; err != nil {
			t.Fatal(err)
		}
	}
	if proxy.Enabled || schedule.OffPeakMul != 0 || schedule.Window1Start != 0 || schedule.Window1End != 0 || schedule.Window2Start != 0 || schedule.Window2End != 0 {
		t.Fatal("restore replaced saved false/0 values with model defaults")
	}
	if group.RequestTimeoutSeconds != 900 || !group.StreamKeepaliveEnabled || !group.AssistantPrefillCompat || group.ForwardMode != "convert" {
		t.Fatal("restore lost P0 lifecycle configuration")
	}
	if result.Protocol != "chat" || result.RequestStream == nil || *result.RequestStream || result.RequestBudgetMS == nil || *result.RequestBudgetMS != 0 || result.RequestFingerprint != "fingerprint" {
		t.Fatal("restore lost protocol evidence or false/zero presence")
	}
	for _, value := range []any{&model.StatsDailyChannel{}, &model.StatsDailyModel{}, &model.StatsDailyAPIKey{}, &model.StatsDailyChannelModel{}, &model.ErrorLog{}} {
		var count int64
		if err := target.Model(value).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("restore %T count=%d error=%v", value, count, err)
		}
	}
}

func TestHistoricalBackupPreservesUnrepresentedConfiguration(t *testing.T) {
	for _, version := range []int{3, 4} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			conn := initBackupTestDB(t, "legacy-extended.db")
			row := model.ProxyConfiguration{ID: 1, Name: "preserve", URL: "http://example.invalid:8080", Enabled: true}
			if err := conn.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := ImportWithMode(context.Background(), &model.DBDump{Version: version}, model.ImportModeFull); err != nil {
				t.Fatal(err)
			}
			var count int64
			conn.Model(&model.ProxyConfiguration{}).Count(&count)
			if version == 3 && count != 1 || version == 4 && count != 0 {
				t.Fatalf("version %d left %d proxy rows", version, count)
			}
		})
	}
}

func TestBackupRejectsFutureVersionBeforeMutation(t *testing.T) {
	conn := initBackupTestDB(t, "future.db")
	row := model.Channel{ID: 1, Name: "keep"}
	if err := conn.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ImportWithMode(context.Background(), &model.DBDump{Version: dbDumpVersion + 1}, model.ImportModeFull); err == nil {
		t.Fatal("accepted a future backup version")
	}
	var count int64
	if err := conn.Model(&model.Channel{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatal("future backup changed target data")
	}
}

// TestRestorePreservesZeroValueColumnsWithModelDefaults 覆盖 GORM 的
// DefaultValueInterface 陷阱：核心表（api_keys / groups / channels 等）的
// struct 批量插入会把零值列替换成模型默认值，于是备份里显式保存的
// enabled=false、hedge_delay_ms=0、relay_retry_count=-1 会在恢复后静默改变。
//
// 这类列必须原样往返。测试同时覆盖非零值，确保修复没有把自增主键或
// 显式 ID 写坏。
func TestRestorePreservesZeroValueColumnsWithModelDefaults(t *testing.T) {
	conn := initBackupTestDB(t, "zero-value-source.db")

	// 先按各字段的非零形态建行，再用 map 更新写回目标零值——
	// 这样种子数据本身不会被 GORM 的默认值替换影响。
	disabledKey := model.APIKey{ID: 7, Name: "disabled", APIKey: "sk-octopus-test-value", APIKeyHash: "hash", Enabled: true}
	if err := conn.Create(&disabledKey).Error; err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	if err := conn.Model(&model.APIKey{}).Where("id = ?", 7).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable api key: %v", err)
	}

	group := model.Group{
		ID:                8,
		Name:              "hedge",
		HedgeEnabled:      true,
		HedgeDelayMs:      300,
		RelayRetryCount:   2,
		RelayRouteRetries: 3,
	}
	if err := conn.Create(&group).Error; err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if err := conn.Model(&model.Group{}).Where("id = ?", 8).Updates(map[string]any{
		"hedge_delay_ms":      0,
		"relay_retry_count":   -1,
		"relay_route_retries": -1,
	}).Error; err != nil {
		t.Fatalf("zero group defaults: %v", err)
	}

	channel := model.Channel{ID: 9, Name: "direct-channel", ProxyMode: model.ProxyUsageModePool}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := conn.Model(&model.Channel{}).Where("id = ?", 9).Update("proxy_mode", "").Error; err != nil {
		t.Fatalf("blank channel proxy mode: %v", err)
	}

	dump, err := ExportAll(context.Background(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	var restored model.DBDump
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}

	target, err := db.OpenStandalone("sqlite", filepath.Join(t.TempDir(), "zero-value-target.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := target.DB(); _ = sqlDB.Close() })
	if err := db.Migrate(target); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportWithModeToDB(context.Background(), target, &restored, model.ImportModeFull); err != nil {
		t.Fatal(err)
	}

	var key model.APIKey
	if err := target.First(&key, 7).Error; err != nil {
		t.Fatalf("reload api key: %v", err)
	}
	if key.Enabled {
		t.Error("restore turned a disabled API key back on (enabled=false replaced by model default true)")
	}

	var gotGroup model.Group
	if err := target.First(&gotGroup, 8).Error; err != nil {
		t.Fatalf("reload group: %v", err)
	}
	if gotGroup.HedgeDelayMs != 0 {
		t.Errorf("hedge_delay_ms = %d, want 0", gotGroup.HedgeDelayMs)
	}
	if gotGroup.RelayRetryCount != -1 {
		t.Errorf("relay_retry_count = %d, want -1", gotGroup.RelayRetryCount)
	}
	if gotGroup.RelayRouteRetries != -1 {
		t.Errorf("relay_route_retries = %d, want -1", gotGroup.RelayRouteRetries)
	}
	if !gotGroup.HedgeEnabled {
		t.Error("hedge_enabled = false, want true")
	}

	var gotChannel model.Channel
	if err := target.First(&gotChannel, 9).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if gotChannel.ProxyMode != "" {
		t.Errorf("proxy_mode = %q, want empty (not replaced by default 'direct')", gotChannel.ProxyMode)
	}
}

// TestRestoreKeepsAutoIncrementIDsWorking 确认精确值路径不会把自增主键
// 钉死在 0：零值主键仍交由数据库分配，导入后新写入的行能拿到新 ID。
func TestRestoreKeepsAutoIncrementIDsWorking(t *testing.T) {
	conn := initBackupTestDB(t, "autoincrement-source.db")
	channels := []model.Channel{
		{ID: 5, Name: "first"},
		{ID: 6, Name: "second"},
	}
	if err := conn.Create(&channels).Error; err != nil {
		t.Fatal(err)
	}
	dump, err := ExportAll(context.Background(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	var restored model.DBDump
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	target, err := db.OpenStandalone("sqlite", filepath.Join(t.TempDir(), "autoincrement-target.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := target.DB(); _ = sqlDB.Close() })
	if err := db.Migrate(target); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportWithModeToDB(context.Background(), target, &restored, model.ImportModeFull); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := target.Model(&model.Channel{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("restored channel count=%d error=%v, want 2 explicit-ID rows", count, err)
	}
	fresh := model.Channel{Name: "fresh"}
	if err := target.Create(&fresh).Error; err != nil {
		t.Fatalf("insert after restore: %v", err)
	}
	if fresh.ID == 0 {
		t.Error("database did not assign an auto-increment ID after restore")
	}
}
