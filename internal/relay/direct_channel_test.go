package relay

import (
	"testing"
)

func TestSplitDirectChannelModel(t *testing.T) {
	channel, model, ok := splitDirectChannelModel("my-channel/gpt-4o")
	if !ok || channel != "my-channel" || model != "gpt-4o" {
		t.Fatalf("split = %q, %q, %v; want my-channel, gpt-4o, true", channel, model, ok)
	}
	// 按首个 `/` 切分：模型名本身可含 `/`（如 meta-llama/Llama-3）。
	channel, model, ok = splitDirectChannelModel("ch/org/model")
	if !ok || channel != "ch" || model != "org/model" {
		t.Fatalf("split = %q, %q, %v; want ch, org/model, true", channel, model, ok)
	}
	for _, raw := range []string{"plain-model", "/model", "channel/", ""} {
		if _, _, ok := splitDirectChannelModel(raw); ok {
			t.Fatalf("split(%q) = true, want false", raw)
		}
	}
	// 严格区分大小写且不 trim：带空格的渠道名原样保留，由 GetByName 精确匹配决定成败。
	channel, _, ok = splitDirectChannelModel(" My-Channel /gpt-4o")
	if !ok || channel != " My-Channel " {
		t.Fatalf("split channel = %q, %v; want %q, true", channel, ok, " My-Channel ")
	}
}
