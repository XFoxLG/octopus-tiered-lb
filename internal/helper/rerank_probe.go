package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	m "github.com/lingyuins/octopus/internal/model"
)

const rerankProbeTimeout = 25 * time.Second

func sendRerankProbe(ctx context.Context, channel *m.Channel, key string, scope probeKeyScope, modelName string) *probeRequestOutcome {
	return sendRerankProbeWithHeaders(ctx, channel, key, scope, modelName, nil)
}

func sendRerankProbeWithHeaders(ctx context.Context, channel *m.Channel, key string, scope probeKeyScope, modelName string, headers []m.CustomHeader) *probeRequestOutcome {
	stream, budget := false, rerankProbeTimeout.Milliseconds()
	out := &probeRequestOutcome{Verdict: m.ProbeVerdictUnknown, Protocol: "rerank", KeyScope: scope.String(), RequestStream: &stream, RequestBudgetMS: &budget,
		Evidence: &m.ChannelProbeEvidence{Transport: "direct", Protocol: "rerank", Model: modelName, KeyID: scope.KeyID, KeyScope: scope.String(), RequestStream: &stream, RequestBudgetMS: &budget, ReadState: "not_started", ParseState: "not_read"}}
	if channel == nil {
		out.Summary = "channel is nil"
		return out
	}
	out.Evidence.Source = "saved"
	if channel.ID == 0 {
		out.Evidence.Source = "draft"
	}
	var endpoint *m.ChannelEndpoint
	if channel.ConnectionConfig != nil {
		plan := channel.ResolveRerankConnectionPlan()
		if plan == nil {
			out.Summary = "No declared rerank endpoint"
			return out
		}
		endpoint = plan.Endpoint
		out.EndpointID = endpoint.ID
	} else {
		// Match legacy media forwarding: /v1 is appended unless already present.
		u, err := url.Parse(channel.GetBaseUrl())
		if err != nil {
			out.Summary = err.Error()
			return out
		}
		u.Path = strings.TrimRight(u.Path, "/")
		if !strings.HasSuffix(u.Path, "/v1") {
			u.Path += "/v1"
		}
		endpoint = &m.ChannelEndpoint{Protocol: "rerank", URL: u.String(), URLMode: "base", Auth: "legacy"}
	}
	endpointURL, err := endpoint.BuildURL(modelName, false)
	if err != nil {
		out.Summary = err.Error()
		return out
	}
	body := rerankProbeRequest(modelName)
	probeCtx, cancel := context.WithTimeout(ctx, rerankProbeTimeout)
	defer cancel()
	if deadline, ok := probeCtx.Deadline(); ok {
		remaining := time.Until(deadline).Milliseconds()
		if remaining < budget {
			budget = max(0, remaining)
		}
	}
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, endpointURL, bytes.NewReader(body))
	if err != nil {
		out.Summary = err.Error()
		return out
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(key))
	for _, header := range headers {
		if strings.TrimSpace(header.HeaderKey) != "" {
			req.Header.Set(header.HeaderKey, header.HeaderValue)
		}
	}
	channel.ApplyConnectionHeaders(req, m.ConnectionPlan{Endpoint: endpoint, AdapterType: -1}, strings.TrimSpace(key))
	out.EndpointURL = auditProbeEndpointURL(req.URL, key)
	out.RequestFingerprint = probeFingerprint(req, body)
	out.Evidence.EndpointID, out.Evidence.EndpointURL = out.EndpointID, out.EndpointURL
	out.Evidence.ConfigurationFingerprint = probeConfigurationFingerprint(channel, m.ConnectionPlan{Endpoint: endpoint}, out.RequestFingerprint, modelName, &stream, &budget)
	client, err := ChannelHttpClient(channel)
	if err != nil {
		out.Summary = err.Error()
		return out
	}
	start := time.Now()
	out.Evidence.RequestStartedAt = start
	defer func() { out.LatencyMS = time.Since(start).Milliseconds() }()
	response, _, network, err := doProbeTransport(probeCtx, client, req, m.ConnectionPlan{Endpoint: endpoint}, false, out.Evidence)
	defer func() {
		if probeCtx.Err() != nil {
			network.Cancelled()
		}
		out.Evidence.Network = network.Snapshot()
	}()
	if err != nil {
		out.Summary = truncateProbeSummary(err.Error())
		return out
	}
	defer response.Body.Close()
	out.StatusCode = response.StatusCode
	raw, err := io.ReadAll(io.LimitReader(&probeTimedReader{body: response.Body, startedAt: start, evidence: out.Evidence}, capabilityProbeMaxBody+1))
	if len(raw) > capabilityProbeMaxBody {
		raw = raw[:capabilityProbeMaxBody]
		out.ResponseTruncated = true
	}
	out.Body = string(raw)
	out.Evidence.ParseState = probeParseState(raw)
	out.Evidence.ReadState = "complete"
	if err != nil || out.ResponseTruncated {
		out.Evidence.ReadState = "error"
		out.Evidence.ResponseTruncated = out.ResponseTruncated
		out.ResponseTruncated = true
		out.Summary = "Incomplete or oversized rerank response"
		return out
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		out.Verdict = probeHTTPStatusVerdict(response.StatusCode)
		out.Summary = response.Status
		return out
	}
	if err := validateRerankProbeResponse(raw, 2); err != nil {
		out.Summary = err.Error()
		return out
	}
	out.Verdict = m.ProbeVerdictPass
	out.Evidence.GenerationState = "completed"
	elapsed := time.Since(start).Milliseconds()
	out.Evidence.FirstEffectiveOutputMS = &elapsed
	network.EffectiveOutput(false, true, false)
	out.Summary = "Received complete rerank scores and document indices"
	return out
}

func validateRerankProbeResponse(body []byte, documents int) error {
	var response struct {
		Error   json.RawMessage `json:"error"`
		Results []struct {
			Index          *int     `json:"index"`
			RelevanceScore *float64 `json:"relevance_score"`
			Score          *float64 `json:"score"`
		} `json:"results"`
	}
	if json.Unmarshal(body, &response) != nil || (len(response.Error) > 0 && string(response.Error) != "null") || len(response.Results) != documents {
		return fmt.Errorf("No complete, valid rerank result for the probe documents")
	}
	seen := make(map[int]bool, documents)
	for _, result := range response.Results {
		score := result.RelevanceScore
		if score == nil {
			score = result.Score
		}
		if result.Index == nil || *result.Index < 0 || *result.Index >= documents || seen[*result.Index] || score == nil || math.IsNaN(*score) || math.IsInf(*score, 0) || (result.Score != nil && result.RelevanceScore != nil && *result.Score != *result.RelevanceScore) {
			return fmt.Errorf("Invalid rerank score or document index")
		}
		seen[*result.Index] = true
	}
	return nil
}

func testRerankGroupItem(ctx context.Context, group *m.Group, item m.GroupItem, channel *m.Channel, key m.ChannelKey, result GroupModelTestResult) GroupModelTestResult {
	if group.RequestTimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(group.RequestTimeoutSeconds)*time.Second)
		defer cancel()
	}
	out := sendRerankProbeWithHeaders(ctx, channel, key.ChannelKey, probeKeyScope{Index: -1, KeyID: key.ID}, item.ModelName, group.CustomHeader)
	result.ChannelKeyID = key.ID
	result.Evidence = out.Evidence
	result.Evidence.Source = result.Source
	if result.SnapshotHash != "" {
		result.Evidence.ConfigurationFingerprint = result.SnapshotHash
	}
	result.Protocol, result.EndpointID = out.Protocol, out.EndpointID
	result.StatusCode, result.Passed = out.StatusCode, out.Verdict == m.ProbeVerdictPass
	secrets := []string{key.ChannelKey}
	for _, header := range group.CustomHeader {
		secrets = append(secrets, header.HeaderValue)
	}
	for _, header := range channel.CustomHeader {
		secrets = append(secrets, header.HeaderValue)
	}
	if channel.ConnectionConfig != nil {
		for _, endpoint := range channel.ConnectionConfig.Endpoints {
			for _, header := range endpoint.Headers {
				secrets = append(secrets, header.HeaderValue)
			}
		}
	}
	result.ResponseText = redactProbeDetail(out.Body, secrets...)
	result.Message = redactProbeDetail(out.Summary, secrets...)
	status := m.AttemptFailed
	if result.Passed {
		status = m.AttemptSuccess
	}
	attempts := []m.ChannelAttempt{{ChannelID: channel.ID, ChannelName: channel.Name, ChannelKeyID: key.ID, ModelName: item.ModelName, AttemptNum: 1, Status: status, Duration: int(out.LatencyMS), Msg: result.Message}}
	if out.EndpointURL == "" {
		result.Attempts = 0
		attempts = nil
	}
	recordTestLog(ctx, m.EndpointTypeRerank, item, result, *channel, attempts, int(out.LatencyMS), rerankProbeRequest(item.ModelName), &transmodel.InternalLLMResponse{NonChatObservation: transmodel.ObserveNonChatResponse(strings.NewReader(out.Body), "rerank")})
	return result
}

func rerankProbeRequest(modelName string) []byte {
	body, _ := json.Marshal(map[string]any{"model": modelName, "query": "Which document describes a blue sky?", "documents": []string{"The sky is blue.", "A red apple is on the table."}, "top_n": 2})
	return body
}
