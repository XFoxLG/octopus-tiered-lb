package relay

import (
	"github.com/lingyuins/octopus/internal/model"
	"testing"
)

func TestContentBlockKeepsConnectionSuccessSeparate(t *testing.T) {
	record := model.RelayLog{Error: "content_filter", TerminationCause: "content_filter", Attempts: []model.ChannelAttempt{{Status: model.AttemptFailed, HTTPStatus: 200}}}
	generation, upstream := relayLogOutcomes(record)
	if generation != model.RelayLogGenerationFailed || upstream != model.RelayLogUpstreamSuccess {
		t.Fatalf("conflated outcomes: %s %s", generation, upstream)
	}
	record.TerminationCause = "transport_interrupted"
	_, upstream = relayLogOutcomes(record)
	if upstream != model.RelayLogUpstreamFailed {
		t.Fatalf("transport failure hidden: %s", upstream)
	}
}

func TestRelayLogOutcomesCancelledAttemptsDoNotCountAsFailure(t *testing.T) {
	record := model.RelayLog{Attempts: []model.ChannelAttempt{{Status: model.AttemptCancelled}}}
	generation, upstream := relayLogOutcomes(record)
	if generation != model.RelayLogGenerationUnknown || upstream != model.RelayLogUpstreamUnknown {
		t.Fatalf("cancelled-only outcomes = (%s, %s), want unknown/unknown", generation, upstream)
	}

	record.Attempts = []model.ChannelAttempt{
		{Status: model.AttemptCancelled},
		{Status: model.AttemptSuccess, HTTPStatus: 200},
	}
	generation, upstream = relayLogOutcomes(record)
	if generation != model.RelayLogGenerationSuccess || upstream != model.RelayLogUpstreamSuccess {
		t.Fatalf("cancelled+success outcomes = (%s, %s), want success/success", generation, upstream)
	}
}
