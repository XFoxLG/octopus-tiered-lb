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
