package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	dbmodel "github.com/lingyuins/octopus/internal/model"
	tm "github.com/lingyuins/octopus/internal/transformer/model"
)

type outboundBudgetSnapshot struct {
	State    string
	Value    *int64
	Source   string
	Field    string
	Adjusted bool
}

// The serializer's body is the authority for presence; metadata describes provenance.
func observeOutboundBudget(req *http.Request, internal *tm.InternalLLMRequest) outboundBudgetSnapshot {
	result := outboundBudgetSnapshot{State: "unknown"}
	if req == nil || req.GetBody == nil || internal == nil {
		return result
	}
	body, err := req.GetBody()
	if err != nil {
		return result
	}
	defer body.Close()
	var fields struct {
		MaxTokens           *int64 `json:"max_tokens"`
		MaxCompletionTokens *int64 `json:"max_completion_tokens"`
		MaxOutputTokens     *int64 `json:"max_output_tokens"`
		GenerationConfig    struct {
			MaxOutputTokens *int64 `json:"maxOutputTokens"`
		} `json:"generationConfig"`
	}
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(&fields); err != nil {
		return result
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return result
	}
	choices := []struct {
		field string
		value *int64
	}{{"max_tokens", fields.MaxTokens}, {"max_completion_tokens", fields.MaxCompletionTokens}, {"max_output_tokens", fields.MaxOutputTokens}, {"generationConfig.maxOutputTokens", fields.GenerationConfig.MaxOutputTokens}}
	result.State = "not_set"
	preferred := internal.TransformerMetadata[tm.TransformerMetadataOutboundMaxTokensField]
	for _, item := range choices {
		if item.value == nil {
			continue
		}
		if result.Value == nil || item.field == preferred {
			result.State, result.Value, result.Field = "set", item.value, item.field
		}
	}
	if result.Value != nil {
		meta := internal.TransformerMetadata
		if meta[tm.TransformerMetadataOutboundMaxTokensValue] == strconv.FormatInt(*result.Value, 10) {
			result.Source = meta[tm.TransformerMetadataOutboundMaxTokensSource]
			result.Adjusted = meta[tm.TransformerMetadataOutboundMaxTokensAdjusted] == "true"
		}
		if result.Source == "" && internal.RawAPIFormat == tm.APIFormatOpenAIChatCompletion && (result.Field == "max_tokens" || result.Field == "max_completion_tokens") {
			var raw map[string]json.RawMessage
			if json.Unmarshal(internal.RawRequest, &raw) == nil {
				var v int64
				if b, ok := raw[result.Field]; ok && json.Unmarshal(b, &v) == nil && v == *result.Value {
					result.Source = "client:" + result.Field
				}
			}
		}
	}
	return result
}

func applyBudgetToAttempt(item *dbmodel.RelayAttemptDiagnostics, b outboundBudgetSnapshot) {
	item.OutboundBudgetState = b.State
	item.OutboundMaxTokens, item.OutboundMaxTokensSource, item.OutboundMaxTokensField, item.OutboundMaxTokensAdjusted = b.Value, b.Source, b.Field, b.Adjusted
}
func (trace *relayRequestTrace) recordOutboundMaxTokens(attempt int, b outboundBudgetSnapshot) {
	if trace == nil {
		return
	}
	trace.mutex.Lock()
	defer trace.mutex.Unlock()
	d := trace.ensureDiagnosticsLocked()
	for i := range d.Attempts {
		if d.Attempts[i].Attempt == attempt {
			applyBudgetToAttempt(&d.Attempts[i], b)
			return
		}
	}
	item := dbmodel.RelayAttemptDiagnostics{Attempt: attempt}
	applyBudgetToAttempt(&item, b)
	d.Attempts = append(d.Attempts, item)
}
func (trace *relayRequestTrace) selectDiagnosticAttempt(attempt int) {
	if trace == nil {
		return
	}
	trace.mutex.Lock()
	defer trace.mutex.Unlock()
	d := trace.ensureDiagnosticsLocked()
	d.SelectedAttempt = attempt
	d.OutboundBudgetState = "unknown"
	d.OutboundMaxTokens, d.OutputTextPresent = nil, nil
	d.Task = nil
	d.Network = nil
	d.OutboundMaxTokensSource, d.OutboundMaxTokensField = "", ""
	d.OutboundMaxTokensAdjusted = false
	for _, item := range d.Attempts {
		if item.Attempt != attempt {
			continue
		}
		d.OutboundBudgetState = item.OutboundBudgetState
		if d.OutboundBudgetState == "" {
			d.OutboundBudgetState = "unknown"
		}
		d.OutboundMaxTokens, d.OutboundMaxTokensSource, d.OutboundMaxTokensField, d.OutboundMaxTokensAdjusted = item.OutboundMaxTokens, item.OutboundMaxTokensSource, item.OutboundMaxTokensField, item.OutboundMaxTokensAdjusted
		d.OutputTextPresent = item.OutputTextPresent
		d.Network = item.Network
		d.RobinEnvelopeKind, d.RobinEnvelopeReason = item.RobinEnvelopeKind, item.RobinEnvelopeReason
		if item.Task != nil {
			task := *item.Task
			d.Task = &task
		}
		return
	}
}
