package model

import (
	"context"
	"fmt"
	"github.com/lingyuins/octopus/internal/pkg/geminicli"
	"net/http"
	"net/url"
	"sort"
	"strings"

	tm "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	oa "github.com/lingyuins/octopus/internal/transformer/outbound/openai"
)

// ConnectionConfig is authoritative when present. Legacy fields are retained only
// for rollback; no request may mix the two configuration generations.
type ConnectionConfig struct {
	Version   int               `json:"version"`
	Selection string            `json:"selection"`
	Endpoints []ChannelEndpoint `json:"endpoints"`
	Catalog   ModelCatalog      `json:"catalog"`
}
type ChannelEndpoint struct {
	ID            string         `json:"id"`
	Protocol      string         `json:"protocol"`
	URL           string         `json:"url"`
	URLMode       string         `json:"url_mode"`
	Auth          string         `json:"auth"`
	Compatibility string         `json:"compatibility,omitempty"`
	ForwardMode   string         `json:"forward_mode,omitempty"`
	Headers       []CustomHeader `json:"headers,omitempty"`
}
type ModelCatalog struct {
	EndpointID string `json:"endpoint_id,omitempty"`
	Format     string `json:"format"`
	URL        string `json:"url,omitempty"`
}
type ConnectionPlan struct {
	Endpoint    *ChannelEndpoint
	AdapterType outbound.OutboundType
}

func (e ChannelEndpoint) AdapterType() outbound.OutboundType {
	if e.ForwardMode == "raw" {
		return outbound.OutboundTypeRaw
	}
	if e.ForwardMode == "passthrough" {
		return outbound.OutboundTypePassthrough
	}
	if e.Protocol == "chat" && (e.Compatibility == "mimo" || e.Compatibility == "legacy_mimo") {
		return outbound.OutboundTypeMimo
	}
	switch e.Protocol {
	case "chat":
		return outbound.OutboundTypeOpenAIChat
	case "responses":
		return outbound.OutboundTypeOpenAIResponse
	case "messages":
		return outbound.OutboundTypeAnthropic
	case "gemini":
		return outbound.OutboundTypeGemini
	case "embeddings":
		return outbound.OutboundTypeOpenAIEmbedding
	case "cloudflare":
		return outbound.OutboundTypeCloudflare
	case "volcengine":
		return outbound.OutboundTypeVolcengine
	case "codex":
		return outbound.OutboundTypeCodex
	default:
		return -1
	}
}

func (config *ConnectionConfig) Validate() error {
	if config == nil {
		return nil
	}
	if config.Version != 1 {
		return fmt.Errorf("unsupported connection config version")
	}
	if config.Selection != "same_protocol" && config.Selection != "configured" {
		return fmt.Errorf("invalid connection selection")
	}
	if len(config.Endpoints) == 0 || len(config.Endpoints) > 32 {
		return fmt.Errorf("connection config requires 1 to 32 endpoints")
	}
	ids := map[string]bool{}
	for _, e := range config.Endpoints {
		if e.ID == "" || len(e.ID) > 64 || ids[e.ID] {
			return fmt.Errorf("invalid or duplicate endpoint id")
		}
		ids[e.ID] = true
		protocolOnly := e
		protocolOnly.ForwardMode = ""
		if protocolOnly.AdapterType() < 0 {
			return fmt.Errorf("unsupported endpoint protocol: %s", e.Protocol)
		}
		if e.URLMode != "base" && e.URLMode != "full" {
			return fmt.Errorf("invalid endpoint URL mode")
		}
		parsedURL, err := validateConnectionURL(e.URL)
		if err != nil {
			return err
		}
		if strings.Contains(e.URL, "{model}") && (!strings.Contains(parsedURL.Path, "{model}") || strings.Contains(parsedURL.RawQuery, "{model}") || strings.Contains(parsedURL.Host, "{model}")) {
			return fmt.Errorf("model placeholder must occur only in the URL path")
		}
		if e.Compatibility != "" && e.Compatibility != "legacy" && e.Compatibility != "mimo" && e.Compatibility != "legacy_mimo" {
			return fmt.Errorf("invalid compatibility profile")
		}
		if strings.Contains(e.Compatibility, "mimo") && e.Protocol != "chat" {
			return fmt.Errorf("MiMo compatibility requires chat protocol")
		}
		switch e.Auth {
		case "default", "bearer", "api_key", "x_api_key", "google", "none", "legacy":
		default:
			return fmt.Errorf("invalid endpoint authentication")
		}
		if e.ForwardMode != "" && e.ForwardMode != "convert" {
			if (e.ForwardMode != "passthrough" && e.ForwardMode != "raw") || (e.Protocol != "chat" && e.Protocol != "responses" && e.Protocol != "messages") || e.URLMode != "base" {
				return fmt.Errorf("unsupported passthrough configuration")
			}
		}
		if strings.Contains(e.URL, "{model}") && (e.URLMode != "full" || (e.Protocol != "gemini" && e.Protocol != "cloudflare")) {
			return fmt.Errorf("model placeholder is only allowed for native full URLs")
		}
		if e.URLMode == "full" && (e.Protocol == "gemini" || e.Protocol == "cloudflare") && !strings.Contains(e.URL, "{model}") {
			return fmt.Errorf("native full URL must contain {model}")
		}
		if len(e.Headers) > 32 {
			return fmt.Errorf("too many endpoint headers")
		}
		for _, h := range e.Headers {
			if !validConnectionHeader(h) {
				return fmt.Errorf("invalid endpoint header")
			}
		}
	}
	switch config.Catalog.Format {
	case "manual", "openai", "anthropic", "gemini", "cloudflare":
	default:
		return fmt.Errorf("invalid model catalog format")
	}
	if config.Catalog.Format != "manual" && !ids[config.Catalog.EndpointID] {
		return fmt.Errorf("model catalog endpoint does not exist")
	}
	if config.Catalog.URL != "" {
		if _, err := validateConnectionURL(config.Catalog.URL); err != nil {
			return err
		}
	}
	return nil
}
func validConnectionHeader(h CustomHeader) bool {
	if h.HeaderKey == "" || len(h.HeaderKey) > 128 || len(h.HeaderValue) > 8192 || strings.ContainsAny(h.HeaderValue, "\r\n") {
		return false
	}
	for _, c := range h.HeaderKey {
		if !strings.ContainsRune("!#$%&'*+-.^_`|~0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", c) {
			return false
		}
	}
	return !strings.EqualFold(h.HeaderKey, "Host") && !strings.EqualFold(h.HeaderKey, "Content-Length")
}
func validateConnectionURL(raw string) (*url.URL, error) {
	if len(raw) > 4096 {
		return nil, fmt.Errorf("endpoint URL too long")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("endpoint requires an HTTP(S) URL without credentials or fragment")
	}
	return u, nil
}

func ProtocolForRequest(r *tm.InternalLLMRequest) string {
	if r == nil {
		return ""
	}
	if r.IsEmbeddingRequest() {
		return "embeddings"
	}
	switch r.RawAPIFormat {
	case tm.APIFormatOpenAIResponse:
		return "responses"
	case tm.APIFormatAnthropicMessage:
		return "messages"
	case tm.APIFormatGeminiContents:
		return "gemini"
	case tm.APIFormatOpenAIChatCompletion:
		return "chat"
	}
	return ""
}
func (c *Channel) ResolveConnectionPlans(r *tm.InternalLLMRequest, groupFormat string) []ConnectionPlan {
	if c.ConnectionConfig == nil {
		types := outbound.ResolveAttemptTypesForChannelDeclared(c.Type, r, groupFormat, c.OutboundFormatOverride, c.UpstreamProtocols)
		plans := make([]ConnectionPlan, 0, len(types))
		for _, t := range types {
			plans = append(plans, ConnectionPlan{AdapterType: t})
		}
		return plans
	}
	if c.ConnectionConfig.Validate() != nil {
		return nil
	}
	plans := []ConnectionPlan{}
	for i := range c.ConnectionConfig.Endpoints {
		e := &c.ConnectionConfig.Endpoints[i]
		if c.SelectedEndpointID != "" && e.ID != c.SelectedEndpointID {
			continue
		}
		if r != nil && r.IsEmbeddingRequest() != (e.Protocol == "embeddings") {
			continue
		}
		if (e.ForwardMode == "raw" || e.ForwardMode == "passthrough") && (!outbound.IsLLMRequestFormat(r) || ProtocolForRequest(r) != e.Protocol) {
			continue
		}
		plans = append(plans, ConnectionPlan{Endpoint: e, AdapterType: e.AdapterType()})
	}
	if c.ConnectionConfig.Selection == "same_protocol" {
		p := ProtocolForRequest(r)
		sort.SliceStable(plans, func(i, j int) bool { return plans[i].Endpoint.Protocol == p && plans[j].Endpoint.Protocol != p })
	}
	return plans
}
func (c *Channel) WithConnectionPlan(p ConnectionPlan) *Channel {
	clone := *c
	if p.Endpoint != nil {
		clone.SelectedEndpointID = p.Endpoint.ID
		clone.Type = p.AdapterType
	}
	return &clone
}
func (c *Channel) ConnectionPlanFor(t outbound.OutboundType) (ConnectionPlan, error) {
	if c.ConnectionConfig == nil {
		return ConnectionPlan{AdapterType: t}, nil
	}
	for i := range c.ConnectionConfig.Endpoints {
		e := &c.ConnectionConfig.Endpoints[i]
		if e.AdapterType() == t && (c.SelectedEndpointID == "" || e.ID == c.SelectedEndpointID) {
			return ConnectionPlan{Endpoint: e, AdapterType: t}, nil
		}
	}
	return ConnectionPlan{}, fmt.Errorf("no configured endpoint for adapter %s", t)
}

// BuildURL joins paths through url.URL, preserving query strings. A full URL is
// never trimmed or given a guessed API prefix.
func (e ChannelEndpoint) BuildURL(modelName string, stream bool) (string, error) {
	u, err := validateConnectionURL(e.URL)
	if err != nil {
		return "", err
	}
	if (e.Protocol == "gemini" || e.Protocol == "cloudflare") && (strings.ContainsAny(modelName, "?#\\") || strings.Contains(modelName, "..")) {
		return "", fmt.Errorf("invalid model path")
	}
	if e.Protocol == "cloudflare" && e.Compatibility == "legacy" {
		modelName = "@cf/" + strings.TrimPrefix(strings.TrimSpace(modelName), "@cf/")
	} else if e.Protocol == "cloudflare" && !strings.HasPrefix(modelName, "@") {
		modelName = "@cf/" + modelName
	}
	if e.URLMode == "full" {
		if strings.Contains(u.Path, "{model}") {
			escaped := strings.ReplaceAll(u.EscapedPath(), "%7Bmodel%7D", escapeConnectionModel(strings.TrimPrefix(modelName, "models/")))
			u.Path, _ = url.PathUnescape(escaped)
			u.RawPath = escaped
		}
	}
	if e.URLMode == "base" {
		root := strings.TrimRight(u.EscapedPath(), "/")
		if root == "" {
			if e.Protocol == "gemini" {
				root = "/v1beta"
			} else if e.Protocol != "cloudflare" {
				root = "/v1"
			}
		}
		switch e.Protocol {
		case "chat":
			u.Path = root + "/chat/completions"
		case "responses", "volcengine", "codex":
			u.Path = root + "/responses"
		case "messages":
			u.Path = root + "/messages"
		case "embeddings":
			u.Path = root + "/embeddings"
		case "gemini":
			u.Path = root + "/models/" + escapeConnectionModel(strings.TrimPrefix(modelName, "models/")) + ":generateContent"
		case "cloudflare":
			u.Path = root + "/ai/run/" + escapeConnectionModel(modelName)
		}
		u.RawPath = u.Path
		u.Path, _ = url.PathUnescape(u.RawPath)
	}
	if e.Protocol == "gemini" && stream {
		escaped := strings.TrimSuffix(u.EscapedPath(), ":generateContent")
		if !strings.HasSuffix(escaped, ":streamGenerateContent") {
			escaped += ":streamGenerateContent"
		}
		u.Path, _ = url.PathUnescape(escaped)
		u.RawPath = escaped
		q := u.Query()
		q.Set("alt", "sse")
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func escapeConnectionModel(name string) string {
	parts := strings.Split(name, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
func (p ConnectionPlan) Adapter() tm.Outbound {
	if p.Endpoint != nil && p.Endpoint.Protocol == "chat" && p.Endpoint.Compatibility == "" && (p.Endpoint.ForwardMode == "" || p.Endpoint.ForwardMode == "convert") {
		return &oa.ChatOutbound{PreserveRequest: true}
	}
	return outbound.Get(p.AdapterType)
}
func (c *Channel) BuildConnectionRequest(ctx context.Context, p ConnectionPlan, adapter tm.Outbound, r *tm.InternalLLMRequest, key string) (*http.Request, error) {
	if p.Endpoint != nil && p.Endpoint.Protocol == "gemini" {
		if _, special := geminicli.ParseCodeAssistCredential(key); special {
			return nil, fmt.Errorf("Gemini CLI credentials require the legacy channel connection")
		}
	}
	var base string
	if p.Endpoint != nil {
		base = p.Endpoint.URL
	} else {
		base = c.GetNormalizedBaseUrlForProtocol(outbound.EndpointProtocolForAdapter(p.AdapterType))
	}
	req, err := adapter.TransformRequest(ctx, r, base, key)
	if err != nil {
		return nil, err
	}
	if p.Endpoint != nil && p.Endpoint.ForwardMode != "raw" {
		target, err := p.Endpoint.BuildURL(r.Model, r.Stream != nil && *r.Stream)
		if err != nil {
			return nil, err
		}
		legacyQueryKey := req.URL.Query().Get("key")
		req.URL, err = url.Parse(target)
		if err != nil {
			return nil, err
		}
		if p.Endpoint.Protocol == "gemini" && p.Endpoint.Auth == "legacy" && legacyQueryKey != "" {
			q := req.URL.Query()
			q.Set("key", legacyQueryKey)
			req.URL.RawQuery = q.Encode()
		}
	}
	c.ApplyConnectionHeaders(req, p, key)
	if p.Endpoint != nil {
		req = req.WithContext(context.WithValue(req.Context(), endpointContextKey{}, p.Endpoint.ID))
	}
	return req, nil
}

// Called after inherited headers, too, so endpoint credentials cannot be silently
// replaced by a different channel-level auth header.
func (c *Channel) ApplyConnectionHeaders(req *http.Request, p ConnectionPlan, key string) {
	for _, h := range c.CustomHeader {
		if h.HeaderKey != "" {
			req.Header.Set(h.HeaderKey, h.HeaderValue)
		}
	}
	if p.Endpoint == nil {
		return
	}
	e := p.Endpoint
	auth := e.Auth
	if auth == "default" {
		switch e.Protocol {
		case "messages":
			auth = "x_api_key"
		case "gemini":
			auth = "google"
		default:
			auth = "bearer"
		}
	}
	if auth != "legacy" {
		for _, h := range []string{"Authorization", "api-key", "x-api-key", "x-goog-api-key"} {
			req.Header.Del(h)
		}
		if key != "" {
			switch auth {
			case "bearer":
				req.Header.Set("Authorization", "Bearer "+key)
			case "api_key":
				req.Header.Set("api-key", key)
			case "x_api_key":
				req.Header.Set("x-api-key", key)
			case "google":
				req.Header.Set("x-goog-api-key", key)
			}
		}
	}
	for _, h := range e.Headers {
		req.Header.Set(h.HeaderKey, h.HeaderValue)
	}
}

type endpointContextKey struct{}

// Validation follows declared interfaces; the retired brand field is not a
// gate for a new Chat interface added to a formerly native channel.
func (c *Channel) RequestRewriteType() outbound.OutboundType {
	if c.ConnectionConfig == nil {
		return c.Type
	}
	for _, e := range c.ConnectionConfig.Endpoints {
		if e.Protocol == "chat" || e.Protocol == "responses" {
			return outbound.OutboundTypeOpenAIChat
		}
	}
	return outbound.OutboundTypeGemini
}

func EndpointIDFromRequest(req *http.Request) string {
	id, _ := req.Context().Value(endpointContextKey{}).(string)
	return id
}
