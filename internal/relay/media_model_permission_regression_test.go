package relay

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/db"
	dbmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReviewMediaHonorsAPIKeyModelAllowlist(t *testing.T) {
	if err := db.InitDB("sqlite", t.TempDir()+"/review-media.db", false); err != nil {
		t.Fatal(err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	xurl.SetSSRFAllowPrivateForTest(true)
	defer xurl.SetSSRFAllowPrivateForTest(false)
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"created\":1,\"data\":[{\"url\":\"https://example.invalid/image.png\"}]}"))
	}))
	defer srv.Close()
	ch := dbmodel.Channel{Name: "review-media-key", Type: outbound.OutboundTypeOpenAIChat, Enabled: true, Model: "forbidden-model", BaseUrls: []dbmodel.BaseUrl{{URL: srv.URL}}, Keys: []dbmodel.ChannelKey{{Enabled: true, ChannelKey: "sk-local-fixture"}}}
	if err := channel.Create(&ch, context.Background()); err != nil {
		t.Fatal(err)
	}
	g := dbmodel.Group{Name: "forbidden-model", EndpointType: dbmodel.EndpointTypeImageGeneration, Mode: dbmodel.GroupModeFailover, Items: []dbmodel.GroupItem{{ChannelID: ch.ID, ModelName: "forbidden-model"}}}
	if err := group.GroupCreate(&g, context.Background()); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader("{\"model\":\"forbidden-model\",\"prompt\":\"cat\"}"))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("api_key_id", 0)
	c.Set("supported_models", "allowed-model")
	MediaHandler(MediaEndpointImageGeneration, c)
	t.Logf("HTTP=%d upstream_hits=%d body=%s", rec.Code, hits.Load(), rec.Body.String())
	if hits.Load() != 0 || rec.Code == http.StatusOK {
		t.Error("model-limited API key was allowed to generate a forbidden media model")
	}
}
