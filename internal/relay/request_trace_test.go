package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/op/setting"
)

func TestRelayRequestTraceCapturesFourBoundaries(t *testing.T) {
	inboundRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"client-model"}`))
	inboundRequest.Header.Set("Content-Type", "application/json")
	trace := newRelayRequestTrace(inboundRequest, true)
	t.Cleanup(trace.close)
	trace.wrapClientIngress(inboundRequest)
	replayedBody, err := io.ReadAll(inboundRequest.Body)
	if err != nil {
		t.Fatalf("read replayed inbound body: %v", err)
	}
	if string(replayedBody) != `{"model":"client-model"}` {
		t.Fatalf("replayed inbound body = %q", replayedBody)
	}

	upstreamRequest := httptest.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", strings.NewReader(`{"model":"upstream-model"}`))
	upstreamRequest.Header.Set("Content-Type", "application/json")
	trace.wrapUpstreamRequest(1, "chat", upstreamRequest)
	if _, err := io.ReadAll(upstreamRequest.Body); err != nil {
		t.Fatalf("read wrapped upstream request: %v", err)
	}

	upstreamResponse := &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(`{"choices":[{"text":"upstream"}]}`)),
		ContentLength: -1,
	}
	trace.wrapUpstreamResponse(1, "chat", upstreamResponse, true)
	if _, err := io.ReadAll(upstreamResponse.Body); err != nil {
		t.Fatalf("read wrapped upstream response: %v", err)
	}

	clientPayload := []byte(`{"choices":[{"message":{"content":"client"}}]}`)
	trace.recordClientWrite(clientPayload, len(clientPayload), nil)
	trace.completeClientEgress(http.StatusOK, http.Header{"Content-Type": []string{"application/json"}})

	contents := trace.contents()
	expectedPayloads := map[string]string{
		model.RelayLogBoundaryClientIngress:    `{"model":"client-model"}`,
		model.RelayLogBoundaryUpstreamRequest:  `{"model":"upstream-model"}`,
		model.RelayLogBoundaryUpstreamResponse: `{"choices":[{"text":"upstream"}]}`,
		model.RelayLogBoundaryClientEgress:     string(clientPayload),
	}
	bodyCount := 0
	for _, content := range contents {
		if content.Kind != "body" {
			continue
		}
		bodyCount++
		if content.State != model.RelayLogContentStateReady || !content.Complete {
			t.Fatalf("boundary %s state=%q complete=%t", content.Boundary, content.State, content.Complete)
		}
		if string(content.Data) != expectedPayloads[content.Boundary] {
			t.Fatalf("boundary %s payload = %q", content.Boundary, content.Data)
		}
	}
	if bodyCount != 4 {
		t.Fatalf("captured body boundary count = %d, want 4", bodyCount)
	}
}

func TestRelayRequestTraceExtractsMultipartAttachmentEntities(t *testing.T) {
	var requestBody bytes.Buffer
	multipartWriter := multipart.NewWriter(&requestBody)
	if err := multipartWriter.WriteField("model", "whisper-1"); err != nil {
		t.Fatalf("write model field: %v", err)
	}
	filePart, err := multipartWriter.CreateFormFile("file", "voice.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := filePart.Write([]byte("voice payload")); err != nil {
		t.Fatalf("write file payload: %v", err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(requestBody.Bytes()))
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	trace := newRelayRequestTrace(request, true)
	t.Cleanup(trace.close)
	trace.wrapClientIngress(request)
	if _, err := io.ReadAll(request.Body); err != nil {
		t.Fatalf("read wrapped multipart request: %v", err)
	}
	trace.completeClientEgress(http.StatusUnauthorized, http.Header{"Content-Type": []string{"application/json"}})

	contents := trace.contents()
	var foundFile bool
	var foundModelField bool
	for _, content := range contents {
		if content.Kind != "attachment" {
			continue
		}
		if content.FieldName == "file" && content.FileName == "voice.txt" && string(content.Data) == "voice payload" {
			foundFile = true
		}
		if content.FieldName == "model" && string(content.Data) == "whisper-1" {
			foundModelField = true
		}
	}
	if !foundFile {
		t.Fatal("multipart file entity was not captured")
	}
	if !foundModelField {
		t.Fatal("multipart model field was not captured")
	}
}

func TestRelayRequestTraceMiddlewareLogsRequestRejectedBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	databasePath := filepath.Join(t.TempDir(), "trace-middleware.db")
	if err := db.InitDB("sqlite", databasePath, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	if err := db.InitLogDB("", "", false); err != nil {
		t.Fatalf("InitLogDB failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := setting.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache failed: %v", err)
	}
	if err := setting.SetString(model.SettingKeyRelayLogKeepEnabled, "true"); err != nil {
		t.Fatalf("enable relay logs: %v", err)
	}
	if err := setting.SetString(model.SettingKeyRelayLogContentEnabled, "true"); err != nil {
		t.Fatalf("enable relay log content: %v", err)
	}
	t.Cleanup(relaylog.SetCacheForTest(nil))

	engine := gin.New()
	engine.Use(RelayRequestTraceMiddleware())
	engine.POST("/v1/chat/completions", func(ginContext *gin.Context) {
		ginContext.JSON(http.StatusUnauthorized, gin.H{"error": "invalid key"})
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"rejected-model","messages":[]}`))
	request.Header.Set("Content-Type", "application/json")
	responseRecorder := httptest.NewRecorder()
	engine.ServeHTTP(responseRecorder, request)
	if responseRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", responseRecorder.Code)
	}

	logs, _ := relaylog.GetCacheAndLock()
	if len(logs) != 1 {
		t.Fatalf("cached logs = %d, want 1", len(logs))
	}
	relayLog := logs[0]
	if relayLog.RequestModelName != "" {
		t.Fatalf("RequestModelName = %q, want empty because auth did not consume the body", relayLog.RequestModelName)
	}
	if relayLog.HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("HTTPStatus = %d, want 401", relayLog.HTTPStatus)
	}
	if relayLog.ClientWriteBytes == 0 || !relayLog.ClientWriteComplete {
		t.Fatalf("client write metadata = bytes:%d complete:%t", relayLog.ClientWriteBytes, relayLog.ClientWriteComplete)
	}
	for _, content := range relayLog.Contents {
		if content.Boundary == model.RelayLogBoundaryUpstreamRequest || content.Boundary == model.RelayLogBoundaryUpstreamResponse {
			t.Fatalf("rejected request invented upstream boundary: %#v", content)
		}
		if content.Boundary == model.RelayLogBoundaryClientIngress && content.Kind == "body" {
			if content.Complete || content.Error == "" {
				t.Fatalf("unconsumed auth-rejected body must be partial with a reason: %#v", content)
			}
		}
	}
}

func TestRelayRequestTraceRedactsCredentialsBeforePersistence(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions?api_key=query-secret&trace=visible",
		strings.NewReader(`{"model":"test","api_key":"body-secret","nested":{"access_token":"nested-secret"},"max_tokens":10}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer header-secret")
	request.Header.Set("Cookie", "session=header-secret")
	request.Header.Set("X-Trace", "visible")

	trace := newRelayRequestTrace(request, true)
	t.Cleanup(trace.close)
	trace.wrapClientIngress(request)
	if _, err := io.ReadAll(request.Body); err != nil {
		t.Fatalf("read wrapped ingress: %v", err)
	}
	trace.completeClientEgress(http.StatusOK, http.Header{"Content-Type": []string{"application/json"}})

	var ingressBody model.RelayLogCapturedContent
	var ingressMetadata model.RelayLogCapturedContent
	for _, content := range trace.contents() {
		if content.Boundary != model.RelayLogBoundaryClientIngress {
			continue
		}
		switch content.Kind {
		case "body":
			ingressBody = content
		case "http_metadata":
			ingressMetadata = content
		}
	}
	if ingressBody.State != model.RelayLogContentStateReady {
		t.Fatalf("ingress body state = %q", ingressBody.State)
	}
	if strings.Contains(string(ingressBody.Data), "body-secret") || strings.Contains(string(ingressBody.Data), "nested-secret") {
		t.Fatalf("credential leaked in captured body: %s", ingressBody.Data)
	}
	var sanitizedBody map[string]any
	if err := json.Unmarshal(ingressBody.Data, &sanitizedBody); err != nil {
		t.Fatalf("decode sanitized body: %v", err)
	}
	if sanitizedBody["api_key"] != "<redacted>" {
		t.Fatalf("api_key = %#v, want redacted", sanitizedBody["api_key"])
	}
	nested, _ := sanitizedBody["nested"].(map[string]any)
	if nested["access_token"] != "<redacted>" {
		t.Fatalf("nested access_token = %#v, want redacted", nested["access_token"])
	}
	if sanitizedBody["max_tokens"] != float64(10) {
		t.Fatalf("max_tokens was incorrectly redacted: %#v", sanitizedBody["max_tokens"])
	}
	metadataText := string(ingressMetadata.Data)
	for _, secret := range []string{"query-secret", "header-secret"} {
		if strings.Contains(metadataText, secret) {
			t.Fatalf("credential %q leaked in HTTP metadata: %s", secret, metadataText)
		}
	}
	if !strings.Contains(metadataText, "visible") || !strings.Contains(metadataText, "redacted") {
		t.Fatalf("sanitized metadata lost safe fields or redaction marker: %s", metadataText)
	}
}

func TestRelayRequestTraceRedactsCredentialMultipartFields(t *testing.T) {
	var requestBody bytes.Buffer
	multipartWriter := multipart.NewWriter(&requestBody)
	if err := multipartWriter.WriteField("model", "whisper-1"); err != nil {
		t.Fatalf("write model field: %v", err)
	}
	if err := multipartWriter.WriteField("api_key", "multipart-secret"); err != nil {
		t.Fatalf("write credential field: %v", err)
	}
	filePart, err := multipartWriter.CreateFormFile("file", "voice.txt")
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := filePart.Write([]byte("voice payload")); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(requestBody.Bytes()))
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	trace := newRelayRequestTrace(request, true)
	t.Cleanup(trace.close)
	trace.wrapClientIngress(request)
	if _, err := io.ReadAll(request.Body); err != nil {
		t.Fatalf("read multipart body: %v", err)
	}
	trace.completeClientEgress(http.StatusOK, http.Header{})

	foundModel := false
	foundFile := false
	foundCredentialMarker := false
	for _, content := range trace.contents() {
		if content.Boundary != model.RelayLogBoundaryClientIngress {
			continue
		}
		if content.Kind == "body" {
			if content.State != model.RelayLogContentStateUnavailable || content.Data != nil {
				t.Fatalf("raw credential-bearing multipart body must be unavailable: %#v", content)
			}
		}
		if content.FieldName == "model" && string(content.Data) == "whisper-1" {
			foundModel = true
		}
		if content.FieldName == "file" && string(content.Data) == "voice payload" {
			foundFile = true
		}
		if content.FieldName == "api_key" {
			foundCredentialMarker = content.State == model.RelayLogContentStateDisabled && content.Data == nil
		}
		if strings.Contains(string(content.Data), "multipart-secret") {
			t.Fatal("multipart credential leaked into persisted content")
		}
	}
	if !foundModel || !foundFile || !foundCredentialMarker {
		t.Fatalf("multipart entities: model=%t file=%t credential_marker=%t", foundModel, foundFile, foundCredentialMarker)
	}
}

func TestRelayRequestTraceRawSSECaptureIsChannelScoped(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test"}`))
	disabledTrace := newRelayRequestTrace(request, true)
	t.Cleanup(disabledTrace.close)
	disabledResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: disabled\n\n")),
	}
	disabledOriginalBody := disabledResponse.Body
	disabledTrace.wrapUpstreamResponse(1, "chat", disabledResponse, false)
	if disabledResponse.Body != disabledOriginalBody {
		t.Fatal("disabled raw SSE capture must not wrap or consume the upstream body")
	}
	disabledTrace.completeClientEgress(http.StatusOK, http.Header{})
	foundDisabled := false
	for _, content := range disabledTrace.contents() {
		if content.Boundary == model.RelayLogBoundaryUpstreamResponse && content.Kind == "body" {
			foundDisabled = content.State == model.RelayLogContentStateDisabled && content.Data == nil
		}
	}
	if !foundDisabled {
		t.Fatal("disabled raw SSE capture was not represented truthfully")
	}

	enabledTrace := newRelayRequestTrace(request, true)
	t.Cleanup(enabledTrace.close)
	enabledResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: enabled\n\n")),
	}
	enabledTrace.wrapUpstreamResponse(1, "chat", enabledResponse, true)
	if _, err := io.ReadAll(enabledResponse.Body); err != nil {
		t.Fatalf("read enabled raw SSE response: %v", err)
	}
	enabledTrace.completeClientEgress(http.StatusOK, http.Header{})
	foundReady := false
	for _, content := range enabledTrace.contents() {
		if content.Boundary == model.RelayLogBoundaryUpstreamResponse && content.Kind == "body" {
			foundReady = content.State == model.RelayLogContentStateReady && content.Complete && string(content.Data) == "data: enabled\n\n"
		}
	}
	if !foundReady {
		t.Fatal("enabled raw SSE payload was not captured")
	}
}

func TestRelayRequestTraceRecordsShortWriterAcceptanceAndCommittedHeaders(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", http.NoBody)
	trace := newRelayRequestTrace(request, true)
	t.Cleanup(trace.close)

	committedHeaders := http.Header{"Content-Type": []string{"application/json"}, "X-Committed": []string{"yes"}}
	trace.commitClientResponse(http.StatusAccepted, committedHeaders)
	trace.recordClientWrite([]byte("abcdef"), 3, nil)
	trace.completeClientEgress(http.StatusInternalServerError, http.Header{"X-Late": []string{"must-not-replace-committed"}})

	_, statusCode, acceptedBytes, complete, writeError := trace.summary()
	if statusCode != http.StatusAccepted || acceptedBytes != 3 || complete || writeError != io.ErrShortWrite.Error() {
		t.Fatalf("writer summary = status:%d bytes:%d complete:%t error:%q", statusCode, acceptedBytes, complete, writeError)
	}
	for _, content := range trace.contents() {
		if content.Boundary != model.RelayLogBoundaryClientEgress || content.Kind != "http_metadata" {
			continue
		}
		metadataText := string(content.Data)
		if !strings.Contains(metadataText, "X-Committed") || strings.Contains(metadataText, "X-Late") {
			t.Fatalf("client metadata does not reflect committed headers: %s", metadataText)
		}
		return
	}
	t.Fatal("client egress HTTP metadata was not captured")
}

func TestRelayBoundaryCaptureBudgetIsFailOpen(t *testing.T) {
	originalBudget := relayTraceBoundaryMaxBytes
	relayTraceBoundaryMaxBytes = 4
	t.Cleanup(func() { relayTraceBoundaryMaxBytes = originalBudget })

	capture := newRelayBoundaryCapture(model.RelayLogBoundaryClientIngress, 0, "application/octet-stream", "raw")
	t.Cleanup(capture.closeAndRemove)
	writtenBytes, err := capture.Write([]byte("123456"))
	if err != nil || writtenBytes != 6 {
		t.Fatalf("capture write changed relay semantics: bytes=%d err=%v", writtenBytes, err)
	}
	content := capture.snapshot(true)
	if content.State != model.RelayLogContentStateUnavailable || content.Data != nil || content.CapturedBytes != 6 {
		t.Fatalf("bounded capture = %#v", content)
	}
}
