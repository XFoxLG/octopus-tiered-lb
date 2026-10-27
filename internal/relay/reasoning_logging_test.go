package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/op/relaylog"
)

// TestHandlerLogsEffectiveGroupReasoningEffort pins the user-visible contract:
// when a silent client receives a group-injected level, the relay log records
// the effective attempt request rather than the untouched client request.
func TestHandlerLogsEffectiveGroupReasoningEffort(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}

	var upstreamEffort string
	fixtureID := prepareIndependentRequestFixture(t, func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		var payload struct {
			ReasoningEffort string `json:"reasoning_effort"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
		upstreamEffort = payload.ReasoningEffort
		return newIndependentFixtureResponse(request, http.StatusOK,
			`{"id":"reasoning-log","object":"chat.completion","model":"creative-fixture","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`), nil
	})

	fixtureGroup, err := group.GroupGet(fixtureID, context.Background())
	if err != nil {
		t.Fatalf("get fixture group: %v", err)
	}
	fixtureGroup.DefaultReasoningEffort = "low"
	group.GetCache().Set(fixtureID, *fixtureGroup)
	group.RebuildIndexes()

	recorder := serveIndependentFixtureRequest(`{"model":"creative-fixture","messages":[{"role":"user","content":"Continue the story"}]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if upstreamEffort != "low" {
		t.Fatalf("upstream reasoning_effort = %q, want low", upstreamEffort)
	}

	logs, cacheLock := relaylog.GetCacheAndLock()
	cacheLock.Lock()
	defer cacheLock.Unlock()
	if len(logs) != 1 {
		t.Fatalf("relay log count = %d, want 1", len(logs))
	}
	if logs[0].ReasoningEffort != "low" {
		t.Fatalf("logged reasoning effort = %q, want effective attempt value low", logs[0].ReasoningEffort)
	}
}
