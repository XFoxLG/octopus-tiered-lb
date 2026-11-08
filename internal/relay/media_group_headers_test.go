package relay

import (
	"github.com/gin-gonic/gin"
	m "github.com/lingyuins/octopus/internal/model"
	"net/http/httptest"
	"testing"
)

func TestMediaGroupHeadersUseChannelThenEndpointPriority(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/rerank", nil)
	c.Request.Header.Set("X-Shared", "client")
	group := m.Group{CustomHeader: []m.CustomHeader{{HeaderKey: "X-Group", HeaderValue: "group"}, {HeaderKey: "X-Shared", HeaderValue: "group"}}}
	channel := m.Channel{CustomHeader: []m.CustomHeader{{HeaderKey: "X-Shared", HeaderValue: "channel"}}}
	r := httptest.NewRequest("POST", "http://upstream.test/v1/rerank", nil)
	copyMediaForwardHeaders(r, c, group, &channel, "fixture-key", "application/json", false)
	if r.Header.Get("X-Group") != "group" || r.Header.Get("X-Shared") != "channel" {
		t.Fatalf("headers=%v", r.Header)
	}
	channel.ApplyConnectionHeaders(r, m.ConnectionPlan{Endpoint: &m.ChannelEndpoint{Protocol: "rerank", Auth: "bearer", Headers: []m.CustomHeader{{HeaderKey: "X-Shared", HeaderValue: "endpoint"}}}}, "fixture-key")
	if r.Header.Get("X-Shared") != "endpoint" {
		t.Fatal("endpoint header lost")
	}
}
