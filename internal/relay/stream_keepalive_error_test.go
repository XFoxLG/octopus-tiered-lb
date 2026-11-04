package relay

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
)

func enableKeepaliveForIndependentFixture(t *testing.T, fixtureID int) {
	t.Helper()
	fixtureGroup, err := group.GroupGet(fixtureID, context.Background())
	if err != nil {
		t.Fatalf("load fixture group: %v", err)
	}
	fixtureGroup.StreamKeepaliveEnabled = true
	group.GetCache().Set(fixtureID, *fixtureGroup)
	group.RebuildIndexes()
}

func assertJSONConflictWithoutKeepalive(t *testing.T, recorder interface {
	Result() *http.Response
}) {
	t.Helper()
	response := recorder.Result()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", response.StatusCode, http.StatusConflict, body)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON; body=%s", contentType, body)
	}
	if strings.Contains(string(body), ": ping") {
		t.Fatalf("error response was committed as a keepalive SSE stream: %s", body)
	}
}

func TestHandlerStartsOneKeepaliveForOwnedSession(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}

	resetRelayStreamSessionsForTest(t)
	fixtureID := prepareIndependentRequestFixture(t, func(request *http.Request) (*http.Response, error) {
		response := newIndependentFixtureResponse(request, http.StatusOK,
			"data: "+`{"id":"keepalive-once","object":"chat.completion.chunk","model":"creative-fixture","choices":[{"index":0,"delta":{"content":"ok"}}]}`+"\n\n"+
				"data: "+`{"id":"keepalive-once","object":"chat.completion.chunk","model":"creative-fixture","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`+"\n\n"+
				"data: [DONE]\n\n")
		response.Header.Set("Content-Type", "text/event-stream")
		return response, nil
	})
	enableKeepaliveForIndependentFixture(t, fixtureID)

	recorder := serveIndependentFixtureRequest(`{"conversation_id":"conv-keepalive-once","model":"creative-fixture","stream":true,"messages":[{"role":"user","content":"Continue"}]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if pings := strings.Count(recorder.Body.String(), ": ping"); pings != 1 {
		t.Fatalf("owned stream initial ping count = %d, want exactly 1; body=%s", pings, recorder.Body.String())
	}
}

func TestHandlerKeepaliveBusySessionReturnsJSONConflictBeforeInitialPing(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}

	resetRelayStreamSessionsForTest(t)
	fixtureID := prepareIndependentRequestFixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("busy session must fail before any upstream dispatch")
		return nil, nil
	})
	enableKeepaliveForIndependentFixture(t, fixtureID)

	requestBody := `{"conversation_id":"conv-keepalive-busy","model":"creative-fixture","stream":true,"messages":[{"role":"user","content":"Continue"}]}`
	requestHash := buildRelayStreamSessionHash(
		appmodel.EndpointTypeChat,
		int(inbound.InboundTypeOpenAIChat),
		890000,
		[]byte(requestBody),
	)
	otherHash := requestHash + 1
	if otherHash == 0 {
		otherHash = requestHash - 1
	}
	session, created, err := acquireRelayStreamSession("conv-keepalive-busy", 890000, otherHash)
	if err != nil || !created || session == nil {
		t.Fatalf("acquire busy fixture session = (%v, %t, %v)", session, created, err)
	}

	recorder := serveIndependentFixtureRequest(requestBody)
	assertJSONConflictWithoutKeepalive(t, recorder)
}

func TestHandlerKeepaliveExpiredReplayReturnsJSONConflictBeforeInitialPing(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}

	resetRelayStreamSessionsForTest(t)
	fixtureID := prepareIndependentRequestFixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("expired replay must fail before any upstream dispatch")
		return nil, nil
	})
	enableKeepaliveForIndependentFixture(t, fixtureID)

	requestBody := `{"conversation_id":"conv-keepalive-expired","last_event_id":1,"model":"creative-fixture","stream":true,"messages":[{"role":"user","content":"Continue"}]}`
	requestHash := buildRelayStreamSessionHash(
		appmodel.EndpointTypeChat,
		int(inbound.InboundTypeOpenAIChat),
		890000,
		[]byte(requestBody),
	)
	session, created, err := acquireRelayStreamSession("conv-keepalive-expired", 890000, requestHash)
	if err != nil || !created || session == nil {
		t.Fatalf("acquire expired replay fixture session = (%v, %t, %v)", session, created, err)
	}
	session.AddPayload([]byte("data: first\n\n"))
	session.droppedBeforeSeq = 10
	session.Finish(nil)

	recorder := serveIndependentFixtureRequest(requestBody)
	assertJSONConflictWithoutKeepalive(t, recorder)
}
