package relay

import (
	"testing"

	appmodel "github.com/lingyuins/octopus/internal/model"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

// 五态语义：未配 / 已配+沉默 / 已配+显式关 / 已配+显式关+强制 / 已配+已有档位。
func TestApplyReasoningPolicy(t *testing.T) {
	tests := []struct {
		name       string
		group      *appmodel.Group
		request    *transmodel.InternalLLMRequest
		wantEffort string
	}{
		{
			name:       "group unconfigured: never touches request",
			group:      &appmodel.Group{},
			request:    &transmodel.InternalLLMRequest{},
			wantEffort: "",
		},
		{
			name:       "nil group: never touches request",
			group:      nil,
			request:    &transmodel.InternalLLMRequest{},
			wantEffort: "",
		},
		{
			name: "configured + silent client: injects default",
			group: &appmodel.Group{
				DefaultReasoningEffort: "medium",
			},
			request:    &transmodel.InternalLLMRequest{},
			wantEffort: "medium",
		},
		{
			name: "configured + explicit off: respected (no inject)",
			group: &appmodel.Group{
				DefaultReasoningEffort: "high",
			},
			request: &transmodel.InternalLLMRequest{
				ReasoningExplicit: true,
			},
			wantEffort: "",
		},
		{
			name: "configured + explicit off + force override: injects",
			group: &appmodel.Group{
				DefaultReasoningEffort: "high",
				ReasoningForceOverride: true,
			},
			request: &transmodel.InternalLLMRequest{
				ReasoningExplicit: true,
			},
			wantEffort: "high",
		},
		{
			name: "configured + client effort present: client wins",
			group: &appmodel.Group{
				DefaultReasoningEffort: "high",
				ReasoningForceOverride: true,
			},
			request: &transmodel.InternalLLMRequest{
				ReasoningEffort:   "low",
				ReasoningExplicit: true,
			},
			wantEffort: "low",
		},
		{
			name: "configured + client budget present: client wins",
			group: &appmodel.Group{
				DefaultReasoningEffort: "high",
				ReasoningForceOverride: true,
			},
			request: &transmodel.InternalLLMRequest{
				ReasoningBudget:   int64Ptr(15000),
				ReasoningExplicit: true,
			},
			wantEffort: "",
		},
		{
			name: "configured + adaptive thinking: client wins",
			group: &appmodel.Group{
				DefaultReasoningEffort: "high",
				ReasoningForceOverride: true,
			},
			request: &transmodel.InternalLLMRequest{
				AdaptiveThinking:  true,
				ReasoningExplicit: true,
			},
			wantEffort: "",
		},
		{
			name: "configured value normalized (case/space)",
			group: &appmodel.Group{
				DefaultReasoningEffort: "  HIGH ",
			},
			request:    &transmodel.InternalLLMRequest{},
			wantEffort: "high",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			applyReasoningPolicy(testCase.group, testCase.request)
			if testCase.request.ReasoningEffort != testCase.wantEffort {
				t.Fatalf("ReasoningEffort = %q, want %q", testCase.request.ReasoningEffort, testCase.wantEffort)
			}
		})
	}
}

// REQ-01 矛盾控制：客户端显式 enable_thinking=false 时，
// 无 force 必须不注入；有 force 注入时不得同时保留 thinking disabled。
func TestApplyReasoningPolicyEnableThinkingFalse(t *testing.T) {
	group := &appmodel.Group{DefaultReasoningEffort: "high"}

	silent := &transmodel.InternalLLMRequest{EnableThinking: boolPtr(false)}
	applyReasoningPolicy(group, silent)
	if silent.ReasoningEffort != "" {
		t.Fatalf("explicit off without force: ReasoningEffort = %q, want empty", silent.ReasoningEffort)
	}
	if silent.EnableThinking == nil || *silent.EnableThinking {
		t.Fatalf("explicit off without force: EnableThinking must stay false")
	}

	forced := &transmodel.InternalLLMRequest{EnableThinking: boolPtr(false)}
	forcedGroup := &appmodel.Group{DefaultReasoningEffort: "high", ReasoningForceOverride: true}
	applyReasoningPolicy(forcedGroup, forced)
	if forced.ReasoningEffort != "high" {
		t.Fatalf("force override: ReasoningEffort = %q, want high", forced.ReasoningEffort)
	}
	if forced.EnableThinking != nil && !*forced.EnableThinking {
		t.Fatalf("force override: EnableThinking still false alongside injected effort")
	}
}

// REQ-01 回归：enable_thinking=true 是显式「开」，属于客户端显式意图层，
// 优先级高于分组默认；因此不再叠加分组档位（force 只覆盖显式关）。
func TestApplyReasoningPolicyEnableThinkingTrueBlocksDefault(t *testing.T) {
	group := &appmodel.Group{DefaultReasoningEffort: "medium"}
	request := &transmodel.InternalLLMRequest{EnableThinking: boolPtr(true)}
	applyReasoningPolicy(group, request)
	if request.ReasoningEffort != "" {
		t.Fatalf("ReasoningEffort = %q, want empty (explicit on must block the group default)", request.ReasoningEffort)
	}
	if request.EnableThinking == nil || !*request.EnableThinking {
		t.Fatalf("explicit on must be preserved")
	}

	// force 只覆盖显式关，不得覆盖显式开。
	forced := &transmodel.InternalLLMRequest{EnableThinking: boolPtr(true)}
	applyReasoningPolicy(&appmodel.Group{DefaultReasoningEffort: "medium", ReasoningForceOverride: true}, forced)
	if forced.ReasoningEffort != "" {
		t.Fatalf("force must not override explicit on: ReasoningEffort = %q, want empty", forced.ReasoningEffort)
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}

func boolPtr(b bool) *bool {
	return &b
}

// prepareInternalRequestForOutbound 层面验证：注入发生在 param_override 之后，
// 且 nil group（无分组路由）不会 panic。
func TestPrepareInternalRequestAppliesReasoningPolicy(t *testing.T) {
	channel := &appmodel.Channel{Type: outbound.OutboundTypeOpenAIChat}
	group := &appmodel.Group{DefaultReasoningEffort: "medium"}

	request := &transmodel.InternalLLMRequest{}
	prepared, _, err := prepareInternalRequestForOutbound(channel, request, "chat", group)
	if err != nil {
		t.Fatalf("prepareInternalRequestForOutbound() error = %v", err)
	}
	if prepared.ReasoningEffort != "medium" {
		t.Fatalf("ReasoningEffort = %q, want medium", prepared.ReasoningEffort)
	}

	if _, _, err := prepareInternalRequestForOutbound(channel, &transmodel.InternalLLMRequest{}, "chat", nil); err != nil {
		t.Fatalf("nil group should not error, got %v", err)
	}
}
