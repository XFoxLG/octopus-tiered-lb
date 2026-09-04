package passthrough

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound/anthropic"
	"github.com/lingyuins/octopus/internal/transformer/outbound/openai"
)

// Outbound forwards the original inbound JSON body to the upstream endpoint.
// Response parsing is delegated to the adapter that matches the original API format.
type Outbound struct {
	delegate model.Outbound

	// PreservePath 为 true 时（分组出站格式 "raw"，原始穿透（信息体）），
	// 上游 URL 使用客户端原始请求路径（InternalLLMRequest.RawPath），
	// 而不是按入站协议映射的固定端点路径。请求体仍只改写 model 字段。
	PreservePath bool
}

func (o *Outbound) TransformRequest(ctx context.Context, request *model.InternalLLMRequest, baseUrl, key string) (*http.Request, error) {
	if request == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if len(request.RawRequest) == 0 {
		return nil, fmt.Errorf("raw request is empty")
	}

	delegate, endpointPath, err := delegateAndEndpointForFormat(request.RawAPIFormat)
	if err != nil {
		return nil, err
	}
	o.delegate = delegate
	body, err := rewritePassthroughModel(request.RawRequest, request.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to rewrite passthrough model: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create passthrough request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if request.Stream != nil && *request.Stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}

	if key != "" {
		if request.RawAPIFormat == model.APIFormatAnthropicMessage {
			req.Header.Set("Anthropic-Version", "2023-06-01")
			req.Header.Set("X-API-Key", key)
		} else {
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("api-key", key)
		}
	}

	upstreamURL, err := o.buildUpstreamURL(baseUrl, endpointPath, request)
	if err != nil {
		return nil, err
	}
	parsedURL, err := url.Parse(upstreamURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse passthrough upstream url: %w", err)
	}
	req.URL = parsedURL
	return req, nil
}

func (o *Outbound) TransformResponse(ctx context.Context, response *http.Response) (*model.InternalLLMResponse, error) {
	delegate, err := o.responseDelegate()
	if err != nil {
		return nil, err
	}
	return delegate.TransformResponse(ctx, response)
}

func (o *Outbound) TransformStream(ctx context.Context, eventData []byte) (*model.InternalLLMResponse, error) {
	delegate, err := o.responseDelegate()
	if err != nil {
		return nil, err
	}
	return delegate.TransformStream(ctx, eventData)
}

func (o *Outbound) responseDelegate() (model.Outbound, error) {
	if o.delegate == nil {
		return nil, fmt.Errorf("passthrough response delegate is not initialized")
	}
	return o.delegate, nil
}

func delegateAndEndpointForFormat(format model.APIFormat) (model.Outbound, string, error) {
	switch format {
	case model.APIFormatOpenAIChatCompletion:
		return &openai.ChatOutbound{}, "/v1/chat/completions", nil
	case model.APIFormatOpenAIResponse:
		return &openai.ResponseOutbound{}, "/v1/responses", nil
	case model.APIFormatAnthropicMessage:
		return &anthropic.MessageOutbound{}, "/messages", nil
	default:
		return nil, "", fmt.Errorf("passthrough does not support raw api format %q", format)
	}
}

// rewritePassthroughModel rewrites the top-level "model" field in the original
// inbound JSON body to the resolved upstream model name. Format conversion is
// intentionally skipped in passthrough mode, but group/channel model mapping
// must still apply — otherwise the group name would be sent upstream as-is.
//
// Byte-stability contract (prompt caching): the rewrite is surgical — only the
// bytes of the top-level "model" value are replaced. Field order, whitespace,
// and every other byte are preserved, because providers only reuse a cached
// prefix when it is byte-identical. A map round-trip (unmarshal + marshal)
// would sort keys and reformat whitespace, silently killing the cache.
func rewritePassthroughModel(raw []byte, modelName string) ([]byte, error) {
	resolvedModelName := strings.TrimSpace(modelName)
	if resolvedModelName == "" || len(raw) == 0 {
		return raw, nil
	}

	valueSpans, isJSONObject, scanErr := locateTopLevelModelValueSpans(raw)
	if scanErr != nil {
		// Scanner failed on input that may still be valid JSON: fall back to
		// the legacy map round-trip for correctness (model mapping must win
		// over byte stability).
		return legacyRewritePassthroughModel(raw, resolvedModelName)
	}
	if !isJSONObject {
		// Keep the original body when it is not a JSON object (defensive).
		return raw, nil
	}
	if len(valueSpans) == 0 {
		return insertTopLevelModelField(raw, resolvedModelName)
	}
	if passthroughModelValuesEqual(raw, valueSpans, resolvedModelName) {
		return raw, nil
	}
	encodedModelName, err := json.Marshal(resolvedModelName)
	if err != nil {
		return raw, nil
	}
	rebuiltBody := make([]byte, 0, len(raw)+len(encodedModelName))
	consumedOffset := 0
	for _, valueSpan := range valueSpans {
		rebuiltBody = append(rebuiltBody, raw[consumedOffset:valueSpan.start]...)
		rebuiltBody = append(rebuiltBody, encodedModelName...)
		consumedOffset = valueSpan.end
	}
	rebuiltBody = append(rebuiltBody, raw[consumedOffset:]...)
	return rebuiltBody, nil
}

// passthroughModelValueSpan is a half-open [start, end) byte range covering
// exactly one top-level "model" value (no surrounding whitespace).
type passthroughModelValueSpan struct {
	start int
	end   int
}

// locateTopLevelModelValueSpans scans a JSON object body and returns the spans
// of every top-level "model" value. Nested "model" keys (inside messages,
// tools, etc.) are intentionally ignored. It returns isJSONObject=false when
// the body is not a JSON object, and a non-nil error when the body looks like
// an object but cannot be scanned safely.
func locateTopLevelModelValueSpans(raw []byte) (spans []passthroughModelValueSpan, isJSONObject bool, err error) {
	bodyLength := len(raw)
	cursor := skipPassthroughWhitespace(raw, 0)
	if cursor >= bodyLength || raw[cursor] != '{' {
		return nil, false, nil
	}
	cursor++
	for {
		cursor = skipPassthroughWhitespace(raw, cursor)
		if cursor >= bodyLength {
			return nil, true, fmt.Errorf("truncated JSON object body")
		}
		if raw[cursor] == '}' {
			cursor++
			cursor = skipPassthroughWhitespace(raw, cursor)
			if cursor != bodyLength {
				// Trailing garbage after the object: not a single JSON value,
				// keep legacy defensive behavior (return body untouched).
				return nil, false, nil
			}
			return spans, true, nil
		}
		if raw[cursor] != '"' {
			return nil, true, fmt.Errorf("expected object key at offset %d", cursor)
		}
		keyEnd, decodedKey, keyErr := parsePassthroughJSONString(raw, cursor)
		if keyErr != nil {
			return nil, true, keyErr
		}
		cursor = skipPassthroughWhitespace(raw, keyEnd)
		if cursor >= bodyLength || raw[cursor] != ':' {
			return nil, true, fmt.Errorf("expected colon at offset %d", cursor)
		}
		cursor = skipPassthroughWhitespace(raw, cursor+1)
		if cursor >= bodyLength {
			return nil, true, fmt.Errorf("truncated JSON object value")
		}
		valueEnd, valueErr := skipPassthroughJSONValue(raw, cursor)
		if valueErr != nil {
			return nil, true, valueErr
		}
		if decodedKey == "model" {
			spans = append(spans, passthroughModelValueSpan{start: cursor, end: valueEnd})
		}
		cursor = skipPassthroughWhitespace(raw, valueEnd)
		if cursor >= bodyLength {
			return nil, true, fmt.Errorf("truncated JSON object body")
		}
		if raw[cursor] == ',' {
			cursor++
			continue
		}
		if raw[cursor] == '}' {
			continue
		}
		return nil, true, fmt.Errorf("expected comma or closing brace at offset %d", cursor)
	}
}

// passthroughModelValuesEqual reports whether every located "model" value is a
// JSON string that already decodes to the resolved model name.
func passthroughModelValuesEqual(raw []byte, valueSpans []passthroughModelValueSpan, resolvedModelName string) bool {
	for _, valueSpan := range valueSpans {
		var currentValue string
		if err := json.Unmarshal(raw[valueSpan.start:valueSpan.end], &currentValue); err != nil {
			return false
		}
		if currentValue != resolvedModelName {
			return false
		}
	}
	return true
}

// insertTopLevelModelField inserts a "model" field right after the opening
// brace of a JSON object body, preserving every original byte after it.
func insertTopLevelModelField(raw []byte, resolvedModelName string) ([]byte, error) {
	encodedModelName, err := json.Marshal(resolvedModelName)
	if err != nil {
		return raw, nil
	}
	braceOffset := skipPassthroughWhitespace(raw, 0)
	if braceOffset >= len(raw) || raw[braceOffset] != '{' {
		return raw, nil
	}
	insertedField := append(append([]byte(`"model":`), encodedModelName...), ',')
	afterBrace := raw[braceOffset+1:]
	afterBraceStart := skipPassthroughWhitespace(raw, braceOffset+1)
	if afterBraceStart < len(raw) && raw[afterBraceStart] == '}' {
		// Empty object (possibly with whitespace): no trailing comma needed.
		insertedField = insertedField[:len(insertedField)-1]
	}
	rebuiltBody := make([]byte, 0, len(raw)+len(insertedField))
	rebuiltBody = append(rebuiltBody, raw[:braceOffset+1]...)
	rebuiltBody = append(rebuiltBody, insertedField...)
	rebuiltBody = append(rebuiltBody, afterBrace...)
	return rebuiltBody, nil
}

// legacyRewritePassthroughModel is the pre-stability fallback: decode into a
// map and re-encode. It reorders keys, so it is only used when the surgical
// scanner cannot handle the input.
func legacyRewritePassthroughModel(raw []byte, resolvedModelName string) ([]byte, error) {
	var decodedBody map[string]any
	if err := json.Unmarshal(raw, &decodedBody); err != nil {
		return raw, nil
	}
	if current, ok := decodedBody["model"].(string); ok && current == resolvedModelName {
		return raw, nil
	}
	decodedBody["model"] = resolvedModelName
	return json.Marshal(decodedBody)
}

// skipPassthroughWhitespace advances past JSON whitespace (space, tab, CR, LF).
func skipPassthroughWhitespace(raw []byte, offset int) int {
	for offset < len(raw) {
		switch raw[offset] {
		case ' ', '\t', '\n', '\r':
			offset++
		default:
			return offset
		}
	}
	return offset
}

// parsePassthroughJSONString parses the JSON string starting at offset (which
// must point at the opening quote) and returns the offset just past the
// closing quote plus the decoded string value.
func parsePassthroughJSONString(raw []byte, offset int) (end int, decoded string, err error) {
	closingOffset, scanErr := scanPassthroughJSONStringEnd(raw, offset)
	if scanErr != nil {
		return 0, "", scanErr
	}
	if err := json.Unmarshal(raw[offset:closingOffset], &decoded); err != nil {
		return 0, "", fmt.Errorf("invalid JSON string at offset %d: %w", offset, err)
	}
	return closingOffset, decoded, nil
}

// scanPassthroughJSONStringEnd returns the offset just past the closing quote
// of the JSON string starting at offset, honoring backslash escapes.
func scanPassthroughJSONStringEnd(raw []byte, offset int) (int, error) {
	if offset >= len(raw) || raw[offset] != '"' {
		return 0, fmt.Errorf("expected string at offset %d", offset)
	}
	cursor := offset + 1
	for cursor < len(raw) {
		switch raw[cursor] {
		case '\\':
			cursor += 2
		case '"':
			return cursor + 1, nil
		default:
			cursor++
		}
	}
	return 0, fmt.Errorf("unterminated string starting at offset %d", offset)
}

// skipPassthroughJSONValue returns the offset just past the JSON value that
// starts at offset (which must be the first non-whitespace byte of the value).
func skipPassthroughJSONValue(raw []byte, offset int) (int, error) {
	if offset >= len(raw) {
		return 0, fmt.Errorf("truncated JSON value at offset %d", offset)
	}
	switch raw[offset] {
	case '"':
		return scanPassthroughJSONStringEnd(raw, offset)
	case '{':
		return skipPassthroughBalanced(raw, offset, '{', '}')
	case '[':
		return skipPassthroughBalanced(raw, offset, '[', ']')
	case 't':
		return skipPassthroughLiteral(raw, offset, "true")
	case 'f':
		return skipPassthroughLiteral(raw, offset, "false")
	case 'n':
		return skipPassthroughLiteral(raw, offset, "null")
	default:
		return skipPassthroughNumber(raw, offset)
	}
}

// skipPassthroughBalanced skips a balanced {...} or [...] value, ignoring
// braces inside JSON strings.
func skipPassthroughBalanced(raw []byte, offset int, opener byte, closer byte) (int, error) {
	if offset >= len(raw) || raw[offset] != opener {
		return 0, fmt.Errorf("expected %q at offset %d", string([]byte{opener}), offset)
	}
	nestingDepth := 0
	cursor := offset
	for cursor < len(raw) {
		currentByte := raw[cursor]
		if currentByte == '"' {
			stringEnd, err := scanPassthroughJSONStringEnd(raw, cursor)
			if err != nil {
				return 0, err
			}
			cursor = stringEnd
			continue
		}
		if currentByte == '\\' {
			// A backslash outside a string is invalid JSON; let the fallback
			// path handle it instead of guessing.
			return 0, fmt.Errorf("unexpected backslash at offset %d", cursor)
		}
		if currentByte == opener {
			nestingDepth++
		} else if currentByte == closer {
			nestingDepth--
			if nestingDepth == 0 {
				return cursor + 1, nil
			}
		}
		cursor++
	}
	return 0, fmt.Errorf("unterminated JSON value starting at offset %d", offset)
}

// skipPassthroughLiteral skips a true/false/null literal.
func skipPassthroughLiteral(raw []byte, offset int, literal string) (int, error) {
	if len(raw)-offset < len(literal) || string(raw[offset:offset+len(literal)]) != literal {
		return 0, fmt.Errorf("invalid literal at offset %d", offset)
	}
	return offset + len(literal), nil
}

// skipPassthroughNumber skips a JSON number up to its delimiter, then
// validates it with the standard library.
func skipPassthroughNumber(raw []byte, offset int) (int, error) {
	cursor := offset
	for cursor < len(raw) {
		switch raw[cursor] {
		case ',', '}', ']', ' ', '\t', '\n', '\r':
			var validatedNumber json.Number
			if err := json.Unmarshal(raw[offset:cursor], &validatedNumber); err != nil {
				return 0, fmt.Errorf("invalid number at offset %d: %w", offset, err)
			}
			return cursor, nil
		default:
			cursor++
		}
	}
	return 0, fmt.Errorf("truncated number starting at offset %d", offset)
}

// buildUpstreamURL picks the upstream URL for the passthrough request.
// In PreservePath mode the client's original request path is appended to the
// channel base URL (reusing the OpenAI path-joining rules so a base URL that
// already ends in /v1 does not duplicate the version segment); when RawPath is
// empty it falls back to the canonical per-format endpoint.
func (o *Outbound) buildUpstreamURL(baseURL, endpointPath string, request *model.InternalLLMRequest) (string, error) {
	if o.PreservePath {
		if rawPath := strings.TrimSpace(request.RawPath); rawPath != "" {
			if !strings.HasPrefix(rawPath, "/") {
				rawPath = "/" + rawPath
			}
			// Anthropic 协议使用独立拼接规则（与 buildPassthroughURL 一致）：
			// base URL 与路径直接拼接，不做 OpenAI 风格的 /v1 前缀去重。
			if request.RawAPIFormat == model.APIFormatAnthropicMessage {
				parsed, err := url.Parse(strings.TrimSuffix(baseURL, "/"))
				if err != nil {
					return "", fmt.Errorf("failed to parse base url: %w", err)
				}
				parsed.Path = strings.TrimSuffix(parsed.Path, "/") + rawPath
				return appendQuery(parsed.String(), request.Query)
			}
			upstreamURL, err := openai.BuildOpenAIUpstreamURL(baseURL, rawPath)
			if err != nil {
				return "", err
			}
			return appendQuery(upstreamURL, request.Query)
		}
	}
	return buildPassthroughURL(baseURL, endpointPath, request.Query, request.RawAPIFormat)
}

func appendQuery(upstreamURL string, query url.Values) (string, error) {
	parsed, err := url.Parse(upstreamURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse built upstream url: %w", err)
	}
	if query != nil {
		parsed.RawQuery = query.Encode()
	}
	return parsed.String(), nil
}

func buildPassthroughURL(baseURL, endpointPath string, query url.Values, format model.APIFormat) (string, error) {
	if format == model.APIFormatAnthropicMessage {
		parsed, err := url.Parse(strings.TrimSuffix(baseURL, "/"))
		if err != nil {
			return "", fmt.Errorf("failed to parse base url: %w", err)
		}
		parsed.Path = strings.TrimSuffix(parsed.Path, "/") + endpointPath
		if query != nil {
			parsed.RawQuery = query.Encode()
		}
		return parsed.String(), nil
	}

	upstreamURL, err := openai.BuildOpenAIUpstreamURL(baseURL, endpointPath)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(upstreamURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse built upstream url: %w", err)
	}
	if query != nil {
		parsed.RawQuery = query.Encode()
	}
	return parsed.String(), nil
}
