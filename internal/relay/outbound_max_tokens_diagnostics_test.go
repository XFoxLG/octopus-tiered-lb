package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
)

func outboundBudgetRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://example.com/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestRelayDiagnosticsRecordsSelectedOutboundBudget(t *testing.T) {
	trace := &relayRequestTrace{id: "request", startedAt: time.Now()}
	trace.startDiagnosticAttempt(7, 42, "messages", context.Background())
	internal := &transmodel.InternalLLMRequest{
		TransformerMetadata: map[string]string{
			transmodel.TransformerMetadataOutboundMaxTokensValue:    "8192",
			transmodel.TransformerMetadataOutboundMaxTokensField:    "max_tokens",
			transmodel.TransformerMetadataOutboundMaxTokensSource:   transmodel.OutboundMaxTokensSourceAnthropicDefault,
			transmodel.TransformerMetadataOutboundMaxTokensAdjusted: "true",
		},
	}
	snapshot := observeOutboundBudget(outboundBudgetRequest(t, `{"model":"claude","max_tokens":8192}`), internal)
	trace.recordOutboundMaxTokens(7, snapshot)

	before := trace.diagnosticsSnapshot()
	if before == nil || len(before.Attempts) != 1 || before.OutboundMaxTokens != nil {
		t.Fatalf("attempt observation leaked into request level before selection: %#v", before)
	}
	trace.selectDiagnosticAttempt(7)
	diagnostics := trace.diagnosticsSnapshot()
	if diagnostics == nil {
		t.Fatal("diagnostics missing")
	}
	if diagnostics.SelectedAttempt != 7 || diagnostics.OutboundBudgetState != "set" {
		t.Fatalf("selected budget state = %#v", diagnostics)
	}
	if diagnostics.OutboundMaxTokens == nil || *diagnostics.OutboundMaxTokens != 8192 {
		t.Fatalf("outbound max_tokens = %v, want 8192", diagnostics.OutboundMaxTokens)
	}
	if diagnostics.OutboundMaxTokensSource != transmodel.OutboundMaxTokensSourceAnthropicDefault {
		t.Fatalf("outbound max_tokens source = %q, want %q",
			diagnostics.OutboundMaxTokensSource, transmodel.OutboundMaxTokensSourceAnthropicDefault)
	}
	if diagnostics.OutboundMaxTokensField != "max_tokens" || !diagnostics.OutboundMaxTokensAdjusted {
		t.Fatalf("wire provenance = %#v", diagnostics)
	}

	payload, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"selected_attempt", "outbound_budget_state", "outbound_max_tokens", "outbound_max_tokens_source"} {
		if _, ok := wire[key]; !ok {
			t.Fatalf("diagnostics JSON missing key %q: %s", key, payload)
		}
	}
}

func TestRelayDiagnosticsRecordsSerializedNotSetBudget(t *testing.T) {
	trace := &relayRequestTrace{id: "request", startedAt: time.Now()}
	trace.startDiagnosticAttempt(1, 42, "chat", context.Background())
	snapshot := observeOutboundBudget(outboundBudgetRequest(t, `{"model":"chat"}`), &transmodel.InternalLLMRequest{})
	trace.recordOutboundMaxTokens(1, snapshot)
	trace.selectDiagnosticAttempt(1)

	diagnostics := trace.diagnosticsSnapshot()
	if diagnostics == nil || diagnostics.OutboundBudgetState != "not_set" || diagnostics.OutboundMaxTokens != nil {
		t.Fatalf("not-set budget state = %#v", diagnostics)
	}
	if len(diagnostics.Attempts) != 1 || diagnostics.Attempts[0].OutboundBudgetState != "not_set" {
		t.Fatalf("attempt budget state = %#v", diagnostics.Attempts)
	}
}

func TestRelayDiagnosticsRecordsExplicitZeroOutboundBudget(t *testing.T) {
	trace := &relayRequestTrace{id: "request", startedAt: time.Now()}
	trace.startDiagnosticAttempt(1, 42, "chat", context.Background())
	internal := &transmodel.InternalLLMRequest{
		TransformerMetadata: map[string]string{
			transmodel.TransformerMetadataOutboundMaxTokensValue:    "0",
			transmodel.TransformerMetadataOutboundMaxTokensField:    "max_tokens",
			transmodel.TransformerMetadataOutboundMaxTokensSource:   transmodel.OutboundMaxTokensSourceClientMaxTokens,
			transmodel.TransformerMetadataOutboundMaxTokensAdjusted: "false",
		},
	}
	trace.recordOutboundMaxTokens(1, observeOutboundBudget(outboundBudgetRequest(t, `{"max_tokens":0}`), internal))
	trace.selectDiagnosticAttempt(1)

	payload, err := json.Marshal(trace.diagnosticsSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		OutboundMaxTokens       *int64 `json:"outbound_max_tokens"`
		OutboundMaxTokensSource string `json:"outbound_max_tokens_source"`
		OutboundBudgetState     string `json:"outbound_budget_state"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.OutboundMaxTokens == nil || *wire.OutboundMaxTokens != 0 ||
		wire.OutboundMaxTokensSource != transmodel.OutboundMaxTokensSourceClientMaxTokens ||
		wire.OutboundBudgetState != "set" {
		t.Fatalf("explicit zero was not preserved: %s", payload)
	}
}

func TestRelayDiagnosticsSelectedAttemptClearsPreviousBudgetProjection(t *testing.T) {
	trace := &relayRequestTrace{id: "request", startedAt: time.Now()}
	trace.startDiagnosticAttempt(1, 11, "chat", context.Background())
	trace.startDiagnosticAttempt(2, 22, "chat", context.Background())
	trace.recordOutboundMaxTokens(1, observeOutboundBudget(
		outboundBudgetRequest(t, `{"max_tokens":4096}`),
		&transmodel.InternalLLMRequest{},
	))
	trace.selectDiagnosticAttempt(1)
	trace.recordOutboundMaxTokens(2, observeOutboundBudget(
		outboundBudgetRequest(t, `{"model":"chat"}`),
		&transmodel.InternalLLMRequest{},
	))
	trace.selectDiagnosticAttempt(2)

	diagnostics := trace.diagnosticsSnapshot()
	if diagnostics == nil || diagnostics.SelectedAttempt != 2 ||
		diagnostics.OutboundBudgetState != "not_set" || diagnostics.OutboundMaxTokens != nil {
		t.Fatalf("stale selected budget survived: %#v", diagnostics)
	}
	if diagnostics.Attempts[0].OutboundMaxTokens == nil || *diagnostics.Attempts[0].OutboundMaxTokens != 4096 {
		t.Fatalf("historical attempt evidence was overwritten: %#v", diagnostics.Attempts[0])
	}
}

func TestObserveOutboundBudgetUsesMetadataPreferredAlias(t *testing.T) {
	internal := &transmodel.InternalLLMRequest{
		TransformerMetadata: map[string]string{
			transmodel.TransformerMetadataOutboundMaxTokensValue:  "200",
			transmodel.TransformerMetadataOutboundMaxTokensField:  "max_completion_tokens",
			transmodel.TransformerMetadataOutboundMaxTokensSource: "group",
		},
	}
	snapshot := observeOutboundBudget(
		outboundBudgetRequest(t, `{"max_tokens":100,"max_completion_tokens":200}`),
		internal,
	)
	if snapshot.State != "set" || snapshot.Value == nil || *snapshot.Value != 200 ||
		snapshot.Field != "max_completion_tokens" || snapshot.Source != "group" {
		t.Fatalf("preferred alias snapshot = %#v", snapshot)
	}
}

func TestObserveOutboundBudgetRejectsTrailingJSON(t *testing.T) {
	snapshot := observeOutboundBudget(
		outboundBudgetRequest(t, `{"max_tokens":8192} trailing`),
		&transmodel.InternalLLMRequest{},
	)
	if snapshot.State != "unknown" || snapshot.Value != nil {
		t.Fatalf("malformed body produced budget evidence: %#v", snapshot)
	}
}
