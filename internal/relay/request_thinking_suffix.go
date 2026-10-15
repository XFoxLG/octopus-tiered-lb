package relay

import (
	"github.com/lingyuins/octopus/internal/transformer/model"
	crossThinking "github.com/lingyuins/octopus/internal/transformer/thinking"
)

// applyModelThinkingSuffix strips a recognised thinking suffix from the model
// name and turns it into an explicit reasoning override on the request.
//
// Grammar (CLIProxyAPI-compatible): model(level|budget|none|auto|-1). The
// suffix is a client-side *override*, so it wins over any reasoning fields that
// arrived in the request body — which is why it also sets ReasoningExplicit,
// preventing a group-level default effort from replacing it. Model names whose
// trailing parentheses are not a recognised thinking value are left untouched
// so they keep routing by their original name.
func applyModelThinkingSuffix(req *model.InternalLLMRequest) {
	if req == nil {
		return
	}
	cfg := crossThinking.ParseModelSuffix(req.Model)
	if !cfg.HasSuffix {
		return
	}
	req.Model = cfg.ModelName
	req.ReasoningExplicit = true
	if cfg.Effort != "" {
		// Level suffix: let each outbound protocol translate the semantic tier.
		req.ReasoningEffort = cfg.Effort
		req.ReasoningBudget = nil
		return
	}
	// Numeric/control suffix: an explicit budget wins over any effort keyword.
	budget := cfg.Budget
	req.ReasoningBudget = &budget
	req.ReasoningEffort = ""
}
