package relay

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tmodel "github.com/lingyuins/octopus/internal/transformer/model"
)

type reasoningThenVisibleOutbound struct {
	stubOutboundAdapter
}

func (reasoningThenVisibleOutbound) TransformStream(_ context.Context, eventData []byte) (*tmodel.InternalLLMResponse, error) {
	raw := strings.TrimSpace(string(eventData))
	switch {
	case raw == "[DONE]":
		return &tmodel.InternalLLMResponse{Object: "[DONE]"}, nil
	case strings.HasPrefix(raw, "reason:"):
		reasoning := strings.TrimPrefix(raw, "reason:")
		return &tmodel.InternalLLMResponse{
			Choices: []tmodel.Choice{{
				Index: 0,
				Delta: &tmodel.Message{ReasoningContent: &reasoning},
			}},
		}, nil
	case strings.HasPrefix(raw, "visible:"):
		content := strings.TrimPrefix(raw, "visible:")
		return &tmodel.InternalLLMResponse{
			Choices: []tmodel.Choice{{
				Index: 0,
				Delta: &tmodel.Message{Content: tmodel.MessageContent{Content: &content}},
			}},
		}, nil
	default:
		return (&stubOutboundAdapter{}).TransformStream(context.Background(), eventData)
	}
}

func TestReasoningBufferDiagnosticsFlushOnlyAfterVisibleOutput(t *testing.T) {
	clientCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ra, recorder := newStreamTestAttempt(t, clientCtx)
	ra.outAdapter = reasoningThenVisibleOutbound{}
	trace := newRelayRequestTrace(ra.c.Request, false)
	trace.setOperationDeadline(context.Background(), "test")
	ra.metrics.SetRequestTrace(trace)

	done := runStreamWithUpstream(t, ra, func(w *io.PipeWriter) {
		_, _ = w.Write([]byte("data: reason:first\n\n"))
		_, _ = w.Write([]byte("data: reason:second\n\n"))
		_, _ = w.Write([]byte("data: reason:third\n\n"))
		_, _ = w.Write([]byte("data: visible:LONG_E2E_BEGIN body LONG_E2E_END\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("handleStreamResponse() = %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not terminate")
	}

	if !strings.Contains(recorder.Body.String(), "LONG_E2E_BEGIN") {
		t.Fatalf("visible body missing: %q", recorder.Body.String())
	}
	diagnostics := trace.diagnosticsSnapshot()
	if diagnostics == nil {
		t.Fatal("diagnostics missing")
	}
	if diagnostics.Version != 2 {
		t.Fatalf("version = %d, want 2", diagnostics.Version)
	}
	if diagnostics.ReasoningBufferStrategy != "buffer" {
		t.Fatalf("strategy = %q, want buffer", diagnostics.ReasoningBufferStrategy)
	}
	if diagnostics.ReasoningChunksReceived != 3 || diagnostics.ReasoningChunksBuffered != 3 || diagnostics.ReasoningChunksFlushed != 3 {
		t.Fatalf("reasoning counts = received:%d buffered:%d flushed:%d, want 3/3/3",
			diagnostics.ReasoningChunksReceived, diagnostics.ReasoningChunksBuffered, diagnostics.ReasoningChunksFlushed)
	}
	if diagnostics.ReasoningFrameBytes <= 0 || diagnostics.ReasoningContentBytes <= 0 {
		t.Fatalf("reasoning byte counters missing: frame=%d content=%d",
			diagnostics.ReasoningFrameBytes, diagnostics.ReasoningContentBytes)
	}
	if diagnostics.FirstReasoningAt == 0 || diagnostics.LastReasoningAt == 0 || diagnostics.ReasoningBufferFlushAt < diagnostics.LastReasoningAt {
		t.Fatalf("invalid reasoning milestones: first=%d last=%d flush=%d",
			diagnostics.FirstReasoningAt, diagnostics.LastReasoningAt, diagnostics.ReasoningBufferFlushAt)
	}
	if diagnostics.VisibleChunks != 1 {
		t.Fatalf("visible chunks = %d, want 1", diagnostics.VisibleChunks)
	}
	if diagnostics.VisibleContentBytes != int64(len("LONG_E2E_BEGIN body LONG_E2E_END")) {
		t.Fatalf("visible bytes = %d", diagnostics.VisibleContentBytes)
	}
	if diagnostics.ToolDeltaCount != 0 {
		t.Fatalf("tool deltas = %d, want 0", diagnostics.ToolDeltaCount)
	}
}

func TestReasoningBufferDiagnosticsRecordOverflowWithoutClaimingDelivery(t *testing.T) {
	trace := &relayRequestTrace{}
	trace.recordReasoningBufferStrategy("buffer", true)
	trace.recordReasoningBuffered(64)
	trace.recordReasoningBuffered(32)
	trace.recordReasoningBufferOverflow(2, 96)

	diagnostics := trace.diagnosticsSnapshot()
	if diagnostics == nil {
		t.Fatal("diagnostics missing")
	}
	if diagnostics.ReasoningChunksBuffered != 0 {
		t.Fatalf("buffered chunks = %d, want 0 after overflow", diagnostics.ReasoningChunksBuffered)
	}
	if diagnostics.ReasoningBufferOverflows != 1 || diagnostics.ReasoningBufferDropped != 96 {
		t.Fatalf("overflow counters = %d/%d, want 1/96",
			diagnostics.ReasoningBufferOverflows, diagnostics.ReasoningBufferDropped)
	}
	if diagnostics.WriterAcceptedBytes != 0 || diagnostics.VisibleChunks != 0 {
		t.Fatalf("overflow must not claim delivery: %+v", diagnostics)
	}
}
