package helper

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

func runStreamingProbeFixture(t *testing.T, body string, choices ...int) *probeRequestOutcome {
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
	if len(choices) > 0 {
		request.RawRequest = []byte(fmt.Sprintf(`{"n":%d}`, choices[0]))
		request.ExtraBody = []byte(fmt.Sprintf(`{"n":%d}`, choices[0]))
		channel.ConnectionConfig = &appmodel.ConnectionConfig{Version: 1, Selection: "same_protocol", Catalog: appmodel.ModelCatalog{Format: "manual"}, Endpoints: []appmodel.ChannelEndpoint{{ID: "chat", Protocol: "chat", URL: server.URL, URLMode: "base", Auth: "bearer", ForwardMode: "convert"}}}
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
			"data: [DONE]\n\n", 2)
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
			"data: [DONE]\n\n", 2)
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

func TestStreamingProbeTailTimeoutIsNotClientCancellation(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"VISIBLE\"},\"finish_reason\":\"stop\"}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	request, _ := buildCapabilityProbeRequest(appmodel.ProbeItemStreaming, "test-model")
	started := time.Now()
	out := sendProbeRequest(context.Background(), &appmodel.Channel{Type: outbound.OutboundTypeOpenAIChat, BaseUrls: []appmodel.BaseUrl{{URL: server.URL}}}, "fixture", probeKeyScope{Index: -1}, outbound.OutboundTypeOpenAIChat, request, appmodel.ProbeItemStreaming)
	if judgeCapabilityResponse(appmodel.ProbeItemStreaming, out) != appmodel.ProbeVerdictPass || out.Evidence.ReadState != "tail_window_expired" || out.Evidence.Network.CancelledAt != 0 || !out.Evidence.TerminalObserved || out.Evidence.DoneObserved || time.Since(started) > 4*time.Second {
		t.Fatalf("tail timeout became cancellation or lost terminal: %+v", out.Evidence)
	}
}

func TestNonstreamProbeFilterRetainsTerminalTiming(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"choices":[{"index":0,"message":{"content":"safe partial"},"finish_reason":"content_filter"}]}`)
	}))
	t.Cleanup(server.Close)
	request, _ := buildCapabilityProbeRequest(appmodel.ProbeItemTextGeneration, "test-model")
	out := sendProbeRequest(context.Background(), &appmodel.Channel{Type: outbound.OutboundTypeOpenAIChat, BaseUrls: []appmodel.BaseUrl{{URL: server.URL}}}, "fixture", probeKeyScope{Index: -1}, outbound.OutboundTypeOpenAIChat, request, appmodel.ProbeItemTextGeneration)
	if !out.Evidence.TerminalObserved || out.Evidence.TerminalMS == nil || out.Evidence.FirstEffectiveOutputMS == nil || out.Evidence.Network.TerminalAt == 0 || out.Evidence.GenerationState != "content_filter" || out.Verdict != appmodel.ProbeVerdictUnknown {
		t.Fatalf("filter terminal timing missing: %+v", out.Evidence)
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
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n"+
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

func TestStreamingProbeCatieTasksRecordsTaskEvidence(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	var submissions, queries atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/openai/tasks":
			submissions.Add(1)
			_, _ = w.Write([]byte(`{"id":"stream-task"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/openai/tasks/stream-task":
			queries.Add(1)
			if r.URL.Query().Get("after") == "0" {
				_, _ = w.Write([]byte(`{"chunks":["data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"VISIBLE\"},\"finish_reason\":\"stop\"}]}\n\n","data: [DONE]\n\n"],"next_after":2,"done":true,"status":"completed"}`))
				return
			}
			http.Error(w, "unexpected cursor", http.StatusConflict)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	channel := &appmodel.Channel{
		Type: outbound.OutboundTypeOpenAIChat,
		ConnectionConfig: &appmodel.ConnectionConfig{
			Version:   appmodel.ConnectionConfigVersionTasks,
			Selection: "same_protocol",
			Endpoints: []appmodel.ChannelEndpoint{{
				ID:          "chat",
				Protocol:    "chat",
				URL:         server.URL + "/v1",
				URLMode:     "base",
				Auth:        "bearer",
				ForwardMode: "convert",
				Transport:   appmodel.EndpointTransportCatieTasks,
				TaskAPIURL:  server.URL + "/v1/openai/tasks",
			}},
			Catalog: appmodel.ModelCatalog{Format: "manual"},
		},
	}
	request, err := buildCapabilityProbeRequest(appmodel.ProbeItemStreaming, "task-model")
	if err != nil {
		t.Fatal(err)
	}
	outcome := sendProbeRequest(
		context.Background(),
		channel,
		"fixture-key",
		probeKeyScope{Index: 0, KeyID: 42},
		outbound.OutboundTypeOpenAIChat,
		request,
		appmodel.ProbeItemStreaming,
	)
	outcome.Verdict = judgeCapabilityResponse(appmodel.ProbeItemStreaming, outcome)
	if outcome.Verdict != appmodel.ProbeVerdictPass {
		t.Fatalf("task probe = %s (%s, evidence=%+v)", outcome.Verdict, outcome.Summary, outcome.Evidence)
	}
	if outcome.Evidence == nil || outcome.Evidence.Task == nil {
		t.Fatal("task diagnostics were not attached to probe evidence")
	}
	task := outcome.Evidence.Task
	if task.SubmissionState != "accepted" || task.Status != "completed" || !task.Done || task.Cursor != 2 || task.PollCount != 1 {
		t.Fatalf("task evidence = %+v", task)
	}
	if submissions.Load() != 1 || queries.Load() != 1 {
		t.Fatalf("task submission/query count = %d/%d", submissions.Load(), queries.Load())
	}
}

func TestStreamingProbeCatieTaskRunningAfterFinishIsNotPass(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	var posts, cancels atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/openai/tasks/task/cancel":
			cancels.Add(1)
			_, _ = fmt.Fprint(w, `{}`)
		case r.Method == http.MethodPost:
			posts.Add(1)
			_, _ = fmt.Fprint(w, `{"id":"task"}`)
		default:
			_, _ = fmt.Fprint(w, `{"status":"running","done":false,"next_after":1,"chunks":["data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"VISIBLE\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"]}`)
		}
	}))
	t.Cleanup(server.Close)
	ch := &appmodel.Channel{Type: outbound.OutboundTypeOpenAIChat, ConnectionConfig: &appmodel.ConnectionConfig{Version: 2, Selection: "configured", Catalog: appmodel.ModelCatalog{Format: "manual"}, Endpoints: []appmodel.ChannelEndpoint{{ID: "tasks", Protocol: "chat", URL: server.URL, URLMode: "base", Auth: "bearer", ForwardMode: "convert", Transport: "catie_tasks", TaskAPIURL: server.URL + "/v1/openai/tasks"}}}}
	request, _ := buildCapabilityProbeRequest(appmodel.ProbeItemStreaming, "task-model")
	out := sendProbeRequest(context.Background(), ch, "fixture", probeKeyScope{KeyID: 42}, outbound.OutboundTypeOpenAIChat, request, appmodel.ProbeItemStreaming)
	if out.Verdict != appmodel.ProbeVerdictUnknown || !out.Evidence.TerminalObserved || out.Evidence.Task == nil || out.Evidence.Task.Done || out.Evidence.Task.CancelState != "confirmed" || posts.Load() != 1 || cancels.Load() != 1 {
		t.Fatalf("task/SSE disagreement hidden: %+v", out.Evidence)
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
