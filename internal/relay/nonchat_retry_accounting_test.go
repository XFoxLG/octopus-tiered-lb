package relay

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/db"
	m "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/op/stats"
	"github.com/lingyuins/octopus/internal/relay/balancer"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNonChatRetryCountsBusinessRequestOnce(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	if err := db.InitDB("sqlite", t.TempDir()+"/counts.db", false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	if err := setting.SetString(m.SettingKeyRelayLogKeepEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	if err := setting.SetString(m.SettingKeyRelayMaxTotalAttempts, "2"); err != nil {
		t.Fatal(err)
	}
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false); relaylog.SetCacheForTest(nil) })
	for _, embedding := range []bool{false, true} {
		endpoint, path, name := m.EndpointTypeRerank, "/v1/rerank", "rerank-retry-count"
		body := `{"query":"safe","documents":["safe","safe"]}`
		answer := `{"results":[{"index":0,"relevance_score":0.9}],"usage":{"total_tokens":1000}}`
		kind := outbound.OutboundTypeOpenAIChat
		if embedding {
			endpoint, path, name = m.EndpointTypeEmbeddings, "/v1/embeddings", "embedding-retry-count"
			body = `{"input":[[10,20],[30]]}`
			answer = `{"data":[{"index":0,"embedding":[0.1,0.2]},{"index":1,"embedding":[0.2,0.3]}],"usage":{"total_tokens":1000}}`
			kind = outbound.OutboundTypeOpenAIEmbedding
		}
		t.Run(endpoint, func(t *testing.T) {
			relaylog.SetCacheForTest(nil)
			var calls atomic.Int32
			g := m.Group{Name: name, EndpointType: endpoint, Mode: m.GroupModeFailover}
			var chosen int
			for i := 0; i < 2; i++ {
				first := i == 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					if first {
						w.Header().Set("Retry-After", "900")
						w.WriteHeader(429)
						_, _ = w.Write([]byte(`{"error":{"code":"model_cooldown","message":"synthetic"}}`))
						return
					}
					_, _ = w.Write([]byte(answer))
				}))
				t.Cleanup(srv.Close)
				ch := m.Channel{Name: name + string(rune('a'+i)), Type: kind, Enabled: true, Model: name, BaseUrls: []m.BaseUrl{{URL: srv.URL}}, Keys: []m.ChannelKey{{Enabled: true, ChannelKey: "synthetic"}}}
				if err := channel.Create(&ch, context.Background()); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { balancer.RemoveChannelKeyCooldowns(ch.ID) })
				g.Items = append(g.Items, m.GroupItem{ChannelID: ch.ID, ModelName: name, Priority: i})
				chosen = ch.ID
			}
			if err := group.GroupCreate(&g, context.Background()); err != nil {
				t.Fatal(err)
			}
			engine := gin.New()
			engine.Use(RelayRequestTraceMiddleware())
			key := chosen + 20000
			engine.POST(path, func(c *gin.Context) {
				c.Set("api_key_id", key)
				if embedding {
					Handler(endpoint, inbound.InboundTypeOpenAIEmbedding, c)
				} else {
					MediaHandler(MediaEndpointRerank, c)
				}
			})
			req := httptest.NewRequest("POST", path, strings.NewReader(`{"model":"`+name+`",`+body[1:]))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			engine.ServeHTTP(rr, req)
			if rr.Code != 200 || rr.Body.String() != answer || calls.Load() != 2 {
				t.Fatalf("status=%d calls=%d body=%s", rr.Code, calls.Load(), rr.Body.String())
			}
			logs, _ := relaylog.GetCacheAndLock()
			if len(logs) != 1 {
				t.Fatalf("log count=%d", len(logs))
			}
			entry := logs[0]
			if entry.TotalTokens() != 1000 || entry.NonChatUsage == nil || entry.NonChatUsage.FirstByteMS == nil || entry.NonChatUsage.ResponseMS == nil {
				t.Fatalf("lost usage/timing: %+v", entry)
			}
			for label, got := range map[string]m.StatsMetrics{"key": stats.APIKeyGet(key).StatsMetrics, "channel": stats.ChannelGet(chosen).StatsMetrics} {
				if got.TotalTokens() != 1000 || got.RequestSuccess != 1 || got.EmbeddingInputItems+got.RerankDocuments != 2 || got.EmbeddingInputObserved+got.RerankDocumentsObserved != 1 {
					t.Errorf("%s double/lost accounting: %+v", label, got)
				}
			}
			var daily m.StatsDailyChannelModel
			if err := db.GetDB().Where("channel_id=?", chosen).First(&daily).Error; err != nil {
				t.Fatal(err)
			}
			if daily.TotalTokens() != 1000 || daily.EmbeddingInputItems+daily.RerankDocuments != 2 {
				t.Fatalf("daily counts=%+v", daily)
			}
		})
	}
}
