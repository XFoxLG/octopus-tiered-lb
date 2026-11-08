package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
	"github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func TestGeminiSymbolicErrorRemainsProviderFailure(t *testing.T) {
	attempt, recorder := newStreamTestAttempt(t, context.Background())
	attempt.adapterType = outbound.OutboundTypeGemini
	attempt.outAdapter = outbound.Get(attempt.adapterType)
	attempt.inAdapter = inbound.Get(inbound.InboundTypeOpenAIChat)
	attempt.group = &appmodel.Group{ReasoningBufferStrategy: "immediate"}
	err := attempt.handleStreamResponse(context.Background(), &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: " + `{"error":{"code":"no_credential","type":"pggo_error","http_status":503,"message":"fixture credential unavailable"}}` + "\n\n")),
	})
	termination, ok := terminalCauseFromError(err)
	if !ok || !errors.Is(err, errProviderTerminalFailure) || termination.Cause != model.TerminationCauseError {
		t.Fatalf("lost provider failure: %v %#v", err, termination)
	}
	if termination.ProviderReason != "pggo_error" || !strings.Contains(err.Error(), "fixture credential unavailable") {
		t.Fatalf("original provider cause masked: %v %#v", err, termination)
	}
	if strings.Contains(err.Error(), "unmarshal") || strings.Contains(recorder.Body.String(), "[DONE]") {
		t.Fatalf("provider error became decode failure or successful completion: %v %s", err, recorder.Body.String())
	}
}
