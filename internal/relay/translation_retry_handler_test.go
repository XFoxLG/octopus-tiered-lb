package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dbmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/relay/balancer"
)

func prepareTranslationRetryFixture(t *testing.T, sameChannel bool, respond independentRequestTransport) (int, int) {
	t.Helper()
	firstID := prepareIndependentRequestFixture(t, respond)
	first, err := channel.Get(firstID, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first.CircuitBreakerThreshold = 1
	channel.GetCache().Set(firstID, *first)
	secondID := firstID
	if !sameChannel {
		secondID = firstID + 10000
		second := *first
		second.ID, second.Name = secondID, "compatible-channel"
		second.Keys = []dbmodel.ChannelKey{{ID: secondID, ChannelID: secondID, Enabled: true, ChannelKey: "fixture-only-key"}}
		channel.GetCache().Set(secondID, second)
		channel.GetKeyCache().Set(secondID, second.Keys[0])
		t.Cleanup(func() { channel.GetCache().Del(secondID); channel.GetKeyCache().Del(secondID) })
	}
	group.GetCache().Set(firstID, dbmodel.Group{
		ID: firstID, Name: "creative-fixture", EndpointType: dbmodel.EndpointTypeChat,
		Mode: dbmodel.GroupModeFailover, OutboundFormat: "chat_only", RelayRetryCount: -1, RelayRouteRetries: -1,
		Items: []dbmodel.GroupItem{
			{ChannelID: firstID, ModelName: "incompatible-model", Priority: 1, Weight: 1},
			{ChannelID: secondID, ModelName: "compatible-model", Priority: 2, Weight: 1},
		},
	})
	group.RebuildIndexes()
	if err := setting.SetString(dbmodel.SettingKeyRelayMaxTotalAttempts, "2"); err != nil {
		t.Fatal(err)
	}
	return firstID, secondID
}

func translationFixtureResponse(r *http.Request, stream bool) *http.Response {
	if stream {
		response := newIndependentFixtureResponse(r, 200, "data: {\"id\":\"translation\",\"object\":\"chat.completion.chunk\",\"model\":\"compatible-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"translated answer\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"translation\",\"object\":\"chat.completion.chunk\",\"model\":\"compatible-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		response.Header.Set("Content-Type", "text/event-stream")
		return response
	}
	return newIndependentFixtureResponse(r, 200, `{"id":"translation","object":"chat.completion","model":"compatible-model","choices":[{"index":0,"message":{"role":"assistant","content":"translated answer"},"finish_reason":"stop"}]}`)
}

func TestHandlerTranslationCompatibilityFallback(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	for _, sameChannel := range []bool{true, false} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("same_channel=%t/stream=%t", sameChannel, stream), func(t *testing.T) {
				var calls atomic.Int32
				firstID, _ := prepareTranslationRetryFixture(t, sameChannel, func(r *http.Request) (*http.Response, error) {
					var request struct {
						Model  string `json:"model"`
						Effort string `json:"reasoning_effort"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						return nil, err
					}
					if request.Effort != "minimal" {
						return nil, fmt.Errorf("client reasoning option was silently rewritten: %q", request.Effort)
					}
					calls.Add(1)
					if request.Model == "incompatible-model" {
						return newIndependentFixtureResponse(r, 400, strings.TrimPrefix(translationCompatibilityErrors[0], "400: ")), nil
					}
					if request.Model != "compatible-model" {
						return nil, fmt.Errorf("unexpected model %s", request.Model)
					}
					return translationFixtureResponse(r, stream), nil
				})
				recorder := serveIndependentFixtureRequest(fmt.Sprintf(`{"model":"creative-fixture","messages":[{"role":"user","content":"translate this"}],"reasoning_effort":"minimal","stream":%t}`, stream))
				if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "translated answer") || strings.Contains(recorder.Body.String(), "literal_error") {
					t.Fatalf("fallback failed: status=%d body=%s", recorder.Code, recorder.Body.String())
				}
				if calls.Load() != 2 {
					t.Fatalf("upstream attempts=%d, want 2", calls.Load())
				}
				if balancer.IsKeyOnCooldown(firstID, firstID, "incompatible-model") {
					t.Fatal("request-specific incompatibility quarantined key")
				}
			})
		}
	}
}

func TestHandlerElevenConcurrentTranslationRetries(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	const concurrency = 11
	entered := make(chan struct{}, concurrency)
	release := make(chan struct{})
	var attempts atomic.Int32
	prepareTranslationRetryFixture(t, false, func(r *http.Request) (*http.Response, error) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			return nil, err
		}
		attempts.Add(1)
		if request.Model == "incompatible-model" {
			entered <- struct{}{}
			<-release
			return newIndependentFixtureResponse(r, 400, strings.TrimPrefix(translationCompatibilityErrors[1], "400: ")), nil
		}
		return translationFixtureResponse(r, false), nil
	})
	finished := make(chan *httptest.ResponseRecorder, concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			finished <- serveIndependentFixtureRequest(`{"model":"creative-fixture","messages":[{"role":"user","content":"translate this"}],"reasoning_effort":"minimal"}`)
		}()
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for i := 0; i < concurrency; i++ {
		select {
		case <-entered:
		case <-deadline.C:
			close(release)
			for j := 0; j < concurrency; j++ {
				<-finished
			}
			t.Fatal("requests did not concurrently enter upstream")
		}
	}
	close(release)
	for i := 0; i < concurrency; i++ {
		recorder := <-finished
		if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "translated answer") {
			t.Errorf("request %d failed: status=%d body=%s", i, recorder.Code, recorder.Body.String())
		}
	}
	if attempts.Load() != concurrency*2 {
		t.Fatalf("attempts=%d, want %d", attempts.Load(), concurrency*2)
	}
}

func TestHandlerTranslationRetryRespectsBudgetAndTerminalErrors(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	for _, test := range []struct {
		name, body string
		budget     string
	}{
		{"budget", strings.TrimPrefix(translationCompatibilityErrors[0], "400: "), "1"},
		{"context", `{"error":{"message":"prompt is too long","code":"context_length_exceeded","type":"invalid_request_error"}}`, "2"},
		{"filter", `{"error":{"message":"content blocked","code":"content_filter","type":"invalid_request_error"}}`, "2"},
		{"unknown", `{"error":{"message":"3051","code":"3051","type":"upstream_error"}}`, "2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			prepareTranslationRetryFixture(t, false, func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				return newIndependentFixtureResponse(r, 400, test.body), nil
			})
			if err := setting.SetString(dbmodel.SettingKeyRelayMaxTotalAttempts, test.budget); err != nil {
				t.Fatal(err)
			}
			recorder := serveIndependentFixtureRequest(`{"model":"creative-fixture","messages":[{"role":"user","content":"translate"}],"reasoning_effort":"minimal"}`)
			if calls.Load() != 1 || recorder.Code < 400 {
				t.Fatalf("must stop once: calls=%d status=%d body=%s", calls.Load(), recorder.Code, recorder.Body.String())
			}
			if test.name != "budget" && (recorder.Code != 400 || recorder.Body.String() != test.body) {
				t.Fatalf("terminal error changed: %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
