package helper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	m "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

func TestRerankProbeValidatesResultEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		pass       bool
	}{
		{"valid", `{"results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.1}]}`, true},
		{"score_alias", `{"results":[{"index":0,"score":0},{"index":1,"score":-2}]}`, true},
		{"html", `<html>200 OK</html>`, false},
		{"chat", `{"choices":[{"message":{"content":"OK"}}]}`, false},
		{"error", `{"error":{},"results":[{"index":0,"score":1},{"index":1,"score":0}]}`, false},
		{"empty", `{"results":[]}`, false},
		{"missing_score", `{"results":[{"index":0},{"index":1,"score":0}]}`, false},
		{"duplicate", `{"results":[{"index":0,"score":1},{"index":0,"score":0}]}`, false},
		{"out_of_range", `{"results":[{"index":0,"score":1},{"index":2,"score":0}]}`, false},
		{"missing_index", `{"results":[{"score":1},{"index":1,"score":0}]}`, false},
		{"not_finite", `{"results":[{"index":0,"score":1e999},{"index":1,"score":0}]}`, false},
		{"conflict", `{"results":[{"index":0,"score":1,"relevance_score":0},{"index":1,"score":0}]}`, false},
		{"partial", `{"results":[{"index":0,"score":1}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateRerankProbeResponse([]byte(tc.body), 2) == nil; got != tc.pass {
				t.Fatalf("pass=%v want=%v", got, tc.pass)
			}
		})
	}
}

func TestGroupRerankProbeUsesRerankAndRedactsHeaderSecrets(t *testing.T) {
	setupHelperDB(t)
	relaylog.SetCacheForTest(nil)
	t.Cleanup(func() { relaylog.SetCacheForTest(nil) })
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/rerank" || r.Header.Get("X-Channel") != "fixture-channel-secret" || r.Header.Get("X-Endpoint") != "fixture-endpoint-secret" || r.Header.Get("X-Group") != "fixture-group-secret" {
			t.Errorf("wrong group probe endpoint/headers: %s", r.URL.Path)
		}
		var body map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body["messages"]) != 0 || len(body["query"]) == 0 || len(body["documents"]) == 0 || len(body["reasoning_effort"]) != 0 {
			t.Errorf("chat settings leaked into rerank probe: %v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"index":0,"score":1},{"index":1,"score":0}],"usage":{"prompt_tokens":7,"total_tokens":7},"echo":"fixture-api-key fixture-channel-secret fixture-endpoint-secret"}`))
	}))
	defer server.Close()
	ch := m.Channel{ID: 871, Name: "rerank-fixture", Enabled: true, Type: outbound.OutboundTypeOpenAIChat,
		Keys:             []m.ChannelKey{{ID: 872, Enabled: true, ChannelKey: "fixture-api-key"}},
		CustomHeader:     []m.CustomHeader{{HeaderKey: "X-Channel", HeaderValue: "fixture-channel-secret"}},
		ConnectionConfig: &m.ConnectionConfig{Version: 1, Selection: "same_protocol", Catalog: m.ModelCatalog{Format: "manual"}, Endpoints: []m.ChannelEndpoint{{ID: "rank", Protocol: "rerank", URL: server.URL, URLMode: "base", Auth: "bearer", Headers: []m.CustomHeader{{HeaderKey: "X-Endpoint", HeaderValue: "fixture-endpoint-secret"}}}}}}
	result := testGroupModelItemWithGroup(context.Background(), &m.Group{EndpointType: m.EndpointTypeRerank, DefaultReasoningEffort: "high", RelayRetryCount: 3, CustomHeader: []m.CustomHeader{{HeaderKey: "X-Group", HeaderValue: "fixture-group-secret"}}}, m.GroupItem{ID: 873, ChannelID: ch.ID, ModelName: "rerank-model"}, map[int]m.Channel{ch.ID: ch})
	if !result.Passed || result.Protocol != "rerank" || result.EndpointID != "rank" || result.Attempts != 1 || calls.Load() != 1 || result.ChannelKeyID != 872 {
		t.Fatalf("invalid group probe result: %#v calls=%d", result, calls.Load())
	}
	logs, _ := relaylog.GetCacheAndLock()
	if len(logs) != 1 || !logs[0].IsTest || logs[0].InputTokens != 7 || logs[0].UsageState != m.RelayLogUsageReported {
		t.Fatalf("group rerank probe log/usage: %#v", logs)
	}
	for _, secret := range []string{"fixture-api-key", "fixture-channel-secret", "fixture-endpoint-secret"} {
		if strings.Contains(result.ResponseText+result.Message+logs[0].ResponseContent+logs[0].Error, secret) {
			t.Fatalf("probe output leaked a header secret")
		}
	}
}

func TestRerankProbeUsesDeclaredEndpointAndSingleRequest(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	defer xurl.SetSSRFAllowPrivateForTest(false)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/custom/rank" || r.Method != http.MethodPost || r.Header.Get("api-key") != "fixture-key" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Endpoint") != "yes" {
			t.Errorf("wrong endpoint/auth: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if string(body["model"]) != `"rerank-model"` || len(body["query"]) == 0 || len(body["documents"]) == 0 || len(body["messages"]) != 0 || len(body["reasoning_effort"]) != 0 {
			t.Errorf("wrong rerank payload: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"index":0,"relevance_score":0.9},{"index":1,"relevance_score":0.2}]}`))
	}))
	defer server.Close()
	channel := m.Channel{Type: outbound.OutboundTypeOpenAIChat, Enabled: true, Model: "rerank-model", Keys: []m.ChannelKey{{ID: 42, Enabled: true, ChannelKey: "fixture-key"}}, CustomHeader: []m.CustomHeader{{HeaderKey: "Authorization", HeaderValue: "Bearer stale"}}, ConnectionConfig: &m.ConnectionConfig{Version: 1, Selection: "same_protocol", Catalog: m.ModelCatalog{Format: "manual"}, Endpoints: []m.ChannelEndpoint{{ID: "rank", Protocol: "rerank", URL: server.URL + "/custom/rank", URLMode: "full", Auth: "api_key", Headers: []m.CustomHeader{{HeaderKey: "X-Endpoint", HeaderValue: "yes"}}}}}}
	results := runConfiguredConnectionProbe(context.Background(), &channel, &m.ChannelProbeRun{}, "fixture-key", probeKeyScope{Index: 0, KeyID: 42}, "rerank-model")
	if calls.Load() != 1 || len(results) != 1 || results[0].Verdict != m.ProbeVerdictPass || results[0].Protocol != "rerank" || results[0].EndpointID != "rank" || !strings.Contains(results[0].KeyScope, "key_id:42") || results[0].RequestFingerprint == "" {
		t.Fatalf("calls=%d results=%#v", calls.Load(), results)
	}
	summary, err := testConfiguredChannel(context.Background(), channel)
	if err != nil || !summary.Passed || calls.Load() != 2 || summary.Results[0].Protocol != "rerank" {
		t.Fatalf("configured test: %#v %v calls=%d", summary, err, calls.Load())
	}
	// Selecting another protocol cannot secretly fall back to the rerank endpoint.
	channel.SelectedEndpointID = "missing"
	out := sendRerankProbe(context.Background(), &channel, "fixture-key", probeKeyScope{}, "rerank-model")
	if out.Verdict == m.ProbeVerdictPass || calls.Load() != 2 {
		t.Fatal("cross-endpoint fallback")
	}
}

func TestLegacyRerankProbeDoesNotUseChatEndpoint(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	defer xurl.SetSSRFAllowPrivateForTest(false)
	for _, basePath := range []string{"", "/v1", "/proxy", "/proxy/v1"} {
		t.Run(basePath, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				prefix := strings.TrimSuffix(basePath, "/v1")
				if r.URL.Path != prefix+"/v1/rerank" {
					t.Errorf("path=%s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"results":[{"index":0,"score":1},{"index":1,"score":0}]}`))
			}))
			defer server.Close()
			ch := m.Channel{BaseUrls: []m.BaseUrl{{URL: server.URL + basePath}}, Type: outbound.OutboundTypeOpenAIChat}
			out := sendRerankProbe(context.Background(), &ch, "fixture", probeKeyScope{}, "model")
			if out.Verdict != m.ProbeVerdictPass || calls.Load() != 1 {
				t.Fatalf("outcome=%#v calls=%d", out, calls.Load())
			}
		})
	}
}
