package relay

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/relay/balancer"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

func TestMediaRecoveryRequiresCompleteCandidateScan(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	if err := db.InitDB("sqlite", t.TempDir()+"/media-recovery.db", false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name                                        string
		budget, secondStatus, wantStatus, wantCalls int
	}{
		{"partial", 1, 200, 502, 1}, {"all_cooling", 2, 429, 503, 2}, {"available", 2, 200, 200, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := setting.SetString(model.SettingKeyRelayMaxTotalAttempts, fmt.Sprint(tc.budget)); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			name := "rerank-recovery-" + tc.name
			g := model.Group{Name: name, EndpointType: model.EndpointTypeRerank, Mode: model.GroupModeFailover}
			for i := 0; i < 2; i++ {
				status := 429
				if i == 1 {
					status = tc.secondStatus
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					if status == 429 {
						w.Header().Set("Retry-After", "900")
						w.WriteHeader(status)
						_, _ = w.Write([]byte(`{"error":{"code":"model_cooldown","message":"synthetic cooldown"}}`))
					} else {
						_, _ = w.Write([]byte(`{"results":[{"index":0,"score":0.9}]}`))
					}
				}))
				t.Cleanup(server.Close)
				ch := model.Channel{Name: fmt.Sprintf("%s-%d", name, i), Type: outbound.OutboundTypeOpenAIChat, Enabled: true, Model: name, BaseUrls: []model.BaseUrl{{URL: server.URL}}, Keys: []model.ChannelKey{{Enabled: true, ChannelKey: "synthetic"}}}
				if err := channel.Create(&ch, context.Background()); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { balancer.RemoveChannelKeyCooldowns(ch.ID) })
				g.Items = append(g.Items, model.GroupItem{ChannelID: ch.ID, ModelName: name, Priority: i})
			}
			if err := group.GroupCreate(&g, context.Background()); err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("POST", "/v1/rerank", strings.NewReader(`{"model":"`+name+`","query":"safe","documents":["safe"]}`))
			c.Request.Header.Set("Content-Type", "application/json")
			MediaHandler(MediaEndpointRerank, c)
			if recorder.Code != tc.wantStatus || int(calls.Load()) != tc.wantCalls {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, calls.Load(), recorder.Body.String())
			}
			if (recorder.Header().Get("Retry-After") != "") != (tc.wantStatus == 503) {
				t.Fatalf("incorrect recovery ETA: %v", recorder.Header())
			}
		})
	}
}
