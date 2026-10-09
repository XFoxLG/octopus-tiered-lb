package helper

import (
	"context"
	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewModelListProbeRedactsEchoedKey(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	defer xurl.SetSSRFAllowPrivateForTest(false)
	secret := "sk-review-echo-not-a-real-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte("invalid api key: " + secret))
	}))
	defer srv.Close()
	ch := &appmodel.Channel{ID: 1, Type: outbound.OutboundTypeOpenAIChat, BaseUrls: []appmodel.BaseUrl{{URL: srv.URL}}}
	got := probeChannelModelList(context.Background(), ch, secret, "test-model")
	t.Logf("returned summary=%s", got.Summary)
	if strings.Contains(got.Summary, secret) {
		t.Error("model-list probe returns the upstream key without redaction")
	}
}
