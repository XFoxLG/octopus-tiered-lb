package tokenizer

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// BenchmarkCountTokensLargeMixedText 对 ~400KB 中英混合文本做一次全量计数，
// 用于校准估算采样阈值与慢告警线（v2.8.13）。CI 以固定次数跑一遍留档，不设
// 硬阈值断言（不同 runner 的绝对耗时差异大）。
func BenchmarkCountTokensLargeMixedText(b *testing.B) {
	const paragraph = "The quick brown fox jumps over the lazy dog. 夜色如墨，泼洒在旧城区错落有致的青瓦白墙之上。" +
		"连日来的秋雨带着湿润的寒意，将整条老街浸润得透亮。石板路上积着一层浅浅的水洼，倒映着街角昏黄的路灯。\n"
	text := strings.Repeat(paragraph, 400<<10/len(paragraph)+2)
	limit := 400 << 10
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	text = text[:limit]
	if len(text) < 400<<10-4 {
		b.Fatalf("benchmark text too short: %d bytes", len(text))
	}
	b.SetBytes(int64(len(text)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := CountTokens(text, "gpt-4o"); got <= 0 {
			b.Fatalf("CountTokens returned %d", got)
		}
	}
}
