package stats

import (
	"context"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/store"
	"github.com/redis/go-redis/v9"
)

// Legacy scopes remain test fixtures only. Production statistics must not read,
// write, or delete these unacknowledged Redis deltas.
const (
	statsScopeTotal             = "total"
	statsScopeDaily             = "daily"
	statsScopeHourly            = "hourly"
	statsScopeChannel           = "channel"
	statsScopeModel             = "model"
	statsScopeAPIKey            = "apikey"
	statsScopeDailyChannel      = "daily_channel"
	statsScopeDailyModel        = "daily_model"
	statsScopeDailyAPIKey       = "daily_apikey"
	statsScopeDailyChannelModel = "daily_channel_model"
	statsIDTotal                = "1"
)

// newStatsTestRedis 启动 miniredis 并注入到 store，返回 miniredis 实例。
func newStatsTestRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	restoreStore := store.InjectForTest(c)
	t.Cleanup(func() {
		restoreStore()
		_ = c.Close()
		mr.Close()
	})
	return mr
}

// TestStatsRedisIncrAndGet 验证 Redis 增量累加：两次 IncrMetrics 后 GetMetrics 应为和。
func TestStatsRedisIncrAndGet(t *testing.T) {
	newStatsTestRedis(t)
	ss := store.GetStats()
	ctx := context.Background()

	m1 := model.StatsMetrics{InputToken: 100, OutputToken: 50, InputCost: 1.5, RequestSuccess: 3}
	m2 := model.StatsMetrics{InputToken: 200, OutputToken: 70, InputCost: 2.0, RequestSuccess: 7, LatencyP95: 800}

	if err := ss.IncrMetrics(ctx, statsScopeChannel, "42", m1); err != nil {
		t.Fatalf("IncrMetrics m1: %v", err)
	}
	if err := ss.IncrMetrics(ctx, statsScopeChannel, "42", m2); err != nil {
		t.Fatalf("IncrMetrics m2: %v", err)
	}

	got, err := ss.GetMetrics(ctx, statsScopeChannel, "42")
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if got.InputToken != 300 {
		t.Fatalf("InputToken = %d, want 300", got.InputToken)
	}
	if got.OutputToken != 120 {
		t.Fatalf("OutputToken = %d, want 120", got.OutputToken)
	}
	if got.RequestSuccess != 10 {
		t.Fatalf("RequestSuccess = %d, want 10", got.RequestSuccess)
	}
	// 浮点累加
	if got.InputCost < 3.49 || got.InputCost > 3.51 {
		t.Fatalf("InputCost = %v, want ~3.5", got.InputCost)
	}
	// max 字段：m2 的 LatencyP95=800 应胜出（初始 0）
	if got.LatencyP95 != 800 {
		t.Fatalf("LatencyP95 = %d, want 800 (max)", got.LatencyP95)
	}
}

// TestStatsRedisIncrMaxField 验证 Lua max 脚本：多次写入取最大值。
func TestStatsRedisIncrMaxField(t *testing.T) {
	newStatsTestRedis(t)
	ss := store.GetStats()
	ctx := context.Background()

	// 依次写入递增的 LatencyP99，最终应为最大值 999。
	for _, p99 := range []int64{100, 500, 999, 300, 50} {
		m := model.StatsMetrics{LatencyP99: p99, FtutP50: p99 / 2}
		if err := ss.IncrMetrics(ctx, statsScopeModel, "1", m); err != nil {
			t.Fatalf("IncrMetrics p99=%d: %v", p99, err)
		}
	}
	got, err := ss.GetMetrics(ctx, statsScopeModel, "1")
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if got.LatencyP99 != 999 {
		t.Fatalf("LatencyP99 = %d, want 999 (max)", got.LatencyP99)
	}
	// FtutP50: 写入 50,250,499,150,25 -> max 499
	if got.FtutP50 != 499 {
		t.Fatalf("FtutP50 = %d, want 499 (max)", got.FtutP50)
	}
}

// TestStatsRedisSnapshotAll 验证全量快照读取多个 scope id。
func TestStatsRedisSnapshotAll(t *testing.T) {
	newStatsTestRedis(t)
	ss := store.GetStats()
	ctx := context.Background()

	for _, id := range []int{1, 2, 3} {
		m := model.StatsMetrics{InputToken: int64(id * 10), RequestSuccess: int64(id)}
		if err := ss.IncrMetrics(ctx, statsScopeChannel, strconv.Itoa(id), m); err != nil {
			t.Fatalf("IncrMetrics %d: %v", id, err)
		}
	}

	snaps, err := ss.SnapshotAll(ctx, statsScopeChannel)
	if err != nil {
		t.Fatalf("SnapshotAll: %v", err)
	}
	if len(snaps) != 3 {
		t.Fatalf("SnapshotAll returned %d entries, want 3", len(snaps))
	}
	if snaps["2"].InputToken != 20 {
		t.Fatalf("snap[2].InputToken = %d, want 20", snaps["2"].InputToken)
	}
}

// TestStatsRedisDelete 验证 scope:id 删除。
func TestStatsRedisDelete(t *testing.T) {
	newStatsTestRedis(t)
	ss := store.GetStats()
	ctx := context.Background()

	_ = ss.IncrMetrics(ctx, statsScopeAPIKey, "7", model.StatsMetrics{InputToken: 100})
	if err := ss.Delete(ctx, statsScopeAPIKey, "7"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err := ss.GetMetrics(ctx, statsScopeAPIKey, "7")
	if err != nil {
		t.Fatalf("GetMetrics after delete: %v", err)
	}
	if got.InputToken != 0 {
		t.Fatalf("InputToken after delete = %d, want 0", got.InputToken)
	}
}

func TestStatsUpdatesDoNotWriteRedis(t *testing.T) {
	initializeStatsPersistenceTest(t, false)
	redisServer := newStatsTestRedis(t)
	commandsBefore := redisServer.CommandCount()
	first := model.StatsMetrics{InputToken: 100, RequestSuccess: 1}
	second := model.StatsMetrics{InputToken: 50, RequestSuccess: 1}
	recordSnapshotMetricsForTest(t, first)
	recordSnapshotMetricsForTest(t, second)
	assertSnapshotMetricsForTest(t, model.StatsMetrics{InputToken: 150, RequestSuccess: 2})
	if commandsAfter := redisServer.CommandCount(); commandsAfter != commandsBefore {
		t.Fatalf("stats updates sent %d Redis commands, want none", commandsAfter-commandsBefore)
	}
	if keys := redisServer.Keys(); len(keys) != 0 {
		t.Fatalf("stats updates created Redis keys: %v", keys)
	}
}
