package relaylog

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
)

// TestRelayLogListFiltersByImageCount 覆盖 v2.8.14 的 has_image 过滤：数据库
// 路径与缓存路径都必须正确区分「含图 / 无图」，并回归列表 Select 是否带出
// image_count（漏列时数据库路径会丢标注）。
func TestRelayLogListFiltersByImageCount(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "relaylog-image.db")
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

	// 数据库路径（绕过缓存）：一行含图、一行无图。
	if err := db.GetDB().Create(&model.RelayLog{
		ID: 811, Time: 10, RequestModelName: "m", ImageCount: 2,
	}).Error; err != nil {
		t.Fatalf("seed image row failed: %v", err)
	}
	if err := db.GetDB().Create(&model.RelayLog{
		ID: 812, Time: 11, RequestModelName: "m",
	}).Error; err != nil {
		t.Fatalf("seed plain row failed: %v", err)
	}

	// 缓存路径：只在内存缓存里放一行含图日志（不入库）。
	restore := SetCacheForTest([]model.RelayLog{{
		ID: 813, Time: 12, RequestModelName: "m", ImageCount: 1,
	}})
	t.Cleanup(restore)

	withImage := true
	logs, err := RelayLogList(context.Background(), LogFilter{HasImage: &withImage}, 1, 50)
	if err != nil {
		t.Fatalf("RelayLogList(with) returned error: %v", err)
	}
	byID := make(map[int64]model.RelayLogListItem, len(logs))
	for _, item := range logs {
		byID[item.ID] = item
	}
	if _, ok := byID[811]; !ok {
		t.Fatal("db image row missing from has_image=true result")
	}
	if _, ok := byID[813]; !ok {
		t.Fatal("cached image row missing from has_image=true result")
	}
	if _, ok := byID[812]; ok {
		t.Fatal("plain row must not match has_image=true")
	}
	if got := byID[811].ImageCount; got != 2 {
		t.Fatalf("db row ImageCount = %d, want 2 (列表 Select 漏列 image_count?)", got)
	}
	if got := byID[813].ImageCount; got != 1 {
		t.Fatalf("cached row ImageCount = %d, want 1 (ToListItem 漏映射?)", got)
	}

	withoutImage := false
	logs, err = RelayLogList(context.Background(), LogFilter{HasImage: &withoutImage}, 1, 50)
	if err != nil {
		t.Fatalf("RelayLogList(without) returned error: %v", err)
	}
	byID = make(map[int64]model.RelayLogListItem, len(logs))
	for _, item := range logs {
		byID[item.ID] = item
	}
	if _, ok := byID[812]; !ok {
		t.Fatal("plain row missing from has_image=false result")
	}
	if _, ok := byID[811]; ok {
		t.Fatal("image row must not match has_image=false")
	}
	if _, ok := byID[813]; ok {
		t.Fatal("cached image row must not match has_image=false")
	}
}
