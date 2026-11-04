package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// The first keepalive comment commits the HTTP response. Go's net/http copies
// the header map at commit time and ignores later mutations, so this test reads
// the response through a real HTTP client to prove the wire-level result rather
// than trusting a recorder snapshot.
func TestKeepaliveFirstCommentHeadersReachRealHTTPClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/stream", func(c *gin.Context) {
		c.Header("X-Conversation-ID", "conv-wire-check")
		writer := installRelayResponseWriter(c)
		defer writer.close()
		stop, err := writer.keepalive(c.Request.Context())
		if err != nil {
			c.Abort()
			return
		}
		defer stop()
		// Give the client a chance to read the committed headers.
		time.Sleep(150 * time.Millisecond)
	})

	server := httptest.NewServer(engine)
	defer server.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(server.URL + "/stream")
	if err != nil {
		t.Fatalf("GET /stream: %v", err)
	}
	defer response.Body.Close()

	headers := response.Header
	if got := headers.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := headers.Get("Cache-Control"); got != "no-cache, no-transform" {
		t.Errorf("Cache-Control = %q, want no-cache, no-transform", got)
	}
	if got := headers.Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", got)
	}
	if got := headers.Get("X-Conversation-ID"); got != "conv-wire-check" {
		t.Errorf("X-Conversation-ID = %q, want conv-wire-check", got)
	}
	if got := headers.Get("ETag"); got != "" {
		t.Errorf("ETag = %q, want removed", got)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read stream body: %v", err)
	}
	if !strings.HasPrefix(string(body), ": ping\n\n") {
		t.Fatalf("stream body = %q, want it to start with the keepalive comment", string(body))
	}
}
