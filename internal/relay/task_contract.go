package relay

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/lingyuins/octopus/internal/pkg/catietasks"
)

// Recovery may learn from a real provider error, but execution must stay bound
// to the submitted task. No classifier or channel rule may resubmit it.
func taskRetryDecision(status int, err error, written bool) RetryDecision {
	decision := ClassifyRelayError(status, err, written)
	decision.Scope = ScopeNone
	decision.IsError = true
	decision.SkipFailureAccounting = true
	decision.Reason = "Catie task cannot be resubmitted"
	return decision
}

func taskResponseError(err error) error {
	var task *catietasks.ResponseError
	if !errors.As(err, &task) || task.StatusCode < http.StatusBadRequest {
		return err
	}
	return fmt.Errorf("%w: %w: %w", catietasks.ErrNoRetry, err, &upstreamHTTPError{
		statusCode: task.StatusCode, body: string(task.Body), retryAfter: task.RetryAfter, receivedAt: time.Now(), cause: task.Cause,
	})
}

func taskStreamCompletionError(state *catietasks.State) error {
	if state == nil {
		return nil
	}
	return state.StreamCompletionError()
}
