package model

import (
	"bytes"
	"context"
	"fmt"
	tm "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"io"
	"reflect"
	"strings"
)

type ConnectionMigrationPreview struct {
	Config    *ConnectionConfig `json:"config,omitempty"`
	Automatic bool              `json:"automatic"`
	Reason    string            `json:"reason"`
}

// Conservative conversion. Inherited group policies, multi-address selection and
// passthrough remain in the legacy reader until explicitly reviewed.
func (c *Channel) PreviewConnectionMigration() ConnectionMigrationPreview {
	if c.ConnectionConfig != nil {
		return ConnectionMigrationPreview{Config: c.ConnectionConfig, Reason: "already_configured"}
	}
	fail := func(reason string) ConnectionMigrationPreview { return ConnectionMigrationPreview{Reason: reason} }
	if len(c.BaseUrls) != 1 {
		return fail("multiple_or_missing_addresses")
	}
	b := c.BaseUrls[0]
	if b.SuffixMode == "custom" || b.Protocol != "" {
		return fail("custom_or_bound_address")
	}
	switch c.Type {
	case outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeOpenAIResponse:
		if len(c.UpstreamProtocols) == 0 && c.OutboundFormatOverride == "" {
			return fail("inherits_group_protocol")
		}
	case outbound.OutboundTypeAnthropic, outbound.OutboundTypeGemini, outbound.OutboundTypeMimo, outbound.OutboundTypeCloudflare:
	default:
		return fail("specialized_legacy_adapter")
	}
	r := &tm.InternalLLMRequest{Model: "migration-model", RawAPIFormat: tm.APIFormatOpenAIChatCompletion, Messages: []tm.Message{{Role: "user", Content: tm.MessageContent{Content: connectionString("migration")}}}}
	plans := c.ResolveConnectionPlans(r, "")
	config := &ConnectionConfig{Version: 1, Selection: "configured", Catalog: ModelCatalog{Format: "manual"}}
	for i, p := range plans {
		if p.AdapterType == outbound.OutboundTypePassthrough || p.AdapterType == outbound.OutboundTypeRaw {
			return fail("passthrough_requires_review")
		}
		base := c.GetNormalizedBaseUrlForProtocol(outbound.EndpointProtocolForAdapter(p.AdapterType))
		adapter := outbound.Get(p.AdapterType)
		old, err := adapter.TransformRequest(context.Background(), r, base, "migration-key")
		if err != nil {
			return fail("legacy_request_unresolved")
		}
		e := ChannelEndpoint{ID: fmt.Sprintf("migrated-%d", i+1), Protocol: connectionProtocol(p.AdapterType), URL: old.URL.String(), URLMode: "full", Auth: "legacy", Compatibility: "legacy"}
		// Gemini's legacy adapter authenticates with a query key. Keep the key at
		// runtime, never serialize the synthetic credential into channel config.
		if e.Protocol == "gemini" {
			clean := *old.URL
			q := clean.Query()
			q.Del("key")
			clean.RawQuery = q.Encode()
			e.URL = clean.String()
		}
		if e.Protocol == "cloudflare" {
			e.URL = strings.ReplaceAll(e.URL, "@cf/migration-model", "{model}")
		}
		if e.Protocol == "gemini" {
			e.URL = strings.ReplaceAll(e.URL, "migration-model", "{model}")
		}
		if p.AdapterType == outbound.OutboundTypeMimo {
			e.Compatibility = "legacy_mimo"
		}
		newPlan := ConnectionPlan{Endpoint: &e, AdapterType: p.AdapterType}
		next, err := c.BuildConnectionRequest(context.Background(), newPlan, newPlan.Adapter(), r, "migration-key")
		if err != nil {
			return fail("conversion_not_equivalent")
		}
		oldBody, _ := io.ReadAll(old.Body)
		nextBody, _ := io.ReadAll(next.Body)
		if !bytes.Equal(oldBody, nextBody) {
			return fail("request_body_differs")
		}
		if old.URL.String() != next.URL.String() {
			return fail("request_path_differs")
		}
		config.Endpoints = append(config.Endpoints, e)
	}
	if len(config.Endpoints) > 0 {
		format := "openai"
		if c.Type == outbound.OutboundTypeGemini {
			format = "gemini"
		}
		if c.Type == outbound.OutboundTypeAnthropic {
			format = "anthropic"
		}
		config.Catalog = ModelCatalog{EndpointID: config.Endpoints[0].ID, Format: format, URL: c.GetNormalizedBaseUrl() + "/models"}
	}
	if config.Validate() != nil {
		return fail("conversion_requires_review")
	}
	if !c.connectionMigrationEquivalent(config) {
		return ConnectionMigrationPreview{Config: config, Reason: "request_variants_require_review"}
	}
	return ConnectionMigrationPreview{Config: config, Automatic: true, Reason: "equivalent"}
}

// Compare every supported conversational ingress, stream mode and inherited group
// policy. This is offline construction only: migration never sends an upstream request.
func (c *Channel) connectionMigrationEquivalent(config *ConnectionConfig) bool {
	nextChannel := *c
	nextChannel.ConnectionConfig = config
	for _, format := range []tm.APIFormat{tm.APIFormatOpenAIChatCompletion, tm.APIFormatOpenAIResponse, tm.APIFormatAnthropicMessage, tm.APIFormatGeminiContents} {
		for _, groupFormat := range []string{"", "chat_only", "responses_only", "messages", "passthrough", "raw"} {
			for _, stream := range []bool{false, true} {
				for _, name := range []string{"migration-model", "provider/model", "@hf/provider/model"} {
					makeRequest := func() *tm.InternalLLMRequest {
						return &tm.InternalLLMRequest{Model: name, RawAPIFormat: format, Stream: &stream, Messages: []tm.Message{{Role: "developer", Content: tm.MessageContent{Content: connectionString("system")}}, {Role: "user", Content: tm.MessageContent{Content: connectionString("migration")}}}}
					}
					oldPlans, newPlans := c.ResolveConnectionPlans(makeRequest(), groupFormat), nextChannel.ResolveConnectionPlans(makeRequest(), groupFormat)
					if len(oldPlans) != len(newPlans) {
						return false
					}
					for i, oldPlan := range oldPlans {
						if oldPlan.AdapterType != newPlans[i].AdapterType {
							return false
						}
						oldReq, oldErr := c.BuildConnectionRequest(context.Background(), oldPlan, oldPlan.Adapter(), makeRequest(), "migration-key")
						newReq, newErr := nextChannel.BuildConnectionRequest(context.Background(), newPlans[i], newPlans[i].Adapter(), makeRequest(), "migration-key")
						if oldErr != nil || newErr != nil {
							return false
						}
						oldBody, e1 := io.ReadAll(oldReq.Body)
						newBody, e2 := io.ReadAll(newReq.Body)
						oldReq.Body.Close()
						newReq.Body.Close()
						if e1 != nil || e2 != nil || oldReq.URL.String() != newReq.URL.String() || oldReq.Method != newReq.Method || !reflect.DeepEqual(oldReq.Header, newReq.Header) || !bytes.Equal(oldBody, newBody) {
							return false
						}
					}
				}
			}
		}
	}
	return true
}
func connectionString(s string) *string { return &s }
func connectionProtocol(t outbound.OutboundType) string {
	switch t {
	case outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeMimo:
		return "chat"
	case outbound.OutboundTypeOpenAIResponse:
		return "responses"
	case outbound.OutboundTypeAnthropic:
		return "messages"
	case outbound.OutboundTypeGemini:
		return "gemini"
	case outbound.OutboundTypeCloudflare:
		return "cloudflare"
	case outbound.OutboundTypeOpenAIEmbedding:
		return "embeddings"
	}
	return ""
}
