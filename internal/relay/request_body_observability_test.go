package relay

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/db"
	dbmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
)

// countingBodyReader records whether the relay read the request body at all.
type countingBodyReader struct {
	data  []byte
	reads int
}

func (reader *countingBodyReader) Read(payload []byte) (int, error) {
	reader.reads++
	if len(reader.data) == 0 {
		return 0, io.EOF
	}
	readBytes := copy(payload, reader.data)
	reader.data = reader.data[readBytes:]
	return readBytes, nil
}

func (reader *countingBodyReader) Close() error { return nil }
func initializeRelayBodyObservabilityTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", t.TempDir()+"/relay-body-observability.db", false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	gin.SetMode(gin.TestMode)
}

func countBackendErrorLogs(t *testing.T) int64 {
	t.Helper()
	var total int64
	if err := db.GetDB().Model(&dbmodel.ErrorLog{}).Where("source = ?", "backend").Count(&total).Error; err != nil {
		t.Fatalf("count error logs: %v", err)
	}
	return total
}

func withJSONBodyLimit(t *testing.T, limit int64) {
	t.Helper()
	original := conf.AppConfig.Relay.MaxJSONBodyBytes
	conf.AppConfig.Relay.MaxJSONBodyBytes = limit
	t.Cleanup(func() { conf.AppConfig.Relay.MaxJSONBodyBytes = original })
}

func TestParseRequestRejectsDeclaredOversizeBodyWithoutReading(t *testing.T) {
	initializeRelayBodyObservabilityTestDB(t)
	withJSONBodyLimit(t, 1024)

	body := &countingBodyReader{data: []byte(strings.Repeat("a", 4096))}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Body = body
	ctx.Request.ContentLength = int64(len(body.data))
	ctx.Request.Header.Set("Content-Type", "application/json")

	_, _, err := parseRequest(inbound.InboundTypeOpenAIChat, ctx)
	if !errors.Is(err, errRelayRequestBodyTooLarge) {
		t.Fatalf("parseRequest() error = %v, want %v", err, errRelayRequestBodyTooLarge)
	}
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
	}
	if body.reads != 0 {
		t.Fatalf("body reads = %d, want 0 (declared size must be rejected before reading)", body.reads)
	}
	responseText := recorder.Body.String()
	if !strings.Contains(responseText, "4096 bytes") || !strings.Contains(responseText, "1024 bytes") {
		t.Fatalf("response body = %q, want actual and configured sizes", responseText)
	}
	if got := countBackendErrorLogs(t); got != 1 {
		t.Fatalf("backend error logs = %d, want 1", got)
	}
	var entry dbmodel.ErrorLog
	if err := db.GetDB().Where("source = ?", "backend").First(&entry).Error; err != nil {
		t.Fatalf("load error log: %v", err)
	}
	if entry.Level != relayErrorLogLevelError {
		t.Fatalf("error log level = %q, want %q", entry.Level, relayErrorLogLevelError)
	}
	for _, fragment := range []string{"413", "limit=1.0 KiB (1024 bytes)", "actual=4.0 KiB (4096 bytes)", "/v1/chat/completions"} {
		if !strings.Contains(entry.Message, fragment) {
			t.Fatalf("error log message = %q, want fragment %q", entry.Message, fragment)
		}
	}
}

func TestParseRequestChunkedOversizeBodyGetsSame413AndErrorLog(t *testing.T) {
	initializeRelayBodyObservabilityTestDB(t)
	withJSONBodyLimit(t, 1024)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(strings.Repeat("a", 4096)))
	// Chunked transfer encoding: no declared length, the stream itself trips
	// the MaxBytesReader fallback.
	ctx.Request.ContentLength = -1
	ctx.Request.Header.Set("Content-Type", "application/json")

	_, _, err := parseRequest(inbound.InboundTypeOpenAIChat, ctx)
	if !errors.Is(err, errRelayRequestBodyTooLarge) {
		t.Fatalf("parseRequest() error = %v, want %v", err, errRelayRequestBodyTooLarge)
	}
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
	}
	if !strings.Contains(recorder.Body.String(), "1024 bytes") {
		t.Fatalf("response body = %q, want configured limit in message", recorder.Body.String())
	}
	if got := countBackendErrorLogs(t); got != 1 {
		t.Fatalf("backend error logs = %d, want 1", got)
	}
}

func TestReadRelayRequestBodyAllowsBodyEqualToLimit(t *testing.T) {
	withJSONBodyLimit(t, 64)

	payload := strings.Repeat("a", 64)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))

	body, err := readRelayRequestBody(ctx, 64)
	if err != nil {
		t.Fatalf("readRelayRequestBody() error = %v, want nil", err)
	}
	if string(body) != payload {
		t.Fatalf("readRelayRequestBody() body length = %d, want %d", len(body), len(payload))
	}
}

func TestExtractModelFromJSONRejectsDeclaredOversizeBodyWithoutReading(t *testing.T) {
	initializeRelayBodyObservabilityTestDB(t)
	withJSONBodyLimit(t, 32)

	body := &countingBodyReader{data: []byte(strings.Repeat("a", 512))}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	ctx.Request.Body = body
	ctx.Request.ContentLength = int64(len(body.data))
	ctx.Request.Header.Set("Content-Type", "application/json")

	_, _, _, err := extractModelFromJSON(ctx)
	if !errors.Is(err, errRelayRequestBodyTooLarge) {
		t.Fatalf("extractModelFromJSON() error = %v, want %v", err, errRelayRequestBodyTooLarge)
	}
	if body.reads != 0 {
		t.Fatalf("body reads = %d, want 0 (declared size must be rejected before reading)", body.reads)
	}
	if got := countBackendErrorLogs(t); got != 0 {
		t.Fatalf("backend error logs = %d, want 0 before the handler responds", got)
	}
}

func TestMediaHandlerRejectsDeclaredOversizeBodyWith413AndErrorLog(t *testing.T) {
	initializeRelayBodyObservabilityTestDB(t)
	withJSONBodyLimit(t, 1024)

	body := &countingBodyReader{data: []byte(strings.Repeat("a", 4096))}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	ctx.Request.Body = body
	ctx.Request.ContentLength = int64(len(body.data))
	ctx.Request.Header.Set("Content-Type", "application/json")

	MediaHandler(MediaEndpointImageGeneration, ctx)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
	if body.reads != 0 {
		t.Fatalf("body reads = %d, want 0 (declared size must be rejected before reading)", body.reads)
	}
	if got := countBackendErrorLogs(t); got != 1 {
		t.Fatalf("backend error logs = %d, want 1", got)
	}
	var entry dbmodel.ErrorLog
	if err := db.GetDB().Where("source = ?", "backend").First(&entry).Error; err != nil {
		t.Fatalf("load error log: %v", err)
	}
	if !strings.Contains(entry.Message, "/v1/images/generations") {
		t.Fatalf("error log message = %q, want the request path", entry.Message)
	}
}
