package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/db"
	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
)

func TestResolveReportedClientIP(t *testing.T) {
	tests := []struct {
		name        string
		headers     map[string]string
		wantIP      string
		wantSource  ReportedClientIPSource
	}{
		{
			name:       "CF-Connecting-IP wins when present",
			headers:    map[string]string{"CF-Connecting-IP": "203.0.113.7", "X-Forwarded-For": "198.51.100.9, 10.0.0.1"},
			wantIP:     "203.0.113.7",
			wantSource: ReportedClientIPSourceCFConnectingIP,
		},
		{
			name:       "falls back to XFF rightmost public",
			headers:    map[string]string{"X-Forwarded-For": "198.51.100.9, 10.0.0.1, 172.16.0.5"},
			wantIP:     "198.51.100.9",
			wantSource: ReportedClientIPSourceXForwardedFor,
		},
		{
			name:       "spoofed leftmost XFF is ignored; rightmost public wins",
			headers:    map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.9"},
			wantIP:     "198.51.100.9",
			wantSource: ReportedClientIPSourceXForwardedFor,
		},
		{
			name:       "all-private XFF yields none",
			headers:    map[string]string{"X-Forwarded-For": "10.0.0.1, 192.168.1.1"},
			wantIP:     "",
			wantSource: ReportedClientIPSourceNone,
		},
		{
			name:       "invalid header values yield none",
			headers:    map[string]string{"CF-Connecting-IP": "not-an-ip", "X-Forwarded-For": "example.com"},
			wantIP:     "",
			wantSource: ReportedClientIPSourceNone,
		},
		{
			name:       "empty headers yield none",
			headers:    map[string]string{},
			wantIP:     "",
			wantSource: ReportedClientIPSourceNone,
		},
		{
			name:       "IPv6 public address accepted",
			headers:    map[string]string{"CF-Connecting-IP": "2001:db8::1"},
			wantIP:     "2001:db8::1",
			wantSource: ReportedClientIPSourceCFConnectingIP,
		},
		{
			name:       "CF loopback value falls through to XFF",
			headers:    map[string]string{"CF-Connecting-IP": "127.0.0.1", "X-Forwarded-For": "203.0.113.7"},
			wantIP:     "203.0.113.7",
			wantSource: ReportedClientIPSourceXForwardedFor,
		},
		{
			name:       "X-Real-IP never trusted",
			headers:    map[string]string{"X-Real-IP": "203.0.113.99"},
			wantIP:     "",
			wantSource: ReportedClientIPSourceNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getter := func(key string) string { return tt.headers[key] }
			got := resolveReportedClientIP(getter)
			if got.IP != tt.wantIP {
				t.Fatalf("IP = %q, want %q", got.IP, tt.wantIP)
			}
			if got.Source != tt.wantSource {
				t.Fatalf("Source = %q, want %q", got.Source, tt.wantSource)
			}
		})
	}
}

func TestCaptureReportedClientIPMiddlewareFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		captureReportedClientIP(c)
		c.Next()
	})
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		resolved := reportedClientIPFromContext(c)
		c.String(http.StatusOK, resolved.IP+"|"+string(resolved.Source))
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("CF-Connecting-IP", "203.0.113.7")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if got := recorder.Body.String(); got != "203.0.113.7|cf-connecting-ip" {
		t.Fatalf("captured %q, want %q", got, "203.0.113.7|cf-connecting-ip")
	}
}

func TestReportedClientIPFromContextWithoutCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	resolved := reportedClientIPFromContext(ctx)
	if resolved.Source != ReportedClientIPSourceNone || resolved.IP != "" {
		t.Fatalf("unexpected resolve without capture: %+v", resolved)
	}
}

func TestHandlerPersistsSourceMetadataWithoutChangingSecurityIP(testContext *testing.T) {
	if runIndependentRequestTestInSubprocess(testContext) {
		return
	}
	for _, scenario := range []struct {
		name           string
		cloudflareIP   string
		forwardedFor   string
		reportedIP     string
		reportedSource ReportedClientIPSource
	}{
		{"cloudflare", "203.0.113.7", "198.51.100.9, 10.0.0.1", "203.0.113.7", ReportedClientIPSourceCFConnectingIP},
		{"forwarded_chain", "", "6.6.6.6, 198.51.100.9, 10.0.0.1", "198.51.100.9", ReportedClientIPSourceXForwardedFor},
		{"private_headers", "127.0.0.1", "10.0.0.1, 192.168.1.1", "", ReportedClientIPSourceNone},
		{"no_headers", "", "", "", ReportedClientIPSourceNone},
	} {
		testContext.Run(scenario.name, func(testContext *testing.T) {
			prepareIndependentRequestFixture(testContext, func(request *http.Request) (*http.Response, error) {
				return newIndependentFixtureResponse(request, http.StatusOK, `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"fixture answer"},"finish_reason":"stop"}]}`), nil
			})
			if err := db.InitLogDB("", "", false); err != nil {
				testContext.Fatal(err)
			}
			for settingKey, value := range map[appmodel.SettingKey]string{
				appmodel.SettingKeyRelayLogKeepEnabled:    "true",
				appmodel.SettingKeyRelayLogContentEnabled: "false",
			} {
				if err := setting.SetString(settingKey, value); err != nil {
					testContext.Fatal(err)
				}
			}

			engine := gin.New()
			if err := engine.SetTrustedProxies(nil); err != nil {
				testContext.Fatal(err)
			}
			engine.Use(RelayRequestTraceMiddleware())
			engine.POST("/v1/chat/completions", func(requestContext *gin.Context) {
				if requestContext.ClientIP() != "127.0.0.1" {
					testContext.Error("display headers changed the security IP")
				}
				requestContext.Set("api_key_id", 890000)
				Handler(appmodel.EndpointTypeChat, inbound.InboundTypeOpenAIChat, requestContext)
			})
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"creative-fixture","messages":[{"role":"user","content":"Continue the story"}]}`))
			request.RemoteAddr = "127.0.0.1:12345"
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("User-Agent", "Tavo/source-fixture")
			request.Header.Set("CF-Connecting-IP", scenario.cloudflareIP)
			request.Header.Set("X-Forwarded-For", scenario.forwardedFor)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				testContext.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}

			assertSourceList := func() int64 {
				testContext.Helper()
				logs, err := relaylog.RelayLogList(context.Background(), relaylog.LogFilter{}, 1, 10)
				if err != nil || len(logs) != 1 {
					testContext.Fatalf("expected one source log: logs=%+v err=%v", logs, err)
				}
				entry := logs[0]
				if entry.ClientIP != "127.0.0.1" || entry.ReportedClientIP != scenario.reportedIP || entry.ReportedClientIPSource != string(scenario.reportedSource) {
					testContext.Fatalf("source metadata changed: %+v", entry)
				}
				return entry.ID
			}
			logID := assertSourceList()
			if err := relaylog.RelayLogSaveDBTask(context.Background()); err != nil {
				testContext.Fatal(err)
			}
			cachedLogs, cacheLock := relaylog.GetCacheAndLock()
			cacheLock.Lock()
			remainingLogs := len(cachedLogs)
			cacheLock.Unlock()
			if remainingLogs != 0 {
				testContext.Fatal("fixture did not flush; persisted list would only retest the cache")
			}
			if persistedID := assertSourceList(); persistedID != logID {
				testContext.Fatalf("persisted log ID=%d, want %d", persistedID, logID)
			}
			detail, err := relaylog.RelayLogGetByID(context.Background(), logID)
			if err != nil || detail == nil {
				testContext.Fatalf("read persisted detail: %v", err)
			}
			if detail.UserAgent != "Tavo/source-fixture" || detail.ClientIP != "127.0.0.1" || detail.ReportedClientIP != scenario.reportedIP {
				testContext.Fatalf("persisted detail lost request source: %+v", detail)
			}
		})
	}
}
