package relay

import (
	"context"
	"testing"
	"time"
)

func TestRelayDiagnosticsRecordsRobinEnvelopePerAttempt(t *testing.T) {
	trace := &relayRequestTrace{id: "request", startedAt: time.Now()}
	trace.startDiagnosticAttempt(4, 88, "chat", context.Background())

	trace.recordRobinEnvelope(4, RobinEnvelopeClassification{
		Kind:   RobinEnvelopeInsufficientOutput,
		Reason: RobinReasonDispatchPayloadEmpty,
	})

	diagnostics := trace.diagnosticsSnapshot()
	if diagnostics == nil {
		t.Fatal("diagnostics missing")
	}
	if diagnostics.RobinEnvelopeKind != string(RobinEnvelopeInsufficientOutput) ||
		diagnostics.RobinEnvelopeReason != string(RobinReasonDispatchPayloadEmpty) {
		t.Fatalf("top-level Robin diagnostics = %#v", diagnostics)
	}
	if len(diagnostics.Attempts) != 1 {
		t.Fatalf("attempts = %d, want 1", len(diagnostics.Attempts))
	}
	attempt := diagnostics.Attempts[0]
	if attempt.RobinEnvelopeKind != diagnostics.RobinEnvelopeKind ||
		attempt.RobinEnvelopeReason != diagnostics.RobinEnvelopeReason {
		t.Fatalf("attempt Robin diagnostics = %#v", attempt)
	}
}

func TestRelayDiagnosticsDoesNotRecordNormalRobinContent(t *testing.T) {
	trace := &relayRequestTrace{id: "request", startedAt: time.Now()}
	trace.recordRobinEnvelope(1, RobinEnvelopeClassification{
		Kind:   RobinEnvelopeNormalContent,
		Reason: RobinReasonNormalContent,
	})
	if diagnostics := trace.diagnosticsSnapshot(); diagnostics != nil {
		t.Fatalf("normal content unexpectedly created diagnostics: %#v", diagnostics)
	}
}
