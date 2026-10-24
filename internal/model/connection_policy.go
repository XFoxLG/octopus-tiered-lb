package model

import (
	"fmt"
	tm "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"strings"
)

// ResolveRelayConnectionPlans applies group constraints to both generations of
// channel configuration. An explicit endpoint mode remains authoritative;
// conflicts fail closed and are returned as diagnostic evidence.
func (c *Channel) ResolveRelayConnectionPlans(r *tm.InternalLLMRequest, groupFormat, mode string) ([]ConnectionPlan, []string) {
	plans := c.ResolveConnectionPlans(r, groupFormat)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "inherit"
	}
	strict := strings.HasSuffix(groupFormat, "_only")
	required := strings.TrimSuffix(groupFormat, "_only")
	if mode == "inherit" && (groupFormat == "raw" || groupFormat == "passthrough") {
		mode = groupFormat
	}
	incoming := ProtocolForRequest(r)
	var result []ConnectionPlan
	var conflicts []string
	for _, plan := range plans {
		protocol := outbound.EndpointProtocolForAdapter(plan.AdapterType)
		if plan.AdapterType == outbound.OutboundTypeGemini {
			protocol = "gemini"
		}
		if plan.AdapterType == outbound.OutboundTypeOpenAIEmbedding {
			protocol = "embeddings"
		}
		if plan.AdapterType == outbound.OutboundTypeMimo {
			protocol = "chat"
		}
		declaredMode := ""
		if plan.Endpoint != nil {
			protocol = plan.Endpoint.Protocol
			declaredMode = plan.Endpoint.ForwardMode
		}
		if protocol == "" && (plan.AdapterType == outbound.OutboundTypeRaw || plan.AdapterType == outbound.OutboundTypePassthrough) {
			protocol = incoming
		}
		if strict && protocol != required {
			conflicts = append(conflicts, fmt.Sprintf("strict protocol %s excludes %s", required, protocol))
			continue
		}
		effective := declaredMode
		if effective == "" {
			if mode != "inherit" {
				effective = mode
			}
		}
		if mode != "inherit" && declaredMode != "" && mode != declaredMode {
			conflicts = append(conflicts, fmt.Sprintf("group mode %s conflicts with endpoint mode %s (%s)", mode, declaredMode, protocol))
			continue
		}
		if effective == "raw" || effective == "passthrough" {
			if incoming != protocol || !outbound.IsLLMRequestFormat(r) || (incoming != "chat" && incoming != "responses" && incoming != "messages") {
				conflicts = append(conflicts, fmt.Sprintf("%s requires the same supported protocol: %s -> %s", effective, incoming, protocol))
				continue
			}
			if plan.Endpoint != nil && plan.Endpoint.URLMode != "base" {
				conflicts = append(conflicts, "raw/passthrough requires a base URL endpoint")
				continue
			}
			if effective == "raw" {
				plan.AdapterType = outbound.OutboundTypeRaw
			} else {
				plan.AdapterType = outbound.OutboundTypePassthrough
			}
		} else if effective == "convert" && (plan.AdapterType == outbound.OutboundTypeRaw || plan.AdapterType == outbound.OutboundTypePassthrough) {
			conflicts = append(conflicts, "convert conflicts with raw/passthrough channel")
			continue
		}
		if plan.Endpoint != nil {
			endpoint := *plan.Endpoint
			endpoint.ForwardMode = effective
			plan.Endpoint = &endpoint
		}
		result = append(result, plan)
	}
	return result, conflicts
}
