package relay

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
)

func TestRelayDiagnosticsRecordsOutboundMaxTokensSnapshot(t *testing.T) {
	trace := &relayRequestTrace{id: "request", startedAt: time.Now()}
	trace.startDiagnosticAttempt(7, 42, "messages", context.Background())
	request := &transmodel.InternalLLMRequest{
		TransformerMetadata: map[string]string{
			transmodel.TransformerMetadataOutboundMaxTokensValue:    "8192",
			transmodel.TransformerMetadataOutboundMaxTokensSource:   transmodel.OutboundMaxTokensSourceAnthropicDefault,
			transmodel.TransformerMetadataOutboundMaxTokensAdjusted: "true",
		},
	}

	trace.recordOutboundMaxTokens(7, request)
	diagnostics := trace.diagnosticsSnapshot()
	if diagnostics == nil {
		t.Fatal("diagnostics missing")
	}
	if diagnostics.Version != 3 {
		t.Fatalf("diagnostics version = %d, want 3", diagnostics.Version)
	}
	if diagnostics.OutboundMaxTokens == nil || *diagnostics.OutboundMaxTokens != 8192 {
		t.Fatalf("outbound max_tokens = %v, want 8192", diagnostics.OutboundMaxTokens)
	}
	if diagnostics.OutboundMaxTokensSource != transmodel.OutboundMaxTokensSourceAnthropicDefault {
		t.Fatalf("outbound max_tokens source = %q, want %q",
			diagnostics.OutboundMaxTokensSource, transmodel.OutboundMaxTokensSourceAnthropicDefault)
	}
	if !diagnostics.OutboundMaxTokensAdjusted {
		t.Fatal("outbound max_tokens adjusted = false, want true")
	}
	if len(diagnostics.Attempts) != 1 {
		t.Fatalf("attempts = %d, want 1", len(diagnostics.Attempts))
	}
	attempt := diagnostics.Attempts[0]
	if attempt.OutboundMaxTokens == nil || *attempt.OutboundMaxTokens != 8192 ||
		attempt.OutboundMaxTokensSource != transmodel.OutboundMaxTokensSourceAnthropicDefault ||
		!attempt.OutboundMaxTokensAdjusted {
		t.Fatalf("attempt max-token provenance = %#v", attempt)
	}

	payload, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "outbound_max_tokens", "outbound_max_tokens_source", "outbound_max_tokens_adjusted"} {
		if _, ok := wire[key]; !ok {
			t.Fatalf("diagnostics JSON missing key %q: %s", key, payload)
		}
	}
}

func TestRelayDiagnosticsIgnoresMissingOutboundMaxTokensMetadata(t *testing.T) {
	trace := &relayRequestTrace{}
	trace.recordOutboundMaxTokens(1, &transmodel.InternalLLMRequest{})

	diagnostics := trace.diagnosticsSnapshot()
	if diagnostics != nil {
		t.Fatalf("outbound max-token diagnostics = %#v, want nil when no provenance exists", diagnostics)
	}
}

func TestRelayDiagnosticsRecordsExplicitZeroOutboundMaxTokensSource(t *testing.T) {
	trace := &relayRequestTrace{id: "request", startedAt: time.Now()}
	trace.startDiagnosticAttempt(1, 42, "chat", context.Background())
	request := &transmodel.InternalLLMRequest{
		TransformerMetadata: map[string]string{
			transmodel.TransformerMetadataOutboundMaxTokensValue:    "0",
			transmodel.TransformerMetadataOutboundMaxTokensSource:   transmodel.OutboundMaxTokensSourceClientMaxTokens,
			transmodel.TransformerMetadataOutboundMaxTokensAdjusted: "false",
		},
	}

	trace.recordOutboundMaxTokens(1, request)
	diagnostics := trace.diagnosticsSnapshot()
	if diagnostics == nil {
		t.Fatal("diagnostics missing")
	}
	if diagnostics.OutboundMaxTokens == nil || *diagnostics.OutboundMaxTokens != 0 ||
		diagnostics.OutboundMaxTokensSource != transmodel.OutboundMaxTokensSourceClientMaxTokens {
		t.Fatalf("top-level explicit zero provenance = %#v", diagnostics)
	}
	if len(diagnostics.Attempts) != 1 {
		t.Fatalf("attempts = %d, want 1", len(diagnostics.Attempts))
	}
	attempt := diagnostics.Attempts[0]
	if attempt.OutboundMaxTokens == nil || *attempt.OutboundMaxTokens != 0 ||
		attempt.OutboundMaxTokensSource != transmodel.OutboundMaxTokensSourceClientMaxTokens {
		t.Fatalf("attempt explicit zero provenance = %#v", attempt)
	}

	payload, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		OutboundMaxTokens *int64 `json:"outbound_max_tokens"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.OutboundMaxTokens == nil || *wire.OutboundMaxTokens != 0 {
		t.Fatalf("explicit zero was not preserved in diagnostics JSON: %s", payload)
	}
}
