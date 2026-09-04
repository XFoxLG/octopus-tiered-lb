package llm

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
)

func initPriceScheduleTestDB(t *testing.T) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
}

func TestPriceScheduleMatch(t *testing.T) {
	initPriceScheduleTestDB(t)
	ctx := context.Background()

	rows := []model.ModelPriceSchedule{
		{Name: "exact-special", RuleType: string(model.ModelPriceCategoryRuleExact), RuleValue: "gpt-special", LLMPrice: model.LLMPrice{Input: 99, Output: 199}, OffPeakMul: 0.5, Window1Start: 540, Window1End: 720, SortOrder: 1, Enabled: true},
		{Name: "prefix-gpt", RuleType: string(model.ModelPriceCategoryRulePrefix), RuleValue: "gpt", LLMPrice: model.LLMPrice{Input: 5, Output: 15}, OffPeakMul: 0.5, Window1Start: 540, Window1End: 720, SortOrder: 10, Enabled: true},
		{Name: "disabled", RuleType: string(model.ModelPriceCategoryRuleContains), RuleValue: "off", LLMPrice: model.LLMPrice{Input: 1, Output: 2}, OffPeakMul: 0.5, Window1Start: 540, Window1End: 720, SortOrder: 0, Enabled: false},
	}
	for _, r := range rows {
		if _, err := CreatePriceSchedule(r, ctx); err != nil {
			t.Fatalf("CreatePriceSchedule(%s): %v", r.Name, err)
		}
	}

	cases := []struct {
		model string
		want  *model.LLMPrice
	}{
		{"gpt-special", &model.LLMPrice{Input: 99, Output: 199}}, // exact wins over prefix
		{"gpt-4o", &model.LLMPrice{Input: 5, Output: 15}},        // prefix
		{"off-model", nil},                                       // disabled ignored
		{"no-match", nil},
	}
	for _, c := range cases {
		got := PriceScheduleMatch(c.model)
		if c.want == nil {
			if got != nil {
				t.Fatalf("PriceScheduleMatch(%s) = %+v, want nil", c.model, got)
			}
			continue
		}
		if got == nil || got.LLMPrice != *c.want {
			t.Fatalf("PriceScheduleMatch(%s) = %+v, want %+v", c.model, got, c.want)
		}
	}
}

func TestPriceScheduleCRUD(t *testing.T) {
	initPriceScheduleTestDB(t)
	ctx := context.Background()

	created, err := CreatePriceSchedule(model.ModelPriceSchedule{
		Name:         "My DeepSeek",
		RuleType:     string(model.ModelPriceCategoryRulePrefix),
		RuleValue:    "deepseek",
		LLMPrice:     model.LLMPrice{Input: 1, Output: 2, CacheRead: 0.5, CacheWrite: 1},
		OffPeakMul:   0.25,
		Window1Start: 600, Window1End: 900,
		SortOrder: 3,
		Enabled:   true,
	}, ctx)
	if err != nil {
		t.Fatalf("CreatePriceSchedule: %v", err)
	}
	if created.Name != "my deepseek" {
		t.Fatalf("name not lowercased: %q", created.Name)
	}
	if created.OffPeakMul != 0.25 || created.Window1Start != 600 || created.Window1End != 900 {
		t.Fatalf("fields not persisted: %+v", created)
	}

	rows, err := ListPriceSchedules(ctx)
	if err != nil {
		t.Fatalf("ListPriceSchedules: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len = %d, want 1", len(rows))
	}

	if p := PriceScheduleMatch("deepseek-v4-flash"); p == nil || p.Input != 1 {
		t.Fatalf("expected match after create, got %+v", p)
	}

	// update 禁用生效
	created.Enabled = false
	created.Input = 7
	if _, err := UpdatePriceSchedule(created, ctx); err != nil {
		t.Fatalf("UpdatePriceSchedule: %v", err)
	}
	if p := PriceScheduleMatch("deepseek-v4-flash"); p != nil {
		t.Fatalf("disabled rule should not match, got %+v", p)
	}

	// delete
	if err := DeletePriceSchedule(created.ID, ctx); err != nil {
		t.Fatalf("DeletePriceSchedule: %v", err)
	}
	rows, _ = ListPriceSchedules(ctx)
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows after delete, got %d", len(rows))
	}
}

func TestPriceScheduleValidation(t *testing.T) {
	initPriceScheduleTestDB(t)
	ctx := context.Background()

	invalidCases := []model.ModelPriceSchedule{
		{RuleType: "bad", RuleValue: "x", LLMPrice: model.LLMPrice{Input: 1, Output: 1}, OffPeakMul: 0.5, Window1Start: 0, Window1End: 60},                    // bad rule_type
		{RuleType: string(model.ModelPriceCategoryRuleExact), RuleValue: " ", LLMPrice: model.LLMPrice{Input: 1, Output: 1}, OffPeakMul: 0.5, Window1Start: 0, Window1End: 60}, // empty rule_value
		{RuleType: string(model.ModelPriceCategoryRuleExact), RuleValue: "x", LLMPrice: model.LLMPrice{Input: 1, Output: 1}, OffPeakMul: -1, Window1Start: 0, Window1End: 60}, // negative mul
		{RuleType: string(model.ModelPriceCategoryRuleExact), RuleValue: "x", LLMPrice: model.LLMPrice{Input: 1, Output: 1}, OffPeakMul: 0.5, Window1Start: 100, Window1End: 60}, // start > end
		{RuleType: string(model.ModelPriceCategoryRuleExact), RuleValue: "x", LLMPrice: model.LLMPrice{Input: 1, Output: 1}, OffPeakMul: 0.5, Window1Start: 0, Window1End: 2000}, // end > 1440
		{Name: " ", RuleType: string(model.ModelPriceCategoryRuleExact), RuleValue: "x", LLMPrice: model.LLMPrice{Input: 1, Output: 1}, OffPeakMul: 0.5, Window1Start: 0, Window1End: 60}, // empty name
	}
	for i, c := range invalidCases {
		if _, err := CreatePriceSchedule(c, ctx); err == nil {
			t.Fatalf("case %d: expected error, got nil", i)
		}
	}
}
