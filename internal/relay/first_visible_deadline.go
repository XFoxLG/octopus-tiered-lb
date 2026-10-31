package relay

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type firstVisibleDeadlineState struct {
	mu       sync.Mutex
	deadline time.Time
}

type firstVisibleDispatchGuard struct {
	ctx      context.Context
	parent   context.Context
	cancel   context.CancelFunc
	timer    *time.Timer
	timedOut atomic.Bool
}

func (req *relayRequest) ensureFirstVisibleDeadlineState() *firstVisibleDeadlineState {
	if req == nil {
		return nil
	}
	if req.firstVisibleDeadline == nil {
		req.firstVisibleDeadline = &firstVisibleDeadlineState{}
	}
	return req.firstVisibleDeadline
}

// withFirstVisibleDeadline attaches the request-wide first visible output
// deadline to an upstream attempt. The absolute deadline is armed exactly once
// and includes DNS, dial, TLS, HTTP response headers, retry, hedge, and protocol
// fallback time. Paths that arm the deadline are all real upstream dispatches.
func (req *relayRequest) withFirstVisibleDeadline(parent context.Context, seconds int) *firstVisibleDispatchGuard {
	if req == nil || seconds <= 0 {
		return &firstVisibleDispatchGuard{ctx: parent, parent: parent, cancel: func() {}}
	}

	state := req.ensureFirstVisibleDeadlineState()
	state.mu.Lock()
	if state.deadline.IsZero() {
		state.deadline = time.Now().Add(time.Duration(seconds) * time.Second)
	}
	deadline := state.deadline
	state.mu.Unlock()

	ctx, cancel := context.WithCancel(parent)
	guard := &firstVisibleDispatchGuard{ctx: ctx, parent: parent, cancel: cancel}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		guard.timedOut.Store(true)
		cancel()
		return guard
	}
	guard.timer = time.AfterFunc(remaining, func() {
		// A whole-request or attempt deadline expiring first must retain its
		// original attribution. Only the request-wide first-visible timer owns
		// this cancellation when the parent is still live.
		if parent.Err() == nil {
			guard.timedOut.Store(true)
		}
		cancel()
	})
	return guard
}

func (g *firstVisibleDispatchGuard) Context() context.Context {
	if g == nil || g.ctx == nil {
		return context.Background()
	}
	return g.ctx
}

func (g *firstVisibleDispatchGuard) ResponseHeadersReceived() {
	if g != nil && g.timer != nil {
		g.timer.Stop()
	}
}

func (g *firstVisibleDispatchGuard) TimedOut() bool {
	return g != nil && g.timedOut.Load()
}

func (g *firstVisibleDispatchGuard) Close() {
	if g != nil {
		if g.timer != nil {
			g.timer.Stop()
		}
		g.cancel()
	}
}

// firstVisibleDeadlineApplies verifies that ctx's expiry is the first-visible
// deadline rather than an earlier whole-request or per-attempt timeout.
func (req *relayRequest) firstVisibleDeadlineApplies(ctx context.Context) bool {
	if req == nil || ctx == nil {
		return false
	}

	state := req.firstVisibleDeadline
	if state == nil {
		return false
	}
	state.mu.Lock()
	firstVisible := state.deadline
	state.mu.Unlock()
	if firstVisible.IsZero() || time.Now().Before(firstVisible) {
		return false
	}

	ctxDeadline, ok := ctx.Deadline()
	return !ok || firstVisible.Before(ctxDeadline)
}

func (req *relayRequest) remainingFirstVisibleDeadline() (time.Duration, bool) {
	if req == nil {
		return 0, false
	}
	state := req.firstVisibleDeadline
	if state == nil {
		return 0, false
	}
	state.mu.Lock()
	deadline := state.deadline
	state.mu.Unlock()
	if deadline.IsZero() {
		return 0, false
	}
	remaining := time.Until(deadline)
	if remaining < 0 {
		remaining = 0
	}
	return remaining, true
}
