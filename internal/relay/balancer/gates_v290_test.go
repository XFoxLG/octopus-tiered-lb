package balancer

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/model"
)

// v2.9.0 候选闸门断言（计划「候选闸门四态」的迭代器层）。
//
// 实现边界（诚实声明）：四态闸门（渠道禁用 / 并发占满 / Key 冷却 / 额度类限流）
// 的判定发生在 relay.executeRelay 的候选循环内，单测无法跑真实 executeRelay
// （需要 DB/cache/HTTP）。本文件断言的是迭代器层不变量：
//   - 被闸门跳过的候选绝不产生真实转发记录（ForwardedAttempts 不增长）；
//   - 每条跳过都带可读原因（Msg），并写入渠道×模型的「最近决策原因」画像；
//   - 熔断跳过（SkipCircuitBreak）与普通 Skip 同口径。
//
// 四态判定在 executeRelay 里的接线（含 exhausted 时 lastErr 带原因、对外
// fail-closed 502 不泄漏内部细节）由 relay 包 routing_gates_v290_test.go 的
// 源码守卫断言；底层组件行为（Key 冷却挡住同模型、并发槽满员拒绝）另有既有
// 用例：balancer/key_cooldown_test.go、op/ratelimitstore/channel_test.go。

// TestRoutingGateSkipsRecordReasonAndNeverForward：四态闸门逐一跳过全部候选时，
// 迭代器必须留下带原因的跳过记录且零真实转发（不静默降级到不可用渠道）。
func TestRoutingGateSkipsRecordReasonAndNeverForward(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	modelName := fmt.Sprintf("gate-skip-%d", time.Now().UnixNano())
	group := model.Group{
		Mode: model.GroupModeFailover,
		Items: []model.GroupItem{
			{ChannelID: 920001, ModelName: modelName, Weight: 100, Priority: 1},
			{ChannelID: 920002, ModelName: modelName, Weight: 100, Priority: 2},
			{ChannelID: 920003, ModelName: modelName, Weight: 100, Priority: 3},
			{ChannelID: 920004, ModelName: modelName, Weight: 100, Priority: 4},
		},
	}
	it := NewIterator(group, 0, modelName, nil)

	// 与 executeRelay 循环里四态闸门完全一致的跳过原因文本：
	// 渠道禁用 / 并发占满 / Key 冷却耗尽（额度类）/ 渠道级限流冷却（额度类映射）。
	reasons := []string{
		"channel disabled",
		"channel concurrency limit reached (3/3)",
		"no available key (all keys in cooldown or disabled)",
		"channel rate limit active, remaining cooldown: 42s",
	}

	visited := 0
	for it.Next() {
		if visited >= len(reasons) {
			t.Fatalf("iterator yielded more candidates than seeded gate reasons")
		}
		item := it.Item()
		it.Skip(item.ChannelID, 0, fmt.Sprintf("ch-%d", item.ChannelID), reasons[visited])
		visited++
	}
	if visited != len(reasons) {
		t.Fatalf("visited %d candidates, want %d", visited, len(reasons))
	}

	// 不静默降级：被闸门跳过的候选没有产生任何真实转发。
	if got := it.ForwardedAttempts(); got != 0 {
		t.Fatalf("ForwardedAttempts = %d, want 0 (skipped candidates must never be forwarded)", got)
	}

	attempts := it.Attempts()
	if len(attempts) != len(reasons) {
		t.Fatalf("len(Attempts()) = %d, want %d", len(attempts), len(reasons))
	}
	for i, attempt := range attempts {
		if attempt.Status != model.AttemptSkipped {
			t.Fatalf("attempt[%d].Status = %v, want AttemptSkipped", i, attempt.Status)
		}
		if attempt.Msg != reasons[i] {
			t.Fatalf("attempt[%d].Msg = %q, want %q", i, attempt.Msg, reasons[i])
		}
		if attempt.ChannelID != group.Items[i].ChannelID {
			t.Fatalf("attempt[%d].ChannelID = %d, want %d", i, attempt.ChannelID, group.Items[i].ChannelID)
		}
	}
	if got := it.LastAttemptNumber(); got != len(reasons) {
		t.Fatalf("LastAttemptNumber = %d, want %d", got, len(reasons))
	}

	// 渠道画像：最后一个候选的「最近决策原因」= 最后一条跳过原因。
	stats := getOrCreateStats(920004, modelName)
	if reason, at := stats.lastDecision(); reason != reasons[3] || at.IsZero() {
		t.Fatalf("lastDecision = (%q, %v), want (%q, non-zero time)", reason, at, reasons[3])
	}
}

// TestSkipCircuitBreakRecordsReasonAndNeverForward：熔断候选同属「硬闸门」——
// 跳过必须留痕（AttemptCircuitBreak + 冷却时间）、写画像、且零转发。
func TestSkipCircuitBreakRecordsReasonAndNeverForward(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	modelName := fmt.Sprintf("gate-cb-%d", time.Now().UnixNano())
	const channelID = 920010
	const keyID = 7
	t.Cleanup(func() {
		RemoveChannelEntries(channelID)
		RemoveChannelStats(channelID)
	})

	group := model.Group{
		Mode:  model.GroupModeFailover,
		Items: []model.GroupItem{{ChannelID: channelID, ModelName: modelName, Weight: 100, Priority: 1}},
	}
	it := NewIterator(group, 0, modelName, nil)
	if !it.Next() {
		t.Fatal("iterator has no candidates")
	}

	// 未熔断：返回 false 且不产生记录。
	if it.SkipCircuitBreak(channelID, keyID, "ch-cb", modelName) {
		t.Fatal("SkipCircuitBreak = true before any failures; circuit must be closed")
	}
	if got := len(it.Attempts()); got != 0 {
		t.Fatalf("len(Attempts()) = %d before trip, want 0", got)
	}

	// 造熔断：默认阈值 5，多打一次确保越线。
	for i := 0; i < 6; i++ {
		RecordFailure(channelID, keyID, modelName)
	}
	tripped, remaining := IsTripped(channelID, keyID, modelName)
	if !tripped {
		t.Fatal("precondition: circuit breaker should have tripped")
	}
	if remaining <= 0 {
		t.Fatalf("precondition: remaining cooldown = %v, want > 0", remaining)
	}

	if !it.SkipCircuitBreak(channelID, keyID, "ch-cb", modelName) {
		t.Fatal("SkipCircuitBreak = false while tripped; tripped candidate must be skipped")
	}
	attempts := it.Attempts()
	if len(attempts) != 1 {
		t.Fatalf("len(Attempts()) = %d, want 1", len(attempts))
	}
	if attempts[0].Status != model.AttemptCircuitBreak {
		t.Fatalf("Status = %v, want AttemptCircuitBreak", attempts[0].Status)
	}
	if !strings.Contains(attempts[0].Msg, "circuit breaker tripped") {
		t.Fatalf("Msg = %q, want it to contain %q", attempts[0].Msg, "circuit breaker tripped")
	}
	if !strings.Contains(attempts[0].Msg, "remaining cooldown:") {
		t.Fatalf("Msg = %q, want it to include remaining cooldown", attempts[0].Msg)
	}
	if got := it.ForwardedAttempts(); got != 0 {
		t.Fatalf("ForwardedAttempts = %d, want 0", got)
	}

	// 画像：熔断跳过同样写入「最近决策原因」。
	stats := getOrCreateStats(channelID, modelName)
	if reason, at := stats.lastDecision(); !strings.Contains(reason, "circuit breaker tripped") || at.IsZero() {
		t.Fatalf("lastDecision = (%q, %v), want circuit-breaker reason with non-zero time", reason, at)
	}

	// nil receiver 防御：不得 panic，已熔断时照实返回 true。
	var nilIt *Iterator
	if !nilIt.SkipCircuitBreak(channelID, keyID, "ch-cb", modelName) {
		t.Fatal("nil iterator with tripped circuit must report true")
	}
}
