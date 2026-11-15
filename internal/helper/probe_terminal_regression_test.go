package helper

import (
	"testing"
	"time"
)

func TestProbeMessagesStopReasonIsNotNormalCompletion(t *testing.T) {
	for _, reason := range []string{"max_tokens", "refusal", "pause_turn", "model_context_window_exceeded"} {
		a := newProbeStreamAccumulator("messages")
		a.Feed([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"safe text\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\""+reason+"\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"), time.Now())
		if !a.TerminalObserved() || a.signals.GenerationState("messages") == "completed" {
			t.Fatalf("%s incorrectly complete: %+v", reason, a.signals)
		}
	}
}

func TestProbeGeminiUnspecifiedDoesNotEndStream(t *testing.T) {
	a := newProbeStreamAccumulator("gemini")
	a.Feed([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"safe text\"}]},\"finishReason\":\"FINISH_REASON_UNSPECIFIED\"}]}\n\n"), time.Now())
	if a.TerminalObserved() {
		t.Fatal("unspecified treated as terminal")
	}
}

func TestProbeFilterReasonCannotBeOverwrittenByLaterStop(t *testing.T) {
	a := newProbeStreamAccumulator("chat")
	a.Feed([]byte("data: {\"choices\":[{\"index\":0,\"finish_reason\":\"content_filter\"}]}\n\ndata: {\"choices\":[{\"index\":0,\"finish_reason\":\"stop\"}]}\n\n"), time.Now())
	if a.signals.GenerationState("chat") != "content_filter" {
		t.Fatal("later stop erased filter")
	}
}

func TestProbeExpectedChoicesCannotGrowFromResponse(t *testing.T) {
	a := newProbeStreamAccumulator("chat")
	a.Feed([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"safe\"},\"finish_reason\":\"stop\"},{\"index\":1,\"finish_reason\":\"stop\"}]}\n\n"), time.Now())
	if a.signals.ChatChoiceCount != 1 || a.TerminalObserved() {
		t.Fatal("response changed expected choices or accepted an unrequested choice")
	}
}

func TestNativeProbeStopsParsingAfterTerminalInSameRead(t *testing.T) {
	for _, protocol := range []string{"messages", "responses", "gemini"} {
		t.Run(protocol, func(t *testing.T) {
			terminal := map[string]string{"messages": `{"type":"message_stop"}`, "responses": `{"type":"response.completed"}`, "gemini": `{"candidates":[{"index":0,"finishReason":"STOP"}]}`}[protocol]
			a := newProbeStreamAccumulator(protocol)
			a.Feed([]byte("data: "+terminal+"\n\ndata: {broken\n\n"), time.Now())
			if !a.TerminalObserved() || a.invalidEvents != 0 || a.eventCount != 1 {
				t.Fatalf("parsed events after %s terminal: %+v", protocol, a)
			}
		})
	}
}

func TestProbeMetadataDoesNotSatisfyFirstOutput(t *testing.T) {
	a := newProbeStreamAccumulator("gemini")
	a.Feed([]byte("data: {\"candidates\":[{\"groundingMetadata\":{\"webSearchQueries\":[\"safe\"]}}]}\n\n"), time.Now())
	if a.firstOutput != nil || a.signals.HasEffectiveOutput() {
		t.Fatal("search metadata satisfied first output")
	}
}

func TestProbeSSECRLFSplitAcrossReads(t *testing.T) {
	a := newProbeStreamAccumulator("messages")
	a.Feed([]byte("data: {\"type\":\"message_stop\"}\r"), time.Now())
	a.Feed([]byte("\n\r"), time.Now())
	if !a.Feed([]byte("\n"), time.Now()) {
		t.Fatal("split CRLF terminal was not parsed")
	}
}

func TestProbeGeminiWaitsForEveryRequestedCandidate(t *testing.T) {
	a := newProbeStreamAccumulator("gemini")
	a.signals.ChatChoiceCount = 2
	a.Feed([]byte("data: {\"candidates\":[{\"index\":0,\"finishReason\":\"MAX_TOKENS\"}]}\n\n"), time.Now())
	if a.TerminalObserved() {
		t.Fatal("stopped before candidate 1")
	}
	a.Feed([]byte("data: {\"candidates\":[{\"index\":1,\"content\":{\"parts\":[{\"text\":\"safe\"}]},\"finishReason\":\"STOP\"}]}\n\n"), time.Now())
	if !a.TerminalObserved() || a.signals.GenerationState("gemini") != "length" {
		t.Fatalf("mixed candidate reason lost: %+v", a.signals)
	}
}

func TestProbeChoiceCountUsesSerializedBody(t *testing.T) {
	for _, tc := range []struct {
		protocol, body string
		want           int
		invalid        bool
	}{
		{"chat", `{}`, 1, false},
		{"chat", `{"n":2}`, 2, false},
		{"gemini", `{"n":3,"generationConfig":{"candidateCount":2}}`, 2, false},
		{"chat", `{"n":0}`, 0, true},
		{"chat", `{"n":1.5}`, 0, true},
	} {
		got, err := probeExpectedChoices([]byte(tc.body), tc.protocol)
		if (err != nil) != tc.invalid || got != tc.want {
			t.Fatalf("%s %s: %d/%v", tc.protocol, tc.body, got, err)
		}
	}
}
