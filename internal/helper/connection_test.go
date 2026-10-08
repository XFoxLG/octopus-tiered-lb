package helper

import (
	"context"
	"fmt"
	m "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func configuredTestChannel(base string) m.Channel {
	return m.Channel{Model: "model-a", Keys: []m.ChannelKey{{Enabled: true, ChannelKey: "test-secret"}}, ConnectionConfig: &m.ConnectionConfig{Version: 1, Selection: "same_protocol", Endpoints: []m.ChannelEndpoint{{ID: "chat-one", Protocol: "chat", URL: base, URLMode: "base", Auth: "default"}}, Catalog: m.ModelCatalog{Format: "openai", EndpointID: "chat-one"}}}
}
func TestConnectionCatalogUsesSelectedInterfaceAndKey(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/custom/models" || r.Header.Get("Authorization") != "Bearer test-secret" || r.Header.Get("api-key") != "" {
			t.Errorf("wrong catalog request %s %+v", r.URL, r.Header)
		}
		fmt.Fprint(w, `{"data":[{"id":"model-a"}]}`)
	}))
	defer server.Close()
	ch := configuredTestChannel(server.URL + "/custom")
	ch.BaseUrls = []m.BaseUrl{{URL: "https://wrong.test"}}
	ch.ConnectionConfig.Endpoints = append([]m.ChannelEndpoint{{ID: "unrelated", Protocol: "messages", URL: "https://wrong.test", URLMode: "base", Auth: "default"}}, ch.ConnectionConfig.Endpoints...)
	names, err := fetchConnectionModels(server.Client(), context.Background(), ch)
	if err != nil || len(names) != 1 || names[0] != "model-a" {
		t.Fatalf("catalog=%v err=%v", names, err)
	}
	ch.ConnectionConfig.Catalog.Format = "manual"
	if _, err := fetchConnectionModels(server.Client(), context.Background(), ch); err == nil {
		t.Fatal("manual treated as empty catalog")
	}
	if calls != 1 {
		t.Fatal("manual called upstream")
	}
}
func TestConnectionCatalogRejectsMalformedAndRepeatingPages(t *testing.T) {
	for _, body := range []string{`{"error":{"message":"unavailable"}}`, `{"data":null}`, `{"data":[{"id":"m"}],"has_more":true,"last_id":"m"}`} {
		t.Run(body, func(t *testing.T) {
			count := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count++; fmt.Fprint(w, body) }))
			defer srv.Close()
			ch := configuredTestChannel(srv.URL)
			if _, err := fetchConnectionModels(srv.Client(), context.Background(), ch); err == nil {
				t.Fatal("invalid catalog accepted")
			}
			if count > 2 {
				t.Fatal("unbounded pagination")
			}
		})
	}
}
func TestConnectionProbeMatchesGenerationAndRedactsHeaders(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	defer xurl.SetSSRFAllowPrivateForTest(false)
	paths := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer test-secret" || r.Header.Get("X-Private") != "private-value" {
			t.Errorf("wrong headers %+v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"OK private-value"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	ch := configuredTestChannel(srv.URL + "/service")
	ch.ConnectionConfig.Endpoints[0].Headers = []m.CustomHeader{{HeaderKey: "X-Private", HeaderValue: "private-value"}}
	ch.ConnectionConfig.Endpoints[0].ForwardMode = "passthrough"
	duplicate := ch.ConnectionConfig.Endpoints[0]
	duplicate.ID = "chat-two"
	duplicate.URL = srv.URL + "/other"
	ch.ConnectionConfig.Endpoints = append(ch.ConnectionConfig.Endpoints, duplicate)
	summary, err := testConfiguredChannel(context.Background(), ch)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Results) != 2 || !summary.Passed {
		t.Fatalf("bad results %+v", summary)
	}
	if strings.Join(paths, ",") != "/service/chat/completions,/other/chat/completions" {
		t.Fatalf("wrong paths %v", paths)
	}
	for i, row := range summary.Results {
		if row.EndpointID != ch.ConnectionConfig.Endpoints[i].ID || strings.Contains(row.ResponseBody, "private-value") {
			t.Fatalf("bad evidence %+v", row)
		}
	}
	ch.SkipModelTest = true
	if _, err := testConfiguredChannel(context.Background(), ch); err == nil {
		t.Fatal("skip model test ignored")
	}
}
func TestConnectionEmbeddingProbeAndNoCrossEndpointCapabilityApply(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	defer xurl.SetSSRFAllowPrivateForTest(false)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"model":"m","usage":{"prompt_tokens":1,"total_tokens":1}}`)
	}))
	defer srv.Close()
	ch := configuredTestChannel(srv.URL)
	ch.ConnectionConfig.Endpoints[0].Protocol = "embeddings"
	r, _ := buildGroupProbeRequest(m.EndpointTypeEmbeddings, "m")
	out := sendProbeRequest(context.Background(), &ch, "test-secret", outbound.OutboundTypeOpenAIEmbedding, r)
	if out.Verdict != m.ProbeVerdictPass {
		t.Fatalf("embedding rejected %+v", out)
	}
	run := &m.ChannelProbeRun{Results: []m.ChannelProbeResult{{EndpointID: "chat-one", Kind: m.ProbeKindCapability, Item: m.ProbeItemToolCalling, Verdict: m.ProbeVerdictPass}}}
	if len(CapabilityVerdicts(run)) != 0 {
		t.Fatal("endpoint capability applied channel-wide")
	}
	if _, err := ApplyChannelProbeRun(context.Background(), &ch, run); err == nil {
		t.Fatal("old protocol apply accepted")
	}
}
