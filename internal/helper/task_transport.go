package helper

import (
	"context"
	"fmt"
	"net/http"
	"time"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/pkg/catietasks"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

// doProbeTransport is the single transport boundary shared by saved/draft
// group tests and channel capability probes. Catie Tasks still receives the
// already-converted Chat request and never gains a probe-only retry path.
func doProbeTransport(
	ctx context.Context,
	client *http.Client,
	req *http.Request,
	plan appmodel.ConnectionPlan,
	stream bool,
	evidence *appmodel.ChannelProbeEvidence,
) (*http.Response, *catietasks.State, *appmodel.RelayNetworkObserver, error) {
	source := "direct"
	if plan.Endpoint != nil && plan.Endpoint.EffectiveTransport() == appmodel.EndpointTransportCatieTasks {
		source = "task_submission"
	}
	observer := appmodel.NewRelayNetworkObserver(source)
	req = req.WithContext(observer.Context(ctx))
	markDispatch := func() {
		observer.Dispatch()
		evidence.RequestStartToDispatchMS = probeElapsedMS(evidence.RequestStartedAt, time.Now())
	}
	markHeaders := func() {
		observer.Headers()
		evidence.ResponseHeadersMS = probeElapsedMS(evidence.RequestStartedAt, time.Now())
	}
	if plan.Endpoint != nil && plan.Endpoint.EffectiveTransport() == appmodel.EndpointTransportCatieTasks {
		state := catietasks.NewState()
		config := catietasks.Config{Stream: stream, OnSubmissionStart: markDispatch, OnSubmissionHeaders: markHeaders}
		response, err := catietasks.Do(req.Context(), client, req, config, state)
		if err == nil {
			observer.ObserveHeaders(response.Header)
			response.Body = observer.Body(response.Body)
		}
		return response, state, observer, err
	}
	pinned, err := xurl.AssertSafeRequestWithPin(req)
	if err != nil {
		return nil, nil, observer, fmt.Errorf("probe URL is not allowed: %w", err)
	}
	markDispatch()
	response, err := client.Do(req.WithContext(pinned))
	if err == nil {
		markHeaders()
		observer.ObserveHeaders(response.Header)
		response.Body = observer.Body(response.Body)
	}
	return response, nil, observer, err
}

func applyTaskProbeEvidence(evidence *appmodel.ChannelProbeEvidence, state *catietasks.State) {
	if evidence == nil || state == nil {
		return
	}
	snapshot := state.Snapshot()
	evidence.Transport = snapshot.Transport
	evidence.Task = &appmodel.RelayTaskDiagnostics{
		Transport:            snapshot.Transport,
		SubmissionState:      snapshot.SubmissionState,
		Status:               snapshot.Status,
		Done:                 snapshot.Done,
		Cursor:               snapshot.Cursor,
		PollCount:            snapshot.PollCount,
		QueryRetryCount:      snapshot.QueryRetryCount,
		CancelState:          snapshot.CancelState,
		RequestID:            snapshot.RequestID,
		CFRay:                snapshot.CFRay,
		RndrID:               snapshot.RndrID,
		StartedAt:            snapshot.StartedAt,
		DeadlineAt:           snapshot.DeadlineAt,
		SubmissionDispatchAt: snapshot.SubmissionDispatchAt,
		SubmissionHeadersAt:  snapshot.SubmissionHeadersAt,
		FirstChunkAt:         snapshot.FirstChunkAt,
		LastObservedAt:       snapshot.LastObservedAt,
		CancelledAt:          snapshot.CancelledAt,
		TimedOut:             snapshot.TimedOut,
		TerminalReported:     snapshot.TerminalReported,
	}
}
