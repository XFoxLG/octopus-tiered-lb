package helper

import (
	"context"
	"fmt"
	m "github.com/lingyuins/octopus/internal/model"
	tm "github.com/lingyuins/octopus/internal/transformer/model"
	"io"
	"net/http"
	"strings"
	"time"
)

// Probe requests are synthetic; explicitly encode the wire body for passthrough.
func prepareConnectionProbeRequest(ctx context.Context, ch *m.Channel, plan m.ConnectionPlan, r *tm.InternalLLMRequest, key string) (*tm.InternalLLMRequest, error) {
	if plan.Endpoint == nil || (plan.Endpoint.ForwardMode != "raw" && plan.Endpoint.ForwardMode != "passthrough") {
		return r, nil
	}
	cloned := *r
	switch plan.Endpoint.Protocol {
	case "chat":
		cloned.RawAPIFormat = tm.APIFormatOpenAIChatCompletion
		cloned.RawPath = "/v1/chat/completions"
	case "responses":
		cloned.RawAPIFormat = tm.APIFormatOpenAIResponse
		cloned.RawPath = "/v1/responses"
	case "messages":
		cloned.RawAPIFormat = tm.APIFormatAnthropicMessage
		cloned.RawPath = "/v1/messages"
	}
	endpoint := *plan.Endpoint
	endpoint.ForwardMode = "convert"
	source := m.ConnectionPlan{Endpoint: &endpoint, AdapterType: endpoint.AdapterType()}
	req, err := ch.BuildConnectionRequest(ctx, source, source.Adapter(), &cloned, key)
	if err != nil {
		return nil, err
	}
	defer req.Body.Close()
	cloned.RawRequest, err = io.ReadAll(req.Body)
	return &cloned, err
}

func runConfiguredConnectionProbe(ctx context.Context, ch *m.Channel, run *m.ChannelProbeRun, key, modelName string) []m.ChannelProbeResult {
	results := []m.ChannelProbeResult{}
	if err := ch.ConnectionConfig.Validate(); err != nil {
		return []m.ChannelProbeResult{{Kind: m.ProbeKindProtocol, Verdict: m.ProbeVerdictFail, Summary: err.Error()}}
	}
	for i := range ch.ConnectionConfig.Endpoints {
		if ctx.Err() != nil {
			break
		}
		endpoint := &ch.ConnectionConfig.Endpoints[i]
		plan := m.ConnectionPlan{Endpoint: endpoint, AdapterType: endpoint.AdapterType()}
		selected := ch.WithConnectionPlan(plan)
		request, _ := buildCapabilityProbeRequest(m.ProbeItemTextGeneration, modelName)
		item := m.ProbeItemProtocolChat
		switch endpoint.Protocol {
		case "responses":
			item = m.ProbeItemProtocolResponses
			request.RawAPIFormat = tm.APIFormatOpenAIResponse
		case "messages":
			item = m.ProbeItemProtocolMessages
			request.RawAPIFormat = tm.APIFormatAnthropicMessage
		case "gemini":
			item = m.ProbeItemProtocolGemini
		case "embeddings":
			item = "protocol_embeddings"
			request, _ = buildGroupProbeRequest(m.EndpointTypeEmbeddings, modelName)
		}
		outcome := sendProbeRequest(ctx, selected, key, plan.AdapterType, request)
		result := buildProbeResult(run, m.ProbeKindProtocol, item, outcome, key)
		result.EndpointID = endpoint.ID
		results = append(results, result)
		if endpoint.Protocol == "embeddings" {
			continue
		}
		for _, capability := range []string{m.ProbeItemTextGeneration, m.ProbeItemToolCalling, m.ProbeItemStructuredOutput, m.ProbeItemWebSearch} {
			if ctx.Err() != nil {
				break
			}
			if outcome.Verdict != m.ProbeVerdictPass {
				results = append(results, m.ChannelProbeResult{ChannelID: ch.ID, ModelName: modelName, Kind: m.ProbeKindCapability, Item: capability, EndpointID: endpoint.ID, Verdict: m.ProbeVerdictUnknown, Summary: "Endpoint has not passed protocol verification"})
				continue
			}
			request, err := buildCapabilityProbeRequest(capability, modelName)
			if err != nil {
				continue
			}
			tested := sendProbeRequest(ctx, selected, key, plan.AdapterType, request, capability)
			tested.Verdict = judgeCapabilityResponse(capability, tested)
			row := buildProbeResult(run, m.ProbeKindCapability, capability, tested, key)
			row.EndpointID = endpoint.ID
			results = append(results, row)
		}
	}
	if ctx.Err() == nil && ch.ConnectionConfig.Catalog.Format != "manual" {
		results = append(results, probeChannelModelList(ctx, ch, key, modelName))
	}
	return results
}

func testConfiguredChannel(ctx context.Context, ch m.Channel) (*ChannelTestSummary, error) {
	if err := ch.ConnectionConfig.Validate(); err != nil {
		return nil, err
	}
	if ch.SkipModelTest {
		return nil, fmt.Errorf("model tests are disabled for this channel")
	}
	name := fallbackModelName(&ch)
	if name == "" {
		return nil, fmt.Errorf("a model is required for endpoint testing")
	}
	summary := &ChannelTestSummary{}
	for i := range ch.ConnectionConfig.Endpoints {
		endpoint := &ch.ConnectionConfig.Endpoints[i]
		plan := m.ConnectionPlan{Endpoint: endpoint, AdapterType: endpoint.AdapterType()}
		for _, key := range ch.Keys {
			if !key.Enabled || strings.TrimSpace(key.ChannelKey) == "" {
				continue
			}
			if !m.ModelMatches(key.SupportedModels, name) {
				continue
			}
			if ctx.Err() != nil {
				return summary, ctx.Err()
			}
			start := time.Now()
			request, _ := buildGroupProbeRequest(m.EndpointTypeChat, name)
			if endpoint.Protocol == "embeddings" {
				request, _ = buildGroupProbeRequest(m.EndpointTypeEmbeddings, name)
			}
			outcome := sendProbeRequest(ctx, ch.WithConnectionPlan(plan), key.ChannelKey, plan.AdapterType, request)
			secrets := []string{key.ChannelKey}
			for _, h := range ch.CustomHeader {
				secrets = append(secrets, h.HeaderValue)
			}
			for _, e := range ch.ConnectionConfig.Endpoints {
				for _, h := range e.Headers {
					secrets = append(secrets, h.HeaderValue)
				}
			}
			row := ChannelTestResult{EndpointID: endpoint.ID, BaseURL: redactProbeDetail(endpoint.URL, secrets...), KeyRemark: key.Remark, KeyMasked: maskSecret(key.ChannelKey), StatusCode: outcome.StatusCode, Passed: outcome.Verdict == m.ProbeVerdictPass, LatencyMS: time.Since(start).Milliseconds(), Message: redactProbeDetail(outcome.Summary, secrets...), ResponseBody: redactProbeDetail(outcome.Body, secrets...)}
			if row.StatusCode == http.StatusTooManyRequests {
				row.Passed = false
			}
			summary.Passed = summary.Passed || row.Passed
			summary.Results = append(summary.Results, row)
		}
	}
	return summary, nil
}
