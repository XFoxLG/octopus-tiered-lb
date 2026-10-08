package model

import (
	"context"
	tm "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"io"
	"strings"
	"testing"
)

func connectionFixture(protocol string) ChannelEndpoint {
	return ChannelEndpoint{ID: protocol, Protocol: protocol, URL: "https://example.com", URLMode: "base", Auth: "default"}
}
func connectionRequest(format tm.APIFormat) *tm.InternalLLMRequest {
	return &tm.InternalLLMRequest{Model: "test-model", RawAPIFormat: format, Messages: []tm.Message{{Role: "developer", Content: tm.MessageContent{Content: connectionString("rules")}}, {Role: "user", Content: tm.MessageContent{Content: connectionString("test")}}}}
}

func TestConnectionURLPathAndQuery(t *testing.T) {
	for _, tc := range []struct {
		protocol, base, mode, model, want string
		stream                            bool
	}{
		{"chat", "https://x.test", "base", "a?b..c", "https://x.test/v1/chat/completions", false},
		{"responses", "https://x.test/proxy/api/v3?tenant=a", "base", "m", "https://x.test/proxy/api/v3/responses?tenant=a", false},
		{"messages", "https://x.test/anthropic/v1", "base", "m", "https://x.test/anthropic/v1/messages", false},
		{"embeddings", "https://x.test/v1", "base", "m", "https://x.test/v1/embeddings", false},
		{"chat", "https://x.test/special/run?z=2&a=1", "full", "m", "https://x.test/special/run?z=2&a=1", false},
		{"chat", "https://x.test/proxy%2Ftenant/run?sig=a%2Fb", "full", "m", "https://x.test/proxy%2Ftenant/run?sig=a%2Fb", false},
		{"chat", "https://x.test/proxy%2Ftenant", "base", "m", "https://x.test/proxy%2Ftenant/chat/completions", false},
		{"gemini", "https://x.test", "base", "models/gemini-test", "https://x.test/v1beta/models/gemini-test:streamGenerateContent?alt=sse", true},
		{"gemini", "https://x.test/custom/{model}:generateContent?tenant=a", "full", "gemini-test", "https://x.test/custom/gemini-test:generateContent?tenant=a", false},
		{"cloudflare", "https://x.test/client/v4/accounts/A", "base", "@hf/org/model", "https://x.test/client/v4/accounts/A/ai/run/@hf/org/model", false},
	} {
		t.Run(tc.protocol+tc.base, func(t *testing.T) {
			e := connectionFixture(tc.protocol)
			e.URL = tc.base
			e.URLMode = tc.mode
			got, err := e.BuildURL(tc.model, tc.stream)
			if err != nil || got != tc.want {
				t.Fatalf("URL=%q err=%v want=%q", got, err, tc.want)
			}
		})
	}
}
func TestConnectionSelectionDoesNotMixLegacyOrSameURLs(t *testing.T) {
	a, b, c := connectionFixture("chat"), connectionFixture("messages"), connectionFixture("responses")
	second := a
	second.ID = "second-chat"
	second.URL = "https://other.test/route"
	ch := Channel{Type: outbound.OutboundTypeGemini, BaseUrls: []BaseUrl{{URL: "https://wrong.test"}}, ConnectionConfig: &ConnectionConfig{Version: 1, Selection: "same_protocol", Endpoints: []ChannelEndpoint{a, b, second, c}, Catalog: ModelCatalog{Format: "manual"}}}
	plans := ch.ResolveConnectionPlans(connectionRequest(tm.APIFormatAnthropicMessage), "raw")
	if len(plans) != 4 || plans[0].Endpoint.ID != "messages" || plans[1].Endpoint.ID != "chat" || plans[2].Endpoint.ID != "second-chat" {
		t.Fatalf("wrong order: %+v", plans)
	}
	selected := ch.WithConnectionPlan(plans[2])
	p, err := selected.ConnectionPlanFor(outbound.OutboundTypeOpenAIChat)
	if err != nil || p.Endpoint.ID != "second-chat" {
		t.Fatal("lost interface identity")
	}
	if ch.Type != outbound.OutboundTypeGemini || ch.SelectedEndpointID != "" {
		t.Fatal("mutated cached channel")
	}
	if _, err := selected.ConnectionPlanFor(outbound.OutboundTypeAnthropic); err == nil {
		t.Fatal("fell back to unrelated interface")
	}
}
func TestConnectionAuthAndStandardChatPreserveRole(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "messages", "gemini", "embeddings", "cloudflare"} {
		t.Run(protocol, func(t *testing.T) {
			e := connectionFixture(protocol)
			ch := Channel{CustomHeader: []CustomHeader{{HeaderKey: "api-key", HeaderValue: "old-secret"}}}
			p := ConnectionPlan{Endpoint: &e, AdapterType: e.AdapterType()}
			r := connectionRequest(tm.APIFormatOpenAIChatCompletion)
			if protocol == "embeddings" {
				return
			} // Embeddings use their dedicated request fixture in helper tests.
			req, err := ch.BuildConnectionRequest(context.Background(), p, p.Adapter(), r, "selected-secret")
			if err != nil {
				t.Fatal(err)
			}
			if req.Header.Get("api-key") != "" {
				t.Fatal("legacy credential leaked")
			}
			auth := req.Header.Get("Authorization")
			if protocol == "gemini" {
				if req.URL.Query().Get("key") != "" || req.Header.Get("x-goog-api-key") != "selected-secret" || auth != "" {
					t.Fatal("Gemini credential duplicated")
				}
			} else if protocol == "messages" {
				if req.Header.Get("x-api-key") != "selected-secret" || auth != "" {
					t.Fatal("Messages auth mismatch")
				}
			} else if auth != "Bearer selected-secret" {
				t.Fatal("missing bearer")
			}
			if EndpointIDFromRequest(req) != e.ID {
				t.Fatal("trace identity missing")
			}
			if protocol == "chat" {
				body, _ := io.ReadAll(req.Body)
				if !strings.Contains(string(body), `"role":"developer"`) {
					t.Fatalf("standard Chat role rewritten: %s", body)
				}
			}
		})
	}
}
func TestConnectionRejectsInvalidAndCrossProtocolPassthrough(t *testing.T) {
	base := connectionFixture("chat")
	for _, mutate := range []func(*ChannelEndpoint){func(e *ChannelEndpoint) { e.Protocol = "invalid"; e.ForwardMode = "raw" }, func(e *ChannelEndpoint) { e.URL = "https://user:password@x.test" }, func(e *ChannelEndpoint) { e.Headers = []CustomHeader{{HeaderKey: "X-Test", HeaderValue: "a\r\nb"}} }, func(e *ChannelEndpoint) {
		e.Protocol = "gemini"
		e.URLMode = "full"
		e.URL = "https://x.test/{model}?x={model}"
	}} {
		e := base
		mutate(&e)
		cfg := &ConnectionConfig{Version: 1, Selection: "same_protocol", Endpoints: []ChannelEndpoint{e}, Catalog: ModelCatalog{Format: "manual"}}
		if cfg.Validate() == nil {
			t.Fatalf("accepted invalid config %+v", e)
		}
	}
	base.ForwardMode = "passthrough"
	ch := Channel{ConnectionConfig: &ConnectionConfig{Version: 1, Selection: "same_protocol", Endpoints: []ChannelEndpoint{base}, Catalog: ModelCatalog{Format: "manual"}}}
	if len(ch.ResolveConnectionPlans(connectionRequest(tm.APIFormatAnthropicMessage), "")) != 0 {
		t.Fatal("passthrough crossed declared protocol")
	}
	if len(ch.ResolveConnectionPlans(connectionRequest(tm.APIFormatOpenAIChatCompletion), "")) != 1 {
		t.Fatal("same-format passthrough missing")
	}
}
func TestConnectionMigrationPreservesLegacyBehavior(t *testing.T) {
	for _, kind := range []outbound.OutboundType{outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeAnthropic, outbound.OutboundTypeGemini, outbound.OutboundTypeMimo, outbound.OutboundTypeCloudflare} {
		t.Run(kind.String(), func(t *testing.T) {
			ch := Channel{Type: kind, BaseUrls: []BaseUrl{{URL: "https://example.com", SuffixMode: "auto"}}, OutboundFormatOverride: "chat_only", CustomHeader: []CustomHeader{{HeaderKey: "X-Test", HeaderValue: "preserved"}}}
			preview := ch.PreviewConnectionMigration()
			if kind == outbound.OutboundTypeGemini {
				if preview.Automatic {
					t.Fatal("ambiguous Gemini model paths were silently migrated")
				}
				return
			}
			if !preview.Automatic {
				t.Fatalf("migration requires review: %s", preview.Reason)
			}
			if !ch.connectionMigrationEquivalent(preview.Config) {
				t.Fatal("not equivalent")
			}
		})
	}
	for _, ch := range []Channel{{Type: outbound.OutboundTypeOpenAIChat, BaseUrls: []BaseUrl{{URL: "https://x.test"}}}, {Type: outbound.OutboundTypeMimo, BaseUrls: []BaseUrl{{URL: "https://x.test"}}}, {Type: outbound.OutboundTypeOpenAIChat, BaseUrls: []BaseUrl{{URL: "https://x.test"}, {URL: "https://x.test"}}}} {
		if ch.PreviewConnectionMigration().Automatic {
			t.Fatal("ambiguous legacy channel auto-migrated")
		}
	}
}
