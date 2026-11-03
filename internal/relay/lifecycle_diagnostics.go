package relay

import (
	"context"
	"strconv"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
)

// ensureDiagnosticsLocked creates the diagnostics envelope on the first
// observed request milestone. Callers must hold trace.mutex.
func (trace *relayRequestTrace) ensureDiagnosticsLocked() *model.RelayDiagnostics {
	if trace.diagnostics == nil {
		trace.diagnostics = &model.RelayDiagnostics{
			Version:    3,
			RequestID:  trace.id,
			InstanceID: relaylog.LiveInstanceID(),
			ReceivedAt: trace.startedAt.UnixMilli(),
		}
	}
	return trace.diagnostics
}

func (trace *relayRequestTrace) setOperationDeadline(ctx context.Context, source string) {
	if trace == nil {
		return
	}
	trace.mutex.Lock()
	diagnostics := trace.ensureDiagnosticsLocked()
	diagnostics.DeadlineSource = source
	if deadline, ok := ctx.Deadline(); ok {
		diagnostics.DeadlineAt = deadline.UnixMilli()
	}
	trace.mutex.Unlock()
}

// recordOutboundMaxTokens copies transformer metadata that survived into the
// actual outbound request. It does not infer values for adapters that did not
// record provenance, so missing data remains absent rather than misleading.
func (trace *relayRequestTrace) recordOutboundMaxTokens(attempt int, request *transmodel.InternalLLMRequest) {
	if trace == nil || request == nil {
		return
	}
	valueText := request.TransformerMetadata[transmodel.TransformerMetadataOutboundMaxTokensValue]
	source := request.TransformerMetadata[transmodel.TransformerMetadataOutboundMaxTokensSource]
	if valueText == "" || source == "" {
		return
	}
	value, err := strconv.ParseInt(valueText, 10, 64)
	if err != nil || value < 0 {
		return
	}

	trace.mutex.Lock()
	defer trace.mutex.Unlock()
	diagnostics := trace.ensureDiagnosticsLocked()
	diagnostics.OutboundMaxTokens = &value
	diagnostics.OutboundMaxTokensSource = source
	diagnostics.OutboundMaxTokensAdjusted =
		request.TransformerMetadata[transmodel.TransformerMetadataOutboundMaxTokensAdjusted] == "true"
	for i := range diagnostics.Attempts {
		item := &diagnostics.Attempts[i]
		if item.Attempt == attempt {
			item.OutboundMaxTokens = &value
			item.OutboundMaxTokensSource = diagnostics.OutboundMaxTokensSource
			item.OutboundMaxTokensAdjusted = diagnostics.OutboundMaxTokensAdjusted
			break
		}
	}
}

func (trace *relayRequestTrace) recordRobinEnvelope(attempt int, classification RobinEnvelopeClassification) {
	if trace == nil || classification.Kind == RobinEnvelopeNormalContent ||
		classification.Reason == RobinReasonEmptyResponse {
		return
	}
	trace.mutex.Lock()
	defer trace.mutex.Unlock()
	diagnostics := trace.ensureDiagnosticsLocked()
	diagnostics.RobinEnvelopeKind = string(classification.Kind)
	diagnostics.RobinEnvelopeReason = string(classification.Reason)
	for i := range diagnostics.Attempts {
		item := &diagnostics.Attempts[i]
		if item.Attempt == attempt {
			item.RobinEnvelopeKind = diagnostics.RobinEnvelopeKind
			item.RobinEnvelopeReason = diagnostics.RobinEnvelopeReason
			break
		}
	}
}

func (trace *relayRequestTrace) recordReasoningBufferStrategy(strategy string, applied bool) {
	if trace == nil {
		return
	}
	trace.mutex.Lock()
	diagnostics := trace.ensureDiagnosticsLocked()
	diagnostics.ReasoningBufferStrategy = strategy
	diagnostics.ReasoningBufferApplied = applied
	trace.mutex.Unlock()
}

func (trace *relayRequestTrace) recordStreamChunk(reasoningBytes int, visible bool, visibleBytes, toolDeltas int) {
	if trace == nil {
		return
	}
	now := time.Now().UnixMilli()
	trace.mutex.Lock()
	diagnostics := trace.ensureDiagnosticsLocked()
	if reasoningBytes > 0 {
		diagnostics.ReasoningChunksReceived++
		diagnostics.ReasoningContentBytes += int64(reasoningBytes)
		if diagnostics.FirstReasoningAt == 0 {
			diagnostics.FirstReasoningAt = now
		}
		diagnostics.LastReasoningAt = now
	}
	if visible {
		diagnostics.VisibleChunks++
		diagnostics.VisibleContentBytes += int64(max(0, visibleBytes))
	}
	diagnostics.ToolDeltaCount += int64(max(0, toolDeltas))
	trace.mutex.Unlock()
}

func (trace *relayRequestTrace) recordReasoningBuffered(frameBytes int) {
	if trace == nil || frameBytes <= 0 {
		return
	}
	trace.mutex.Lock()
	diagnostics := trace.ensureDiagnosticsLocked()
	diagnostics.ReasoningChunksBuffered++
	diagnostics.ReasoningFrameBytes += int64(frameBytes)
	trace.mutex.Unlock()
}

func (trace *relayRequestTrace) recordReasoningBufferOverflow(droppedChunks, droppedBytes int) {
	if trace == nil {
		return
	}
	trace.mutex.Lock()
	diagnostics := trace.ensureDiagnosticsLocked()
	diagnostics.ReasoningBufferOverflows++
	diagnostics.ReasoningChunksBuffered -= int64(max(0, droppedChunks))
	if diagnostics.ReasoningChunksBuffered < 0 {
		diagnostics.ReasoningChunksBuffered = 0
	}
	diagnostics.ReasoningBufferDropped += int64(max(0, droppedBytes))
	trace.mutex.Unlock()
}

func (trace *relayRequestTrace) recordReasoningBufferFlush(chunks int) {
	if trace == nil || chunks <= 0 {
		return
	}
	trace.mutex.Lock()
	diagnostics := trace.ensureDiagnosticsLocked()
	diagnostics.ReasoningChunksFlushed += int64(chunks)
	diagnostics.ReasoningBufferFlushAt = time.Now().UnixMilli()
	trace.mutex.Unlock()
}

func (trace *relayRequestTrace) markRuntimeTerminal(ctx context.Context, terminalSource string, err error) {
	if trace == nil {
		return
	}
	trace.mutex.Lock()
	if trace.diagnostics != nil && trace.diagnostics.TerminalAt == 0 {
		trace.diagnostics.TerminalAt = time.Now().UnixMilli()
		trace.diagnostics.TerminalSource = terminalSource
		trace.diagnostics.PersistenceState = model.RelayLogPersistencePending
		if err != nil && ctx != nil && ctx.Err() != nil {
			trace.diagnostics.CancelledAt = trace.diagnostics.TerminalAt
			trace.diagnostics.CancelReason = context.Cause(ctx).Error()
		}
	}
	trace.mutex.Unlock()
	relaylog.UpdateLiveRequest(trace.id, func(entry *relaylog.LiveRequest) {
		entry.State = "completed"
		entry.PersistenceState = model.RelayLogPersistencePending
	})
}

func (trace *relayRequestTrace) startDiagnosticAttempt(attempt, channel int, protocol string, ctx context.Context) {
	if trace == nil {
		return
	}
	trace.mutex.Lock()
	defer trace.mutex.Unlock()
	trace.ensureDiagnosticsLocked()
	item := model.RelayAttemptDiagnostics{Attempt: attempt, ChannelID: channel, Protocol: protocol, StartedAt: time.Now().UnixMilli()}
	if deadline, ok := ctx.Deadline(); ok {
		item.DeadlineAt = deadline.UnixMilli()
	}
	trace.diagnostics.Attempts = append(trace.diagnostics.Attempts, item)
}

func (trace *relayRequestTrace) endDiagnosticAttempt(attempt int, ctx context.Context, terminal bool) {
	if trace == nil {
		return
	}
	trace.mutex.Lock()
	defer trace.mutex.Unlock()
	if trace.diagnostics == nil {
		return
	}
	for i := range trace.diagnostics.Attempts {
		item := &trace.diagnostics.Attempts[i]
		if item.Attempt != attempt {
			continue
		}
		item.EndedAt = time.Now().UnixMilli()
		if terminal {
			item.TerminalSource = item.Protocol
		}
		if ctx.Err() != nil {
			item.CancelReason = context.Cause(ctx).Error()
		}
	}
}

func (trace *relayRequestTrace) diagnosticsSnapshot() *model.RelayDiagnostics {
	if trace == nil {
		return nil
	}
	trace.mutex.Lock()
	defer trace.mutex.Unlock()
	if trace.diagnostics == nil {
		return nil
	}
	result := *trace.diagnostics
	result.Attempts = append([]model.RelayAttemptDiagnostics(nil), result.Attempts...)
	result.RoutingConflicts = append([]string(nil), result.RoutingConflicts...)
	result.WriterAcceptedBytes = trace.clientWriteBytes
	return &result
}

func (req *relayRequest) clientCancelled() bool {
	return req.streamSession == nil && isClientDisconnected(req.clientCtx)
}

func (trace *relayRequestTrace) recordRoutingConflicts(conflicts []string) {
	if trace == nil || len(conflicts) == 0 {
		return
	}
	trace.mutex.Lock()
	defer trace.mutex.Unlock()
	if trace.diagnostics == nil {
		return
	}
	for _, conflict := range conflicts {
		if len(trace.diagnostics.RoutingConflicts) >= 64 {
			break
		}
		trace.diagnostics.RoutingConflicts = append(trace.diagnostics.RoutingConflicts, conflict)
	}
}
