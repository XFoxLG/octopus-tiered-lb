package relay

import (
	"testing"

	appmodel "github.com/lingyuins/octopus/internal/model"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
)

func TestApplyModelThinkingSuffix(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		effort       string
		budget       *int64
		wantModel    string
		wantEffort   string
		wantBudget   *int64
		wantExplicit bool
	}{
		{
			name:         "level suffix strips model and sets effort",
			model:        "gemini-3.1-pro-preview(high)",
			wantModel:    "gemini-3.1-pro-preview",
			wantEffort:   "high",
			wantExplicit: true,
		},
		{
			name:         "numeric suffix sets budget",
			model:        "gemini-2.5-pro(24576)",
			wantModel:    "gemini-2.5-pro",
			wantBudget:   int64Ptr(24576),
			wantExplicit: true,
		},
		{
			name:         "none suffix disables via zero budget",
			model:        "gemini-2.5-flash(none)",
			wantModel:    "gemini-2.5-flash",
			wantBudget:   int64Ptr(0),
			wantExplicit: true,
		},
		{
			name:         "auto suffix requests dynamic budget",
			model:        "gemini-2.5-flash(auto)",
			wantModel:    "gemini-2.5-flash",
			wantBudget:   int64Ptr(-1),
			wantExplicit: true,
		},
		{
			name:         "suffix wins over body effort",
			model:        "claude-sonnet-4-5(max)",
			effort:       "low",
			wantModel:    "claude-sonnet-4-5",
			wantEffort:   "max",
			wantExplicit: true,
		},
		{
			name:         "numeric suffix wins over body budget",
			model:        "claude-sonnet-4-5(4096)",
			budget:       int64Ptr(999),
			wantModel:    "claude-sonnet-4-5",
			wantBudget:   int64Ptr(4096),
			wantExplicit: true,
		},
		{
			name:         "unrecognised suffix keeps model untouched",
			model:        "vendor/model(experimental)",
			effort:       "medium",
			wantModel:    "vendor/model(experimental)",
			wantEffort:   "medium",
			wantExplicit: false,
		},
		{
			name:         "plain model untouched",
			model:        "gemini-3.8-flash",
			wantModel:    "gemini-3.8-flash",
			wantExplicit: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &transmodel.InternalLLMRequest{
				Model:           tc.model,
				ReasoningEffort: tc.effort,
				ReasoningBudget: tc.budget,
			}
			applyModelThinkingSuffix(req)

			if req.Model != tc.wantModel {
				t.Errorf("model = %q, want %q", req.Model, tc.wantModel)
			}
			if req.ReasoningEffort != tc.wantEffort {
				t.Errorf("effort = %q, want %q", req.ReasoningEffort, tc.wantEffort)
			}
			switch {
			case tc.wantBudget == nil && req.ReasoningBudget != nil:
				t.Errorf("budget = %d, want nil", *req.ReasoningBudget)
			case tc.wantBudget != nil && req.ReasoningBudget == nil:
				t.Errorf("budget = nil, want %d", *tc.wantBudget)
			case tc.wantBudget != nil && *req.ReasoningBudget != *tc.wantBudget:
				t.Errorf("budget = %d, want %d", *req.ReasoningBudget, *tc.wantBudget)
			}
			if req.ReasoningExplicit != tc.wantExplicit {
				t.Errorf("ReasoningExplicit = %v, want %v", req.ReasoningExplicit, tc.wantExplicit)
			}
		})
	}
}

// The suffix is an explicit client choice, so a group-level default effort must
// not overwrite it (ReasoningExplicit is set) while a plain request still gets
// the group default.
func TestApplyModelThinkingSuffix_BeatsGroupDefault(t *testing.T) {
	group := &appmodel.Group{DefaultReasoningEffort: "medium"}

	suffixed := &transmodel.InternalLLMRequest{Model: "claude-sonnet-4-5(xhigh)"}
	applyModelThinkingSuffix(suffixed)
	applyReasoningPolicy(group, suffixed)
	if suffixed.ReasoningEffort != "xhigh" {
		t.Fatalf("suffixed effort = %q, want xhigh (group default must not win)", suffixed.ReasoningEffort)
	}
	if suffixed.Model != "claude-sonnet-4-5" {
		t.Fatalf("model = %q, want stripped model", suffixed.Model)
	}

	plain := &transmodel.InternalLLMRequest{Model: "claude-sonnet-4-5"}
	applyModelThinkingSuffix(plain)
	applyReasoningPolicy(group, plain)
	if plain.ReasoningEffort != "medium" {
		t.Fatalf("plain effort = %q, want group default medium", plain.ReasoningEffort)
	}
}

func TestModelSuffixClearsBodyControlsAndBeatsForcedDefault(t *testing.T) {
	group := &appmodel.Group{DefaultReasoningEffort: "high", ReasoningForceOverride: true}
	for _, suffix := range []string{"none", "auto", "4096", "low"} {
		t.Run(suffix, func(t *testing.T) {
			request := &transmodel.InternalLLMRequest{Model: "model(" + suffix + ")", EnableThinking: boolPtr(false), AdaptiveThinking: true, ReasoningEffort: "max"}
			applyModelThinkingSuffix(request)
			applyReasoningPolicy(group, request)
			if !request.ReasoningFromModelSuffix || request.EnableThinking != nil || request.AdaptiveThinking {
				t.Fatal("lower-precedence body controls survived suffix")
			}
			if suffix == "none" && (request.ReasoningBudget == nil || *request.ReasoningBudget != 0 || request.ReasoningEffort != "") {
				t.Fatal("force overrode suffix opt-out")
			}
		})
	}
}
