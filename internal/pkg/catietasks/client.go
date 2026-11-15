package catietasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lingyuins/octopus/internal/utils/xurl"
)

const (
	TotalTimeout   = 30 * time.Minute
	requestTimeout = 10 * time.Second
	cancelTimeout  = 5 * time.Second
	busyPollDelay  = 500 * time.Millisecond
	idlePollDelay  = 2 * time.Second
	maxQueryTries  = 3
	maxJSONBody    = 8 << 20
)

var (
	ErrNoRetry           = errors.New("Catie Tasks request cannot be retried")
	ErrSubmissionUnknown = errors.New("task_submission_unknown")
	errTaskClientClosed  = errors.New("Catie Tasks response body closed")
	errTaskTimeout       = fmt.Errorf("Catie Tasks exceeded 30 minute limit: %w", context.DeadlineExceeded)
	errBodyTooLarge      = errors.New("task response exceeds size limit")
)

// These hooks observe the real POST, never the later polled result.
type Config struct {
	Stream              bool
	OnSubmissionStart   func()
	OnSubmissionHeaders func()
}

type Snapshot struct {
	Transport            string `json:"transport"`
	SubmissionState      string `json:"submission_state,omitempty"`
	Status               string `json:"status,omitempty"`
	Done                 bool   `json:"done"`
	Cursor               int    `json:"cursor,omitempty"`
	PollCount            int    `json:"poll_count,omitempty"`
	QueryRetryCount      int    `json:"query_retry_count,omitempty"`
	CancelState          string `json:"cancel_state,omitempty"`
	RequestID            string `json:"request_id,omitempty"`
	CFRay                string `json:"cf_ray,omitempty"`
	RndrID               string `json:"rndr_id,omitempty"`
	StartedAt            int64  `json:"started_at,omitempty"`
	DeadlineAt           int64  `json:"deadline_at,omitempty"`
	SubmissionDispatchAt int64  `json:"submission_dispatch_at,omitempty"`
	SubmissionHeadersAt  int64  `json:"submission_headers_at,omitempty"`
	FirstChunkAt         int64  `json:"first_chunk_at,omitempty"`
	LastObservedAt       int64  `json:"last_observed_at,omitempty"`
	CancelledAt          int64  `json:"cancelled_at,omitempty"`
	TimedOut             bool   `json:"timed_out,omitempty"`
	TerminalReported     bool   `json:"terminal_reported,omitempty"`
}
type State struct {
	mu             sync.Mutex
	snapshot       Snapshot
	taskID         string
	cancel         context.CancelCauseFunc
	clientClosed   bool
	workerFinished bool
}

func NewState() *State {
	return &State{snapshot: Snapshot{Transport: "catie_tasks", SubmissionState: "not_started", CancelState: "not_requested"}}
}
func (s *State) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot
}

// A stream protocol terminal and the task's own terminal are independent.
func (s *State) StreamCompletionError() error {
	snapshot := s.Snapshot()
	if snapshot.Done && snapshot.TerminalReported && snapshot.Status == "completed" {
		return nil
	}
	return fmt.Errorf("%w: SSE terminal conflicts with task status %q (done=%t)", ErrNoRetry, snapshot.Status, snapshot.Done)
}
func (s *State) update(fn func(*Snapshot)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.snapshot)
	s.snapshot.LastObservedAt = time.Now().UnixMilli()
}
func (s *State) requestClientClose() {
	s.mu.Lock()
	if s.workerFinished {
		s.mu.Unlock()
		return
	}
	s.clientClosed = true
	cancel := s.cancel
	s.mu.Unlock()
	cancel(errTaskClientClosed)
}
func (s *State) taskIdentity() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.taskID
}

// ResponseError preserves actual query status/body and transport error identity.
type ResponseError struct {
	StatusCode int
	Body       []byte
	RetryAfter time.Duration
	Cause      error
}

func (e *ResponseError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("task query HTTP %d: %s", e.StatusCode, boundedTaskDetail(e.Body))
	}
	return fmt.Sprintf("task query: %v", e.Cause)
}
func (e *ResponseError) Unwrap() error { return e.Cause }

type terminalError struct {
	status string
	code   int
	detail string
}

func (e *terminalError) Error() string {
	message := "Catie task " + e.status
	if e.code > 0 {
		message += fmt.Sprintf(" (HTTP %d)", e.code)
	}
	if e.detail != "" {
		message += ": " + e.detail
	}
	return message
}
func (e *terminalError) Unwrap() error { return ErrNoRetry }
func ErrorCode(err error) string {
	if errors.Is(err, ErrSubmissionUnknown) {
		return "task_submission_unknown"
	}
	var terminal *terminalError
	if errors.As(err, &terminal) {
		return "task_" + terminal.status
	}
	if errors.Is(err, errTaskTimeout) {
		return "task_timeout"
	}
	var response *ResponseError
	if errors.As(err, &response) {
		return "task_query_error"
	}
	if errors.Is(err, ErrNoRetry) {
		return "task_protocol_error"
	}
	return ""
}

type codedError struct {
	error
	code string
}

func (e codedError) Unwrap() error { return e.error }

func (e codedError) ClientErrorCode() string { return e.code }

// ClientError carries a protocol-renderable code without coupling adapters to
// task execution or replacing the original error identity.
func ClientError(err error) error {
	if code := ErrorCode(err); code != "" {
		return codedError{error: err, code: code}
	}
	return err
}

type taskEnvelope struct {
	ID         json.RawMessage `json:"id"`
	Chunks     []string        `json:"chunks"`
	NextAfter  *int            `json:"next_after"`
	Done       *bool           `json:"done"`
	Status     string          `json:"status"`
	Result     json.RawMessage `json:"result"`
	StatusCode int             `json:"status_code"`
	Error      json.RawMessage `json:"error"`
}
type runtimeState struct {
	state            *State
	client           *http.Client
	submit           *http.Request
	config           Config
	ctx              context.Context
	cancel           context.CancelCauseFunc
	deadlineCancel   context.CancelFunc
	finished         chan struct{}
	finishOnce       sync.Once
	terminalObserved bool // polling goroutine owns this field
}

// Do submits once, then binds GET and cancel to one task, credential and host.
func Do(ctx context.Context, client *http.Client, req *http.Request, config Config, state *State) (*http.Response, error) {
	if state == nil {
		state = NewState()
	}
	// Redirects could replay POST with a new URL or silently change the task host.
	// A per-operation copy preserves the shared connection pool without doing so.
	taskClient := *client
	taskClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	limited, deadlineCancel := context.WithTimeoutCause(ctx, TotalTimeout, errTaskTimeout)
	taskCtx, cancel := context.WithCancelCause(limited)
	run := &runtimeState{state: state, client: &taskClient, config: config, ctx: taskCtx, cancel: cancel, deadlineCancel: deadlineCancel, finished: make(chan struct{})}
	state.mu.Lock()
	state.cancel = cancel
	state.snapshot.StartedAt = time.Now().UnixMilli()
	deadline, _ := taskCtx.Deadline()
	state.snapshot.DeadlineAt = deadline.UnixMilli()
	state.mu.Unlock()
	body, err := readAndRestoreRequestBody(req)
	if err != nil {
		run.finish(false)
		return nil, fmt.Errorf("%w: read task request body: %w", ErrNoRetry, err)
	}
	submit := req.Clone(taskCtx)
	submit.Method = http.MethodPost
	submit.Body = io.NopCloser(bytes.NewReader(body))
	submit.ContentLength = int64(len(body))
	submit.GetBody = nil // even an inherited idempotency header must not replay POST
	submit.Header.Del("Content-Length")
	submit.Header.Del("Transfer-Encoding")
	if submit.Header.Get("Content-Type") == "" {
		submit.Header.Set("Content-Type", "application/json")
	}
	safeCtx, err := xurl.AssertSafeRequestWithPin(submit)
	if err != nil {
		state.update(func(s *Snapshot) { s.SubmissionState = "rejected_before_send" })
		run.finish(false)
		return nil, fmt.Errorf("%w: task URL is not allowed: %w", ErrNoRetry, err)
	}
	run.ctx = safeCtx
	run.submit = submit.WithContext(safeCtx)
	// The operation context covers the entire task-ID body, including slow chunks.
	opCtx, cancelSubmit := context.WithTimeout(safeCtx, requestTimeout)
	state.update(func(s *Snapshot) { s.SubmissionState = "sending"; s.SubmissionDispatchAt = time.Now().UnixMilli() })
	if config.OnSubmissionStart != nil {
		config.OnSubmissionStart()
	}
	response, err := run.client.Do(run.submit.WithContext(opCtx))
	if err != nil {
		cancelSubmit()
		state.update(func(s *Snapshot) { s.SubmissionState = "unknown" })
		run.finish(false)
		return nil, fmt.Errorf("%w: %w: %w", ErrNoRetry, ErrSubmissionUnknown, err)
	}
	if config.OnSubmissionHeaders != nil {
		config.OnSubmissionHeaders()
	}
	state.update(func(s *Snapshot) {
		s.SubmissionHeadersAt = time.Now().UnixMilli()
		s.RequestID = strings.TrimSpace(response.Header.Get("X-Request-ID"))
		s.CFRay = strings.TrimSpace(response.Header.Get("CF-Ray"))
		s.RndrID = strings.TrimSpace(response.Header.Get("Rndr-Id"))
	})
	raw, readErr := readLimitedBody(response.Body, 256<<10)
	_ = response.Body.Close()
	cancelSubmit()
	if readErr != nil {
		state.update(func(s *Snapshot) { s.SubmissionState = "unknown" })
		run.finish(false)
		return nil, fmt.Errorf("%w: %w: %w", ErrNoRetry, ErrSubmissionUnknown, readErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		state.update(func(s *Snapshot) { s.SubmissionState = "rejected" })
		run.finish(false)
		response.Body = io.NopCloser(bytes.NewReader(raw))
		response.ContentLength = int64(len(raw))
		return response, nil
	}
	var submitted taskEnvelope
	if err := json.Unmarshal(raw, &submitted); err != nil {
		state.update(func(s *Snapshot) { s.SubmissionState = "unknown" })
		run.finish(false)
		return nil, fmt.Errorf("%w: %w: invalid submission JSON: %w", ErrNoRetry, ErrSubmissionUnknown, err)
	}
	id := parseTaskID(submitted.ID)
	if id == "" {
		state.update(func(s *Snapshot) { s.SubmissionState = "unknown" })
		run.finish(false)
		return nil, fmt.Errorf("%w: %w: no trustworthy task id", ErrNoRetry, ErrSubmissionUnknown)
	}
	state.mu.Lock()
	state.taskID = id
	state.mu.Unlock()
	state.update(func(s *Snapshot) { s.SubmissionState = "accepted" })
	if config.Stream {
		return run.streamResponse(response.Header), nil
	}
	return run.nonStreamResponse(response.Header)
}
func readAndRestoreRequestBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}
func syntheticHeaders(submit http.Header, contentType string) http.Header {
	headers := make(http.Header)
	for _, key := range []string{"X-Request-ID", "CF-Ray", "Rndr-Id"} {
		if value := submit.Get(key); value != "" {
			headers.Set(key, value)
		}
	}
	headers.Set("Content-Type", contentType)
	headers.Set("Cache-Control", "no-cache")
	headers.Set("X-Accel-Buffering", "no")
	return headers
}
func (r *runtimeState) response(headers http.Header, contentType string, status int, body io.ReadCloser, length int64) *http.Response {
	return &http.Response{Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), StatusCode: status, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: syntheticHeaders(headers, contentType), Body: body, ContentLength: length, Request: r.submit}
}
func (r *runtimeState) streamResponse(headers http.Header) *http.Response {
	reader, writer := io.Pipe()
	body := &taskResponseBody{PipeReader: reader, run: r}
	// A Pipe write has no context argument. Reader close unblocks producer and consumer.
	stop := context.AfterFunc(r.ctx, func() { _ = writer.CloseWithError(r.contextError()) })
	go func() {
		_, err := r.poll(func(chunk []byte) error { _, err := writer.Write(chunk); return err })
		stop()
		r.finish(err != nil && !r.terminalObserved)
		_ = writer.CloseWithError(err)
	}()
	return r.response(headers, "text/event-stream; charset=utf-8", 200, body, -1)
}

type taskResponseBody struct {
	*io.PipeReader
	run *runtimeState
}

func (b *taskResponseBody) Close() error {
	b.run.state.requestClientClose()
	err := b.PipeReader.Close()
	<-b.run.finished // includes the bounded service-cancel acknowledgement
	return err
}
func (r *runtimeState) nonStreamResponse(headers http.Header) (*http.Response, error) {
	final, err := r.poll(nil)
	r.finish(err != nil && !r.terminalObserved)
	if err != nil {
		return nil, err
	}
	result := bytes.TrimSpace(final.Result)
	statusCode := final.StatusCode
	if statusCode == 0 {
		if final.Status == "completed" {
			statusCode = 200
		} else {
			statusCode = 502
		}
	}
	if statusCode < 100 || statusCode > 599 {
		return nil, fmt.Errorf("%w: invalid task status_code %d", ErrNoRetry, statusCode)
	}
	if len(result) == 0 || bytes.Equal(result, []byte("null")) {
		if final.Status == "completed" {
			return nil, fmt.Errorf("%w: completed task returned no result", ErrNoRetry)
		}
		if len(final.Error) > 0 && final.Error[0] == '{' {
			var envelope map[string]json.RawMessage
			_ = json.Unmarshal(final.Error, &envelope)
			if _, nested := envelope["error"]; nested {
				result = final.Error
			} else {
				result = append(append([]byte("{\"error\":"), final.Error...), '}')
			}
		} else {
			var message string
			_ = json.Unmarshal(final.Error, &message)
			if message == "" {
				message = "Catie task " + final.Status
			}
			result, _ = json.Marshal(map[string]any{"error": map[string]any{"message": message, "type": "upstream_error", "code": "task_" + final.Status}})
		}
	}
	return r.response(headers, "application/json", statusCode, io.NopCloser(bytes.NewReader(result)), int64(len(result))), nil
}
func (r *runtimeState) poll(emit func([]byte) error) (*taskEnvelope, error) {
	cursor := 0
	for {
		if r.ctx.Err() != nil {
			return nil, r.contextError()
		}
		envelope, err := r.query(cursor)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNoRetry, err)
		}
		if envelope.Done == nil {
			return nil, fmt.Errorf("%w: task query omitted done", ErrNoRetry)
		}
		status := strings.ToLower(strings.TrimSpace(envelope.Status))
		switch status {
		case "queued", "running", "completed", "failed", "cancelled", "expired":
		default:
			return nil, fmt.Errorf("%w: invalid task status %q", ErrNoRetry, envelope.Status)
		}
		if err := validateCursor(cursor, envelope); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNoRetry, err)
		}
		if r.config.Stream && envelope.NextAfter == nil {
			return nil, fmt.Errorf("%w: stream task query omitted next_after", ErrNoRetry)
		}
		cursor += len(envelope.Chunks)
		// Observe task done before writing final chunks; it never substitutes for SSE finish.
		terminal := *envelope.Done && (status == "completed" || status == "failed" || status == "cancelled" || status == "expired")
		r.terminalObserved = terminal
		r.state.update(func(s *Snapshot) {
			s.Cursor = cursor
			s.Status = status
			s.Done = *envelope.Done
			s.TerminalReported = terminal
			if len(envelope.Chunks) > 0 && s.FirstChunkAt == 0 {
				s.FirstChunkAt = time.Now().UnixMilli()
			}
		})
		if emit != nil {
			for _, chunk := range envelope.Chunks {
				if chunk == "" {
					continue
				}
				if err := emit([]byte(chunk)); err != nil {
					if r.ctx.Err() != nil {
						return nil, r.contextError()
					}
					return nil, fmt.Errorf("%w: deliver task chunk: %w", ErrNoRetry, err)
				}
			}
		}
		if *envelope.Done {
			if !terminal {
				return nil, fmt.Errorf("%w: task reported done while status remained %s", ErrNoRetry, status)
			}
			envelope.Status = status
			if emit != nil && status != "completed" {
				return envelope, &terminalError{status: status, code: envelope.StatusCode, detail: boundedTaskDetail(envelope.Error)}
			}
			return envelope, nil
		}
		if status != "queued" && status != "running" {
			return nil, fmt.Errorf("%w: terminal task status %s without done", ErrNoRetry, status)
		}
		delay := idlePollDelay
		if len(envelope.Chunks) > 0 {
			delay = busyPollDelay
		}
		if err := sleepContext(r.ctx, delay); err != nil {
			return nil, r.contextError()
		}
	}
}
func (r *runtimeState) query(cursor int) (*taskEnvelope, error) {
	var lastErr error
	attempts := 0
	for attempts < maxQueryTries {
		if r.ctx.Err() != nil {
			return nil, r.contextError()
		}
		attempts++
		r.state.update(func(s *Snapshot) {
			s.PollCount++
			if attempts > 1 {
				s.QueryRetryCount++
			}
		})
		envelope, retryAfter, retryable, err := r.queryOnce(cursor)
		if err == nil {
			return envelope, nil
		}
		lastErr = err
		if !retryable || attempts == maxQueryTries {
			break
		}
		delay := retryAfter
		if delay <= 0 {
			delay = time.Duration(attempts) * time.Second
		}
		deadline, _ := r.ctx.Deadline()
		if delay >= time.Until(deadline) {
			return nil, fmt.Errorf("retry delay exceeds remaining task budget: %w", err)
		}
		if err := sleepContext(r.ctx, delay); err != nil {
			return nil, r.contextError()
		}
	}
	return nil, fmt.Errorf("task query failed after %d attempts: %w", attempts, lastErr)
}
func (r *runtimeState) followupRequest(ctx context.Context, method, suffix string) *http.Request {
	req := r.submit.Clone(ctx)
	endpoint := *r.submit.URL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + r.state.taskIdentity() + suffix
	endpoint.RawPath = strings.TrimRight(r.submit.URL.EscapedPath(), "/") + "/" + url.PathEscape(r.state.taskIdentity()) + suffix
	endpoint.RawQuery = ""
	req.URL = &endpoint
	req.Method = method
	req.Body = http.NoBody
	req.GetBody = nil
	req.ContentLength = 0
	req.Header.Del("Content-Type")
	req.Header.Del("Content-Length")
	req.Header.Del("Transfer-Encoding")
	return req
}
func (r *runtimeState) queryOnce(cursor int) (*taskEnvelope, time.Duration, bool, error) {
	opCtx, cancel := context.WithTimeout(r.ctx, requestTimeout)
	defer cancel()
	query := r.followupRequest(opCtx, http.MethodGet, "")
	query.URL.RawQuery = "after=" + strconv.Itoa(cursor)
	response, err := r.client.Do(query)
	if err != nil {
		if r.ctx.Err() != nil {
			return nil, 0, false, r.contextError()
		}
		return nil, 0, recoverableTransportError(err), &ResponseError{Cause: err}
	}
	defer response.Body.Close()
	raw, err := readLimitedBody(response.Body, maxJSONBody)
	if err != nil {
		if r.ctx.Err() != nil {
			return nil, 0, false, r.contextError()
		}
		return nil, 0, recoverableTransportError(err), &ResponseError{StatusCode: response.StatusCode, Body: raw, Cause: err}
	}
	retryAfter := parseRetryAfter(response.Header.Get("Retry-After"), time.Now())
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, retryAfter, retryableTaskStatus(response.StatusCode), &ResponseError{StatusCode: response.StatusCode, Body: raw, RetryAfter: retryAfter}
	}
	var envelope taskEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, 0, false, &ResponseError{StatusCode: response.StatusCode, Body: raw, Cause: fmt.Errorf("invalid task query JSON: %w", err)}
	}
	return &envelope, 0, false, nil
}
func validateCursor(cursor int, envelope *taskEnvelope) error {
	if len(envelope.Chunks) > math.MaxInt-cursor {
		return errors.New("task cursor overflow")
	}
	expected := cursor + len(envelope.Chunks)
	if envelope.NextAfter == nil {
		return nil
	}
	next := *envelope.NextAfter
	if next < cursor {
		return fmt.Errorf("task cursor moved backwards from %d to %d", cursor, next)
	}
	if next > expected {
		return fmt.Errorf("task cursor skipped from %d to %d after %d chunks", cursor, next, len(envelope.Chunks))
	}
	if next != expected {
		return fmt.Errorf("task cursor %d conflicts with %d returned chunks from %d", next, len(envelope.Chunks), cursor)
	}
	return nil
}
func (r *runtimeState) contextError() error {
	return fmt.Errorf("%w: %w", ErrNoRetry, context.Cause(r.ctx))
}
func (r *runtimeState) finish(cancelTask bool) {
	r.finishOnce.Do(func() {
		if r.ctx.Err() != nil && !r.terminalObserved {
			r.state.update(func(s *Snapshot) { s.CancelledAt = time.Now().UnixMilli() })
		}
		if errors.Is(r.ctx.Err(), context.DeadlineExceeded) && !r.terminalObserved {
			r.state.update(func(s *Snapshot) { s.TimedOut = true })
		}
		if cancelTask && r.state.taskIdentity() != "" {
			r.state.update(func(s *Snapshot) { s.CancelState = "requested" })
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), cancelTimeout)
			response, err := r.client.Do(r.followupRequest(ctx, http.MethodPost, "/cancel"))
			confirmed := false
			if err == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
				_ = response.Body.Close()
				confirmed = response.StatusCode >= 200 && response.StatusCode < 300
			}
			cancel()
			r.state.update(func(s *Snapshot) {
				if confirmed {
					s.CancelState = "confirmed"
				} else {
					s.CancelState = "unconfirmed"
				}
			})
		}
		r.cancel(nil)
		r.deadlineCancel()
		r.state.mu.Lock()
		r.state.workerFinished = true
		r.state.mu.Unlock()
		close(r.finished)
	})
}
func readLimitedBody(body io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return raw, err
	}
	if int64(len(raw)) > limit {
		return raw[:limit], fmt.Errorf("%w: %d bytes", errBodyTooLarge, limit)
	}
	return raw, nil
}
func parseTaskID(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		text = strings.TrimSpace(text)
		if text == "." || text == ".." || strings.ContainsAny(text, "/\\?#\r\n") || len(text) > 256 {
			return ""
		}
		return text
	}
	var integer uint64
	if json.Unmarshal(raw, &integer) == nil && integer > 0 {
		return strconv.FormatUint(integer, 10)
	}
	return ""
}
func recoverableTransportError(err error) bool {
	if errors.Is(err, errBodyTooLarge) || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var dns *net.DNSError
	if errors.As(err, &dns) && dns.IsNotFound {
		return false
	}
	var network net.Error
	return errors.As(err, &network) && (network.Timeout() || network.Temporary())
}
func retryableTaskStatus(status int) bool {
	switch status {
	case 408, 425, 429, 500, 502, 503, 504:
		return true
	}
	return false
}
func parseRetryAfter(raw string, now time.Time) time.Duration {
	raw = strings.TrimSpace(raw)
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds >= 0 {
		if seconds > math.MaxInt64/int64(time.Second) {
			return time.Duration(math.MaxInt64)
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil && when.After(now) {
		return when.Sub(now)
	}
	return 0
}
func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func boundedTaskDetail(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if len(text) > 2048 {
		text = text[:2048]
	}
	return text
}
