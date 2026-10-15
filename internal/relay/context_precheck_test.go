package relay

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/op/channelmodellimit"
	"github.com/lingyuins/octopus/internal/op/setting"
)

func setupPrecheckTestDB(t *testing.T) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "context-precheck.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := setting.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache failed: %v", err)
	}
}

// TestContextPrecheckEvaluateBoundaries 验证请求前预检的边界语义：
// 超过已知上限 → 排除候选；恰好等于上限 → 不排除；≥90% 上限 → 仅告警；
// 未知上限 / 关闭预检 / 无估算 → 全部 no-op。
func TestContextPrecheckEvaluateBoundaries(t *testing.T) {
	setupPrecheckTestDB(t)
	ctx := context.Background()
	const channelID = 901
	const modelName = "precheck-model"
	if err := channelmodellimit.SetManual(ctx, channelID, modelName, 1000); err != nil {
		t.Fatalf("SetManual failed: %v", err)
	}

	plan := &contextPrecheckPlan{enabled: true, warnThreshold: 0.9, estimatedInput: 1001}
	over, limit, source, warn := plan.evaluate(channelID, modelName)
	if !over || warn || limit != 1000 || source != "manual" {
		t.Fatalf("over-limit evaluate = (%v,%d,%q,%v), want (true,1000,manual,false)", over, limit, source, warn)
	}

	// 恰好等于上限：不排除，但已到告警阈值。
	plan.estimatedInput = 1000
	over, _, _, warn = plan.evaluate(channelID, modelName)
	if over || !warn {
		t.Fatalf("at-limit evaluate = (over=%v warn=%v), want (false,true)", over, warn)
	}

	// 90% 边界：等于阈值告警，低于阈值不告警。
	plan.estimatedInput = 900
	if _, _, _, warn = plan.evaluate(channelID, modelName); !warn {
		t.Fatal("90% of limit must warn")
	}
	plan.estimatedInput = 899
	if _, _, _, warn = plan.evaluate(channelID, modelName); warn {
		t.Fatal("below 90% of limit must not warn")
	}

	// 未知上限 / 关闭预检 / 无估算：一律不排除不告警。
	plan.estimatedInput = 100000
	if over, _, _, warn = plan.evaluate(9999, modelName); over || warn {
		t.Fatalf("unknown limit = (over=%v warn=%v), want (false,false)", over, warn)
	}
	plan.enabled = false
	if over, _, _, warn = plan.evaluate(channelID, modelName); over || warn {
		t.Fatalf("disabled precheck = (over=%v warn=%v), want (false,false)", over, warn)
	}
	plan.enabled = true
	plan.estimatedInput = 0
	if over, _, _, warn = plan.evaluate(channelID, modelName); over || warn {
		t.Fatalf("zero estimate = (over=%v warn=%v), want (false,false)", over, warn)
	}
}

// TestPrepareContextPrecheckNilInputs 验证无请求/无内部请求时安全降级
// （不触发估算，计划直接禁用）。
func TestPrepareContextPrecheckNilInputs(t *testing.T) {
	setupPrecheckTestDB(t)
	if plan := prepareContextPrecheck(nil); plan.enabled {
		t.Fatal("nil request must disable the precheck plan")
	}
	if plan := prepareContextPrecheck(&relayRequest{}); plan.enabled {
		t.Fatal("request without internal request must disable the precheck plan")
	}
}

// TestIsContextOverflowErrorMarkers 验证上下文超限错误的识别口径：
// 只认 400/413/422 且命中上下文类标记（不吞其它错误）。
func TestIsContextOverflowErrorMarkers(t *testing.T) {
	cases := []struct {
		name   string
		status int
		err    error
		want   bool
	}{
		{"400 maximum context length", 400, errors.New("This model's maximum context length is 32768 tokens"), true},
		{"400 context_length_exceeded", 400, errors.New(`{"code":"context_length_exceeded"}`), true},
		{"413 prompt is too long", 413, errors.New("prompt is too long"), true},
		{"422 too many tokens", 422, errors.New("too many tokens"), true},
		{"400 unrelated message", 400, errors.New("invalid api key"), false},
		{"500 context message", 500, errors.New("maximum context length"), false},
		{"nil error", 400, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isContextOverflowError(tc.status, tc.err); got != tc.want {
				t.Fatalf("isContextOverflowError(%d, %v) = %v, want %v", tc.status, tc.err, got, tc.want)
			}
		})
	}
}

// TestMaybeRecordContextOverflowRecordsEvidence 验证确定性超限终态会累计学习
// 证据（每一次命中记 1 条证据行），非上下文错误与关闭预检时不落任何证据。
func TestMaybeRecordContextOverflowRecordsEvidence(t *testing.T) {
	setupPrecheckTestDB(t)
	ctx := context.Background()
	const channelID = 902
	const modelName = "learn-via-relay"

	plan := &contextPrecheckPlan{enabled: true, warnThreshold: 0.9, estimatedInput: 40000}
	overflowErr := errors.New("This model's maximum context length is 32768 tokens")
	maybeRecordContextOverflow(ctx, plan, channelID, modelName, 400, overflowErr)

	entries, err := channelmodellimit.ListByChannel(ctx, channelID)
	if err != nil || len(entries) != 1 {
		t.Fatalf("evidence rows = %d err=%v, want 1", len(entries), err)
	}
	if entries[0].EvidenceCount != 1 || entries[0].Source != "learned" {
		t.Fatalf("evidence entry = %+v, want learned/1", entries[0])
	}

	// 非上下文类错误 / 关闭预检：不产生证据。
	maybeRecordContextOverflow(ctx, plan, 903, "no-evidence", 400, errors.New("invalid request"))
	maybeRecordContextOverflow(ctx, &contextPrecheckPlan{enabled: false}, 904, "no-evidence-2", 400, overflowErr)
	for _, id := range []int{903, 904} {
		rows, listErr := channelmodellimit.ListByChannel(ctx, id)
		if listErr != nil || len(rows) != 0 {
			t.Fatalf("channel %d rows = %d err=%v, want 0", id, len(rows), listErr)
		}
	}
}
