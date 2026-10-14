package relaylog

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
)

// TestRelayLogListCarriesTokenEstimates 回归 v2.8.13：估算列必须同时出现在
// 缓存路径（ToListItem）与数据库路径（显式 Select 列清单），否则会出现
// 「详情页有估算、列表页没有」。
func TestRelayLogListCarriesTokenEstimates(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "relaylog-estimate.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	if err := db.InitLogDB("", "", false); err != nil {
		t.Fatalf("InitLogDB(shared) failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := setting.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache failed: %v", err)
	}
	if err := setting.SetString(model.SettingKeyRelayLogKeepEnabled, "true"); err != nil {
		t.Fatalf("enable relay log keep failed: %v", err)
	}

	// 数据库路径：直接落库一行（绕过缓存）。
	if err := db.GetDB().Create(&model.RelayLog{
		ID:               901,
		Time:             10,
		RequestModelName: "gemini-3.1-pro",
		InputTokens:      106_140,
		OutputTokens:     0,
		OutputTokensEst:  234,
	}).Error; err != nil {
		t.Fatalf("seed relay log failed: %v", err)
	}
	// 缓存路径：只在内存缓存里放一行（不入库）。
	restore := SetCacheForTest([]model.RelayLog{{
		ID:               902,
		Time:             20,
		RequestModelName: "gemini-3.1-pro",
		InputTokens:      0,
		OutputTokens:     0,
		InputTokensEst:   88_000,
		OutputTokensEst:  22_000,
	}})
	t.Cleanup(restore)

	logs, err := RelayLogList(context.Background(), LogFilter{}, 1, 50)
	if err != nil {
		t.Fatalf("RelayLogList returned error: %v", err)
	}
	byID := make(map[int64]model.RelayLogListItem, len(logs))
	for _, listItem := range logs {
		byID[listItem.ID] = listItem
	}

	dbRow, ok := byID[901]
	if !ok {
		t.Fatal("db row missing from list")
	}
	if dbRow.InputTokensEst != 0 || dbRow.OutputTokensEst != 234 {
		t.Fatalf("db path estimates = (%d, %d), want (0, 234)", dbRow.InputTokensEst, dbRow.OutputTokensEst)
	}

	cachedRow, ok := byID[902]
	if !ok {
		t.Fatal("cached row missing from list")
	}
	if cachedRow.InputTokensEst != 88_000 || cachedRow.OutputTokensEst != 22_000 {
		t.Fatalf("cache path estimates = (%d, %d), want (88000, 22000)", cachedRow.InputTokensEst, cachedRow.OutputTokensEst)
	}
}
