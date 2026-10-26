package relay

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type relayWriteResult struct {
	n       int
	err     error
	status  int
	size    int
	written bool
}
type relayWriteCommand struct {
	run  func(gin.ResponseWriter) relayWriteResult
	done chan relayWriteResult
}

// All access to the real HTTP writer runs on this one goroutine. Header() is
// request-owner state; heartbeat commands never read or mutate that map.
type relayResponseWriter struct {
	gin.ResponseWriter
	header   http.Header
	commands chan relayWriteCommand
	finished chan struct{}
	stopOnce sync.Once
	semantic bool // owned by the writer goroutine
	visible  bool
}

func newRelayResponseWriter(base gin.ResponseWriter) *relayResponseWriter {
	w := &relayResponseWriter{ResponseWriter: base, header: base.Header().Clone(), commands: make(chan relayWriteCommand), finished: make(chan struct{})}
	go func() {
		defer close(w.finished)
		for cmd := range w.commands {
			cmd.done <- cmd.run(base)
		}
	}()
	return w
}
func (w *relayResponseWriter) call(run func(gin.ResponseWriter) relayWriteResult) relayWriteResult {
	cmd := relayWriteCommand{run: run, done: make(chan relayWriteResult, 1)}
	select {
	case w.commands <- cmd:
		return <-cmd.done
	case <-w.finished:
		return relayWriteResult{err: io.ErrClosedPipe}
	}
}
func (w *relayResponseWriter) close()              { w.stopOnce.Do(func() { close(w.commands); <-w.finished }) }
func (w *relayResponseWriter) Header() http.Header { return w.header }
func applyWriterHeaders(base gin.ResponseWriter, headers http.Header) {
	if base.Written() {
		return
	}
	dst := base.Header()
	clear(dst)
	for k, v := range headers {
		dst[k] = v
	}
}
func (w *relayResponseWriter) WriteHeader(status int) {
	h := w.header.Clone()
	w.call(func(base gin.ResponseWriter) relayWriteResult {
		applyWriterHeaders(base, h)
		base.WriteHeader(status)
		return relayWriteResult{}
	})
}
func (w *relayResponseWriter) WriteHeaderNow() {
	h := w.header.Clone()
	w.call(func(base gin.ResponseWriter) relayWriteResult {
		applyWriterHeaders(base, h)
		base.WriteHeaderNow()
		return relayWriteResult{}
	})
}
func (w *relayResponseWriter) Write(p []byte) (int, error) {
	h := w.header.Clone()
	r := w.call(func(base gin.ResponseWriter) relayWriteResult {
		applyWriterHeaders(base, h)
		n, err := base.Write(p)
		if n > 0 && !sseCommentOnly(p) {
			w.semantic = true
		}
		return relayWriteResult{n: n, err: err}
	})
	return r.n, r.err
}
func (w *relayResponseWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *relayResponseWriter) Flush() {
	h := w.header.Clone()
	w.call(func(base gin.ResponseWriter) relayWriteResult {
		applyWriterHeaders(base, h)
		base.Flush()
		return relayWriteResult{}
	})
}
func (w *relayResponseWriter) Status() int {
	return w.call(func(base gin.ResponseWriter) relayWriteResult { return relayWriteResult{status: base.Status()} }).status
}
func (w *relayResponseWriter) Size() int {
	return w.call(func(base gin.ResponseWriter) relayWriteResult { return relayWriteResult{size: base.Size()} }).size
}
func (w *relayResponseWriter) Written() bool {
	return w.call(func(base gin.ResponseWriter) relayWriteResult { return relayWriteResult{written: base.Written()} }).written
}
func (w *relayResponseWriter) semanticCommitted() bool {
	return w.call(func(gin.ResponseWriter) relayWriteResult { return relayWriteResult{written: w.semantic} }).written
}
func (w *relayResponseWriter) markVisible() {
	w.call(func(gin.ResponseWriter) relayWriteResult { w.visible = true; return relayWriteResult{} })
}
func (w *relayResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("relay streams cannot hijack the HTTP writer")
}
func (w *relayResponseWriter) CloseNotify() <-chan bool { return nil }
func (w *relayResponseWriter) Pusher() http.Pusher      { return nil }
func sseCommentOnly(p []byte) bool {
	found := false
	for _, line := range strings.Split(string(p), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, ":") {
			if !strings.HasPrefix(line, "id:") && !strings.HasPrefix(line, "event:") && !strings.HasPrefix(line, "retry:") {
				return false
			}
		}
		found = true
	}
	return found
}

func (w *relayResponseWriter) ping() error {
	r := w.call(func(base gin.ResponseWriter) relayWriteResult {
		if w.visible {
			return relayWriteResult{}
		}
		if !base.Written() {
			base.Header().Set("Content-Type", "text/event-stream")
			base.Header().Set("Cache-Control", "no-cache")
			base.Header().Set("X-Accel-Buffering", "no")
		}
		n, err := base.Write([]byte(": ping\n\n"))
		if err == nil {
			base.Flush()
		}
		return relayWriteResult{n: n, err: err}
	})
	return r.err
}
func (w *relayResponseWriter) keepalive(ctx context.Context) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTicker(15 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-timer.C:
				if w.ping() != nil {
					return
				}
			}
		}
	}()
	return func() { close(stop); <-done }
}
func installRelayResponseWriter(c *gin.Context) *relayResponseWriter {
	if w, ok := c.Writer.(*relayResponseWriter); ok {
		return w
	}
	w := newRelayResponseWriter(c.Writer)
	c.Writer = w
	return w
}
func relaySemanticWritten(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if w, ok := c.Writer.(*relayResponseWriter); ok {
		return w.semanticCommitted()
	}
	return c.Writer.Written()
}
