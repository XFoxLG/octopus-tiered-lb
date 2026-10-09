package relay

import (
	"context"
	"fmt"
	"github.com/lingyuins/octopus/internal/client"
	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/setting"
	"net/http"
	"strings"
	"testing"
)

// A failed protocol must not put the shared key into cooldown when the next
// interface succeeds. A second client request proves the key remains usable.
func TestHandlerConnectionFallbackDoesNotPoisonSharedKey(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	paths := []string{}
	id := prepareIndependentRequestFixture(t, nil)
	fixtureClient, err := client.GetHTTPClientSystemProxy(false)
	if err != nil {
		t.Fatal(err)
	}
	fixtureClient.Transport = independentRequestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "192.0.2.1" || r.Header.Get("Authorization") != "Bearer fixture-only-key" {
			return nil, fmt.Errorf("unexpected fixture target or credentials")
		}
		paths = append(paths, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			return newIndependentFixtureResponse(r, 500, `{"error":{"message":"temporary failure"}}`), nil
		}
		return newIndependentFixtureResponse(r, 200, `{"id":"response-fixture","object":"response","status":"completed","model":"creative-fixture","output":[{"id":"message-fixture","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"fallback answer","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`), nil
	})
	ch, err := channel.Get(id, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ch.ConnectionConfig = &appmodel.ConnectionConfig{Version: 1, Selection: "same_protocol", Endpoints: []appmodel.ChannelEndpoint{{ID: "chat", Protocol: "chat", URL: "http://192.0.2.1/v1", URLMode: "base", Auth: "default"}, {ID: "response", Protocol: "responses", URL: "http://192.0.2.1/v1", URLMode: "base", Auth: "default"}}, Catalog: appmodel.ModelCatalog{Format: "manual"}}
	channel.GetCache().Set(id, *ch)
	if err := setting.SetString(appmodel.SettingKeyRelayMaxTotalAttempts, "4"); err != nil {
		t.Fatal(err)
	}
	for request := 0; request < 2; request++ {
		recorder := serveIndependentFixtureRequest(`{"model":"creative-fixture","messages":[{"role":"user","content":"test"}]}`)
		if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "fallback answer") {
			t.Fatalf("request %d failed status=%d body=%s", request, recorder.Code, recorder.Body.String())
		}
	}
	if strings.Join(paths, ",") != "/v1/chat/completions,/v1/responses,/v1/chat/completions,/v1/responses" {
		t.Fatalf("unexpected attempts %v", paths)
	}
}
