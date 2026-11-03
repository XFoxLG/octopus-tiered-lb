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

const (
	relayWriteDeadline      = 10 * time.Second
	relayWriterCloseGrace   = relayWriteDeadline + time.Second
	relayWriterFinishDetach = 25 * time.Millisecond
	relayKeepaliveInterval  = 15 * time.Second

	relayKeepalivePhaseHeaderWait    = "header_wait"
	relayKeepalivePhaseRetryWait     = "retry_wait"
	relayKeepalivePhaseReasoningWait = "reasoning_wait"
	relayKeepalivePhaseBodyWait      = "body_wait"
)

var (
	errRelayWriteDeadlineUnsupported = errors.New("relay response writer does not support bounded writes")
	errRelayWriteCommandTimeout      = errors.New("relay response write exceeded its bounded deadline")
)

// All access to the real HTTP writer runs on this one goroutine. Header() is
// request-owner state; heartbeat commands never read or mutate that map.
type relayResponseWriter struct {
	gin.ResponseWriter
	header             http.Header
	commands           chan relayWriteCommand
	finished           chan struct{}
	stopOnce           sync.Once
	interruptOnce      sync.Once
	closeMu            sync.Mutex
	accepted           bool
	semantic           bool // owned by the writer goroutine
	visible            bool
	stopRequestWatcher func() bool
	requireDeadline    bool
	keepaliveMu        sync.RWMutex
	keepalivePhase     string
	onKeepalive        func(time.Time, string)
}

func newRelayResponseWriter(base gin.ResponseWriter) *relayResponseWriter {
	return newRelayResponseWriterWithDeadlinePolicy(base, !isTestResponseSink(base))
}

func newRelayResponseWriterWithDeadlinePolicy(base gin.ResponseWriter, requireDeadline bool) *relayResponseWriter {
	w := &relayResponseWriter{
		ResponseWriter:  base,
		header:          base.Header().Clone(),
		commands:        make(chan relayWriteCommand),
		finished:        make(chan struct{}),
		requireDeadline: requireDeadline,
		keepalivePhase:  relayKeepalivePhaseHeaderWait,
	}
	go func() {
		defer close(w.finished)
		for cmd := range w.commands {
			cmd.done <- w.runBounded(base, cmd.run)
		}
	}()
	return w
}

func (w *relayResponseWriter) runBounded(base gin.ResponseWriter, run func(gin.ResponseWriter) relayWriteResult) relayWriteResult {
	controller := http.NewResponseController(base)
	deadlineErr := controller.SetWriteDeadline(time.Now().Add(relayWriteDeadline))
	if errors.Is(deadlineErr, http.ErrNotSupported) && w.requireDeadline {
		return relayWriteResult{err: errRelayWriteDeadlineUnsupported}
	}
	if deadlineErr != nil && !errors.Is(deadlineErr, http.ErrNotSupported) {
		return relayWriteResult{err: deadlineErr}
	}
	result := run(base)
	if deadlineErr == nil {
		_ = controller.SetWriteDeadline(time.Time{})
	}
	return result
}

func (w *relayResponseWriter) call(run func(gin.ResponseWriter) relayWriteResult) relayWriteResult {
	cmd := relayWriteCommand{run: run, done: make(chan relayWriteResult, 1)}
	// close() may fire concurrently; reject commands once it has started so a
	// send can never race the channel close and panic.
	w.closeMu.Lock()
	if w.accepted {
		w.closeMu.Unlock()
		return relayWriteResult{err: io.ErrClosedPipe}
	}
	select {
	case w.commands <- cmd:
		w.closeMu.Unlock()
	case <-w.finished:
		w.closeMu.Unlock()
		return relayWriteResult{err: io.ErrClosedPipe}
	}

	timer := time.NewTimer(relayWriteDeadline + time.Second)
	defer timer.Stop()
	select {
	case result := <-cmd.done:
		return result
	case <-w.finished:
		select {
		case result := <-cmd.done:
			return result
		default:
			return relayWriteResult{err: io.ErrClosedPipe}
		}
	case <-timer.C:
		return relayWriteResult{err: errRelayWriteCommandTimeout}
	}
}
func (w *relayResponseWriter) close() {
	w.stopOnce.Do(func() {
		if w.stopRequestWatcher != nil {
			w.stopRequestWatcher()
		}
		w.closeMu.Lock()
		w.accepted = true
		close(w.commands)
		w.closeMu.Unlock()

		timer := time.NewTimer(relayWriterCloseGrace)
		defer timer.Stop()
		select {
		case <-w.finished:
		case <-timer.C:
			// The request must not hang forever on a writer that ignored both the
			// deadline and the cancellation hook. The goroutine remains isolated;
			// no further command can be accepted by this writer.
		}
	})
}
func (w *relayResponseWriter) Header() http.Header { return w.header }
func (w *relayResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
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
func (w *relayResponseWriter) FlushError() error {
	h := w.header.Clone()
	return w.call(func(base gin.ResponseWriter) relayWriteResult {
		applyWriterHeaders(base, h)
		controller := http.NewResponseController(base)
		if err := controller.Flush(); err != nil {
			return relayWriteResult{err: err}
		}
		return relayWriteResult{}
	}).err
}
func (w *relayResponseWriter) Flush() {
	_ = w.FlushError()
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
func (w *relayResponseWriter) interrupt() {
	w.interruptOnce.Do(func() {
		_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now())
	})
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
		if !base.Written() {
			base.Header().Set("Content-Type", "text/event-stream")
			base.Header().Set("Cache-Control", "no-cache, no-transform")
			base.Header().Set("X-Accel-Buffering", "no")
			base.Header().Del("ETag")
		}
		n, err := base.Write([]byte(": ping\n\n"))
		if err == nil {
			err = http.NewResponseController(base).Flush()
		}
		return relayWriteResult{n: n, err: err}
	})
	return r.err
}

func (w *relayResponseWriter) setKeepalivePhase(phase string) {
	if w == nil || phase == "" {
		return
	}
	w.keepaliveMu.Lock()
	w.keepalivePhase = phase
	w.keepaliveMu.Unlock()
}

func (w *relayResponseWriter) currentKeepalivePhase() string {
	if w == nil {
		return relayKeepalivePhaseHeaderWait
	}
	w.keepaliveMu.RLock()
	phase := w.keepalivePhase
	w.keepaliveMu.RUnlock()
	if phase == "" {
		return relayKeepalivePhaseHeaderWait
	}
	return phase
}

func (w *relayResponseWriter) notifyKeepalive(at time.Time) {
	if w == nil || w.onKeepalive == nil {
		return
	}
	w.onKeepalive(at, w.currentKeepalivePhase())
}

func (w *relayResponseWriter) keepalive(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return func() {}, err
	}
	// Commit the SSE response immediately after authentication and route
	// preflight. Waiting 15 seconds for the first comment leaves proxies with
	// an avoidable "origin has not started responding" window.
	if err := w.ping(); err != nil {
		return func() {}, err
	}
	w.notifyKeepalive(time.Now())

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTicker(relayKeepaliveInterval)
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
				w.notifyKeepalive(time.Now())
			}
		}
	}()
	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			close(stop)
			<-done
		})
	}, nil
}
func installRelayResponseWriter(c *gin.Context) *relayResponseWriter {
	if w, ok := c.Writer.(*relayResponseWriter); ok {
		return w
	}
	w := newRelayResponseWriter(c.Writer)
	if trace := relayRequestTraceFromContext(c); trace != nil {
		w.onKeepalive = trace.recordKeepalive
	}
	if c.Request != nil {
		w.stopRequestWatcher = context.AfterFunc(c.Request.Context(), w.interrupt)
	}
	c.Writer = w
	return w
}

func setRelayKeepalivePhase(c *gin.Context, phase string) {
	if c == nil {
		return
	}
	if w, ok := c.Writer.(*relayResponseWriter); ok {
		w.setKeepalivePhase(phase)
	}
}

func (req *relayRequest) setKeepalivePhase(phase string) {
	if req != nil {
		setRelayKeepalivePhase(req.c, phase)
	}
}

func (ra *relayAttempt) setKeepalivePhase(phase string) {
	if ra != nil {
		setRelayKeepalivePhase(ra.c, phase)
	}
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

func isTestResponseSink(writer http.ResponseWriter) bool {
	type resultWriter interface {
		Result() *http.Response
	}
	type unwrapper interface {
		Unwrap() http.ResponseWriter
	}
	for writer != nil {
		if _, ok := writer.(resultWriter); ok {
			return true
		}
		next, ok := writer.(unwrapper)
		if !ok {
			return false
		}
		writer = next.Unwrap()
	}
	return false
}
