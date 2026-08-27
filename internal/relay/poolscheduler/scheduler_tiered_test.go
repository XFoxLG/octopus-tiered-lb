package poolscheduler

import (
	"testing"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/pool"
)

// setTieredStats 直接注入账号 EWMA 统计，模拟历史成功/失败轨迹。
func setTieredStats(t *testing.T, poolID, accountID int, errorRate float64) {
	t.Helper()
	val, _ := globalPoolStats.LoadOrStore(statsKey(poolID, accountID), &accountStats{})
	stats := val.(*accountStats)
	stats.mu.Lock()
	stats.errorRate = errorRate
	stats.mu.Unlock()
}

func tieredCandidates(poolID int, ids ...int) []model.PoolAccount {
	candidates := make([]model.PoolAccount, 0, len(ids))
	for _, id := range ids {
		candidates = append(candidates, model.PoolAccount{ID: id})
	}
	return candidates
}

// 健康层优先：高错误账号即使 EWMA 层内会被选中（分数更低），也必须被沉层跳过。
func TestSelectByTieredAdaptive_PrefersHealthyTier(t *testing.T) {
	poolID, _ := setupSchedulerPoolDB(t)
	a1 := addAccount(t, poolID, &model.PoolAccount{Name: "flaky"})
	a2 := addAccount(t, poolID, &model.PoolAccount{Name: "solid"})
	setTieredStats(t, poolID, a1, 0.5) // 观察层
	setTieredStats(t, poolID, a2, 0.1) // 健康层

	got := selectByTieredAdaptive(tieredCandidates(poolID, a1, a2), poolID)
	if got.ID != a2 {
		t.Fatalf("tiered_adaptive should prefer healthy account %d, got %d", a2, got.ID)
	}
}

// 健康层为空时必须回退全量 EWMA（保证有返回），选择分数最低者。
func TestSelectByTieredAdaptive_FallsBackWhenAllUnhealthy(t *testing.T) {
	poolID, _ := setupSchedulerPoolDB(t)
	a1 := addAccount(t, poolID, &model.PoolAccount{Name: "bad1"})
	a2 := addAccount(t, poolID, &model.PoolAccount{Name: "bad2"})
	setTieredStats(t, poolID, a1, 0.9) // 分数 0.63
	setTieredStats(t, poolID, a2, 0.5) // 分数 0.35 → 全量 EWMA 下最优

	got := selectByTieredAdaptive(tieredCandidates(poolID, a1, a2), poolID)
	if got.ID != a2 {
		t.Fatalf("all-unhealthy should fall back to best EWMA score %d, got %d", a2, got.ID)
	}
}

// 无统计的新账号视为健康层（冷启动可被选中），并优先于带错误历史的老账号。
func TestSelectByTieredAdaptive_NoStatsTreatedHealthy(t *testing.T) {
	poolID, _ := setupSchedulerPoolDB(t)
	a1 := addAccount(t, poolID, &model.PoolAccount{Name: "brand-new"})
	a2 := addAccount(t, poolID, &model.PoolAccount{Name: "recovering"})
	setTieredStats(t, poolID, a2, 0.3) // 刚失败过，观察层

	got := selectByTieredAdaptive(tieredCandidates(poolID, a1, a2), poolID)
	if got.ID != a1 {
		t.Fatalf("no-stats new account %d should be treated healthy and win, got %d", a1, got.ID)
	}
}

// 健康层内 EWMA 打分生效：错误率更低者胜。
func TestSelectByTieredAdaptive_EWMAScoreWithinHealthyTier(t *testing.T) {
	poolID, _ := setupSchedulerPoolDB(t)
	a1 := addAccount(t, poolID, &model.PoolAccount{Name: "ok-1"})
	a2 := addAccount(t, poolID, &model.PoolAccount{Name: "ok-2"})
	setTieredStats(t, poolID, a1, 0.10)
	setTieredStats(t, poolID, a2, 0.02)

	got := selectByTieredAdaptive(tieredCandidates(poolID, a1, a2), poolID)
	if got.ID != a2 {
		t.Fatalf("lower error rate within healthy tier should win (%d), got %d", a2, got.ID)
	}
}

// 阈值边界：errorRate 恰等于 tieredHealthyErrorRate 仍属健康层。
func TestSelectByTieredAdaptive_ThresholdBoundaryInclusive(t *testing.T) {
	poolID, _ := setupSchedulerPoolDB(t)
	a1 := addAccount(t, poolID, &model.PoolAccount{Name: "boundary"})
	setTieredStats(t, poolID, a1, tieredHealthyErrorRate)

	got := selectByTieredAdaptive(tieredCandidates(poolID, a1), poolID)
	if got.ID != a1 {
		t.Fatalf("errorRate == threshold should stay healthy, got account %d", got.ID)
	}
}

// 健康层内 weight tiebreaker 与 ewma 策略同语义：高 weight 减分更多。
func TestSelectByTieredAdaptive_WeightTiebreakerWithinTier(t *testing.T) {
	poolID, _ := setupSchedulerPoolDB(t)
	a1 := addAccount(t, poolID, &model.PoolAccount{Name: "low-weight"})
	a2 := addAccount(t, poolID, &model.PoolAccount{Name: "high-weight"})
	setTieredStats(t, poolID, a1, 0.0)
	setTieredStats(t, poolID, a2, 0.0)

	got := selectByTieredAdaptive([]model.PoolAccount{
		{ID: a1, Weight: 0},
		{ID: a2, Weight: 5},
	}, poolID)
	if got.ID != a2 {
		t.Fatalf("higher weight should win within healthy tier, got %d", got.ID)
	}
}

// selectByStrategy 分发：池策略为 tiered_adaptive 时走分层选择（而非回落 ewma）。
func TestSelectByStrategy_TieredAdaptiveDispatch(t *testing.T) {
	poolID, _ := setupSchedulerPoolDB(t)
	a1 := addAccount(t, poolID, &model.PoolAccount{Name: "flaky"})
	a2 := addAccount(t, poolID, &model.PoolAccount{Name: "solid"})
	setTieredStats(t, poolID, a1, 0.9)
	setTieredStats(t, poolID, a2, 0.0)

	// 把池策略改为 tiered_adaptive。
	if err := pool.UpdatePool(poolID, map[string]interface{}{"strategy": "tiered_adaptive"}); err != nil {
		t.Fatalf("update pool strategy: %v", err)
	}
	got := selectByStrategy(tieredCandidates(poolID, a1, a2), poolID)
	if got.ID != a2 {
		t.Fatalf("strategy dispatch should route to tiered_adaptive and pick %d, got %d", a2, got.ID)
	}
}
