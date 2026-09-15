package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/client"
	"github.com/lingyuins/octopus/internal/db"
	dbmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/op/stats"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

type independentRequestTransport func(*http.Request) (*http.Response, error)

func (transport independentRequestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

var independentRequestFixtureID atomic.Int64

func runIndependentRequestTestInSubprocess(t *testing.T) bool {
	t.Helper()
	const subprocessEnvironmentKey = "OCTOPUS_TEST_INDEPENDENT_REQUEST_SUBPROCESS"
	if os.Getenv(subprocessEnvironmentKey) == t.Name() {
		return false
	}

	// InitDB and InitCache replace process-wide settings, generations, and
	// caches. Keep the entire fixture in a child process, not later tests.
	executablePath, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	command := exec.Command(executablePath, "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.count=1", "-test.timeout=60s")
	command.Env = append(os.Environ(), subprocessEnvironmentKey+"="+t.Name())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("isolated request fixture failed: %v\n%s", err, output)
	}
	return true
}

func prepareIndependentRequestFixture(t *testing.T, respond independentRequestTransport) int {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "independent-requests.db"), false); err != nil {
		t.Fatalf("initialize isolated database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.InitCache(); err != nil {
		t.Fatalf("initialize fixture caches: %v", err)
	}
	t.Cleanup(relaylog.SetCacheForTest(nil))
	for settingKey, settingValue := range map[dbmodel.SettingKey]string{
		dbmodel.SettingKeyRelayRetryCount:       "0",
		dbmodel.SettingKeyRelayRouteRetries:     "1",
		dbmodel.SettingKeyRelayMaxTotalAttempts: "1",
	} {
		if err := setting.SetString(settingKey, settingValue); err != nil {
			t.Fatalf("set fixture setting %s: %v", settingKey, err)
		}
	}

	fixtureID := 890000 + int(independentRequestFixtureID.Add(1))
	fixtureChannel := dbmodel.Channel{
		ID:                     fixtureID,
		Name:                   "independent-request-fixture",
		Type:                   outbound.OutboundTypeOpenAIChat,
		OutboundFormatOverride: "chat_only",
		Enabled:                true,
		BaseUrls:               []dbmodel.BaseUrl{{URL: "http://192.0.2.1"}},
		Keys: []dbmodel.ChannelKey{{
			ID: fixtureID, ChannelID: fixtureID, Enabled: true, ChannelKey: "fixture-only-key",
		}},
	}
	channel.GetCache().Set(fixtureID, fixtureChannel)
	channel.GetKeyCache().Set(fixtureID, fixtureChannel.Keys[0])
	group.GetCache().Set(fixtureID, dbmodel.Group{
		ID: fixtureID, Name: "creative-fixture", EndpointType: dbmodel.EndpointTypeChat,
		Mode: dbmodel.GroupModeFailover, OutboundFormat: "chat_only",
		Items: []dbmodel.GroupItem{{ChannelID: fixtureID, ModelName: "creative-fixture", Priority: 1, Weight: 1}},
	})
	group.RebuildIndexes()
	t.Cleanup(func() {
		channel.GetCache().Del(fixtureID)
		channel.GetKeyCache().Del(fixtureID)
		group.GetCache().Del(fixtureID)
		group.RebuildIndexes()
	})

	fixtureClient, err := client.GetHTTPClientSystemProxy(false)
	if err != nil {
		t.Fatalf("get fixture HTTP client: %v", err)
	}
	originalTransport := fixtureClient.Transport
	// The numeric documentation address needs no DNS. This transport never
	// opens a socket, and the production SSRF validation stays enabled.
	fixtureClient.Transport = independentRequestTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "192.0.2.1" || request.URL.Path != "/v1/chat/completions" {
			return nil, fmt.Errorf("unexpected fixture request URL: %s", request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer fixture-only-key" {
			return nil, fmt.Errorf("fixture upstream authorization was not forwarded")
		}
		return respond(request)
	})
	t.Cleanup(func() { fixtureClient.Transport = originalTransport })
	return fixtureID
}

func serveIndependentFixtureRequest(requestBody string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	requestContext, _ := gin.CreateTestContext(recorder)
	requestContext.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(requestBody))
	requestContext.Request.Header.Set("Content-Type", "application/json")
	requestContext.Set("api_key_id", 890000)
	Handler(dbmodel.EndpointTypeChat, inbound.InboundTypeOpenAIChat, requestContext)
	return recorder
}

func newIndependentFixtureResponse(request *http.Request, statusCode int, responseBody string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(responseBody)),
		Request:    request,
	}
}

func TestHandlerRetiredSemanticCacheAlwaysCallsUpstream(test *testing.T) {
	if runIndependentRequestTestInSubprocess(test) {
		return
	}
	var upstreamCalls atomic.Int64
	var embeddingCalls atomic.Int64
	var unexpectedCalls atomic.Int64
	prepareIndependentRequestFixture(test, func(request *http.Request) (*http.Response, error) {
		generation := upstreamCalls.Add(1)
		body := fmt.Sprintf(`{"id":"fresh-%d","object":"chat.completion","model":"creative-fixture","choices":[{"index":0,"message":{"role":"assistant","content":"fresh answer %d"},"finish_reason":"stop"}]}`, generation, generation)
		return newIndependentFixtureResponse(request, http.StatusOK, body), nil
	})
	originalDefaultTransport := http.DefaultTransport
	http.DefaultTransport = independentRequestTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "192.0.2.2" && request.URL.Path == "/v1/embeddings" {
			embeddingCalls.Add(1)
			return newIndependentFixtureResponse(request, http.StatusOK, `{"data":[{"embedding":[1,0,0]}]}`), nil
		}
		unexpectedCalls.Add(1)
		return nil, fmt.Errorf("unexpected default-transport request: %s", request.URL)
	})
	test.Cleanup(func() { http.DefaultTransport = originalDefaultTransport })

	// Seed old shared-database values directly, not through ordinary setting
	// writes. Both transports above are mocks and never open network sockets.
	legacySettings := []dbmodel.Setting{
		{Key: dbmodel.SettingKeySemanticCacheEnabled, Value: "true"},
		{Key: dbmodel.SettingKeySemanticCacheTTL, Value: "3600"},
		{Key: dbmodel.SettingKeySemanticCacheThreshold, Value: "98"},
		{Key: dbmodel.SettingKeySemanticCacheMaxEntries, Value: "1000"},
		{Key: dbmodel.SettingKeySemanticCacheEmbeddingBaseURL, Value: "http://192.0.2.2/v1"},
		{Key: dbmodel.SettingKeySemanticCacheEmbeddingAPIKey, Value: "fixture-only-embedding-key"},
		{Key: dbmodel.SettingKeySemanticCacheEmbeddingModel, Value: "fixture-embedding"},
		{Key: dbmodel.SettingKeySemanticCacheEmbeddingTimeoutSeconds, Value: "1"},
	}
	for _, legacySetting := range legacySettings {
		if err := db.GetDB().Save(&legacySetting).Error; err != nil {
			test.Fatalf("seed legacy setting %q: %v", legacySetting.Key, err)
		}
	}
	if err := setting.RefreshCache(context.Background()); err != nil {
		test.Fatalf("refresh with legacy enabled setting: %v", err)
	}
	// Even a compatibility caller restoring the old runtime cache cannot enable
	// answer reuse: there is no relay lookup, embedding, or store path left.
	for _, legacySetting := range legacySettings {
		setting.GetCache().Set(legacySetting.Key, legacySetting.Value)
	}

	requestBody := `{"model":"creative-fixture","messages":[{"role":"user","content":"What is two plus two?"}],"temperature":0}`
	for requestNumber := 1; requestNumber <= 3; requestNumber++ {
		recorder := serveIndependentFixtureRequest(requestBody)
		if recorder.Code != http.StatusOK {
			test.Fatalf("request %d status = %d: %s", requestNumber, recorder.Code, recorder.Body.String())
		}
		var payload struct {
			ID string `json:"id"`
		}
		if err := jsonAPI.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			test.Fatalf("decode request %d response: %v", requestNumber, err)
		}
		if expectedID := fmt.Sprintf("fresh-%d", requestNumber); payload.ID != expectedID {
			test.Errorf("request %d response ID = %q, want %q", requestNumber, payload.ID, expectedID)
		}
	}
	if upstreamCalls.Load() != 3 || embeddingCalls.Load() != 0 || unexpectedCalls.Load() != 0 {
		test.Errorf("upstream=%d embedding=%d unexpected=%d, want 3 independent upstream calls only", upstreamCalls.Load(), embeddingCalls.Load(), unexpectedCalls.Load())
	}
	var storedSetting dbmodel.Setting
	if err := db.GetDB().First(&storedSetting, "key = ?", dbmodel.SettingKeySemanticCacheEnabled).Error; err != nil || storedSetting.Value != "true" {
		test.Errorf("legacy enabled row was not preserved: value=%q error=%v", storedSetting.Value, err)
	}
	if recorded := stats.APIKeyGet(890000); recorded.RequestSuccess != 3 || recorded.RequestFailed != 0 {
		test.Errorf("request metrics = %+v, want exactly three successes", recorded)
	}
}

func TestHandlerConcurrentGenerationsRemainIndependent(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	for _, differentParameters := range []bool{false, true} {
		t.Run(fmt.Sprintf("different_parameters=%t", differentParameters), func(t *testing.T) {
			var upstreamCalls atomic.Int64
			entered := make(chan struct{}, 2)
			release := make(chan struct{})
			prepareIndependentRequestFixture(t, func(request *http.Request) (*http.Response, error) {
				generation := upstreamCalls.Add(1)
				entered <- struct{}{}
				<-release
				body := fmt.Sprintf(`{"id":"generation-%d","object":"chat.completion","model":"creative-fixture","choices":[{"index":0,"message":{"role":"assistant","content":"independent draft %d"},"finish_reason":"stop"}]}`, generation, generation)
				return newIndependentFixtureResponse(request, http.StatusOK, body), nil
			})
			firstBody := `{"model":"creative-fixture","messages":[{"role":"user","content":"Write the next scene"}],"temperature":0.7}`
			secondBody := firstBody
			if differentParameters {
				secondBody = `{"model":"creative-fixture","messages":[{"role":"user","content":"Write the next scene"}],"temperature":1.2,"seed":42,"max_tokens":256}`
			}
			finished := make(chan *httptest.ResponseRecorder, 2)
			go func() { finished <- serveIndependentFixtureRequest(firstBody) }()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				close(release)
				<-finished
				t.Fatal("first request did not reach fixture transport")
			}
			go func() { finished <- serveIndependentFixtureRequest(secondBody) }()
			select {
			case <-entered:
			case <-time.After(500 * time.Millisecond):
				t.Error("second generation was blocked behind the first request")
			}
			close(release)
			responseIDs := make(map[string]bool)
			for responseIndex := 0; responseIndex < 2; responseIndex++ {
				recorder := <-finished
				if recorder.Code != http.StatusOK {
					t.Errorf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
				}
				var payload struct {
					ID string `json:"id"`
				}
				if err := jsonAPI.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
					t.Errorf("response must be exactly one JSON document: %v; body=%s", err, recorder.Body.String())
				}
				responseIDs[payload.ID] = true
			}
			if upstreamCalls.Load() != 2 || len(responseIDs) != 2 {
				t.Errorf("upstream calls = %d, distinct responses = %d; want two independent generations", upstreamCalls.Load(), len(responseIDs))
			}
			if recorded := stats.APIKeyGet(890000); recorded.RequestSuccess != 2 || recorded.RequestFailed != 0 {
				t.Errorf("request metrics = %+v, want exactly two successes", recorded)
			}
		})
	}
}

func TestHandlerGenerationFailureDoesNotRestartRelay(t *testing.T) {
	if runIndependentRequestTestInSubprocess(t) {
		return
	}
	for _, statusCode := range []int{http.StatusBadRequest, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			var upstreamCalls atomic.Int64
			prepareIndependentRequestFixture(t, func(request *http.Request) (*http.Response, error) {
				upstreamCalls.Add(1)
				return newIndependentFixtureResponse(request, statusCode, `{"error":{"message":"fixture failure","type":"invalid_request_error"}}`), nil
			})
			recorder := serveIndependentFixtureRequest(`{"model":"creative-fixture","messages":[{"role":"user","content":"Write the next scene"}]}`)
			if upstreamCalls.Load() != 1 {
				t.Errorf("upstream calls = %d, want one budgeted attempt", upstreamCalls.Load())
			}
			var payload map[string]any
			if err := jsonAPI.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Errorf("error response must be exactly one JSON document: %v; body=%s", err, recorder.Body.String())
			}
			if recorder.Code < http.StatusBadRequest {
				t.Errorf("status = %d, want failure", recorder.Code)
			}
			if recorded := stats.APIKeyGet(890000); recorded.RequestFailed != 1 || recorded.RequestSuccess != 0 {
				t.Errorf("request metrics = %+v, want exactly one failure", recorded)
			}
		})
	}
}
