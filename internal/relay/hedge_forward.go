package relay

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	dbmodel "github.com/lingyuins/octopus/internal/model"
	ch "github.com/lingyuins/octopus/internal/op/channel"
	rl "github.com/lingyuins/octopus/internal/op/ratelimitstore"
	st "github.com/lingyuins/octopus/internal/op/stats"
	"github.com/lingyuins/octopus/internal/relay/balancer"
	inanthropic "github.com/lingyuins/octopus/internal/transformer/inbound/anthropic"
	inopenai "github.com/lingyuins/octopus/internal/transformer/inbound/openai"
	"github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

// This writer only hands protocol frames to the coordinator. Each clone owns
// its header/status state, and no lane can access the client's writer.
type relayHedgeWriter struct {
	header  http.Header
	size    int
	status  int
	attempt *relayAttempt
	emit    func(relayHedgeFrame) error
}

func (w *relayHedgeWriter) Header() http.Header { return w.header }
func (w *relayHedgeWriter) WriteHeader(status int) {
	if w.size < 0 {
		w.status = status
	}
}
func (w *relayHedgeWriter) WriteHeaderNow() {
	// The coordinator owns the real client writer. A lane reaching this method
	// has not committed client output, so preserve Gin's no-written state.
}
func (w *relayHedgeWriter) Write(payload []byte) (int, error) {
	visible := w.attempt.hedgeVisibleChunk
	terminal := w.attempt.hedgeTerminalChunk()
	if len(payload) == 0 && !visible && !terminal {
		return 0, nil
	}
	err := w.emit(relayHedgeFrame{Payload: payload, Visible: visible, Terminal: terminal, Headers: w.header.Clone()})
	if err != nil {
		return 0, err
	}
	// The coordinator may acknowledge a buffered reasoning or metadata frame
	// before anything reaches the client. Keep Writer.Written false until this
	// lane has emitted the first visible/native-terminal frame; after that all
	// subsequent frames are downstream committed.
	if w.size < 0 && (w.attempt.hedgeVisibleChunk || w.attempt.hedgeTerminalChunk()) {
		w.size = 0
	}
	if w.size >= 0 {
		w.size += len(payload)
	}
	return len(payload), nil
}
func (w *relayHedgeWriter) WriteString(payload string) (int, error) { return w.Write([]byte(payload)) }
func (w *relayHedgeWriter) Flush()                                  {} // coordinator already flushed before acknowledging Write
func (w *relayHedgeWriter) Status() int                             { return w.status }
func (w *relayHedgeWriter) Size() int                               { return w.size }
func (w *relayHedgeWriter) Written() bool                           { return w.size >= 0 }
func (w *relayHedgeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("hedged streams cannot hijack")
}
func (w *relayHedgeWriter) CloseNotify() <-chan bool { return nil }
func (w *relayHedgeWriter) Pusher() http.Pusher      { return nil }

// Hedge lanes use the same isolated working copy as serial attempts.
func cloneHedgeRequest(request *model.InternalLLMRequest) (*model.InternalLLMRequest, error) {
	return model.CloneForAttempt(request), nil
}

func newHedgeInbound(original model.Inbound, request *model.InternalLLMRequest) (model.Inbound, error) {
	var adapter model.Inbound
	switch original.(type) {
	case *inopenai.ChatInbound:
		adapter = &inopenai.ChatInbound{}
	case *inopenai.ResponseInbound:
		adapter = &inopenai.ResponseInbound{}
	case *inanthropic.MessagesInbound:
		adapter = &inanthropic.MessagesInbound{}
	default:
		return nil, errors.New("unsupported hedge inbound protocol")
	}
	if len(request.RawRequest) > 0 {
		if _, err := adapter.TransformRequest(context.Background(), request.RawRequest); err != nil {
			return nil, err
		}
	}
	return adapter, nil
}
func supportsHedgeAdapter(adapter outbound.OutboundType) bool {
	switch adapter {
	case outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeOpenAIResponse, outbound.OutboundTypeAnthropic:
		return true
	default:
		return false
	}
}
func (ra *relayAttempt) canHedge() bool {
	req := ra.relayRequest
	if req == nil || req.group == nil || !req.group.HedgeEnabled || req.group.Mode == dbmodel.GroupModeFailover || req.hedgeAttempted ||
		req.hedgeRemaining < 2 || req.streamSession != nil || req.internalRequest == nil || req.internalRequest.Stream == nil || !*req.internalRequest.Stream ||
		req.iter == nil || req.iter.IsSticky() || ra.channel == nil || ra.channel.Disposable || !supportsHedgeAdapter(ra.adapterType) {
		return false
	}
	switch req.inAdapter.(type) {
	case *inopenai.ChatInbound, *inopenai.ResponseInbound, *inanthropic.MessagesInbound:
	default:
		return false
	}
	for _, item := range req.group.Items {
		if balancer.DisposableChannelFunc != nil && balancer.DisposableChannelFunc(item.ChannelID) {
			return false
		}
	}
	_, ok := req.iter.PeekNext()
	return ok
}
func cloneHedgeAttempt(original *relayAttempt) (*relayAttempt, error) {
	if original == nil || original.relayRequest == nil || original.internalRequest == nil || original.inAdapter == nil || original.metrics == nil || original.c == nil {
		return nil, errors.New("incomplete hedge attempt")
	}
	request, err := cloneHedgeRequest(original.internalRequest)
	if err != nil {
		return nil, err
	}
	adapter, err := newHedgeInbound(original.inAdapter, request)
	if err != nil {
		return nil, err
	}
	if original.channel == nil {
		return nil, errors.New("hedge attempt has no channel")
	}
	plan, err := original.channel.ConnectionPlanFor(original.adapterType)
	if err != nil {
		return nil, err
	}
	outAdapter := plan.Adapter()
	if outAdapter == nil {
		return nil, fmt.Errorf("unsupported hedge outbound adapter %s", original.adapterType)
	}
	// Context.Copy shares its Keys map. Give the lane a private map and remove
	// the request trace so speculative lanes cannot duplicate client-level
	// boundary captures; the selected lane remains covered by normal tracing.
	reqKeys := make(map[any]any, len(original.c.Keys))
	for key, value := range original.c.Keys {
		reqKeys[key] = value
	}
	delete(reqKeys, relayRequestTraceContextKey)
	metrics := *original.metrics
	metrics.InternalRequest = request
	metrics.InternalResponse = nil
	metrics.RequestTrace = nil
	req := *original.relayRequest
	req.internalRequest = request
	req.inAdapter = adapter
	req.metrics = &metrics
	req.c = original.c.Copy()
	req.c.Request = original.c.Request.Clone(original.operationCtx)
	req.c.Keys = reqKeys
	attempt := *original
	attempt.relayRequest = &req
	attempt.channel = original.channel.WithConnectionPlan(plan)
	attempt.outAdapter = outAdapter
	return &attempt, nil
}

func (ra *relayAttempt) forwardWithOptionalHedge(primarySpan *balancer.AttemptSpan) (int, error, *balancer.AttemptSpan) {
	if !ra.canHedge() {
		status, err := ra.forward()
		return status, err, primarySpan
	}
	primary, err := cloneHedgeAttempt(ra)
	if err != nil {
		status, err := ra.forward()
		return status, err, primarySpan
	}
	req := ra.relayRequest
	req.hedgeAttempted = true
	lanes := [2]*relayAttempt{primary, nil}
	spans := [2]*balancer.AttemptSpan{primarySpan, nil}
	statuses := [2]int{}
	laneErrors := [2]error{}
	newLane := func(index int) relayHedgeLane {
		return func(ctx context.Context, emit func(relayHedgeFrame) error) error {
			lane := lanes[index]
			lane.operationCtx = ctx
			lane.c.Request = lane.c.Request.WithContext(ctx)
			lane.c.Writer = &relayHedgeWriter{header: make(http.Header), size: -1, status: http.StatusOK, attempt: lane, emit: emit}
			statuses[index], laneErrors[index] = lane.forward()
			if cause := context.Cause(ctx); cause != nil {
				laneErrors[index] = cause
				return cause
			}
			if hedgeMustStop(lane, statuses[index], laneErrors[index]) {
				return &hedgeTerminalError{laneErrors[index]}
			}
			return laneErrors[index]
		}
	}
	var releaseBackup func()
	startBackup := func() relayHedgeLane {
		item, ok := req.iter.PeekNext()
		if !ok || item.ChannelID == ra.channel.ID || req.operationCtx.Err() != nil || isClientDisconnected(req.clientCtx) {
			return nil
		}
		channel, err := ch.Get(item.ChannelID, req.operationCtx)
		if err != nil || !channel.Enabled || channel.Disposable {
			return nil
		}
		modelName := resolveCandidateModelName(req.iter.RequestModel(), item)
		if strings.TrimSpace(modelName) == "" || !isZenCandidateChannelAllowed(req.requestModel, channel.Type, false) {
			return nil
		}
		if limited, _ := balancer.IsChannelRateLimited(channel.ID, modelName); limited {
			return nil
		}
		plans, conflicts := channel.ResolveRelayConnectionPlans(req.internalRequest, req.group.OutboundFormat, req.group.ForwardMode)
		if trace := relayRequestTraceFromContext(req.c); trace != nil {
			trace.recordRoutingConflicts(conflicts)
		}
		if len(plans) == 0 || !supportsHedgeAdapter(plans[0].AdapterType) || plans[0].Adapter() == nil {
			return nil
		}
		key := channel.GetChannelKeyWithCooldown(modelName, getRatelimitCooldown())
		if key.ChannelKey == "" {
			return nil
		}
		if _, blocked := globalFailureHintCache.get(channel.ID, key.ID, modelName); blocked {
			return nil
		}
		if tripped, _ := balancer.IsTripped(channel.ID, key.ID, modelName); tripped {
			return nil
		}
		backup, err := cloneHedgeAttempt(ra)
		if err != nil {
			return nil
		}
		if channel.MaxConcurrency > 0 {
			if !rl.AcquireChannelSlot(channel.ID, channel.MaxConcurrency) {
				return nil
			}
			releaseBackup = func() { rl.ReleaseChannelSlot(channel.ID) }
		}
		if channel.RPMLimit > 0 {
			if allowed, _ := rl.CheckChannelRPM(channel.ID, modelName, channel.RPMLimit); !allowed {
				if releaseBackup != nil {
					releaseBackup()
					releaseBackup = nil
				}
				return nil
			}
		}
		req.iter.Next()
		backup.channel = channel.WithConnectionPlan(plans[0])
		backup.usedKey = key
		backup.adapterType = plans[0].AdapterType
		backup.outAdapter = plans[0].Adapter()
		backup.internalRequest.Model = modelName
		backup.firstTokenTimeOutSec = channel.EffectiveFirstTokenTimeOut(req.group.FirstTokenTimeOut)
		backup.attemptTimeOutSec = channel.EffectiveAttemptTimeOut(req.group.AttemptTimeOut)
		backup.protocolFallbackRemaining = false
		backup.hedgeWinnerIsBackup = true
		span := req.iter.StartAttempt(channel.ID, key.ID, channel.Name, modelName)
		span.SetAdapterType(backup.adapterType.String())
		backup.logAttemptNumber = span.AttemptNumber()
		lanes[1] = backup
		spans[1] = span
		return newLane(1)
	}
	delay := time.Duration(req.group.HedgeDelayMs) * time.Millisecond
	if delay < 0 || delay > 30*time.Second {
		delay = 300 * time.Millisecond
	}
	coordinatorCtx, cancel := context.WithCancel(req.operationCtx)
	stop := context.AfterFunc(req.clientCtx, cancel)
	defer func() { stop(); cancel() }()
	winner := -1
	outcome, coordinateErr := coordinateRelayHedge(coordinatorCtx, delay, newLane(0), startBackup, func(index int, payload []byte, headers http.Header) error {
		if isClientDisconnected(req.clientCtx) {
			return errClientDisconnected
		}
		if !req.c.Writer.Written() {
			for name, values := range headers {
				req.c.Writer.Header()[name] = append([]string(nil), values...)
			}
		}
		count, err := req.c.Writer.Write(payload)
		if err != nil || count != len(payload) {
			return errClientDisconnected
		}
		req.c.Writer.Flush()
		if writer, ok := req.c.Writer.(*relayResponseWriter); ok {
			writer.markVisible()
		}
		winner = index
		return nil
	})
	if releaseBackup != nil {
		releaseBackup()
	}
	selected := outcome.Winner
	if selected < 0 {
		selected = outcome.StopLane
	}
	if selected < 0 {
		selected = 0
	}
	for index, lane := range lanes {
		if lane == nil || index == selected {
			continue
		}
		failure := laneErrors[index]
		if errors.Is(failure, errHedgeLost) || errors.Is(failure, context.Canceled) || errors.Is(failure, errClientDisconnected) || failure == nil {
			spans[index].End(dbmodel.AttemptCancelled, statuses[index], "hedge lane not selected; no channel health or usage accounting")
		} else {
			spans[index].End(dbmodel.AttemptFailed, statuses[index], failure.Error())
			recordUnselectedHedgeFailure(lane, statuses[index], failure, spans[index].Duration())
		}
	}
	chosen := lanes[selected]
	// All lane goroutines have finished: response state can be moved safely.
	trace := req.metrics.RequestTrace
	*req.metrics = *chosen.metrics
	req.metrics.RequestTrace = trace
	req.internalRequest = chosen.internalRequest
	req.inAdapter = chosen.inAdapter
	savedRequest := ra.relayRequest
	*ra = *chosen
	ra.relayRequest = savedRequest
	ra.streamOutputCommitted = winner >= 0
	if winner < 0 {
		req.metrics.FirstTokenTime = time.Time{}
	}
	if isClientDisconnected(req.clientCtx) {
		return statuses[selected], errClientDisconnected, spans[selected]
	}
	if errors.Is(coordinateErr, errClientDisconnected) {
		return statuses[selected], errClientDisconnected, spans[selected]
	}
	if coordinateErr != nil && outcome.Winner >= 0 {
		return statuses[selected], coordinateErr, spans[selected]
	}
	if outcome.Winner < 0 && laneErrors[selected] == nil {
		return statuses[selected], errEmptyOutput, spans[selected]
	}
	return statuses[selected], laneErrors[selected], spans[selected]
}

// A real upstream failure may still be useful health evidence. Cancellation
// never reaches this helper, and unselected lanes never collect usage/sticky.
func recordUnselectedHedgeFailure(lane *relayAttempt, status int, err error, duration time.Duration) {
	if errors.Is(err, errHedgeLost) || errors.Is(err, context.Canceled) || errors.Is(err, errClientDisconnected) ||
		errors.Is(err, errEmptyOutput) || errors.Is(err, errTruncatedOutput) {
		return
	}
	decision := ClassifyRelayError(status, err, false)
	if termination, ok := terminalCauseFromError(err); ok && customErrorIsDeterministicFailure(termination) {
		return
	}
	decision = applyErrorPolicy(decision, lane.channel, status, extractUpstreamErrorDetail(err))
	if decision.SkipFailureAccounting {
		return
	}
	if status == http.StatusTooManyRequests {
		balancer.RecordChannelRateLimit(lane.channel.ID, lane.usedKey.ID, lane.internalRequest.Model,
			decision.RateLimitScope == RateLimitScopeChannel, decision.RetryAfter)
	}
	if status >= 400 {
		balancer.RecordKeyCooldownWithRetryAfter(lane.channel.ID, lane.usedKey.ID, lane.internalRequest.Model, status, retryAfterFromError(err))
	}
	balancer.RecordKeyAvailability(lane.channel.ID, lane.usedKey.ID, lane.internalRequest.Model, status, false)
	if shouldRecordChannelFailure(decision) {
		balancer.RecordFailure(lane.channel.ID, lane.usedKey.ID, lane.internalRequest.Model)
		balancer.RecordAutoFailure(lane.channel.ID, lane.internalRequest.Model)
		balancer.OutlierReport(lane.channel.ID, false, status, time.Now())
	}
	st.ChannelUpdate(lane.channel.ID, dbmodel.StatsMetrics{WaitTime: duration.Milliseconds(), RequestFailed: 1})
	st.ModelRecord(lane.channel.ID, lane.internalRequest.Model, dbmodel.StatsMetrics{WaitTime: duration.Milliseconds(), RequestFailed: 1})
	recordFailureHint(lane.channel.ID, lane.usedKey.ID, lane.internalRequest.Model, decision, err, getRatelimitCooldown())
}

// Protocol-native refusals and policy terminals must reach the client even
// when they contain no answer text. They do not count as a visible-token metric.
func (ra *relayAttempt) hedgeTerminalChunk() bool {
	if !ra.streamSawTerminalEvent || ra.hasUnfinishedObservedStreamChoices() {
		return false
	}
	cause := ra.streamTermination.Cause
	return cause.IsProviderRefusal() || !cause.AllowsIdenticalReplay() || customErrorIsDeterministicFailure(ra.streamTermination)
}

func hedgeMustStop(lane *relayAttempt, status int, err error) bool {
	if err == nil || errors.Is(err, errEmptyOutput) || errors.Is(err, errTruncatedOutput) {
		return false
	}
	if termination, ok := terminalCauseFromError(err); ok && customErrorIsDeterministicFailure(termination) {
		return true
	}
	decision := applyErrorPolicy(ClassifyRelayError(status, err, false), lane.channel, status, extractUpstreamErrorDetail(err))
	return decision.Scope == ScopeNone || decision.Scope == ScopeAbortAll
}
