package helper

import (
	"testing"

	"github.com/lingyuins/octopus/internal/model"
)

func TestCapabilityVerdictsPreservePreciseEndpointProtocolAndKeyScope(t *testing.T) {
	run := &model.ChannelProbeRun{
		KeyID: 10,
		Results: []model.ChannelProbeResult{
			{
				Kind:       model.ProbeKindCapability,
				Item:       model.ProbeItemToolCalling,
				Verdict:    model.ProbeVerdictPass,
				EndpointID: "chat-primary",
				Protocol:   "chat",
				Evidence:   &model.ChannelProbeEvidence{ConfigurationFingerprint: "fingerprint-chat-primary"},
			},
			{
				Kind:       model.ProbeKindCapability,
				Item:       model.ProbeItemStructuredOutput,
				Verdict:    model.ProbeVerdictUnsupported,
				EndpointID: "chat-secondary",
				Protocol:   "chat",
				Evidence:   &model.ChannelProbeEvidence{ConfigurationFingerprint: "fingerprint-chat-secondary"},
			},
			{
				Kind:       model.ProbeKindCapability,
				Item:       model.ProbeItemStreaming,
				Verdict:    model.ProbeVerdictPass,
				EndpointID: "messages",
				Protocol:   "messages",
				Evidence:   &model.ChannelProbeEvidence{ConfigurationFingerprint: "fingerprint-messages"},
			},
			{
				Kind:       model.ProbeKindCapability,
				Item:       model.ProbeItemWebSearch,
				Verdict:    model.ProbeVerdictPass,
				EndpointID: "",
				Protocol:   "",
				Evidence:   &model.ChannelProbeEvidence{ConfigurationFingerprint: "fingerprint-legacy"},
			},
			// The last conclusive observation wins for an identical scope.
			{
				Kind:       model.ProbeKindCapability,
				Item:       model.ProbeItemToolCalling,
				Verdict:    model.ProbeVerdictUnsupported,
				EndpointID: "chat-primary",
				Protocol:   "chat",
				Evidence:   &model.ChannelProbeEvidence{ConfigurationFingerprint: "fingerprint-chat-primary-new"},
			},
			// Inconclusive observations never become persisted capability evidence.
			{
				Kind:       model.ProbeKindCapability,
				Item:       model.ProbeItemWebSearch,
				Verdict:    model.ProbeVerdictUnknown,
				EndpointID: "chat-primary",
				Protocol:   "chat",
			},
		},
	}

	verdicts := CapabilityVerdicts(run)
	if len(verdicts) != 4 {
		t.Fatalf("verdict count = %d, want 4 precise scopes: %+v", len(verdicts), verdicts)
	}
	type key struct {
		endpoint   string
		protocol   string
		capability model.CapabilityName
	}
	got := make(map[key]CapabilityVerdict, len(verdicts))
	for _, verdict := range verdicts {
		got[key{verdict.EndpointID, verdict.Protocol, verdict.Capability}] = verdict
		if verdict.KeyID != 10 {
			t.Fatalf("key scope lost: %+v", verdict)
		}
	}
	primary, ok := got[key{"chat-primary", "chat", model.CapabilityToolCalling}]
	if !ok || primary.Supported || primary.ConfigurationFingerprint != "fingerprint-chat-primary-new" {
		t.Fatalf("same-scope update was not retained correctly: %+v", primary)
	}
	if _, ok := got[key{"chat-secondary", "chat", model.CapabilityStructured}]; !ok {
		t.Fatal("second endpoint with the same protocol was collapsed")
	}
	if _, ok := got[key{"messages", "messages", model.CapabilityStreaming}]; !ok {
		t.Fatal("different protocol was collapsed")
	}
	if _, ok := got[key{"", "", model.CapabilityWebSearch}]; !ok {
		t.Fatal("legacy scope was not represented explicitly")
	}
}
