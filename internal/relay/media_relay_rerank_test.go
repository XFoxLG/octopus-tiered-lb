package relay

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/db"
	dbmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

func TestMediaHandlerRerankConnectionBoundaries(t *testing.T) {
	if err := db.InitDB("sqlite", t.TempDir()+"/rerank-boundaries.db", false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	for _, tc := range []struct {
		name, protocol string
		wantForward    bool
	}{
		{"legacy", "", true},
		{"declared", "rerank", true},
		{"chat_only", "chat", false},
		{"embedding_only", "embeddings", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pathsMu sync.Mutex
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				pathsMu.Lock()
				paths = append(paths, r.URL.Path)
				pathsMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"results":[{"index":0,"score":0.9}]}`))
			}))
			defer server.Close()
			name := "rerank-boundary-" + tc.name
			ch := dbmodel.Channel{Name: name, Type: outbound.OutboundTypeOpenAIChat, Enabled: true, Model: name, BaseUrls: []dbmodel.BaseUrl{{URL: server.URL}}, Keys: []dbmodel.ChannelKey{{Enabled: true, ChannelKey: "safe-fixture"}}}
			if tc.protocol != "" {
				ch.ConnectionConfig = &dbmodel.ConnectionConfig{Version: 1, Selection: "same_protocol", Catalog: dbmodel.ModelCatalog{Format: "manual"}, Endpoints: []dbmodel.ChannelEndpoint{{ID: "explicit", Protocol: tc.protocol, URL: server.URL, URLMode: "base", Auth: "default"}}}
			}
			if err := channel.Create(&ch, context.Background()); err != nil {
				t.Fatal(err)
			}
			g := dbmodel.Group{Name: name, EndpointType: dbmodel.EndpointTypeRerank, Mode: dbmodel.GroupModeFailover, Items: []dbmodel.GroupItem{{ChannelID: ch.ID, ModelName: name}}}
			if err := group.GroupCreate(&g, context.Background()); err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", strings.NewReader(`{"model":"`+name+`","query":"safe query","documents":["safe document"]}`))
			c.Request.Header.Set("Content-Type", "application/json")
			MediaHandler(MediaEndpointRerank, c)
			pathsMu.Lock()
			defer pathsMu.Unlock()
			if tc.wantForward {
				if recorder.Code != http.StatusOK || len(paths) != 1 || paths[0] != "/v1/rerank" {
					t.Fatalf("status=%d upstream paths=%v body=%s", recorder.Code, paths, recorder.Body.String())
				}
			} else if recorder.Code == http.StatusOK || len(paths) != 0 {
				t.Fatalf("undeclared rerank was forwarded: status=%d paths=%v", recorder.Code, paths)
			}
		})
	}
}

func TestForwardMediaRequestJSONRerankConnectionEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })

	var (
		mu          sync.Mutex
		gotPath     string
		gotBody     string
		gotAuth     string
		gotAPIKey   string
		gotChannel  string
		gotEndpoint string
		gotClient   string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		mu.Lock()
		gotPath = r.URL.Path
		gotBody = string(body)
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("api-key")
		gotChannel = r.Header.Get("X-Channel")
		gotEndpoint = r.Header.Get("X-Endpoint")
		gotClient = r.Header.Get("X-Client")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"index":0,"score":0.9}]}`))
	}))
	defer upstream.Close()

	originalBody := `{"model":"proxy-model","query":"search text","documents":["one","two"],"top_k":5}`
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", strings.NewReader(originalBody))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("X-Client", "client-header")

	channel := &dbmodel.Channel{
		CustomHeader: []dbmodel.CustomHeader{
			{HeaderKey: "Authorization", HeaderValue: "Bearer inherited-secret"},
			{HeaderKey: "X-Channel", HeaderValue: "channel-header"},
		},
	}
	endpoint := &dbmodel.ChannelEndpoint{
		ID:       "declared-rerank",
		Protocol: "rerank",
		URL:      upstream.URL,
		URLMode:  "base",
		Auth:     "api_key",
		Headers:  []dbmodel.CustomHeader{{HeaderKey: "X-Endpoint", HeaderValue: "endpoint-header"}},
	}
	status, err := forwardMediaRequestJSON(
		ctx,
		getMediaEndpointConfig(MediaEndpointRerank),
		dbmodel.Group{EndpointProvider: "raw"},
		channel,
		"selected-secret",
		[]byte(originalBody),
		"proxy-model",
		"upstream-model",
		false,
		context.Background(),
		nil,
		0,
		endpoint,
	)
	if err != nil {
		t.Fatalf("forwardMediaRequestJSON() error = %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/v1/rerank" {
		t.Fatalf("upstream path = %q, want /v1/rerank", gotPath)
	}
	if gotAuth != "" {
		t.Fatalf("inherited Authorization leaked: %q", gotAuth)
	}
	if gotAPIKey != "selected-secret" {
		t.Fatalf("api-key = %q, want selected secret", gotAPIKey)
	}
	if gotChannel != "channel-header" || gotEndpoint != "endpoint-header" || gotClient != "client-header" {
		t.Fatalf("headers mismatch: channel=%q endpoint=%q client=%q", gotChannel, gotEndpoint, gotClient)
	}
	if !strings.Contains(gotBody, `"model":"upstream-model"`) || !strings.Contains(gotBody, `"documents":["one","two"]`) || !strings.Contains(gotBody, `"top_k":5`) {
		t.Fatalf("rerank JSON changed beyond model remap: %s", gotBody)
	}
	if !strings.Contains(recorder.Body.String(), `"score":0.9`) {
		t.Fatalf("client response = %q", recorder.Body.String())
	}
}
