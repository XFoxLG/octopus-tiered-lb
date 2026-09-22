package relaylog

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
)

// listOrderIDs 提取列表结果按展示顺序排列的 ID 序列。
func listOrderIDs(logs []model.RelayLogListItem) []int64 {
	ids := make([]int64, 0, len(logs))
	for _, item := range logs {
		ids = append(ids, item.ID)
	}
	return ids
}

// assertIDOrder 断言 ID 序列完全一致（顺序敏感）。
func assertIDOrder(t *testing.T, got []int64, want ...int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestRelayLogListOrdersByTimeNotEnqueueOrder 回归：晚入队但时间旧的日志
// （挂起请求 11 分钟后经 saveUnclaimedRelayTrace 兜底落库，或队列丢弃补写）
// ID 最大但 Time 最旧，列表曾按 ID 降序把它卡在顶部，展示时间乱序。
// 列表统一按 (time DESC, id DESC) 排序后，展示顺序 = 时间顺序。
func TestRelayLogListOrdersByTimeNotEnqueueOrder(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "relaylog-order.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := setting.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache failed: %v", err)
	}
	// 关闭日志保存：只读内存缓存，覆盖缓存路径的排序。
	if err := setting.SetString(model.SettingKeyRelayLogKeepEnabled, "false"); err != nil {
		t.Fatalf("disable relay log keep failed: %v", err)
	}

	// 缓存序 = 入队序：ID 5（Time 最旧）最后入队。旧行为下它会排最前。
	restore := SetCacheForTest([]model.RelayLog{
		{ID: 1, Time: 100, RequestModelName: "m1"},
		{ID: 2, Time: 300, RequestModelName: "m2"},
		{ID: 3, Time: 200, RequestModelName: "m3"},
		{ID: 5, Time: 50, RequestModelName: "late-persisted"},
	})
	t.Cleanup(restore)

	logs, err := RelayLogList(context.Background(), LogFilter{}, 1, 50)
	if err != nil {
		t.Fatalf("RelayLogList returned error: %v", err)
	}
	assertIDOrder(t, listOrderIDs(logs), 2, 3, 1, 5)
}

// TestRelayLogListDBOrdersByTimeNotID 覆盖 DB 路径：落库晚于同批日志的
// 兜底记录（新 ID + 旧 Time）不再按 ID 排到顶部。
func TestRelayLogListDBOrdersByTimeNotID(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "relaylog-order-db.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	// 显式声明共用主库模式，清除前序「独立日志库」测试残留的全局状态。
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

	// ID 4 = 晚落库的兜底日志：ID 最大、Time 最旧。
	seed := []model.RelayLog{
		{ID: 1, Time: 100, RequestModelName: "m1"},
		{ID: 2, Time: 300, RequestModelName: "m2"},
		{ID: 3, Time: 200, RequestModelName: "m3"},
		{ID: 4, Time: 50, RequestModelName: "late-persisted"},
	}
	if err := db.GetDB().Create(&seed).Error; err != nil {
		t.Fatalf("seed relay logs failed: %v", err)
	}

	logs, err := RelayLogList(context.Background(), LogFilter{}, 1, 50)
	if err != nil {
		t.Fatalf("RelayLogList returned error: %v", err)
	}
	assertIDOrder(t, listOrderIDs(logs), 2, 3, 1, 4)
}

// TestRelayLogListCacheDBBoundarySorted 覆盖缓存+DB 混合路径：缓存里挂着
// 旧时间的晚入队日志、DB 里有更新时间的日志时，两路合并后仍严格按时间降序，
// 不再出现"缓存条目卡在列表顶部、后面跟着更新的 DB 条目"的乱序。
func TestRelayLogListCacheDBBoundarySorted(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "relaylog-order-merge.db")
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

	// DB 已落库 400/350（新），缓存里还挂着晚入队的 100（旧）+ 500（新）。
	// 旧行为：缓存全排 DB 前 → 100(旧) 卡在 350/400 之上。
	seed := []model.RelayLog{
		{ID: 1, Time: 400, RequestModelName: "db-newest"},
		{ID: 2, Time: 350, RequestModelName: "db-newer"},
	}
	if err := db.GetDB().Create(&seed).Error; err != nil {
		t.Fatalf("seed relay logs failed: %v", err)
	}
	restore := SetCacheForTest([]model.RelayLog{
		{ID: 3, Time: 500, RequestModelName: "cache-newest"},
		{ID: 4, Time: 100, RequestModelName: "cache-late-persisted"},
	})
	t.Cleanup(restore)

	logs, err := RelayLogList(context.Background(), LogFilter{}, 1, 50)
	if err != nil {
		t.Fatalf("RelayLogList returned error: %v", err)
	}
	assertIDOrder(t, listOrderIDs(logs), 3, 1, 2, 4)
}

// TestRelayLogListSameSecondTiesByID 覆盖同秒并列：时间相同按 ID 降序，
// 保证同秒内日志以入队序（完成序）稳定展示。
func TestRelayLogListSameSecondTiesByID(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "relaylog-order-ties.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := setting.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache failed: %v", err)
	}
	if err := setting.SetString(model.SettingKeyRelayLogKeepEnabled, "false"); err != nil {
		t.Fatalf("disable relay log keep failed: %v", err)
	}

	restore := SetCacheForTest([]model.RelayLog{
		{ID: 11, Time: 100},
		{ID: 33, Time: 100},
		{ID: 22, Time: 100},
	})
	t.Cleanup(restore)

	logs, err := RelayLogList(context.Background(), LogFilter{}, 1, 50)
	if err != nil {
		t.Fatalf("RelayLogList returned error: %v", err)
	}
	assertIDOrder(t, listOrderIDs(logs), 33, 22, 11)
}
