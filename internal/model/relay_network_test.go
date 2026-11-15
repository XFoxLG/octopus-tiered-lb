package model

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
)

func TestRelayNetworkObserverSeparatesHeadersBodyAndReusedPhases(t *testing.T) {
	headerSent, bodyRelease := make(chan struct{}, 2), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("CF-Ray", "fixture-ray")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		headerSent <- struct{}{}
		<-bodyRelease
		_, _ = w.Write([]byte("safe body"))
	}))
	defer server.Close()
	client := server.Client()
	first := NewRelayNetworkObserver("direct")
	request, _ := http.NewRequestWithContext(first.Context(context.Background()), http.MethodGet, server.URL, nil)
	first.Dispatch()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	<-headerSent
	first.Headers()
	first.ObserveHeaders(response.Header)
	headerSnapshot := first.Snapshot()
	if headerSnapshot.ResponseHeadersAt == 0 || headerSnapshot.HeaderFirstByteAt == 0 || headerSnapshot.FirstBodyByteAt != 0 {
		t.Fatalf("headers misreported as body: %+v", headerSnapshot)
	}
	close(bodyRelease)
	body := first.Body(response.Body)
	bytes, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(bytes) != "safe body" {
		t.Fatalf("response changed: %q %v", bytes, err)
	}
	first.EffectiveOutput(true, false, false)
	if first.Snapshot().FirstEffectiveOutputAt != 0 {
		t.Fatal("reasoning became visible output")
	}
	first.EffectiveOutput(false, true, true)
	snapshot := first.Snapshot()
	if snapshot.FirstBodyByteAt == 0 || snapshot.FirstReasoningAt == 0 || snapshot.FirstEffectiveOutputAt == 0 || snapshot.TerminalAt == 0 || snapshot.CFRay != "fixture-ray" {
		t.Fatalf("missing milestones: %+v", snapshot)
	}
	if snapshot.DNSStartAt != 0 || snapshot.TLSStartAt != 0 {
		t.Fatal("literal HTTP fabricated DNS/TLS")
	}
	second := NewRelayNetworkObserver("direct")
	request, _ = http.NewRequestWithContext(second.Context(context.Background()), http.MethodGet, server.URL, nil)
	second.Dispatch()
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	second.Headers()
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if got := second.Snapshot(); got.ConnectionReused == nil || !*got.ConnectionReused || got.DialStartAt != 0 || got.DNSStartAt != 0 || got.TLSStartAt != 0 {
		t.Fatalf("reuse invented connection stages: %+v", got)
	}
}

func TestRelayNetworkObserverTaskPollsDoNotReplaceSubmission(t *testing.T) {
	observer := NewRelayNetworkObserver("task_submission")
	ctx := observer.Context(context.Background())
	trace := httptrace.ContextClientTrace(ctx)
	trace.DNSStart(httptrace.DNSStartInfo{Host: "fixture.example"})
	trace.DNSDone(httptrace.DNSDoneInfo{})
	observer.Dispatch()
	observer.Headers()
	trace.ConnectStart("tcp", "fixture.example:443")
	trace.GotFirstResponseByte()
	observer.EffectiveOutput(false, true, true)
	observer.Cancelled()
	got := observer.Snapshot()
	if got.DNSStartAt == 0 || got.DNSEndAt == 0 || got.DialStartAt != 0 || got.HeaderFirstByteAt != 0 || got.CancelledAt == 0 {
		t.Fatalf("poll mixed with submission: %+v", got)
	}
}
