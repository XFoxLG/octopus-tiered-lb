package model

import (
	"errors"
	"testing"
)

func boolPointer(value bool) *bool { return &value }

func int64Pointer(value int64) *int64 { return &value }

// TestReasoningIntentOf pins the source-agnostic classification that the relay's
// group-default policy and the conflict validator both build on.
func TestReasoningIntentOf(t *testing.T) {
	tests := []struct {
		name    string
		request *InternalLLMRequest
		want    ReasoningIntent
	}{
		{
			name:    "silent client is unspecified",
			request: &InternalLLMRequest{},
			want:    ReasoningIntentUnspecified,
		},
		{
			name:    "nil request is unspecified",
			request: nil,
			want:    ReasoningIntentUnspecified,
		},
		{
			name:    "enable_thinking true is explicit on",
			request: &InternalLLMRequest{EnableThinking: boolPointer(true)},
			want:    ReasoningIntentOn,
		},
		{
			name:    "enable_thinking false is explicit off",
			request: &InternalLLMRequest{EnableThinking: boolPointer(false)},
			want:    ReasoningIntentOff,
		},
		{
			name:    "reasoning_effort none is explicit off",
			request: &InternalLLMRequest{ReasoningEffort: "none"},
			want:    ReasoningIntentOff,
		},
		{
			name:    "reasoning explicit with no level is off (anthropic disabled / responses {})",
			request: &InternalLLMRequest{ReasoningExplicit: true},
			want:    ReasoningIntentOff,
		},
		{
			name:    "effort keyword is a level",
			request: &InternalLLMRequest{ReasoningEffort: "high"},
			want:    ReasoningIntentLevel,
		},
		{
			name:    "budget is a level",
			request: &InternalLLMRequest{ReasoningBudget: int64Pointer(8192)},
			want:    ReasoningIntentLevel,
		},
		{
			name:    "adaptive is a level",
			request: &InternalLLMRequest{AdaptiveThinking: true},
			want:    ReasoningIntentLevel,
		},
		{
			name: "anthropic enabled derived effort+budget is a level, not a conflict",
			request: &InternalLLMRequest{
				ReasoningEffort:   "medium",
				ReasoningBudget:   int64Pointer(8192),
				ReasoningExplicit: true,
			},
			want: ReasoningIntentLevel,
		},
		{
			name: "adaptive derived effort+adaptive is a level, not a conflict",
			request: &InternalLLMRequest{
				ReasoningEffort:   "high",
				AdaptiveThinking:  true,
				ReasoningExplicit: true,
			},
			want: ReasoningIntentLevel,
		},
		{
			name: "model suffix override derived effort is a level",
			request: &InternalLLMRequest{
				ReasoningEffort:   "xhigh",
				ReasoningExplicit: true,
			},
			want: ReasoningIntentLevel,
		},
		{
			name: "enable_thinking true with a level stays a level",
			request: &InternalLLMRequest{
				EnableThinking:  boolPointer(true),
				ReasoningEffort: "high",
			},
			want: ReasoningIntentLevel,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReasoningIntentOf(tc.request); got != tc.want {
				t.Fatalf("ReasoningIntentOf() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestValidateClientReasoningControls covers the same-precedence contradictions
// that must be rejected with 400 before any upstream attempt, and the derived
// combinations that must NOT be rejected.
func TestValidateClientReasoningControls(t *testing.T) {
	tests := []struct {
		name    string
		request *InternalLLMRequest
		wantErr bool
	}{
		{
			name:    "silent client is valid",
			request: &InternalLLMRequest{},
		},
		{
			name:    "enable_thinking false alone is valid",
			request: &InternalLLMRequest{EnableThinking: boolPointer(false)},
		},
		{
			name:    "enable_thinking true alone is valid",
			request: &InternalLLMRequest{EnableThinking: boolPointer(true)},
		},
		{
			name: "enable_thinking true plus effort is valid (both ask for thinking)",
			request: &InternalLLMRequest{
				EnableThinking:  boolPointer(true),
				ReasoningEffort: "high",
			},
		},
		{
			name: "enable_thinking true plus budget is valid",
			request: &InternalLLMRequest{
				EnableThinking:  boolPointer(true),
				ReasoningBudget: int64Pointer(4096),
			},
		},
		{
			name: "enable_thinking false plus reasoning_effort none is valid (both off)",
			request: &InternalLLMRequest{
				EnableThinking:  boolPointer(false),
				ReasoningEffort: "none",
			},
		},
		{
			name: "enable_thinking false plus effort is a conflict",
			request: &InternalLLMRequest{
				EnableThinking:  boolPointer(false),
				ReasoningEffort: "high",
			},
			wantErr: true,
		},
		{
			name: "enable_thinking false plus budget is a conflict",
			request: &InternalLLMRequest{
				EnableThinking:  boolPointer(false),
				ReasoningBudget: int64Pointer(4096),
			},
			wantErr: true,
		},
		{
			name: "enable_thinking false plus adaptive is a conflict",
			request: &InternalLLMRequest{
				EnableThinking:   boolPointer(false),
				AdaptiveThinking: true,
			},
			wantErr: true,
		},
		{
			name: "enable_thinking true plus reasoning_effort none is a conflict",
			request: &InternalLLMRequest{
				EnableThinking:  boolPointer(true),
				ReasoningEffort: "none",
			},
			wantErr: true,
		},
		{
			name: "anthropic enabled derived effort+budget is not a conflict",
			request: &InternalLLMRequest{
				ReasoningEffort:   "medium",
				ReasoningBudget:   int64Pointer(8192),
				ReasoningExplicit: true,
			},
		},
		{
			name: "adaptive derived effort+flag is not a conflict",
			request: &InternalLLMRequest{
				ReasoningEffort:   "high",
				AdaptiveThinking:  true,
				ReasoningExplicit: true,
			},
		},
		{
			name: "reasoning explicit with no level is not a conflict",
			request: &InternalLLMRequest{
				ReasoningExplicit: true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateClientReasoningControls(tc.request)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateClientReasoningControls() = nil, want conflict error")
				}
				if !errors.Is(err, ErrReasoningControlsConflict) {
					t.Fatalf("error %v does not wrap ErrReasoningControlsConflict", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateClientReasoningControls() = %v, want nil", err)
			}
		})
	}
}
