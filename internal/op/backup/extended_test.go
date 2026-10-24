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
