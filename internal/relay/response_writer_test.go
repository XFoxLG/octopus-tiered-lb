package relay

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestKeepaliveCommitsHTTPWithoutSemanticOutput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		w := installRelayResponseWriter(c)
		defer w.close()
		stop := w.keepalive(context.Background())
		defer stop()
		time.Sleep(14 * time.Second)
		synctest.Wait()
		if w.Written() {
			t.Fatal("early heartbeat")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if !w.Written() || relaySemanticWritten(c) || recorder.Body.String() != ": ping\n\n" {
			t.Fatalf("heartbeat claimed content: %q", recorder.Body.String())
		}
		if recorder.Header().Get("Content-Type") != "text/event-stream" {
			t.Fatal("heartbeat missing SSE type")
		}
		writeClientTerminalError(c, nil, 0, 502, context.DeadlineExceeded)
		if !strings.Contains(recorder.Body.String(), "event: error\n") || !relaySemanticWritten(c) {
			t.Fatal("committed heartbeat swallowed final error")
		}
	})
}

func TestKeepaliveStopsAtVisibleContentAndClientCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		w := installRelayResponseWriter(c)
		defer w.close()
		ctx, cancel := context.WithCancel(context.Background())
		stop := w.keepalive(ctx)
		defer stop()
		time.Sleep(15 * time.Second)
		synctest.Wait()
		w.markVisible()
		_, _ = w.Write([]byte("data: hello\n\n"))
		w.Flush()
		time.Sleep(30 * time.Second)
		synctest.Wait()
		cancel()
		synctest.Wait()
		if strings.Count(recorder.Body.String(), ": ping") != 1 {
			t.Fatal("pre-visible heartbeat continued")
		}
	})
}
