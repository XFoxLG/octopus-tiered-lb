package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/apikey"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/utils/log"
)

const (
	relayRequestTraceContextKey = "octopus_relay_request_trace"
	relayTraceBundleMaxBytes    = int64(64 << 20)
)

var relayTraceBoundaryMaxBytes = int64(64 << 20)

var relayTraceFinalizationSemaphore = make(chan struct{}, 1)

type relayBoundaryCapture struct {
	mutex          sync.Mutex
	boundary       string
	attemptNumber  int
	slot           int
	kind           string
	fieldName      string
	fileName       string
	contentType    string
	protocol       string
	httpStatus     int
	temporaryFile  *os.File
	temporaryPath  string
	observedBytes  int64
	complete       bool
	captureError   string
	disabledReason string
}

func newRelayBoundaryCapture(boundary string, attemptNumber int, contentType string, protocol string) *relayBoundaryCapture {
	return &relayBoundaryCapture{
		boundary:      boundary,
		attemptNumber: attemptNumber,
		kind:          "body",
		contentType:   contentType,
		protocol:      protocol,
	}
}

func (capture *relayBoundaryCapture) ensureTemporaryFileLocked() error {
	if capture.temporaryFile != nil {
		return nil
	}
	temporaryFile, err := os.CreateTemp("", "octopus-relay-log-*")
	if err != nil {
		capture.captureError = fmt.Sprintf("create relay capture file: %v", err)
		return err
	}
	capture.temporaryFile = temporaryFile
	capture.temporaryPath = temporaryFile.Name()
	return nil
}

// Write is deliberately fail-open. Capture failures are recorded in metadata,
// but the caller still receives a successful write count so observability can
// never change relay behavior.
func (capture *relayBoundaryCapture) Write(payload []byte) (int, error) {
	if capture == nil || len(payload) == 0 {
		return len(payload), nil
	}
	capture.mutex.Lock()
	defer capture.mutex.Unlock()
	previousObservedBytes := capture.observedBytes
	capture.observedBytes += int64(len(payload))
	if capture.captureError != "" {
		return len(payload), nil
	}
	remainingCaptureBytes := relayTraceBoundaryMaxBytes - previousObservedBytes
	if remainingCaptureBytes <= 0 {
		capture.captureError = "boundary exceeds the bounded capture budget"
		return len(payload), nil
	}
	if err := capture.ensureTemporaryFileLocked(); err != nil {
		return len(payload), nil
	}
	payloadToCapture := payload
	if int64(len(payloadToCapture)) > remainingCaptureBytes {
		payloadToCapture = payloadToCapture[:remainingCaptureBytes]
		capture.captureError = "boundary exceeds the bounded capture budget"
	}
	if _, err := capture.temporaryFile.Write(payloadToCapture); err != nil {
		capture.captureError = fmt.Sprintf("write relay capture file: %v", err)
	}
	return len(payload), nil
}

func (capture *relayBoundaryCapture) markComplete() {
	if capture == nil {
		return
	}
	capture.mutex.Lock()
	capture.complete = true
	capture.mutex.Unlock()
}

func (capture *relayBoundaryCapture) markReadError(err error) {
	if capture == nil || err == nil || errors.Is(err, io.EOF) {
		return
	}
	capture.mutex.Lock()
	if capture.captureError == "" {
		capture.captureError = fmt.Sprintf("boundary read failed: %v", err)
	}
	capture.mutex.Unlock()
}

func (capture *relayBoundaryCapture) setHTTPStatus(statusCode int) {
	if capture == nil {
		return
	}
	capture.mutex.Lock()
	capture.httpStatus = statusCode
	capture.mutex.Unlock()
}

func (capture *relayBoundaryCapture) size() int64 {
	if capture == nil {
		return 0
	}
	capture.mutex.Lock()
	defer capture.mutex.Unlock()
	return capture.observedBytes
}

func (capture *relayBoundaryCapture) snapshot(allowPayload bool) model.RelayLogCapturedContent {
	if capture == nil {
		return model.RelayLogCapturedContent{}
	}
	capture.mutex.Lock()
	defer capture.mutex.Unlock()

	content := model.RelayLogCapturedContent{RelayLogContentRef: model.RelayLogContentRef{
		AttemptNum:    capture.attemptNumber,
		Boundary:      capture.boundary,
		Slot:          capture.slot,
		Kind:          capture.kind,
		Protocol:      capture.protocol,
		FieldName:     capture.fieldName,
		FileName:      capture.fileName,
		ContentType:   capture.contentType,
		HTTPStatus:    capture.httpStatus,
		Complete:      capture.complete,
		CapturedBytes: capture.observedBytes,
		CreatedAt:     time.Now().Unix(),
	}}
	if capture.disabledReason != "" {
		content.State = model.RelayLogContentStateDisabled
		content.Error = capture.disabledReason
		return content
	}

	if !allowPayload {
		content.State = model.RelayLogContentStateUnavailable
		content.Error = "complete boundary bundle exceeds memory safety budget"
		return content
	}

	if capture.observedBytes == 0 {
		content.State = model.RelayLogContentStateReady
		content.Data = make([]byte, 0)
		if capture.captureError != "" {
			content.Error = capture.captureError
		} else if !capture.complete {
			content.Error = "boundary ended before EOF or handler completion"
		}
		return content
	}
	if capture.temporaryPath == "" {
		content.State = model.RelayLogContentStateUnavailable
		content.Error = "capture file is missing"
		return content
	}
	payload, err := os.ReadFile(capture.temporaryPath)
	if err != nil {
		content.State = model.RelayLogContentStateUnavailable
		content.Error = fmt.Sprintf("read relay capture file: %v", err)
		return content
	}
	if int64(len(payload)) != capture.observedBytes {
		content.State = model.RelayLogContentStateUnavailable
		content.Error = fmt.Sprintf("captured %d of %d boundary bytes", len(payload), capture.observedBytes)
		return content
	}
	content.State = model.RelayLogContentStateReady
	content.Data = payload
	if capture.captureError != "" {
		content.Error = capture.captureError
	} else if !capture.complete {
		content.Error = "boundary ended before EOF or handler completion"
	}
	return content
}

func (capture *relayBoundaryCapture) closeAndRemove() {
	if capture == nil {
		return
	}
	capture.mutex.Lock()
	if capture.temporaryFile != nil {
		_ = capture.temporaryFile.Close()
		capture.temporaryFile = nil
	}
	if capture.temporaryPath != "" {
		_ = os.Remove(capture.temporaryPath)
		capture.temporaryPath = ""
	}
	capture.mutex.Unlock()
}

type relayAttemptBoundaryCapture struct {
	request     *relayBoundaryCapture
	response    *relayBoundaryCapture
	sendStarted bool
}

type relayRequestTrace struct {
	mutex                   sync.Mutex
	id                      string
	startedAt               time.Time
	requestPath             string
	requestContentType      string
	captureContent          bool
	clientIngress           *relayBoundaryCapture
	clientEgress            *relayBoundaryCapture
	attempts                map[int]*relayAttemptBoundaryCapture
	logPersisted            bool
	relayLogID              int64
	clientWriteBytes        int64
	clientWriteError        string
	clientWriteComplete     bool
	httpStatus              int
	clientResponseHeaders   http.Header
	clientResponseCommitted bool
	contentsOnce            sync.Once
	capturedContents        []model.RelayLogCapturedContent
	declaredContents        []model.RelayLogCapturedContent
	stagedRelayLog          *model.RelayLog
}

func newRelayRequestTrace(request *http.Request, captureContent bool) *relayRequestTrace {
	requestPath := ""
	requestContentType := ""
	if request != nil {
		requestContentType = request.Header.Get("Content-Type")
		if request.URL != nil {
			requestPath = request.URL.Path
		}
	}
	trace := &relayRequestTrace{
		id:                 uuid.NewString(),
		startedAt:          time.Now(),
		requestPath:        requestPath,
		requestContentType: requestContentType,
		captureContent:     captureContent,
		attempts:           make(map[int]*relayAttemptBoundaryCapture),
	}
	if captureContent {
		clientProtocol := relayTraceEndpointType(requestPath)
		trace.clientIngress = newRelayBoundaryCapture(model.RelayLogBoundaryClientIngress, 0, requestContentType, clientProtocol)
		trace.clientEgress = newRelayBoundaryCapture(model.RelayLogBoundaryClientEgress, 0, "", clientProtocol)
		trace.declaredContents = append(trace.declaredContents, buildHTTPMetadataContent(
			model.RelayLogBoundaryClientIngress,
			0,
			clientProtocol,
			request,
			nil,
		))
	}
	return trace
}

func (trace *relayRequestTrace) wrapClientIngress(request *http.Request) {
	if trace == nil || !trace.captureContent || trace.clientIngress == nil || request == nil || request.Body == nil {
		if trace != nil && trace.clientIngress != nil {
			trace.clientIngress.markComplete()
		}
		return
	}
	if request.Body == http.NoBody {
		trace.clientIngress.markComplete()
		return
	}
	request.Body = &relayCapturingReadCloser{
		source:         request.Body,
		capture:        trace.clientIngress,
		expectedLength: request.ContentLength,
	}
}

func (trace *relayRequestTrace) wrapUpstreamRequest(attemptNumber int, protocol string, request *http.Request) {
	if trace == nil || !trace.captureContent || request == nil {
		return
	}
	capture := newRelayBoundaryCapture(model.RelayLogBoundaryUpstreamRequest, attemptNumber, request.Header.Get("Content-Type"), protocol)
	trace.mutex.Lock()
	attemptCapture := trace.attempts[attemptNumber]
	if attemptCapture == nil {
		attemptCapture = &relayAttemptBoundaryCapture{}
		trace.attempts[attemptNumber] = attemptCapture
	}
	attemptCapture.request = capture
	trace.declaredContents = append(trace.declaredContents, buildHTTPMetadataContent(
		model.RelayLogBoundaryUpstreamRequest,
		attemptNumber,
		protocol,
		request,
		nil,
	))
	trace.mutex.Unlock()

	if request.Body == nil || request.Body == http.NoBody {
		capture.markComplete()
		return
	}
	request.Body = &relayCapturingReadCloser{
		source:         request.Body,
		capture:        capture,
		expectedLength: request.ContentLength,
	}
}

func (trace *relayRequestTrace) wrapUpstreamResponse(attemptNumber int, protocol string, response *http.Response, captureRawSSE bool) {
	if trace == nil || !trace.captureContent || response == nil {
		return
	}
	capture := newRelayBoundaryCapture(model.RelayLogBoundaryUpstreamResponse, attemptNumber, response.Header.Get("Content-Type"), protocol)
	capture.setHTTPStatus(response.StatusCode)
	trace.mutex.Lock()
	attemptCapture := trace.attempts[attemptNumber]
	if attemptCapture == nil {
		attemptCapture = &relayAttemptBoundaryCapture{}
		trace.attempts[attemptNumber] = attemptCapture
	}
	attemptCapture.response = capture
	trace.declaredContents = append(trace.declaredContents, buildHTTPMetadataContent(
		model.RelayLogBoundaryUpstreamResponse,
		attemptNumber,
		protocol,
		nil,
		response,
	))
	trace.mutex.Unlock()
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") && !captureRawSSE {
		capture.disabledReason = "provider-native SSE frame capture is disabled for this channel"
		capture.markComplete()
		return
	}

	if response.Body == nil || response.Body == http.NoBody {
		capture.markComplete()
		return
	}
	response.Body = &relayCapturingReadCloser{
		source:         response.Body,
		capture:        capture,
		expectedLength: response.ContentLength,
	}
}

func (trace *relayRequestTrace) markUpstreamSendStarted(attemptNumber int) {
	if trace == nil || !trace.captureContent {
		return
	}
	trace.mutex.Lock()
	attemptCapture := trace.attempts[attemptNumber]
	if attemptCapture == nil {
		attemptCapture = &relayAttemptBoundaryCapture{}
		trace.attempts[attemptNumber] = attemptCapture
	}
	attemptCapture.sendStarted = true
	trace.mutex.Unlock()
}

func (trace *relayRequestTrace) annotateAttempts(attempts []model.ChannelAttempt) []model.ChannelAttempt {
	if trace == nil || len(attempts) == 0 {
		return attempts
	}
	annotated := append([]model.ChannelAttempt(nil), attempts...)
	trace.mutex.Lock()
	defer trace.mutex.Unlock()
	for attemptIndex := range annotated {
		attempt := &annotated[attemptIndex]
		capture := trace.attempts[attempt.AttemptNum]
		if capture == nil {
			continue
		}
		attempt.SendStarted = capture.sendStarted
		if capture.request != nil {
			capture.request.mutex.Lock()
			attempt.RequestPrepared = true
			attempt.RequestBytes = capture.request.observedBytes
			attempt.RequestComplete = capture.request.complete
			capture.request.mutex.Unlock()
		}
		if capture.response != nil {
			capture.response.mutex.Lock()
			attempt.ResponseReceived = true
			attempt.ResponseBytes = capture.response.observedBytes
			attempt.ResponseComplete = capture.response.complete
			if attempt.HTTPStatus == 0 {
				attempt.HTTPStatus = capture.response.httpStatus
			}
			capture.response.mutex.Unlock()
		}
	}
	return annotated
}

func (trace *relayRequestTrace) recordClientWrite(payload []byte, writeCount int, writeErr error) {
	if trace == nil {
		return
	}
	if writeCount > len(payload) {
		writeCount = len(payload)
	}
	if writeCount < 0 {
		writeCount = 0
	}
	if trace.captureContent && trace.clientEgress != nil && writeCount > 0 {
		_, _ = trace.clientEgress.Write(payload[:writeCount])
	}
	trace.mutex.Lock()
	trace.clientWriteBytes += int64(writeCount)
	if writeCount < len(payload) && writeErr == nil {
		writeErr = io.ErrShortWrite
	}
	if writeErr != nil && trace.clientWriteError == "" {
		trace.clientWriteError = writeErr.Error()
	}
	trace.mutex.Unlock()
}

func (trace *relayRequestTrace) completeClientEgress(statusCode int, responseHeaders http.Header) {
	if trace == nil {
		return
	}
	trace.mutex.Lock()
	if trace.clientResponseCommitted {
		statusCode = trace.httpStatus
		responseHeaders = trace.clientResponseHeaders.Clone()
	} else {
		trace.httpStatus = statusCode
		trace.clientResponseHeaders = responseHeaders.Clone()
	}
	trace.httpStatus = statusCode
	trace.clientWriteComplete = trace.clientWriteError == ""
	if trace.captureContent {
		trace.declaredContents = append(trace.declaredContents, buildHTTPMetadataContent(
			model.RelayLogBoundaryClientEgress,
			0,
			relayTraceEndpointType(trace.requestPath),
			nil,
			&http.Response{StatusCode: statusCode, Header: responseHeaders.Clone()},
		))
	}
	trace.mutex.Unlock()
	if trace.clientEgress != nil {
		trace.clientEgress.mutex.Lock()
		trace.clientEgress.contentType = responseHeaders.Get("Content-Type")
		trace.clientEgress.mutex.Unlock()
		trace.clientEgress.setHTTPStatus(statusCode)
		trace.clientEgress.markComplete()
	}
}

func (trace *relayRequestTrace) commitClientResponse(statusCode int, responseHeaders http.Header) {
	if trace == nil {
		return
	}
	if statusCode <= 0 {
		statusCode = http.StatusOK
	}
	trace.mutex.Lock()
	if !trace.clientResponseCommitted {
		trace.clientResponseCommitted = true
		trace.httpStatus = statusCode
		trace.clientResponseHeaders = responseHeaders.Clone()
	}
	trace.mutex.Unlock()
}

func (trace *relayRequestTrace) summary() (traceID string, statusCode int, clientWriteBytes int64, clientWriteComplete bool, clientWriteError string) {
	if trace == nil {
		return "", 0, 0, false, ""
	}
	trace.mutex.Lock()
	defer trace.mutex.Unlock()
	return trace.id, trace.httpStatus, trace.clientWriteBytes, trace.clientWriteComplete, trace.clientWriteError
}

func (trace *relayRequestTrace) markLogPersisted(relayLogID int64) {
	if trace == nil {
		return
	}
	trace.mutex.Lock()
	trace.logPersisted = true
	trace.relayLogID = relayLogID
	trace.mutex.Unlock()
}

func (trace *relayRequestTrace) stageRelayLog(relayLog model.RelayLog) {
	if trace == nil {
		return
	}
	trace.mutex.Lock()
	staged := relayLog
	trace.stagedRelayLog = &staged
	trace.mutex.Unlock()
}

func (trace *relayRequestTrace) addDeclaredContent(content model.RelayLogCapturedContent) {
	if trace == nil || !trace.captureContent {
		return
	}
	trace.mutex.Lock()
	trace.declaredContents = append(trace.declaredContents, content)
	trace.mutex.Unlock()
}

func (trace *relayRequestTrace) addNormalizedJSON(boundary string, attemptNumber int, kind string, protocol string, payload []byte) {
	if trace == nil || !trace.captureContent || len(payload) == 0 {
		return
	}
	trace.addDeclaredContent(model.RelayLogCapturedContent{
		RelayLogContentRef: model.RelayLogContentRef{
			AttemptNum:    attemptNumber,
			Boundary:      boundary,
			Slot:          -2,
			Kind:          kind,
			Protocol:      protocol,
			ContentType:   "application/json",
			State:         model.RelayLogContentStateReady,
			Complete:      true,
			CapturedBytes: int64(len(payload)),
			CreatedAt:     time.Now().Unix(),
		},
		Data: append([]byte(nil), payload...),
	})
}

func (trace *relayRequestTrace) takeStagedRelayLog() *model.RelayLog {
	if trace == nil {
		return nil
	}
	trace.mutex.Lock()
	defer trace.mutex.Unlock()
	if trace.stagedRelayLog == nil {
		return nil
	}
	staged := *trace.stagedRelayLog
	trace.stagedRelayLog = nil
	return &staged
}

func (trace *relayRequestTrace) hasPersistedLog() bool {
	if trace == nil {
		return false
	}
	trace.mutex.Lock()
	defer trace.mutex.Unlock()
	return trace.logPersisted
}

func (trace *relayRequestTrace) contents() []model.RelayLogCapturedContent {
	if trace == nil || !trace.captureContent {
		return nil
	}
	trace.contentsOnce.Do(func() {
		relayTraceFinalizationSemaphore <- struct{}{}
		defer func() { <-relayTraceFinalizationSemaphore }()

		captures := trace.orderedCaptures()
		trace.mutex.Lock()
		declaredContents := append([]model.RelayLogCapturedContent(nil), trace.declaredContents...)
		trace.mutex.Unlock()
		var totalBytes int64
		for index := range declaredContents {
			totalBytes += int64(len(declaredContents[index].Data))
		}
		for _, capture := range captures {
			totalBytes += capture.size()
		}
		allowPayload := totalBytes <= relayTraceBundleMaxBytes

		contents := make([]model.RelayLogCapturedContent, 0, len(declaredContents)+len(captures))
		for declaredIndex := range declaredContents {
			sanitizeRelayLogCapturedContent(&declaredContents[declaredIndex])
			contents = append(contents, declaredContents[declaredIndex])
		}
		for _, capture := range captures {
			content := capture.snapshot(allowPayload)
			attachments := relayMultipartAttachmentContents(content)
			sanitizeRelayLogCapturedContent(&content)
			contents = append(contents, content)
			for attachmentIndex := range attachments {
				sanitizeRelayLogCapturedContent(&attachments[attachmentIndex])
				contents = append(contents, attachments[attachmentIndex])
			}
		}

		var finalPayloadBytes int64
		for index := range contents {
			finalPayloadBytes += int64(len(contents[index].Data))
		}
		if finalPayloadBytes > relayTraceBundleMaxBytes {
			for index := range contents {
				contents[index].Data = nil
				contents[index].Text = ""
				contents[index].BlobDigest = ""
				contents[index].State = model.RelayLogContentStateUnavailable
				contents[index].Error = "complete boundary and attachment bundle exceeds memory safety budget"
			}
		}
		trace.capturedContents = contents
	})
	return append([]model.RelayLogCapturedContent(nil), trace.capturedContents...)
}

func (trace *relayRequestTrace) orderedCaptures() []*relayBoundaryCapture {
	trace.mutex.Lock()
	defer trace.mutex.Unlock()
	captures := make([]*relayBoundaryCapture, 0, 2+len(trace.attempts)*2)
	if trace.clientIngress != nil {
		captures = append(captures, trace.clientIngress)
	}
	attemptNumbers := make([]int, 0, len(trace.attempts))
	for attemptNumber := range trace.attempts {
		attemptNumbers = append(attemptNumbers, attemptNumber)
	}
	sort.Ints(attemptNumbers)
	for _, attemptNumber := range attemptNumbers {
		attemptCapture := trace.attempts[attemptNumber]
		if attemptCapture.request != nil {
			captures = append(captures, attemptCapture.request)
		}
		if attemptCapture.response != nil {
			captures = append(captures, attemptCapture.response)
		}
	}
	if trace.clientEgress != nil {
		captures = append(captures, trace.clientEgress)
	}
	return captures
}

func (trace *relayRequestTrace) close() {
	if trace == nil {
		return
	}
	for _, capture := range trace.orderedCaptures() {
		capture.closeAndRemove()
	}
}

type relayCapturingReadCloser struct {
	source         io.ReadCloser
	capture        *relayBoundaryCapture
	expectedLength int64
}

func (reader *relayCapturingReadCloser) Read(payload []byte) (int, error) {
	readCount, readErr := reader.source.Read(payload)
	if readCount > 0 {
		_, _ = reader.capture.Write(payload[:readCount])
	}
	if errors.Is(readErr, io.EOF) || (reader.expectedLength >= 0 && reader.capture.size() >= reader.expectedLength) {
		reader.capture.markComplete()
	} else if readErr != nil {
		reader.capture.markReadError(readErr)
	}
	return readCount, readErr
}

func (reader *relayCapturingReadCloser) Close() error {
	return reader.source.Close()
}

type relayCaptureResponseWriter struct {
	gin.ResponseWriter
	trace *relayRequestTrace
}

func (writer *relayCaptureResponseWriter) WriteHeader(statusCode int) {
	wasCommitted := writer.ResponseWriter.Written()
	writer.ResponseWriter.WriteHeader(statusCode)
	if !wasCommitted {
		writer.trace.commitClientResponse(writer.ResponseWriter.Status(), writer.ResponseWriter.Header())
	}
}

func (writer *relayCaptureResponseWriter) Write(payload []byte) (int, error) {
	wasCommitted := writer.ResponseWriter.Written()
	writtenBytes, writeErr := writer.ResponseWriter.Write(payload)
	if !wasCommitted {
		writer.trace.commitClientResponse(writer.ResponseWriter.Status(), writer.ResponseWriter.Header())
	}
	writer.trace.recordClientWrite(payload, writtenBytes, writeErr)
	return writtenBytes, writeErr
}

func (writer *relayCaptureResponseWriter) WriteString(payload string) (int, error) {
	wasCommitted := writer.ResponseWriter.Written()
	writtenBytes, writeErr := writer.ResponseWriter.WriteString(payload)
	if !wasCommitted {
		writer.trace.commitClientResponse(writer.ResponseWriter.Status(), writer.ResponseWriter.Header())
	}
	writer.trace.recordClientWrite([]byte(payload), writtenBytes, writeErr)
	return writtenBytes, writeErr
}

// RelayRequestTraceMiddleware starts capture before authentication so rejected
// requests still receive an ingress/egress record. It only applies to public
// relay endpoints and never captures management APIs.
func RelayRequestTraceMiddleware() gin.HandlerFunc {
	return func(ginContext *gin.Context) {
		if !shouldTraceRelayRequest(ginContext.Request) {
			ginContext.Next()
			return
		}

		// 展示轨来源 IP 每请求解析一次，供所有 RelayLog 落盘点统一读取；
		// 与安全轨 c.ClientIP() 互不影响（见 reported_client_ip.go）。
		captureReportedClientIP(ginContext)

		captureContent, captureSettingErr := setting.GetBool(model.SettingKeyRelayLogContentEnabled)
		if captureSettingErr != nil {
			captureContent = false
		}
		trace := newRelayRequestTrace(ginContext.Request, captureContent)
		ginContext.Set(relayRequestTraceContextKey, trace)
		ginContext.Writer = &relayCaptureResponseWriter{ResponseWriter: ginContext.Writer, trace: trace}
		defer trace.close()

		// Tee the body only when the existing auth/parser path consumes it. This
		// preserves middleware ordering and avoids reading an unauthenticated
		// upload merely for logging.
		trace.wrapClientIngress(ginContext.Request)

		ginContext.Next()
		trace.completeClientEgress(ginContext.Writer.Status(), ginContext.Writer.Header())
		if !trace.hasPersistedLog() {
			saveUnclaimedRelayTrace(ginContext, trace)
		}
	}
}

func shouldTraceRelayRequest(request *http.Request) bool {
	if request == nil || request.Method != http.MethodPost || request.URL == nil {
		return false
	}
	switch request.URL.Path {
	case "/v1/chat/completions",
		"/v1/responses",
		"/v1/messages",
		"/v1/embeddings",
		"/v1/images/generations",
		"/v1/images/edits",
		"/v1/images/variations",
		"/v1/audio/speech",
		"/v1/audio/transcriptions",
		"/v1/videos/generations",
		"/v1/music/generations",
		"/v1/search",
		"/v1/rerank",
		"/v1/moderations":
		return true
	default:
		return false
	}
}

func relayRequestTraceFromContext(ginContext *gin.Context) *relayRequestTrace {
	if ginContext == nil {
		return nil
	}
	value, exists := ginContext.Get(relayRequestTraceContextKey)
	if !exists {
		return nil
	}
	trace, _ := value.(*relayRequestTrace)
	return trace
}

func saveUnclaimedRelayTrace(ginContext *gin.Context, trace *relayRequestTrace) {
	if ginContext == nil || trace == nil || trace.hasPersistedLog() {
		return
	}
	traceID, statusCode, clientWriteBytes, clientWriteComplete, clientWriteError := trace.summary()
	stagedRelayLog := trace.takeStagedRelayLog()
	var relayLog model.RelayLog
	if stagedRelayLog != nil {
		relayLog = *stagedRelayLog
	} else {
		requestModel := relayTraceRequestModel(trace)
		relayLog = model.RelayLog{
			Time:             trace.startedAt.Unix(),
			RequestModelName: requestModel,
			RequestAPIKeyID:  ginContext.GetInt("api_key_id"),
			ClientIP:         ginContext.ClientIP(),
			UserAgent:        ginContext.Request.UserAgent(),
			EndpointType:     relayTraceEndpointType(trace.requestPath),
			ActualModelName:  requestModel,
			UseTime:          int(time.Since(trace.startedAt).Milliseconds()),
		}
		if statusCode >= http.StatusBadRequest {
			relayLog.Error = fmt.Sprintf("request completed without an upstream attempt (HTTP %d)", statusCode)
		}
	}
	if relayLog.ReportedClientIP == "" {
		reportedIP := reportedClientIPFromContext(ginContext)
		relayLog.ReportedClientIP = reportedIP.IP
		relayLog.ReportedClientIPSource = string(reportedIP.Source)
	}
	relayLog.TraceID = traceID
	relayLog.HTTPStatus = statusCode
	relayLog.ClientWriteBytes = clientWriteBytes
	relayLog.ClientWriteComplete = clientWriteComplete
	relayLog.ClientWriteError = clientWriteError
	relayLog.PersistenceState = model.RelayLogPersistencePending
	relayLog.ClientDeliveryState = relayLogClientDeliveryState(clientWriteBytes, clientWriteComplete, clientWriteError)
	relayLog.Attempts = trace.annotateAttempts(relayLog.Attempts)
	if relayLog.GenerationOutcome == "" || relayLog.UpstreamOutcome == "" {
		generationOutcome, upstreamOutcome := relayLogOutcomes(relayLog)
		if relayLog.GenerationOutcome == "" {
			relayLog.GenerationOutcome = generationOutcome
		}
		if relayLog.UpstreamOutcome == "" {
			relayLog.UpstreamOutcome = upstreamOutcome
		}
	}
	relayLog.Contents = trace.contents()
	if apiKey, err := apikey.Get(relayLog.RequestAPIKeyID, context.Background()); err == nil {
		relayLog.RequestAPIKeyName = apiKey.Name
	}

	persistenceContext, cancel := newRelayPersistenceContext()
	defer cancel()
	relayLogID, err := relaylog.RelayLogAdd(persistenceContext, relayLog)
	if err != nil {
		log.Warnf("failed to save request trace without upstream attempt: %v", err)
		return
	}
	trace.markLogPersisted(relayLogID)
}

func relayLogClientDeliveryState(clientWriteBytes int64, clientWriteComplete bool, clientWriteError string) string {
	if clientWriteBytes <= 0 {
		return model.RelayLogClientDeliveryNotStarted
	}
	if clientWriteComplete && clientWriteError == "" {
		return model.RelayLogClientDeliveryAccepted
	}
	return model.RelayLogClientDeliveryPartial
}

func relayLogOutcomes(relayLog model.RelayLog) (generationOutcome string, upstreamOutcome string) {
	forwardedAttempts := 0
	failedAttempts := 0
	successfulAttempts := 0
	for _, attempt := range relayLog.Attempts {
		switch attempt.Status {
		case model.AttemptSuccess:
			forwardedAttempts++
			successfulAttempts++
		case model.AttemptFailed:
			forwardedAttempts++
			failedAttempts++
		}
	}
	if relayLog.SemanticCacheHit {
		return model.RelayLogGenerationCacheHit, model.RelayLogUpstreamNone
	}
	if forwardedAttempts == 0 {
		if relayLog.Error != "" || relayLog.HTTPStatus >= http.StatusBadRequest {
			return model.RelayLogGenerationNotStarted, model.RelayLogUpstreamNone
		}
		return model.RelayLogGenerationUnknown, model.RelayLogUpstreamNone
	}
	if successfulAttempts > 0 && relayLog.Error == "" {
		return model.RelayLogGenerationSuccess, model.RelayLogUpstreamSuccess
	}
	if failedAttempts > 0 || relayLog.Error != "" {
		return model.RelayLogGenerationFailed, model.RelayLogUpstreamFailed
	}
	return model.RelayLogGenerationUnknown, model.RelayLogUpstreamUnknown
}

func relayTraceRequestModel(trace *relayRequestTrace) string {
	if trace == nil || trace.clientIngress == nil {
		return ""
	}
	content := trace.clientIngress.snapshot(trace.clientIngress.size() <= relayTraceBundleMaxBytes)
	if content.State != model.RelayLogContentStateReady || len(content.Data) == 0 {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(trace.requestContentType), "multipart/form-data") {
		for _, attachment := range relayMultipartAttachmentContents(content) {
			if attachment.FieldName == "model" && attachment.FileName == "" {
				return strings.TrimSpace(string(attachment.Data))
			}
		}
		return ""
	}
	var body struct {
		Model string `json:"model"`
	}
	if err := jsonAPI.Unmarshal(content.Data, &body); err != nil {
		return ""
	}
	return strings.TrimSpace(body.Model)
}

func relayTraceEndpointType(requestPath string) string {
	switch requestPath {
	case "/v1/chat/completions":
		return model.EndpointTypeChat
	case "/v1/responses":
		return model.EndpointTypeResponses
	case "/v1/messages":
		return model.EndpointTypeMessages
	case "/v1/embeddings":
		return model.EndpointTypeEmbeddings
	case "/v1/images/generations", "/v1/images/edits", "/v1/images/variations":
		return model.EndpointTypeImageGeneration
	case "/v1/audio/speech":
		return model.EndpointTypeAudioSpeech
	case "/v1/audio/transcriptions":
		return model.EndpointTypeAudioTranscription
	case "/v1/videos/generations":
		return model.EndpointTypeVideoGeneration
	case "/v1/music/generations":
		return model.EndpointTypeMusicGeneration
	case "/v1/search":
		return model.EndpointTypeSearch
	case "/v1/rerank":
		return model.EndpointTypeRerank
	case "/v1/moderations":
		return model.EndpointTypeModerations
	default:
		return model.EndpointTypeAll
	}
}

func buildHTTPMetadataContent(boundary string, attemptNumber int, protocol string, request *http.Request, response *http.Response) model.RelayLogCapturedContent {
	metadata := map[string]any{}
	statusCode := 0
	if request != nil {
		metadata["method"] = request.Method
		metadata["url"] = sanitizeRelayLogURL(request)
		metadata["headers"] = sanitizeRelayLogHeaders(request.Header)
		metadata["content_length"] = request.ContentLength
	}
	if response != nil {
		statusCode = response.StatusCode
		metadata["status"] = response.StatusCode
		metadata["headers"] = sanitizeRelayLogHeaders(response.Header)
		metadata["content_length"] = response.ContentLength
	}
	payload, err := jsonAPI.Marshal(metadata)
	content := model.RelayLogCapturedContent{RelayLogContentRef: model.RelayLogContentRef{
		AttemptNum:    attemptNumber,
		Boundary:      boundary,
		Slot:          -1,
		Kind:          "http_metadata",
		Protocol:      protocol,
		ContentType:   "application/json",
		HTTPStatus:    statusCode,
		State:         model.RelayLogContentStateReady,
		Complete:      true,
		CapturedBytes: int64(len(payload)),
		CreatedAt:     time.Now().Unix(),
	}}
	if err != nil {
		content.State = model.RelayLogContentStateUnavailable
		content.Error = fmt.Sprintf("serialize HTTP metadata: %v", err)
		return content
	}
	content.Data = payload
	return content
}

func sanitizeRelayLogURL(request *http.Request) string {
	if request == nil || request.URL == nil {
		return ""
	}
	clonedURL := *request.URL
	clonedURL.User = nil
	queryValues := clonedURL.Query()
	for queryKey := range queryValues {
		if relayLogCredentialName(queryKey) {
			queryValues[queryKey] = []string{"<redacted>"}
		}
	}
	clonedURL.RawQuery = queryValues.Encode()
	if !clonedURL.IsAbs() && request.Host != "" {
		clonedURL.Host = request.Host
		if request.TLS != nil {
			clonedURL.Scheme = "https"
		} else {
			clonedURL.Scheme = "http"
		}
	}
	return clonedURL.String()
}

func sanitizeRelayLogHeaders(headers http.Header) map[string][]string {
	sanitized := make(map[string][]string, len(headers))
	for headerName, values := range headers {
		if relayLogCredentialName(headerName) {
			sanitized[headerName] = []string{"<redacted>"}
			continue
		}
		sanitized[headerName] = append([]string(nil), values...)
	}
	return sanitized
}

func relayLogCredentialName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return false
	}
	compactName := strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(normalized)
	switch compactName {
	case "authorization", "proxyauthorization", "cookie", "cookies", "setcookie",
		"apikey", "xapikey", "googapikey", "xgoogapikey", "password", "passwd",
		"clientsecret", "accesskey", "secretkey", "accesskeyid", "secretaccesskey",
		"accesstoken", "refreshtoken", "idtoken", "sessiontoken", "bearertoken",
		"authtoken", "token", "secret", "sessionid", "sessionkey", "credential", "credentials":
		return true
	}
	return strings.HasSuffix(compactName, "apikey") ||
		strings.HasSuffix(compactName, "accesstoken") ||
		strings.HasSuffix(compactName, "refreshtoken") ||
		strings.HasSuffix(compactName, "bearertoken") ||
		strings.HasSuffix(compactName, "clientsecret") ||
		strings.HasSuffix(compactName, "password")
}

func sanitizeRelayLogCapturedContent(content *model.RelayLogCapturedContent) {
	if content == nil || content.State != model.RelayLogContentStateReady || content.Data == nil {
		return
	}
	if content.Kind == "attachment" && relayLogCredentialName(content.FieldName) {
		content.Data = nil
		content.Text = ""
		content.State = model.RelayLogContentStateDisabled
		content.Error = "credential-bearing multipart field was redacted"
		return
	}

	mediaType, _, _ := mime.ParseMediaType(content.ContentType)
	normalizedMediaType := strings.ToLower(strings.TrimSpace(mediaType))
	switch {
	case strings.Contains(normalizedMediaType, "json"):
		sanitizeRelayLogJSONContent(content)
	case normalizedMediaType == "application/x-www-form-urlencoded":
		sanitizeRelayLogFormContent(content)
	case strings.HasPrefix(normalizedMediaType, "multipart/"):
		if relayMultipartContainsCredential(content.Data, content.ContentType) {
			content.Data = nil
			content.Text = ""
			content.State = model.RelayLogContentStateUnavailable
			content.Error = "raw multipart body omitted because it contains credential-bearing fields; non-credential parts remain available separately"
		}
	}
}

func sanitizeRelayLogJSONContent(content *model.RelayLogCapturedContent) {
	var value any
	if err := jsonAPI.Unmarshal(content.Data, &value); err != nil {
		if relayPayloadMayContainCredential(content.Data) {
			content.Data = nil
			content.Text = ""
			content.State = model.RelayLogContentStateUnavailable
			content.Error = "malformed or partial JSON may contain credentials and was not persisted"
		}
		return
	}
	if !redactRelayLogJSONCredentials(value) {
		return
	}
	sanitized, err := jsonAPI.Marshal(value)
	if err != nil {
		content.Data = nil
		content.Text = ""
		content.State = model.RelayLogContentStateUnavailable
		content.Error = fmt.Sprintf("serialize credential-redacted JSON: %v", err)
		return
	}
	content.Data = sanitized
	content.Error = appendRelayLogContentNote(content.Error, "credential fields redacted before persistence")
}

func redactRelayLogJSONCredentials(value any) bool {
	redacted := false
	switch typedValue := value.(type) {
	case map[string]any:
		for fieldName, fieldValue := range typedValue {
			if relayLogCredentialName(fieldName) {
				typedValue[fieldName] = "<redacted>"
				redacted = true
				continue
			}
			if redactRelayLogJSONCredentials(fieldValue) {
				redacted = true
			}
		}
	case []any:
		for _, item := range typedValue {
			if redactRelayLogJSONCredentials(item) {
				redacted = true
			}
		}
	}
	return redacted
}

func sanitizeRelayLogFormContent(content *model.RelayLogCapturedContent) {
	values, err := url.ParseQuery(string(content.Data))
	if err != nil {
		if relayPayloadMayContainCredential(content.Data) {
			content.Data = nil
			content.Text = ""
			content.State = model.RelayLogContentStateUnavailable
			content.Error = "malformed form body may contain credentials and was not persisted"
		}
		return
	}
	redacted := false
	for fieldName := range values {
		if relayLogCredentialName(fieldName) {
			values[fieldName] = []string{"<redacted>"}
			redacted = true
		}
	}
	if redacted {
		content.Data = []byte(values.Encode())
		content.Error = appendRelayLogContentNote(content.Error, "credential fields redacted before persistence")
	}
}

func relayPayloadMayContainCredential(payload []byte) bool {
	normalized := strings.ToLower(string(payload))
	credentialMarkers := []string{
		`"authorization"`, `"api_key"`, `"api-key"`, `"apikey"`, `"access_token"`,
		`"refresh_token"`, `"client_secret"`, `"password"`, `"token"`, `"secret"`,
		`"session_id"`, `name="api_key"`, `name="authorization"`, `name="access_token"`,
		`name="password"`, `name="token"`, `name="secret"`, `name="session_id"`,
	}
	for _, marker := range credentialMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func relayMultipartContainsCredential(payload []byte, contentType string) bool {
	_, parameters, err := mime.ParseMediaType(contentType)
	if err != nil || parameters["boundary"] == "" {
		return relayPayloadMayContainCredential(payload)
	}
	reader := multipart.NewReader(bytes.NewReader(payload), parameters["boundary"])
	for {
		part, partErr := reader.NextPart()
		if errors.Is(partErr, io.EOF) {
			return false
		}
		if partErr != nil {
			return relayPayloadMayContainCredential(payload)
		}
		credentialField := relayLogCredentialName(part.FormName())
		_ = part.Close()
		if credentialField {
			return true
		}
	}
}

func appendRelayLogContentNote(existing string, note string) string {
	if existing == "" {
		return note
	}
	return existing + "; " + note
}

func relayMultipartAttachmentContents(parent model.RelayLogCapturedContent) []model.RelayLogCapturedContent {
	mediaType, parameters, err := mime.ParseMediaType(parent.ContentType)
	if err != nil || !strings.HasPrefix(strings.ToLower(mediaType), "multipart/") || parameters["boundary"] == "" || parent.Data == nil {
		return nil
	}
	reader := multipart.NewReader(bytes.NewReader(parent.Data), parameters["boundary"])
	attachments := make([]model.RelayLogCapturedContent, 0)
	slot := 1
	for {
		part, partErr := reader.NextPart()
		if errors.Is(partErr, io.EOF) {
			break
		}
		if partErr != nil {
			break
		}
		payload, readErr := io.ReadAll(part)
		_ = part.Close()
		if readErr != nil {
			continue
		}
		attachments = append(attachments, model.RelayLogCapturedContent{
			RelayLogContentRef: model.RelayLogContentRef{
				AttemptNum:    parent.AttemptNum,
				Boundary:      parent.Boundary,
				Slot:          slot,
				Kind:          "attachment",
				Protocol:      parent.Protocol,
				FieldName:     part.FormName(),
				FileName:      part.FileName(),
				ContentType:   part.Header.Get("Content-Type"),
				State:         model.RelayLogContentStateReady,
				Complete:      true,
				CapturedBytes: int64(len(payload)),
				CreatedAt:     parent.CreatedAt,
			},
			Data: payload,
		})
		slot++
	}
	return attachments
}
