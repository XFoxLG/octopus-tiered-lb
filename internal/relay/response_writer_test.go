package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
)

// synchronizedGinResponseWriter protects httptest.ResponseRecorder's body while
// a keepalive goroutine can still append control frames. Production writers are
// serialized by relayResponseWriter; the recorder's bytes.Buffer is not.
type synchronizedGinResponseWriter struct {
	gin.ResponseWriter
	recorder *httptest.ResponseRecorder
	mu       sync.Mutex
}

func newSynchronizedTestContext(t *testing.T) (*gin.Context, *synchronizedGinResponseWriter) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	writer := &synchronizedGinResponseWriter{
		ResponseWriter: c.Writer,
		recorder:       recorder,
	}
	c.Writer = writer
	return c, writer
}

func (w *synchronizedGinResponseWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Write(data)
}

func (w *synchronizedGinResponseWriter) WriteString(data string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.WriteString(data)
}

func (w *synchronizedGinResponseWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ResponseWriter.WriteHeader(code)
}

func (w *synchronizedGinResponseWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *synchronizedGinResponseWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Status()
}

func (w *synchronizedGinResponseWriter) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Size()
}

func (w *synchronizedGinResponseWriter) Written() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Written()
}

func (w *synchronizedGinResponseWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ResponseWriter.Flush()
}

func (w *synchronizedGinResponseWriter) bodyString() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.recorder.Body.String()
}

func (w *synchronizedGinResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func TestKeepaliveCommitsHTTPWithoutSemanticOutput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, recorder := newSynchronizedTestContext(t)
		w := installRelayResponseWriter(c)
		defer w.close()
		stop, err := w.keepalive(context.Background())
		if err != nil {
			t.Fatalf("keepalive start: %v", err)
		}
		defer stop()
		if !w.Written() || relaySemanticWritten(c) || recorder.bodyString() != ": ping\n\n" {
			t.Fatalf("initial heartbeat missing or claimed content: %q", recorder.bodyString())
		}
		if recorder.Header().Get("Cache-Control") != "no-cache, no-transform" {
			t.Fatalf("heartbeat Cache-Control = %q", recorder.Header().Get("Cache-Control"))
		}
		time.Sleep(14 * time.Second)
		synctest.Wait()
		if strings.Count(recorder.bodyString(), ": ping") != 1 {
			t.Fatal("heartbeat repeated before the 15 second interval")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if relaySemanticWritten(c) || strings.Count(recorder.bodyString(), ": ping") != 2 {
			t.Fatalf("heartbeat claimed content: %q", recorder.bodyString())
		}
		if recorder.Header().Get("Content-Type") != "text/event-stream" {
			t.Fatal("heartbeat missing SSE type")
		}
		writeClientTerminalError(c, nil, 0, 502, context.DeadlineExceeded)
		if !strings.Contains(recorder.bodyString(), "event: error\n") || !relaySemanticWritten(c) {
			t.Fatal("committed heartbeat swallowed final error")
		}
	})
}

func TestSingleKeepaliveOwnerContinuesAfterVisibleContent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		w := installRelayResponseWriter(c)
		defer w.close()
		ctx, cancel := context.WithCancel(context.Background())
		stop, err := w.keepalive(ctx)
		if err != nil {
			t.Fatalf("keepalive start: %v", err)
		}
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
		if strings.Count(recorder.Body.String(), ": ping") != 4 {
			t.Fatalf("keepalive owner count = %d, want 4 continuous pings", strings.Count(recorder.Body.String(), ": ping"))
		}
	})
}

// The first comment commits the HTTP response before the relay applies its SSE
// headers. Anything the relay sets after that commit is dropped by net/http, so
// the early commit must publish a complete SSE header set on its own.
func TestKeepaliveFirstCommentCommitsCompleteSSEHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	w := installRelayResponseWriter(c)
	defer w.close()
	c.Header("X-Conversation-ID", "conv-early-commit")

	stop, err := w.keepalive(context.Background())
	if err != nil {
		t.Fatalf("keepalive start: %v", err)
	}
	defer stop()

	headers := recorder.Result().Header
	if headers.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type = %q", headers.Get("Content-Type"))
	}
	if headers.Get("Cache-Control") != "no-cache, no-transform" {
		t.Fatalf("Cache-Control = %q", headers.Get("Cache-Control"))
	}
	if headers.Get("Connection") != "keep-alive" {
		t.Fatalf("Connection = %q", headers.Get("Connection"))
	}
	if headers.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("X-Accel-Buffering = %q", headers.Get("X-Accel-Buffering"))
	}
	if headers.Get("X-Conversation-ID") != "conv-early-commit" {
		t.Fatalf("X-Conversation-ID = %q", headers.Get("X-Conversation-ID"))
	}
	if headers.Get("ETag") != "" {
		t.Fatalf("ETag = %q, want removed", headers.Get("ETag"))
	}
	if recorder.Body.String() != ": ping\n\n" {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestKeepaliveDiagnosticsSeparateControlFromSemanticBytes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", http.NoBody)
		trace := newRelayRequestTrace(c.Request, false)
		c.Set(relayRequestTraceContextKey, trace)
		c.Writer = &relayCaptureResponseWriter{ResponseWriter: c.Writer, trace: trace}
		w := installRelayResponseWriter(c)
		stop, err := w.keepalive(context.Background())
		if err != nil {
			t.Fatalf("keepalive start: %v", err)
		}
		stop()

		visible := []byte("data: semantic\n\n")
		if _, err := w.Write(visible); err != nil {
			t.Fatalf("write semantic frame: %v", err)
		}
		w.close()

		diagnostics := trace.diagnosticsSnapshot()
		if diagnostics == nil {
			t.Fatal("missing diagnostics")
		}
		pingBytes := int64(len(": ping\n\n"))
		if diagnostics.KeepalivePings != 1 || diagnostics.ControlBytes != pingBytes ||
			diagnostics.SemanticBytes != int64(len(visible)) ||
			diagnostics.WriterAcceptedBytes != pingBytes+int64(len(visible)) {
			t.Fatalf("diagnostics = %+v", diagnostics)
		}
		if diagnostics.FirstKeepaliveAt == 0 || diagnostics.LastKeepaliveAt == 0 ||
			diagnostics.LastKeepalivePhase != relayKeepalivePhaseHeaderWait {
			t.Fatalf("keepalive diagnostics = %+v", diagnostics)
		}
	})
}

func TestRelayResponseWriterCallCloseRace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	w := installRelayResponseWriter(c)

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := w.call(func(base gin.ResponseWriter) relayWriteResult {
				return relayWriteResult{}
			})
			if r.err != nil && !errors.Is(r.err, io.ErrClosedPipe) {
				t.Errorf("call() error = %v, want nil or io.ErrClosedPipe", r.err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.close()
	}()
	wg.Wait()
}

func TestRelayResponseWriterCallAfterClose(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	w := installRelayResponseWriter(c)
	w.close()
	r := w.call(func(base gin.ResponseWriter) relayWriteResult {
		return relayWriteResult{}
	})
	if !errors.Is(r.err, io.ErrClosedPipe) {
		t.Fatalf("call() after close error = %v, want io.ErrClosedPipe", r.err)
	}
}

func TestRelayResponseWriterRejectsUnboundedProductionSink(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	w := newRelayResponseWriterWithDeadlinePolicy(c.Writer, true)
	defer w.close()

	err := w.ping()
	if !errors.Is(err, errRelayWriteDeadlineUnsupported) {
		t.Fatalf("ping error = %v, want %v", err, errRelayWriteDeadlineUnsupported)
	}
}

func TestRelayResponseWriterBoundedDeadlineTraversesTraceWrapper(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handlerDone := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		c, _ := gin.CreateTestContext(response)
		c.Request = request
		trace := newRelayRequestTrace(request, false)
		c.Writer = &relayCaptureResponseWriter{ResponseWriter: c.Writer, trace: trace}
		w := installRelayResponseWriter(c)
		defer w.close()

		if _, err := w.Write([]byte("data: visible\n\n")); err != nil {
			handlerDone <- err
			return
		}
		handlerDone <- w.FlushError()
	}))
	defer server.Close()

	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer response.Body.Close()
	if err := <-handlerDone; err != nil {
		t.Fatalf("handled bounded write: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "data: visible\n\n" {
		t.Fatalf("body = %q, want visible frame", body)
	}
}

func TestRelayResponseWriterCloseWaitsForAcceptedCommand(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	w := installRelayResponseWriter(c)

	started := make(chan struct{})
	release := make(chan struct{})
	callDone := make(chan relayWriteResult, 1)
	go func() {
		callDone <- w.call(func(gin.ResponseWriter) relayWriteResult {
			close(started)
			<-release
			return relayWriteResult{}
		})
	}()
	<-started

	closeDone := make(chan struct{})
	go func() {
		w.close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
		t.Fatal("close() returned while an accepted command was still running")
	case <-time.After(50 * time.Millisecond):
	}

	afterCloseDone := make(chan relayWriteResult, 1)
	go func() {
		afterCloseDone <- w.call(func(gin.ResponseWriter) relayWriteResult {
			return relayWriteResult{}
		})
	}()
	select {
	case result := <-afterCloseDone:
		if !errors.Is(result.err, io.ErrClosedPipe) {
			t.Fatalf("call() during close error = %v, want io.ErrClosedPipe", result.err)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("call() did not fail fast after close started")
	}

	close(release)
	if result := <-callDone; result.err != nil {
		t.Fatalf("accepted call error = %v, want nil", result.err)
	}
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("close() did not finish after accepted command completed")
	}
}
