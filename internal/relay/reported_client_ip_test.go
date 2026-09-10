package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
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
