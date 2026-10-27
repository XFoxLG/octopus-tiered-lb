package relay

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/server/resp"
)

var errRelayRequestBodyTooLarge = errors.New("request body too large")

// relayRequestBodyTooLargeError carries the evidence an operator needs to
// explain a rejection: the configured limit and, when known, the actual size.
// Actual stays <= 0 for chunked requests, where the real size only becomes
// known while reading and MaxBytesReader aborts mid-stream.
type relayRequestBodyTooLargeError struct {
	actual int64
	limit  int64
}

func (e *relayRequestBodyTooLargeError) Error() string {
	if e == nil {
		return errRelayRequestBodyTooLarge.Error()
	}
	if e.actual > 0 {
		return fmt.Sprintf(
			"request body too large: received %s, the configured limit is %s",
			formatRelayBodyBytes(e.actual),
			formatRelayBodyBytes(e.limit),
		)
	}
	return fmt.Sprintf("request body too large: the configured limit is %s", formatRelayBodyBytes(e.limit))
}

// Is keeps errors.Is(err, errRelayRequestBodyTooLarge) working for every
// oversized-body path (Content-Length precheck and MaxBytesReader fallback).
func (e *relayRequestBodyTooLargeError) Is(target error) bool {
	return target == errRelayRequestBodyTooLarge
}

func formatRelayBodyBytes(size int64) string {
	if size < 0 {
		return "unknown"
	}
	const (
		kib = int64(1024)
		mib = int64(1024 * 1024)
		gib = int64(1024 * 1024 * 1024)
	)
	switch {
	case size >= gib:
		return fmt.Sprintf("%.1f GiB (%d bytes)", float64(size)/float64(gib), size)
	case size >= mib:
		return fmt.Sprintf("%.1f MiB (%d bytes)", float64(size)/float64(mib), size)
	case size >= kib:
		return fmt.Sprintf("%.1f KiB (%d bytes)", float64(size)/float64(kib), size)
	default:
		return fmt.Sprintf("%d bytes", size)
	}
}

func getMaxRelayJSONBodyBytes() int64 {
	if v := conf.AppConfig.Relay.MaxJSONBodyBytes; v > 0 {
		return v
	}
	return 64 << 20
}

func getMaxRelayMultipartBodyBytes() int64 {
	if v := conf.AppConfig.Relay.MaxMultipartBodyBytes; v > 0 {
		return v
	}
	return 64 << 20
}

// precheckRelayRequestBodySize rejects oversized bodies before a single byte is
// read. A request whose declared size already exceeds the limit can never
// succeed, so reading it only wastes memory and CPU (and used to leave no
// trace at all when the client gave up first).
func precheckRelayRequestBodySize(c *gin.Context, limit int64) error {
	if c == nil || c.Request == nil || limit <= 0 {
		return nil
	}
	if c.Request.ContentLength > limit {
		return &relayRequestBodyTooLargeError{actual: c.Request.ContentLength, limit: limit}
	}
	return nil
}

// readRelayRequestBody is the single entry point relay body readers should use:
// Content-Length precheck first, MaxBytesReader as the chunked-stream fallback.
func readRelayRequestBody(c *gin.Context, limit int64) ([]byte, error) {
	if err := precheckRelayRequestBodySize(c, limit); err != nil {
		return nil, err
	}
	return readLimitedRequestBody(c, limit)
}

func readLimitedRequestBody(c *gin.Context, limit int64) ([]byte, error) {
	if c != nil && c.Request != nil && c.Request.Body != nil {
		limitRequestBody(c, limit)
		select {
		case <-c.Request.Context().Done():
			// Close the request body so a blocked upstream read cannot
			// keep this goroutine waiting after the client went away.
			_ = c.Request.Body.Close()
			return nil, normalizeRelayRequestBodyError(c.Request.Context().Err())
		default:
		}
	}
	body, err := readContextAware(c)
	if err != nil {
		return nil, normalizeRelayRequestBodyError(err)
	}
	return body, nil
}

// readContextAware reads the body while honoring request cancellation. The
// read runs on its own goroutine and the body is closed exactly once by the
// winner so a blocked reader cannot wedge the request.
func readContextAware(c *gin.Context) ([]byte, error) {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return nil, nil
	}
	type readOutcome struct {
		body []byte
		err  error
	}
	done := make(chan readOutcome, 1)
	ctx := c.Request.Context()
	go func() {
		body, err := io.ReadAll(c.Request.Body)
		done <- readOutcome{body: body, err: err}
	}()
	// Prefer a completed read when completion and cancellation are observed at
	// the same time. A successfully read body must not be discarded solely
	// because the select saw ctx.Done first.
	select {
	case result := <-done:
		return result.body, result.err
	default:
	}
	select {
	case result := <-done:
		return result.body, result.err
	case <-ctx.Done():
		// Recheck completion before closing. This makes cancellation deterministic
		// when both channels became ready between the first and second select.
		select {
		case result := <-done:
			return result.body, result.err
		default:
		}
		_ = c.Request.Body.Close()
		return nil, ctx.Err()
	}
}

func limitRequestBody(c *gin.Context, limit int64) {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
}

func normalizeRelayRequestBodyError(err error) error {
	if err == nil {
		return nil
	}

	var tooLargeErr *relayRequestBodyTooLargeError
	if errors.As(err, &tooLargeErr) {
		return err
	}
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return &relayRequestBodyTooLargeError{limit: maxBytesErr.Limit}
	}
	return err
}

func relayRequestBodyErrorStatus(err error) int {
	if errors.Is(err, errRelayRequestBodyTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

// relayRequestBodyTooLargeSizes extracts the size evidence from a rejection.
// actual is 0 when the client streamed the body without a Content-Length.
func relayRequestBodyTooLargeSizes(err error) (actual int64, limit int64) {
	var tooLargeErr *relayRequestBodyTooLargeError
	if errors.As(err, &tooLargeErr) {
		return tooLargeErr.actual, tooLargeErr.limit
	}
	return 0, 0
}

// rejectRelayRequestBody writes the terminal response for a failed body read.
// Oversized requests additionally get an error-log entry (never the body
// itself), so "big request got stuck and produced no log anywhere" becomes
// directly diagnosable from the management UI.
func rejectRelayRequestBody(c *gin.Context, err error) {
	// Once the client has gone away there is no peer to receive a 400. More
	// importantly, writing one can make upper layers classify a client
	// cancellation as a written 4xx and pollute retry/failure accounting.
	if c != nil && c.Request != nil && isClientDisconnected(c.Request.Context()) {
		return
	}
	status := relayRequestBodyErrorStatus(err)
	resp.Error(c, status, err.Error())
	if status != http.StatusRequestEntityTooLarge {
		return
	}
	writeRelayErrorLog(c, relayErrorLogLevelError, describeRelayOversizedBody(c, err))
}

func describeRelayOversizedBody(c *gin.Context, err error) string {
	actual, limit := relayRequestBodyTooLargeSizes(err)
	path := ""
	endpoint := ""
	apiKeyID := 0
	if c != nil {
		apiKeyID = c.GetInt("api_key_id")
		endpoint = c.FullPath()
		if c.Request != nil && c.Request.URL != nil {
			path = c.Request.URL.Path
		}
	}
	return fmt.Sprintf(
		"rejected oversized relay request body (413): endpoint=%s path=%s api_key_id=%d limit=%s actual=%s",
		endpoint,
		path,
		apiKeyID,
		formatRelayBodyBytes(limit),
		formatRelayBodyBytes(actual),
	)
}
