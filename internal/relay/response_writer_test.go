package relay

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
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
		t.Fatalf("call() started after close() returned early: %v", result.err)
	case <-time.After(50 * time.Millisecond):
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
	if result := <-afterCloseDone; !errors.Is(result.err, io.ErrClosedPipe) {
		t.Fatalf("call() after close error = %v, want io.ErrClosedPipe", result.err)
	}
}
