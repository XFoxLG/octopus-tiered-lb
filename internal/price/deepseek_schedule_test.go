package price

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/llm"
)

// shanghaiLoc 与 deepSeekLocation 使用相同固定偏移构造，避免依赖系统 tzdata。
var shanghaiLoc = time.FixedZone("UTC+8", 8*3600)

func mustTime(t *testing.T, h, m, s int) time.Time {
	t.Helper()
	return time.Date(2026, 8, 17, h, m, s, 0, shanghaiLoc)
}

// initScheduleTestDB 建独立测试库并 seed 默认峰谷规则（与启动 seed 同源）。
func initScheduleTestDB(t *testing.T) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	if err := llm.SeedPriceSchedules(ctx); err != nil {
		t.Fatalf("SeedPriceSchedules: %v", err)
	}
	if err := llm.RefreshPriceScheduleCache(ctx); err != nil {
		t.Fatalf("RefreshPriceScheduleCache: %v", err)
	}
}

// TestSeedPriceSchedules 验证 seed 幂等性与默认规则内容（DeepSeek 官方高峰价）。
func TestSeedPriceSchedules(t *testing.T) {
	initScheduleTestDB(t)
	ctx := t.Context()

	rows, err := llm.ListPriceSchedules(ctx)
	if err != nil {
		t.Fatalf("ListPriceSchedules: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("seeded rows = %d, want 2", len(rows))
	}

	// 再次 seed 应跳过（幂等）
	if err := llm.SeedPriceSchedules(ctx); err != nil {
		t.Fatalf("second SeedPriceSchedules: %v", err)
	}
	rows, _ = llm.ListPriceSchedules(ctx)
	if len(rows) != 2 {
		t.Fatalf("after re-seed rows = %d, want 2", len(rows))
	}

	// 规则内容：prefix deepseek-v4-flash / deepseek-v4-pro，倍率 0.5，默认窗口
	var flash, pro *model.ModelPriceSchedule
	for i := range rows {
		switch rows[i].Name {
		case "deepseek-v4-flash":
			flash = &rows[i]
		case "deepseek-v4-pro":
			pro = &rows[i]
		}
	}
	if flash == nil || pro == nil {
		t.Fatalf("missing seeded rules: %+v", rows)
	}
	if flash.RuleType != string(model.ModelPriceCategoryRulePrefix) || flash.RuleValue != "deepseek-v4-flash" {
		t.Fatalf("flash rule = %s/%s", flash.RuleType, flash.RuleValue)
	}
	if flash.OffPeakMul != 0.5 || flash.Window1Start != 540 || flash.Window1End != 720 ||
		flash.Window2Start != 840 || flash.Window2End != 1080 {
		t.Fatalf("flash windows/mul = %+v", flash)
	}
	if !flash.WeekendOffPeak {
		t.Fatalf("flash weekend_off_peak = false, want true (official rule since 2026-08-23)")
	}
	if !floatEqual(flash.Input, 0.44) || !floatEqual(flash.Output, 1.32) ||
		!floatEqual(flash.CacheRead, 0.014) {
		t.Fatalf("flash peak price = %+v", flash.LLMPrice)
	}
	if !floatEqual(pro.Input, 1.32) || !floatEqual(pro.Output, 3.96) ||
		!floatEqual(pro.CacheRead, 0.044) {
		t.Fatalf("pro peak price = %+v", pro.LLMPrice)
	}
}

func TestBillingWindow(t *testing.T) {
	initScheduleTestDB(t)
	// 高峰窗口 [09:00,12:00) ∪ [14:00,18:00)（北京），其余空闲。
	peak := []time.Time{
		mustTime(t, 9, 0, 0),
		mustTime(t, 9, 30, 0),
		mustTime(t, 11, 59, 59),
		mustTime(t, 14, 0, 0),
		mustTime(t, 16, 0, 0),
		mustTime(t, 17, 59, 59),
	}
	offpeak := []time.Time{
		mustTime(t, 0, 0, 0),
		mustTime(t, 8, 59, 59),
		mustTime(t, 12, 0, 0),
		mustTime(t, 13, 59, 59),
		mustTime(t, 18, 0, 0),
		mustTime(t, 23, 59, 59),
	}
	for _, at := range peak {
		if got := BillingWindow("deepseek-v4-flash", at); got != BillingWindowPeak {
			t.Errorf("BillingWindow(%v) = %q, want %q", at, got, BillingWindowPeak)
		}
	}
	for _, at := range offpeak {
		if got := BillingWindow("deepseek-v4-flash", at); got != BillingWindowOffPeak {
			t.Errorf("BillingWindow(%v) = %q, want %q", at, got, BillingWindowOffPeak)
		}
	}
}

func TestBillingWindowUTCInput(t *testing.T) {
	initScheduleTestDB(t)
	// UTC 输入：01:00Z = 北京 09:00 → 高峰；04:00Z = 北京 12:00 → 空闲。
	cases := []struct {
		name string
		utc  time.Time
		want string
	}{
		{"01:00Z is beijing 09:00 peak", time.Date(2026, 8, 17, 1, 0, 0, 0, time.UTC), BillingWindowPeak},
		{"04:00Z is beijing 12:00 offpeak", time.Date(2026, 8, 17, 4, 0, 0, 0, time.UTC), BillingWindowOffPeak},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BillingWindow("deepseek-v4-pro", tc.utc); got != tc.want {
				t.Fatalf("BillingWindow = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBillingWindowNoRule 未命中任何启用规则 → None（默认规则不覆盖非 v4 与中转商前缀）。
func TestBillingWindowNoRule(t *testing.T) {
	initScheduleTestDB(t)
	if got := BillingWindow("gpt-4o", mustTime(t, 10, 0, 0)); got != BillingWindowNone {
		t.Fatalf("BillingWindow(gpt-4o) = %q, want %q", got, BillingWindowNone)
	}
	// 默认规则是 prefix，中转商前缀 olm/deepseek-v4-pro 不命中（除非用户配 contains）
	if got := BillingWindow("olm/deepseek-v4-pro", mustTime(t, 10, 0, 0)); got != BillingWindowNone {
		t.Fatalf("BillingWindow(olm/deepseek-v4-pro) = %q, want %q", got, BillingWindowNone)
	}
}

// TestBillingWindowWeekend 周末（周六/周日，北京时间）全天按空闲价：
// 即使落在原高峰窗口内也按 offpeak 计费（官方 2026-08-23 起规则）。
func TestBillingWindowWeekend(t *testing.T) {
	initScheduleTestDB(t)
	// 2026-08-22 周六 / 2026-08-23 周日（北京时区）
	weekend := []time.Time{
		time.Date(2026, 8, 22, 10, 0, 0, 0, shanghaiLoc), // 周六 10:00（原高峰）
		time.Date(2026, 8, 22, 0, 0, 0, 0, shanghaiLoc),  // 周六 00:00
		time.Date(2026, 8, 22, 12, 0, 0, 0, shanghaiLoc), // 周六 12:00（原空闲）
		time.Date(2026, 8, 23, 15, 0, 0, 0, shanghaiLoc), // 周日 15:00（原高峰）
		time.Date(2026, 8, 23, 23, 59, 59, 0, shanghaiLoc),
	}
	for _, at := range weekend {
		if got := BillingWindow("deepseek-v4-flash", at); got != BillingWindowOffPeak {
			t.Errorf("BillingWindow(%v) = %q, want %q", at, got, BillingWindowOffPeak)
		}
	}
	// 工作日（周一 2026-08-17）高峰时段仍为高峰
	if got := BillingWindow("deepseek-v4-flash", mustTime(t, 10, 0, 0)); got != BillingWindowPeak {
		t.Fatalf("Monday 10:00 = %q, want %q", got, BillingWindowPeak)
	}
	// 周末有效价 = 高峰 × 0.5（周六 10:00 原高峰价 0.44 → 0.22）
	eff := EffectiveLLMPrice("deepseek-v4-flash", weekend[0])
	if eff == nil || !floatEqual(eff.Input, 0.22) || !floatEqual(eff.Output, 0.66) {
		t.Fatalf("weekend EffectiveLLMPrice = %+v, want Input 0.22 Output 0.66", eff)
	}
}

// TestBillingWindowWeekendDisabled 关闭 WeekendOffPeak 后，周末回到按窗口判断。
func TestBillingWindowWeekendDisabled(t *testing.T) {
	initScheduleTestDB(t)
	ctx := t.Context()
	rows, err := llm.ListPriceSchedules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flash := rows[0]
	flash.WeekendOffPeak = false
	if _, err := llm.UpdatePriceSchedule(flash, ctx); err != nil {
		t.Fatal(err)
	}
	// 周六 10:00 回到高峰窗口
	at := time.Date(2026, 8, 22, 10, 0, 0, 0, shanghaiLoc)
	if got := BillingWindow("deepseek-v4-flash", at); got != BillingWindowPeak {
		t.Fatalf("weekend-off-peak disabled: BillingWindow = %q, want %q", got, BillingWindowPeak)
	}
}

func TestScaleLLMPrice(t *testing.T) {
	p := model.LLMPrice{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}
	got := ScaleLLMPrice(p, 0.5)
	want := model.LLMPrice{Input: 0.5, Output: 1, CacheRead: 1.5, CacheWrite: 2}
	if got != want {
		t.Fatalf("ScaleLLMPrice = %+v, want %+v", got, want)
	}
}

func TestEffectiveLLMPrice(t *testing.T) {
	initScheduleTestDB(t)

	// 命中默认规则：高峰 10:00 → 规则高峰价；空闲 13:00 → 高峰价 × 0.5。
	peak := EffectiveLLMPrice("deepseek-v4-flash", mustTime(t, 10, 0, 0))
	if peak == nil || !floatEqual(peak.Input, 0.44) || !floatEqual(peak.Output, 1.32) {
		t.Fatalf("peak EffectiveLLMPrice = %+v", peak)
	}
	off := EffectiveLLMPrice("deepseek-v4-flash", mustTime(t, 13, 0, 0))
	if off == nil || !floatEqual(off.Input, 0.5*0.44) || !floatEqual(off.Output, 0.5*1.32) {
		t.Fatalf("offpeak EffectiveLLMPrice = %+v", off)
	}

	// 未命中规则：走 GetLLMPrice（注入 llmPrice 验证不缩放）。
	restore := setPricesForTest(map[string]model.LLMPrice{
		"gpt-4o": {Input: 5, Output: 15, CacheRead: 0, CacheWrite: 0},
	})
	t.Cleanup(restore)
	a := EffectiveLLMPrice("gpt-4o", mustTime(t, 10, 0, 0))
	b := EffectiveLLMPrice("gpt-4o", mustTime(t, 13, 0, 0))
	if a == nil || b == nil || !floatEqual(a.Input, b.Input) || !floatEqual(a.Input, 5) {
		t.Fatalf("gpt-4o prices differ/incorrect across windows: %+v vs %+v", a, b)
	}
	// 未知模型 → nil
	if got := EffectiveLLMPrice("totally-unknown", mustTime(t, 10, 0, 0)); got != nil {
		t.Fatalf("unknown model = %+v, want nil", got)
	}
}

// TestCustomScheduleRule 前端自定义规则覆盖默认规则：改窗口/倍率/价格立即生效。
func TestCustomScheduleRule(t *testing.T) {
	initScheduleTestDB(t)
	ctx := t.Context()

	rows, err := llm.ListPriceSchedules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flashID := rows[0].ID

	// 更新默认 flash 规则：倍率 0.25，单窗口 20:00-22:00，改高峰价。
	updated, err := llm.UpdatePriceSchedule(model.ModelPriceSchedule{
		ID:        flashID,
		Name:      "deepseek-v4-flash",
		RuleType:  string(model.ModelPriceCategoryRulePrefix),
		RuleValue: "deepseek-v4-flash",
		LLMPrice:  model.LLMPrice{Input: 2, Output: 6, CacheRead: 0.2},
		OffPeakMul:   0.25,
		Window1Start: 20 * 60, Window1End: 22 * 60,
		SortOrder: 1,
		Enabled:   true,
	}, ctx)
	if err != nil {
		t.Fatalf("UpdatePriceSchedule: %v", err)
	}
	if !floatEqual(updated.Input, 2) {
		t.Fatalf("updated price = %+v", updated.LLMPrice)
	}

	// 新窗口外（10:00）→ 空闲，价 = 2×0.25
	off := EffectiveLLMPrice("deepseek-v4-flash", mustTime(t, 10, 0, 0))
	if off == nil || !floatEqual(off.Input, 0.5) {
		t.Fatalf("custom window offpeak = %+v, want Input 0.5", off)
	}
	// 新窗口内（21:00）→ 高峰，价 = 2
	peak := EffectiveLLMPrice("deepseek-v4-flash", mustTime(t, 21, 0, 0))
	if peak == nil || !floatEqual(peak.Input, 2) {
		t.Fatalf("custom window peak = %+v, want Input 2", peak)
	}
	// 禁用后不再命中
	updated.Enabled = false
	if _, err := llm.UpdatePriceSchedule(updated, ctx); err != nil {
		t.Fatal(err)
	}
	if got := BillingWindow("deepseek-v4-flash", mustTime(t, 21, 0, 0)); got != BillingWindowNone {
		t.Fatalf("disabled rule still matches: %q", got)
	}
}

func floatEqual(a, b float64) bool {
	const eps = 1e-9
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < eps
}
