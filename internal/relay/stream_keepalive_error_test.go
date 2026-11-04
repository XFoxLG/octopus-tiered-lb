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
