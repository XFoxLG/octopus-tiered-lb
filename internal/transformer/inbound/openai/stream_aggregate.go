package openai

import (
	"github.com/lingyuins/octopus/internal/transformer/model"
	"sort"
	"strings"
)

type toolTextBuilder struct{ name, arguments strings.Builder }

func mergeToolCallLinear(calls []model.ToolCall, delta model.ToolCall, builders map[int]*toolTextBuilder) []model.ToolCall {
	builder := builders[delta.Index]
	if builder == nil {
		builder = &toolTextBuilder{}
		builders[delta.Index] = builder
	}
	builder.name.WriteString(delta.Function.Name)
	builder.arguments.WriteString(delta.Function.Arguments)
	for index := range calls {
		if calls[index].Index == delta.Index {
			if delta.ID != "" {
				calls[index].ID = delta.ID
			}
			if delta.Type != "" {
				calls[index].Type = delta.Type
			}
			return calls
		}
	}
	delta.Function.Name, delta.Function.Arguments = "", ""
	return append(calls, delta)
}

// Some compatible providers emit tool-call indexes out of order. Clients and
// downstream tools still expect the aggregate to follow the declared index.
func sortToolCallsByIndex(calls []model.ToolCall) {
	sort.SliceStable(calls, func(i, j int) bool { return calls[i].Index < calls[j].Index })
}
