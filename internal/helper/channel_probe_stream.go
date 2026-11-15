package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	appmodel "github.com/lingyuins/octopus/internal/model"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
)

// probeStreamAccumulator consumes complete SSE frames as they arrive. It avoids
// reparsing the entire response after every network read and keeps a partial
// frame for the next batch.
type probeStreamAccumulator struct {
	protocol string
	pending  string
	signals  capabilityResponseSignals

	responseBytes int64
	eventCount    int64
	parsedEvents  int64
	invalidEvents int64
	firstOutput   *time.Time
	terminalAt    *time.Time
	doneAt        *time.Time
}

func newProbeStreamAccumulator(protocol string) *probeStreamAccumulator {
	return &probeStreamAccumulator{protocol: protocol, signals: capabilityResponseSignals{ChatChoiceCount: 1}}
}

// Feed returns true when a complete protocol terminal has been observed. A Chat
// terminal additionally requires a usable finish_reason for every observed
// choice; [DONE] by itself is never sufficient.
func (a *probeStreamAccumulator) Feed(chunk []byte, now time.Time) bool {
	if len(chunk) == 0 {
		return false
	}
	a.responseBytes += int64(len(chunk))
	a.pending += string(chunk)
	terminalBefore := a.TerminalObserved()
	for {
		index := strings.Index(a.pending, "\n\n")
		separatorSize := 2
		if crlf := strings.Index(a.pending, "\r\n\r\n"); crlf >= 0 && (index < 0 || crlf < index) {
			index, separatorSize = crlf, 4
		}
		if index < 0 {
			break
		}
		frame := a.pending[:index]
		a.pending = a.pending[index+separatorSize:]
		a.processFrame(frame, now)
		if a.TerminalObserved() && (a.protocol != "chat" || a.signals.HasStreamDone) {
			a.pending = ""
			break
		}
	}
	if !terminalBefore && a.TerminalObserved() {
		value := now
		a.terminalAt = &value
	}
	return a.TerminalObserved()
}

// Flush processes a final frame that arrived without the normal blank-line
// terminator before EOF.
func (a *probeStreamAccumulator) Flush(now time.Time) {
	if strings.TrimSpace(a.pending) == "" {
		a.pending = ""
		return
	}
	terminalBefore := a.TerminalObserved()
	a.processFrame(a.pending, now)
	a.pending = ""
	if !terminalBefore && a.TerminalObserved() {
		value := now
		a.terminalAt = &value
	}
}

func (a *probeStreamAccumulator) processFrame(frame string, now time.Time) {
	payloads, done := probeSSEFramePayloads(frame)
	if done {
		a.signals.HasStreamEnvelope = true
		a.signals.HasStreamDone = true
		a.signals.HasStreamTerminal = true
		if a.signals.StreamTerminalKind == "" {
			a.signals.StreamTerminalKind = "done"
		}
		value := now
		a.doneAt = &value
	}
	for _, payload := range payloads {
		a.eventCount++
		a.signals.HasStreamEnvelope = true
		var event map[string]any
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			a.invalidEvents++
			continue
		}
		a.parsedEvents++
		collectProbeEnvelope(event, &a.signals)
		collectProbeStreamTerminal(event, &a.signals)
		if a.signals.InvalidChoiceIndex {
			a.invalidEvents++
		}
		if a.firstOutput == nil && a.signals.HasEffectiveOutput() {
			value := now
			a.firstOutput = &value
		}
	}
}

func probeSSEFramePayloads(frame string) ([]string, bool) {
	lines := strings.Split(strings.ReplaceAll(frame, "\r\n", "\n"), "\n")
	data := make([]string, 0, len(lines))
	done := false
	for _, line := range lines {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if value == "[DONE]" {
			done = true
			continue
		}
		if value != "" {
			data = append(data, value)
		}
	}
	if len(data) == 0 {
		return nil, done
	}
	return []string{strings.Join(data, "\n")}, done
}

func (s capabilityResponseSignals) HasEffectiveOutput() bool {
	return s.HasVisibleText || s.HasToolOutput || s.HasToolCall || s.HasStructuredOK
}

func (s capabilityResponseSignals) ChatTerminalComplete() bool {
	if len(s.FinishReasons) == 0 || s.InvalidChoiceIndex {
		return false
	}
	for rawIndex := range s.FinishReasons {
		index, err := parseProbeChoiceIndex(rawIndex)
		if err != nil {
			return false
		}
		if index+1 > s.ChatChoiceCount {
			return false
		}
	}
	for index := 0; index < s.ChatChoiceCount; index++ {
		if strings.TrimSpace(s.FinishReasons[formatProbeChoiceIndex(index)]) == "" {
			return false
		}
	}
	return true
}

func (s capabilityResponseSignals) TerminalComplete(protocol string) bool {
	switch protocol {
	case "chat":
		return s.ChatTerminalComplete()
	case "messages":
		return s.StreamTerminalKind == "message_stop"
	case "responses":
		switch s.StreamTerminalKind {
		case "response.completed", "response.failed", "response.incomplete", "response.error", "error":
			return true
		default:
			return false
		}
	case "gemini":
		if s.InvalidChoiceIndex || len(s.GeminiFinishReasons) != s.ChatChoiceCount {
			return false
		}
		for index := 0; index < s.ChatChoiceCount; index++ {
			if s.GeminiFinishReasons[index] == "" {
				return false
			}
		}
		return true
	default:
		return s.HasStreamTerminal
	}
}

func (s capabilityResponseSignals) GenerationState(protocol string) string {
	if !s.TerminalComplete(protocol) {
		return "unknown"
	}
	if protocol == "responses" && s.StreamTerminalKind == "response.incomplete" {
		return "incomplete"
	}
	switch s.Termination.Cause {
	case transmodel.TerminationCauseComplete, transmodel.TerminationCauseStopSequence, transmodel.TerminationCauseToolCall:
		return "completed"
	case transmodel.TerminationCauseTokenLimit:
		return "length"
	case transmodel.TerminationCauseContentFilter, transmodel.TerminationCauseRefusal:
		return "content_filter"
	case transmodel.TerminationCauseError, transmodel.TerminationCauseMalformedToolCall:
		return "failed"
	case transmodel.TerminationCausePauseTurn, transmodel.TerminationCauseContextExhausted, transmodel.TerminationCauseRecitation:
		return "incomplete"
	default:
		return "unknown"
	}
}

func (a *probeStreamAccumulator) TerminalObserved() bool {
	return a.signals.TerminalComplete(a.protocol)
}

func (a *probeStreamAccumulator) TerminalKind() string {
	if a.protocol == "chat" && a.signals.ChatTerminalComplete() && a.signals.StreamTerminalKind == "" {
		return "finish_reason"
	}
	return a.signals.StreamTerminalKind
}

func (a *probeStreamAccumulator) ParseState() string {
	switch {
	case a.eventCount == 0:
		return "no_data_events"
	case a.parsedEvents == 0:
		return "invalid"
	case a.invalidEvents > 0:
		return "mixed"
	default:
		return "valid"
	}
}

func probeElapsedMS(start time.Time, end time.Time) *int64 {
	value := end.Sub(start).Milliseconds()
	return &value
}

func markProbeResponseComplete(evidence *appmodel.ChannelProbeEvidence, startedAt time.Time) {
	if evidence != nil && evidence.ResponseCompleteMS == nil {
		value := time.Since(startedAt).Milliseconds()
		evidence.ResponseCompleteMS = &value
	}
}

func probeParseState(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	switch {
	case trimmed == "":
		return "empty"
	case json.Valid(body):
		return "valid"
	case strings.HasPrefix(strings.ToLower(trimmed), "<!doctype html"),
		strings.HasPrefix(strings.ToLower(trimmed), "<html"),
		strings.HasPrefix(trimmed, "<"):
		return "html"
	default:
		return "invalid"
	}
}

func parseProbeChoiceIndex(raw string) (int, error) {
	index, err := strconv.Atoi(raw)
	if err != nil || index < 0 {
		return 0, &probeChoiceIndexError{raw: raw}
	}
	return index, nil
}

func formatProbeChoiceIndex(index int) string {
	if index == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for index > 0 {
		digits = append(digits, byte('0'+index%10))
		index /= 10
	}
	for left, right := 0, len(digits)-1; left < right; left, right = left+1, right-1 {
		digits[left], digits[right] = digits[right], digits[left]
	}
	return string(digits)
}

type probeChoiceIndexError struct{ raw string }

func (e *probeChoiceIndexError) Error() string { return "invalid probe choice index: " + e.raw }

type probeTimedReader struct {
	body      io.ReadCloser
	startedAt time.Time
	evidence  *appmodel.ChannelProbeEvidence
}

func (r *probeTimedReader) Close() error { return r.body.Close() }

func (r *probeTimedReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	now := time.Now()
	if n > 0 {
		r.evidence.ResponseBytes += int64(n)
		if r.evidence.FirstBodyByteMS == nil {
			value := now.Sub(r.startedAt).Milliseconds()
			r.evidence.FirstBodyByteMS = &value
		}
	}
	if errors.Is(err, io.EOF) && r.evidence.ResponseCompleteMS == nil {
		value := now.Sub(r.startedAt).Milliseconds()
		r.evidence.ResponseCompleteMS = &value
	}
	return n, err
}

func readProbeStreamBody(
	body io.ReadCloser,
	startedAt time.Time,
	protocol string,
	requestCancel func(),
	evidence *appmodel.ChannelProbeEvidence,
	expectedChoices int,
	network *appmodel.RelayNetworkObserver,
) ([]byte, capabilityResponseSignals, error, bool) {
	reader := &probeTimedReader{body: body, startedAt: startedAt, evidence: evidence}
	accumulator := newProbeStreamAccumulator(protocol)
	accumulator.signals.ChatChoiceCount = expectedChoices
	defer func() {
		signals := accumulator.signals
		evidence.EffectiveEventCount = accumulator.parsedEvents
		evidence.ParseState = accumulator.ParseState()
		evidence.DoneObserved = signals.HasStreamDone
		evidence.TerminalObserved = accumulator.TerminalObserved()
		evidence.TerminalKind = accumulator.TerminalKind()
		evidence.GenerationState = signals.GenerationState(protocol)
		if protocol == "chat" {
			evidence.FinishReasons = signals.FinishReasons
			evidence.ChatCompletedChoices = len(signals.FinishReasons)
			evidence.ChatExpectedChoices = expectedChoices
		}
	}()
	raw := make([]byte, 0, 8192)
	chunk := make([]byte, 4096)
	var tailTimer *time.Timer
	var tailExpired atomic.Bool
	truncated := false

	stopTailTimer := func() {
		if tailTimer != nil {
			tailTimer.Stop()
		}
	}
	defer stopTailTimer()

	for {
		count, readErr := reader.Read(chunk)
		if count > 0 {
			if len(raw)+count > capabilityProbeMaxBody {
				remaining := capabilityProbeMaxBody - len(raw)
				if remaining > 0 {
					raw = append(raw, chunk[:remaining]...)
				}
				truncated = true
				evidence.ResponseTruncated = true
				evidence.ReadState = "truncated"
				stopTailTimer()
				return raw, accumulator.signals, nil, true
			}
			raw = append(raw, chunk[:count]...)
			terminal := accumulator.Feed(chunk[:count], time.Now())
			network.EffectiveOutput(accumulator.signals.HasReasoning, accumulator.signals.HasEffectiveOutput(), terminal)
			if accumulator.firstOutput != nil && evidence.FirstEffectiveOutputMS == nil {
				value := accumulator.firstOutput.Sub(startedAt).Milliseconds()
				evidence.FirstEffectiveOutputMS = &value
			}
			if terminal {
				if evidence.TerminalMS == nil && accumulator.terminalAt != nil {
					value := accumulator.terminalAt.Sub(startedAt).Milliseconds()
					evidence.TerminalMS = &value
				}
				if protocol != "chat" || accumulator.signals.HasStreamDone {
					evidence.ReadState = "stopped_after_terminal"
					return raw, accumulator.signals, nil, truncated
				}
				if tailTimer == nil {
					tailTimer = time.AfterFunc(2*time.Second, func() {
						tailExpired.Store(true)
						requestCancel()
					})
				}
			}
		}
		if readErr != nil {
			accumulator.Flush(time.Now())
			network.EffectiveOutput(accumulator.signals.HasReasoning, accumulator.signals.HasEffectiveOutput(), accumulator.TerminalObserved())
			if accumulator.firstOutput != nil && evidence.FirstEffectiveOutputMS == nil {
				value := accumulator.firstOutput.Sub(startedAt).Milliseconds()
				evidence.FirstEffectiveOutputMS = &value
			}
			if accumulator.terminalAt != nil && evidence.TerminalMS == nil {
				value := accumulator.terminalAt.Sub(startedAt).Milliseconds()
				evidence.TerminalMS = &value
			}
			evidence.ReadState = "error"
			if errors.Is(readErr, io.EOF) {
				evidence.ReadState = "complete"
			}
			if tailExpired.Load() && accumulator.TerminalObserved() {
				evidence.ReadState = "tail_window_expired"
				return raw, accumulator.signals, nil, truncated
			}
			if accumulator.TerminalObserved() {
				// A provider terminal is authoritative; a late socket failure
				// must not replace it with a second synthetic outcome.
				return raw, accumulator.signals, nil, truncated
			}
			return raw, accumulator.signals, readErr, truncated
		}
	}
}

func probeConfigurationFingerprint(
	channel *appmodel.Channel,
	plan appmodel.ConnectionPlan,
	requestFingerprint string,
	modelName string,
	stream *bool,
	budgetMS *int64,
) string {
	if channel == nil {
		return ""
	}
	type fingerprintInput struct {
		ChannelType            string                  `json:"channel_type"`
		OutboundFormatOverride string                  `json:"outbound_format_override,omitempty"`
		UpstreamProtocols      []string                `json:"upstream_protocols,omitempty"`
		CustomHeaders          []appmodel.CustomHeader `json:"custom_headers,omitempty"`
		EndpointID             string                  `json:"endpoint_id,omitempty"`
		EndpointProtocol       string                  `json:"endpoint_protocol,omitempty"`
		EndpointURL            string                  `json:"endpoint_url,omitempty"`
		EndpointURLMode        string                  `json:"endpoint_url_mode,omitempty"`
		EndpointAuth           string                  `json:"endpoint_auth,omitempty"`
		EndpointCompatibility  string                  `json:"endpoint_compatibility,omitempty"`
		EndpointForwardMode    string                  `json:"endpoint_forward_mode,omitempty"`
		EndpointTransport      string                  `json:"endpoint_transport,omitempty"`
		TaskAPIURL             string                  `json:"task_api_url,omitempty"`
		EndpointHeaders        []appmodel.CustomHeader `json:"endpoint_headers,omitempty"`
		RequestFingerprint     string                  `json:"request_fingerprint,omitempty"`
		Model                  string                  `json:"model,omitempty"`
		Stream                 *bool                   `json:"stream,omitempty"`
		BudgetMS               *int64                  `json:"budget_ms,omitempty"`
	}
	input := fingerprintInput{
		ChannelType:            channel.Type.String(),
		OutboundFormatOverride: channel.OutboundFormatOverride,
		UpstreamProtocols:      channel.UpstreamProtocols,
		CustomHeaders:          channel.CustomHeader,
		RequestFingerprint:     requestFingerprint,
		Model:                  modelName,
		Stream:                 stream,
		BudgetMS:               budgetMS,
	}
	if plan.Endpoint != nil {
		input.EndpointID = plan.Endpoint.ID
		input.EndpointProtocol = plan.Endpoint.Protocol
		input.EndpointURL = plan.Endpoint.URL
		input.EndpointURLMode = plan.Endpoint.URLMode
		input.EndpointAuth = plan.Endpoint.Auth
		input.EndpointCompatibility = plan.Endpoint.Compatibility
		input.EndpointForwardMode = plan.Endpoint.ForwardMode
		input.EndpointTransport = plan.Endpoint.EffectiveTransport()
		input.TaskAPIURL = plan.Endpoint.TaskAPIURL
		input.EndpointHeaders = plan.Endpoint.Headers
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
