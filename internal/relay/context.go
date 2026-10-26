package relay

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/utils/log"
)

const (
	clientDisconnectedLogMessage   = "client disconnected; explicit replay generation remains within its grace period"
	defaultRelayPersistenceTimeout = 10 * time.Second
)

var (
	relayUpstreamTimeout    time.Duration
	relayPersistenceTimeout = defaultRelayPersistenceTimeout
)

func init() {
	if raw := strings.TrimSpace(os.Getenv(strings.ToUpper(conf.APP_NAME) + "_RELAY_UPSTREAM_TIMEOUT_SECONDS")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			relayUpstreamTimeout = time.Duration(v) * time.Second
		}
	}
	if raw := strings.TrimSpace(os.Getenv(strings.ToUpper(conf.APP_NAME) + "_RELAY_PERSISTENCE_TIMEOUT_SECONDS")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			relayPersistenceTimeout = time.Duration(v) * time.Second
		}
	}
}

func newRelayOperationContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if relayUpstreamTimeout > 0 {
		return context.WithTimeout(parent, relayUpstreamTimeout)
	}
	return context.WithCancel(parent)
}

// Deadlines share the request's arrival time; retrying or resolving a group
// never grants a fresh operation budget. Parent deadlines always remain caps.
func withRelayRequestBudget(parent context.Context, receivedAt time.Time, groupSeconds, keySeconds int) (context.Context, context.CancelFunc, string) {
	deadline, hasDeadline := parent.Deadline()
	source := "client_or_global"
	for _, budget := range []struct {
		seconds int
		source  string
	}{{groupSeconds, "group"}, {keySeconds, "api_key"}} {
		if budget.seconds <= 0 {
			continue
		}
		candidate := receivedAt.Add(time.Duration(budget.seconds) * time.Second)
		if !hasDeadline || candidate.Before(deadline) {
			deadline, hasDeadline, source = candidate, true, budget.source
		}
	}
	if hasDeadline {
		ctx, cancel := context.WithDeadline(parent, deadline)
		return ctx, cancel, source
	}
	ctx, cancel := context.WithCancel(parent)
	return ctx, cancel, "none"
}

// Only an admitted, explicitly named replay session detaches cancellation.
// Its operation still retains the original effective deadline.
func newRelayReplayOperationContext(parent context.Context) (context.Context, context.CancelCauseFunc, context.CancelFunc) {
	base := context.WithoutCancel(parent)
	deadlineCancel := func() {}
	if deadline, ok := parent.Deadline(); ok {
		base, deadlineCancel = context.WithDeadline(base, deadline)
	}
	ctx, cancel := context.WithCancelCause(base)
	return ctx, cancel, deadlineCancel
}

func newRelayPersistenceContext() (context.Context, context.CancelFunc) {
	timeout := relayPersistenceTimeout
	if timeout <= 0 || timeout > defaultRelayPersistenceTimeout {
		timeout = defaultRelayPersistenceTimeout
	}
	return context.WithTimeout(context.Background(), timeout)
}

func logRelayErrorfByContext(err error, format string, args ...any) {
	switch {
	case err == nil:
		log.Warnf(format, args...)
	case errors.Is(err, context.Canceled):
		log.Warnf(format, args...)
	case errors.Is(err, context.DeadlineExceeded):
		log.Errorf(format, args...)
	default:
		log.Warnf(format, args...)
	}
}
