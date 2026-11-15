package helper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

func runStreamingProbeFixture(t *testing.T, body string) *probeRequestOutcome {
	t.Helper()
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	channel := &appmodel.Channel{
		Type:     outbound.OutboundTypeOpenAIChat,
		BaseUrls: []appmodel.BaseUrl{{URL: server.URL}},
	}
	request, err := buildCapabilityProbeRequest(appmodel.ProbeItemStreaming, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	outcome := sendProbeRequest(
		context.Background(),
		channel,
		"sk-test-key",
		probeKeyScope{Index: -1},
		outbound.OutboundTypeOpenAIChat,
		request,
		appmodel.ProbeItemStreaming,
	)
	outcome.Verdict = judgeCapabilityResponse(appmodel.ProbeItemStreaming, outcome)
	return outcome
}

func TestStreamingProbeDoneWithoutFinishIsNotTerminal(t *testing.T) {
	outcome := runStreamingProbeFixture(t,
		"data: {\"choices\":[{\"delta\":{\"content\":\"VISIBLE\"}}]}\n\n"+
			"data: [DONE]\n\n")
	if outcome.Verdict != appmodel.ProbeVerdictUnknown {
		t.Fatalf("DONE without finish_reason = %s, want unknown", outcome.Verdict)
	}
	if outcome.Evidence == nil || outcome.Evidence.TerminalObserved {
		t.Fatalf("DONE without finish_reason was recorded as terminal: %+v", outcome.Evidence)
	}
}

func TestStreamingProbeRequiresEveryObservedChoice(t *testing.T) {
	outcome := runStreamingProbeFixture(t,
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"VISIBLE\"},\"finish_reason\":\"stop\"},{\"index\":1,\"delta\":{\"content\":\"PARTIAL\"}}]}\n\n"+
			"data: {\"choices\":[{\"index\":0,\"finish_reason\":\"stop\"},{\"index\":1,\"finish_reason\":\"stop\"}]}\n\n"+
			"data: [DONE]\n\n")
	if outcome.Verdict != appmodel.ProbeVerdictPass {
		t.Fatalf("all choices eventually terminated = %s, want pass; summary=%s", outcome.Verdict, outcome.Summary)
	}
	if outcome.Evidence.ChatExpectedChoices != 2 || outcome.Evidence.ChatCompletedChoices != 2 {
		t.Fatalf("choice evidence = %d/%d", outcome.Evidence.ChatCompletedChoices, outcome.Evidence.ChatExpectedChoices)
	}
}

func TestStreamingProbeIncompleteChoiceRemainsUnknown(t *testing.T) {
	outcome := runStreamingProbeFixture(t,
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"VISIBLE\"},\"finish_reason\":\"stop\"},{\"index\":1,\"delta\":{\"content\":\"PARTIAL\"}}]}\n\n"+
			"data: [DONE]\n\n")
	if outcome.Verdict != appmodel.ProbeVerdictUnknown {
		t.Fatalf("missing second choice terminal = %s, want unknown", outcome.Verdict)
	}
}

func TestStreamingProbeControlOnlyDoesNotCountAsOutput(t *testing.T) {
	outcome := runStreamingProbeFixture(t,
		": keepalive\n\n"+
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"+
			"data: [DONE]\n\n")
	if outcome.Verdict != appmodel.ProbeVerdictUnknown {
		t.Fatalf("control-only stream = %s, want unknown", outcome.Verdict)
	}
	if outcome.Evidence.FirstEffectiveOutputMS != nil {
		t.Fatal("control frame was counted as first effective output")
	}
}

func TestStreamingProbeContentFilterRetainsTerminalEvidence(t *testing.T) {
	outcome := runStreamingProbeFixture(t,
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"VISIBLE\"},\"finish_reason\":\"stop\"}]}\n\n"+
			"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"content_filter\"}]}\n\n"+
			"data: [DONE]\n\n")
	if outcome.Verdict != appmodel.ProbeVerdictUnknown {
		t.Fatalf("content_filter terminal = %s, want unknown rather than normal success", outcome.Verdict)
	}
	if outcome.Evidence == nil || !outcome.Evidence.TerminalObserved ||
		outcome.Evidence.GenerationState != "content_filter" {
		t.Fatalf("content_filter terminal evidence lost: %+v", outcome.Evidence)
	}
}

func TestStreamingProbeRecordsHeadersAndBodySeparately(t *testing.T) {
	outcome := runStreamingProbeFixture(t,
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"VISIBLE\"},\"finish_reason\":\"stop\"}]}\n\n"+
			"data: [DONE]\n\n")
	if outcome.Evidence.ResponseHeadersMS == nil || outcome.Evidence.FirstBodyByteMS == nil ||
		outcome.Evidence.FirstEffectiveOutputMS == nil || outcome.Evidence.TerminalMS == nil ||
		outcome.Evidence.ResponseCompleteMS == nil {
		t.Fatalf("missing timing evidence: %+v", outcome.Evidence)
	}
	if outcome.Evidence.ResponseBytes == 0 || outcome.Evidence.EffectiveEventCount == 0 {
		t.Fatalf("missing byte/event evidence: %+v", outcome.Evidence)
	}
}

func runNativeStreamingProbeFixture(t *testing.T, adapter outbound.OutboundType, body string) *probeRequestOutcome {
	t.Helper()
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	channel := &appmodel.Channel{Type: adapter, BaseUrls: []appmodel.BaseUrl{{URL: server.URL}}}
	request, err := buildCapabilityProbeRequest(appmodel.ProbeItemStreaming, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	return sendProbeRequest(
		context.Background(),
		channel,
		"sk-test-key",
		probeKeyScope{Index: -1},
		adapter,
		request,
		appmodel.ProbeItemStreaming,
	)
}

func TestStreamingProbeMessagesRequiresMessageStop(t *testing.T) {
	blockOnly := runNativeStreamingProbeFixture(t, outbound.OutboundTypeAnthropic,
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"VISIBLE\"}}\n\n"+
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
	blockOnly.Verdict = judgeCapabilityResponse(appmodel.ProbeItemStreaming, blockOnly)
	if blockOnly.Verdict != appmodel.ProbeVerdictUnknown {
		t.Fatalf("content_block_stop was treated as message terminal: %s", blockOnly.Verdict)
	}

	complete := runNativeStreamingProbeFixture(t, outbound.OutboundTypeAnthropic,
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"VISIBLE\"}}\n\n"+
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"+
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	complete.Verdict = judgeCapabilityResponse(appmodel.ProbeItemStreaming, complete)
	if complete.Verdict != appmodel.ProbeVerdictPass {
		t.Fatalf("message_stop should complete a valid Messages stream: %s (%s)", complete.Verdict, complete.Summary)
	}
}

func TestStreamingProbeResponsesPreservesIncomplete(t *testing.T) {
	outcome := runNativeStreamingProbeFixture(t, outbound.OutboundTypeOpenAIResponse,
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"VISIBLE\"}\n\n"+
			"event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\"}}\n\n")
	outcome.Verdict = judgeCapabilityResponse(appmodel.ProbeItemStreaming, outcome)
	if outcome.Evidence == nil || !outcome.Evidence.TerminalObserved || outcome.Evidence.GenerationState != "incomplete" {
		t.Fatalf("Responses incomplete terminal lost: %+v", outcome.Evidence)
	}
	if outcome.Verdict != appmodel.ProbeVerdictUnknown {
		t.Fatalf("incomplete Responses stream = %s, want unknown", outcome.Verdict)
	}
}

func TestStreamingProbeGeminiRequiresFinishReason(t *testing.T) {
	outcome := runNativeStreamingProbeFixture(t, outbound.OutboundTypeGemini,
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"VISIBLE\"}]}}]}\n\n")
	outcome.Verdict = judgeCapabilityResponse(appmodel.ProbeItemStreaming, outcome)
	if outcome.Verdict != appmodel.ProbeVerdictUnknown || outcome.Evidence.TerminalObserved {
		t.Fatalf("Gemini stream without finishReason was accepted: %s %+v", outcome.Verdict, outcome.Evidence)
	}

	complete := runNativeStreamingProbeFixture(t, outbound.OutboundTypeGemini,
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"VISIBLE\"}]},\"finishReason\":\"STOP\"}]}\n\n")
	complete.Verdict = judgeCapabilityResponse(appmodel.ProbeItemStreaming, complete)
	if complete.Verdict != appmodel.ProbeVerdictPass || !complete.Evidence.TerminalObserved {
		t.Fatalf("Gemini finishReason did not complete: %s %+v", complete.Verdict, complete.Evidence)
	}
}
