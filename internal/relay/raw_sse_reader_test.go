package relay

import (
	"context"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestRawSSEPreservesNativeFraming(t *testing.T) {
	wire := ": comment\r\n\r\nid: abc\r\nevent: chunk\r\ndata: {\"a\":\r\ndata: 1}\r\n\r\nretry: 1000\n\ndata: [DONE]\n\n"
	scanner := rawSSEScanner(strings.NewReader(wire))
	var frames []string
	for scanner.Scan() {
		frames = append(frames, string(scanner.Bytes()))
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(frames, "") != wire || rawSSEData([]byte(frames[1])) != "{\"a\":\n1}" {
		t.Fatalf("raw frames changed: %q", frames)
	}
}

func TestRawStreamBypassesInboundReframing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempt, recorder := newStreamTestAttempt(t, context.Background())
		attempt.adapterType = outbound.OutboundTypeRaw
		attempt.internalRequest.RawAPIFormat = "openai/chat_completions"
		// Use the stub decoder while raw forwarding must ignore its renderer.
		wire := ": upstream\r\n\r\nid: original\r\nevent: delta\r\ndata: hello\r\n\r\ndata: [DONE]\r\n\r\n"
		reader, writer := io.Pipe()
		defer writer.Close()
		go func() { _, _ = io.WriteString(writer, wire) }()
		done := make(chan error, 1)
		go func() {
			done <- attempt.handleStreamResponse(context.Background(), &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader})
		}()
		synctest.Wait()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("raw terminal waited for socket")
		}
		if recorder.Body.String() != wire {
			t.Fatalf("raw SSE changed: %q", recorder.Body.String())
		}
	})
}

func TestRawReplayPreservesWireWithoutSyntheticID(t *testing.T) {
	attempt, _ := newStreamTestAttempt(t, context.Background())
	session := attachStreamSession(t, attempt)
	session.preserveWire = true
	wire := []byte("id: provider-id\r\nevent: content\r\ndata: hello\r\n\r\n")
	events := session.AddPayload(wire)
	if len(events) != 1 || string(session.formatEvent(events[0])) != string(wire) {
		t.Fatal("raw replay changed original id/event/CRLF")
	}
}
