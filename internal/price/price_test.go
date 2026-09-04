package price

import (
	"testing"

	"github.com/lingyuins/octopus/internal/model"
)

// setPricesForTest replaces the global llmPrice map for the duration of a test
// and returns a restore function. Callers that invoke matchFallbackPrice
// directly must hold llmPriceLock (RLock) themselves, matching GetLLMPrice.
func setPricesForTest(prices map[string]model.LLMPrice) func() {
	llmPriceLock.Lock()
	old := llmPrice
	llmPrice = prices
	llmPriceLock.Unlock()
	return func() {
		llmPriceLock.Lock()
		llmPrice = old
		llmPriceLock.Unlock()
	}
}

func TestMatchFallbackPrice(t *testing.T) {
	prices := map[string]model.LLMPrice{
		"gpt-4o":            {Input: 1},
		"gpt-4o-mini":       {Input: 2},
		"claude-3-5-sonnet": {Input: 3},
	}
	restore := setPricesForTest(prices)
	t.Cleanup(restore)

	cases := []struct {
		name      string
		modelName string
		want      string // expected matched key, "" means no match
	}{
		// Strategy 1: provider/ prefix stripped
		{"provider prefix", "openai/gpt-4o", "gpt-4o"},
		{"nested provider prefix", "azure/openai/gpt-4o-mini", "gpt-4o-mini"},

		// Strategy 2: whole-word substring, longest match wins
		{"exact match", "gpt-4o", "gpt-4o"},
		{"longest wins", "gpt-4o-mini", "gpt-4o-mini"},
		{"prefix separator", "my-gpt-4o", "gpt-4o"},
		{"surrounding separators", "proxy.gpt-4o.relay", "gpt-4o"},

		// Boundary correctness: alphanumeric粘连 must NOT match
		{"leading alphanum rejected", "xgpt-4o", ""},
		{"trailing alphanum rejected", "gpt-4ox", ""},
		{"trailing uppercase rejected", "gpt-4oMini", ""},

		// No match
		{"unknown model", "totally-unknown-model", ""},
	}

	llmPriceLock.RLock()
	defer llmPriceLock.RUnlock()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchFallbackPrice(tc.modelName)
			switch {
			case tc.want == "" && got != nil:
				t.Fatalf("matchFallbackPrice(%q) = %+v, want nil", tc.modelName, got)
			case tc.want != "" && got == nil:
				t.Fatalf("matchFallbackPrice(%q) = nil, want key %q", tc.modelName, tc.want)
			case tc.want != "" && got.Input != prices[tc.want].Input:
				t.Fatalf("matchFallbackPrice(%q) Input = %v, want %v (key %q)",
					tc.modelName, got.Input, prices[tc.want].Input, tc.want)
			}
		})
	}
}

func TestContainsWholeWord(t *testing.T) {
	cases := []struct {
		s, sub string
		want   bool
	}{
		{"gpt-4o", "gpt-4o", true},
		{"gpt-4o-mini", "gpt-4o", true},
		{"my-gpt-4o", "gpt-4o", true},
		{"a.gpt-4o.b", "gpt-4o", true},
		{"xgpt-4o", "gpt-4o", false},
		{"gpt-4ox", "gpt-4o", false},
		{"gpt-4ogpt-4o", "gpt-4o", false}, // 两次出现都被字母粘连
		{"totally-unknown", "gpt-4o", false},
	}
	for _, tc := range cases {
		if got := containsWholeWord(tc.s, tc.sub); got != tc.want {
			t.Errorf("containsWholeWord(%q, %q) = %v, want %v", tc.s, tc.sub, got, tc.want)
		}
	}
}

// TestGetLLMPriceFromUpstream 验证同步价格查询：只查外部同步 + 托底价格源，
// 不查数据库；外部命中用外部价，未命中回落托底（presets_manual）价，均未命中返回 nil。
func TestGetLLMPriceFromUpstream(t *testing.T) {
	// 模拟外部价格文件同步后的 map：外部条目覆盖了 gpt-4o，deepseek-v4-flash
	// 未被外部覆盖（保留 presets_manual.go 托底价）。
	prices := map[string]model.LLMPrice{
		"gpt-4o":            {Input: 5, Output: 15, CacheRead: 2.5, CacheWrite: 0},
		"gpt-4o-mini":       {Input: 0.15, Output: 0.6, CacheRead: 0.08, CacheWrite: 0},
		"claude-3-5-sonnet": {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
	}
	restore := setPricesForTest(prices)
	t.Cleanup(restore)

	cases := []struct {
		name      string
		modelName string
		wantInput float64
		wantNil   bool
	}{
		{"exact upstream hit", "gpt-4o", 5, false},
		{"exact upstream hit mixed case", "GPT-4O", 5, false},
		{"provider prefix fallback", "openai/gpt-4o", 5, false},
		{"whole-word fallback", "my-gpt-4o-mini", 0.15, false},
		{"no match", "totally-unknown-model", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GetLLMPriceFromUpstream(tc.modelName)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("GetLLMPriceFromUpstream(%q) = %+v, want nil", tc.modelName, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("GetLLMPriceFromUpstream(%q) = nil, want Input %v", tc.modelName, tc.wantInput)
			}
			if got.Input != tc.wantInput {
				t.Fatalf("GetLLMPriceFromUpstream(%q) Input = %v, want %v", tc.modelName, got.Input, tc.wantInput)
			}
		})
	}
}
