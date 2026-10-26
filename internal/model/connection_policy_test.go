package model

import (
	tm "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"testing"
)

func TestGroupProtocolConstraintsAcrossConnectionGenerations(t *testing.T) {
	request := &tm.InternalLLMRequest{RawAPIFormat: tm.APIFormatOpenAIChatCompletion, Messages: []tm.Message{{Role: "user"}}}
	channels := []*Channel{
		{Type: outbound.OutboundTypeOpenAIChat, UpstreamProtocols: []string{"chat", "responses"}},
		{ConnectionConfig: &ConnectionConfig{Version: 1, Selection: "configured", Catalog: ModelCatalog{Format: "manual"}, Endpoints: []ChannelEndpoint{{ID: "chat", Protocol: "chat", URL: "https://example.test/v1", URLMode: "base", Auth: "default"}, {ID: "responses", Protocol: "responses", URL: "https://example.test/v1", URLMode: "base", Auth: "default"}}}},
	}
	for _, channel := range channels {
		plans, conflicts := channel.ResolveRelayConnectionPlans(request, "responses_only", "inherit")
		if len(plans) != 1 || plans[0].AdapterType != outbound.OutboundTypeOpenAIResponse || len(conflicts) != 1 {
			t.Fatalf("strict selection: %+v %v", plans, conflicts)
		}
		plans, conflicts = channel.ResolveRelayConnectionPlans(request, "", "raw")
		if len(plans) != 1 || plans[0].AdapterType != outbound.OutboundTypeRaw || len(conflicts) == 0 {
			t.Fatalf("raw selection: %+v %v", plans, conflicts)
		}
		selected := channel.WithConnectionPlan(plans[0])
		plan, err := selected.ConnectionPlanFor(outbound.OutboundTypeRaw)
		if err != nil || plan.AdapterType != outbound.OutboundTypeRaw {
			t.Fatalf("lost selected plan: %v", err)
		}
	}
}

func TestGroupForwardModeDoesNotRewriteEndpointPolicy(t *testing.T) {
	request := &tm.InternalLLMRequest{RawAPIFormat: tm.APIFormatOpenAIChatCompletion, Messages: []tm.Message{{Role: "user"}}}
	channel := &Channel{ConnectionConfig: &ConnectionConfig{Version: 1, Selection: "configured", Catalog: ModelCatalog{Format: "manual"}, Endpoints: []ChannelEndpoint{{ID: "chat", Protocol: "chat", URL: "https://example.test/v1", URLMode: "base", Auth: "default", ForwardMode: "raw"}}}}
	plans, conflicts := channel.ResolveRelayConnectionPlans(request, "", "convert")
	if len(plans) != 0 || len(conflicts) != 1 || channel.ConnectionConfig.Endpoints[0].ForwardMode != "raw" {
		t.Fatalf("endpoint conflict silently overwritten: %v %v", plans, conflicts)
	}
}
