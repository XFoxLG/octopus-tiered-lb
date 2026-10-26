package relay

import (
	"context"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/relaylog"
)

func (trace *relayRequestTrace) setOperationDeadline(ctx context.Context, source string) {
	if trace == nil {
		return
	}
	trace.mutex.Lock()
	if trace.diagnostics == nil {
		trace.diagnostics = &model.RelayDiagnostics{Version: 1, RequestID: trace.id, InstanceID: relaylog.LiveInstanceID(), ReceivedAt: trace.startedAt.UnixMilli()}
	}
	trace.diagnostics.DeadlineSource = source
	if deadline, ok := ctx.Deadline(); ok {
		trace.diagnostics.DeadlineAt = deadline.UnixMilli()
	}
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
	if trace.diagnostics == nil {
		return
	}
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
