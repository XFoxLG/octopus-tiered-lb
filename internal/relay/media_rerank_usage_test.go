package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/db"
	m "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/op/stats"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

func TestRerankRequestPersistsOneLogWithReportedUsage(t *testing.T) {
	if err := db.InitDB("sqlite", t.TempDir()+"/rerank-log.db", false); err != nil {
		t.Fatal(err)
	}
	if err := db.InitLogDB("", "", false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	if err := setting.SetString(m.SettingKeyRelayLogKeepEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	// Usage must not depend on retaining private request/response content.
	if err := setting.SetString(m.SettingKeyRelayLogContentEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { relaylog.SetCacheForTest(nil) })
	gin.SetMode(gin.TestMode)
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	for _, tc := range []struct {
		name, body, state string
		status, input     int
		noEndpoint        bool
	}{
		{"reported", `{"results":[{"index":0,"relevance_score":0.9}],"usage":{"prompt_tokens":245,"total_tokens":245}}`, m.RelayLogUsageReported, 200, 245, false},
		{"missing", `{"results":[{"index":0,"relevance_score":0.9}]}`, m.RelayLogUsageNotReported, 200, 0, false},
		{"invalid_usage", `{"results":[{"index":0,"relevance_score":0.9}],"usage":{"prompt_tokens":-1}}`, m.RelayLogUsageNotReported, 200, 0, false},
		{"upstream_error", `{"error":{"message":"invalid request"}}`, m.RelayLogUsageFailedNoResponse, 400, 0, false},
		{"no_rerank_endpoint", ``, m.RelayLogUsageFailedNoResponse, 502, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relaylog.SetCacheForTest(nil)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/v1/rerank" {
					t.Errorf("wrong endpoint: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			name := "rerank-log-" + tc.name
			ch := m.Channel{Name: name, Type: outbound.OutboundTypeOpenAIChat, Enabled: true, Model: name, BaseUrls: []m.BaseUrl{{URL: server.URL}}, Keys: []m.ChannelKey{{Enabled: true, ChannelKey: "fixture"}}}
			if tc.noEndpoint {
				ch.ConnectionConfig = &m.ConnectionConfig{Version: 1, Selection: "same_protocol", Catalog: m.ModelCatalog{Format: "manual"}, Endpoints: []m.ChannelEndpoint{{ID: "chat", Protocol: "chat", URL: server.URL, URLMode: "base", Auth: "bearer"}}}
			}
			if err := channel.Create(&ch, context.Background()); err != nil {
				t.Fatal(err)
			}
			g := m.Group{Name: name, EndpointType: m.EndpointTypeRerank, Mode: m.GroupModeFailover, Items: []m.GroupItem{{ChannelID: ch.ID, ModelName: name}}}
			if err := group.GroupCreate(&g, context.Background()); err != nil {
				t.Fatal(err)
			}
			engine := gin.New()
			engine.Use(RelayRequestTraceMiddleware())
			engine.POST("/v1/rerank", func(c *gin.Context) { MediaHandler(MediaEndpointRerank, c) })
			req := httptest.NewRequest(http.MethodPost, "/v1/rerank", strings.NewReader(`{"model":"`+name+`","query":"safe","documents":["safe"]}`))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			before := stats.TotalGet()
			engine.ServeHTTP(rr, req)
			logs, _ := relaylog.GetCacheAndLock()
			wantCalls := int32(1)
			if tc.noEndpoint {
				wantCalls = 0
			}
			if len(logs) != 1 || calls.Load() != wantCalls {
				t.Fatalf("logs=%d upstream calls=%d, want one log/%d calls", len(logs), calls.Load(), wantCalls)
			}
			got := logs[0]
			if got.TraceID == "" || (!tc.noEndpoint && (got.ChannelId != ch.ID || len(got.Attempts) != 1)) || got.HTTPStatus != tc.status || rr.Code != tc.status {
				t.Fatalf("lost trace/attempt metadata: %#v", got)
			}
			if tc.noEndpoint {
				after := stats.TotalGet()
				if got.Error == "" || after.RequestSuccess != before.RequestSuccess || after.RequestFailed != before.RequestFailed+1 {
					t.Fatalf("unroutable request recorded as success: error=%q before=%#v after=%#v", got.Error, before, after)
				}
			}
			if got.UsageState != tc.state || got.InputTokens != tc.input || got.OutputTokens != 0 {
				t.Fatalf("usage=%s %d/%d", got.UsageState, got.InputTokens, got.OutputTokens)
			}
			if tc.status == 200 && (rr.Body.String() != tc.body || got.ClientWriteBytes != int64(len(tc.body)) || !got.ClientWriteComplete) {
				t.Fatalf("response or egress accounting changed: %#v", got)
			}
		})
	}
}

func TestRerankUsageDoesNotInventTokens(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"usage":null}`, `{"usage":{"total_tokens":245}}`,
		`{"usage":{"prompt_tokens":1.5}}`, `{"usage":{"prompt_tokens":2,"input_tokens":3}}`,
		`{"usage":{"prompt_tokens":2,"total_tokens":1}}`, `{"error":{},"usage":{"prompt_tokens":2}}`,
		`{"usage":{"prompt_tokens":2}} trailing`,
	} {
		if got := parseMediaRerankUsage([]byte(body)); got.reported {
			t.Errorf("invented valid usage for %s", body)
		}
	}
	for _, body := range []string{`{"usage":{"input_tokens":0,"output_tokens":0}}`, `{"usage":{"prompt_tokens":0,"total_tokens":0}}`} {
		if got := parseMediaRerankUsage([]byte(body)); !got.reported || got.input != 0 || got.output != 0 {
			t.Errorf("lost explicit zero: %s", body)
		}
	}
	capture := &mediaRerankUsageCapture{}
	payload := []byte(strings.Repeat("x", mediaRerankUsageMaxBytes+1))
	if n, err := capture.Write(payload); err != nil || n != len(payload) || !capture.oversized || capture.Len() != 0 {
		t.Fatal("usage capture truncated forwarding or kept oversized content")
	}
}
