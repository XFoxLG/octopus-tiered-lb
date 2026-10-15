package balancer

import (
	"fmt"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/model"
)

// TestSessionKeyWithCacheVariants 验证 prompt_cache_key 亲和键的生成与回退：
// 缺失/空白 cache key 退回原有两级键，携带时追加为第三级。
func TestSessionKeyWithCacheVariants(t *testing.T) {
	base := sessionKey(7, "gpt-5")
	if got := sessionKeyWithCache(7, "gpt-5", ""); got != base {
		t.Fatalf("empty cache key = %q, want fallback %q", got, base)
	}
	if got := sessionKeyWithCache(7, "gpt-5", "   "); got != base {
		t.Fatalf("blank cache key = %q, want fallback %q", got, base)
	}
	if got := sessionKeyWithCache(7, "gpt-5", " cache-A "); got != base+":cache-A" {
		t.Fatalf("cache key = %q, want %q", got, base+":cache-A")
	}
	if a, b := sessionKeyWithCache(7, "gpt-5", "cache-A"), sessionKeyWithCache(7, "gpt-5", "cache-B"); a == b {
		t.Fatalf("different cache keys must produce different session keys: %q", a)
	}
}

// TestStickyForCacheKeyIsolationAndEscape 验证：
//   - cache-key 维度粘性可正确读写；
//   - 不同 cache key / 无 cache key 的请求互不命中（会话隔离）；
//   - 「成功率 <0.3 自动解除粘性」逃生门对 cache-key 维度同样生效
//     （GetStickyFor 内部按 entry.ResolvedModel 查统计，与亲和键粒度无关）。
func TestStickyForCacheKeyIsolationAndEscape(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	apiKeyID := int(time.Now().UnixNano()%1_000_000) + 1000
	modelName := fmt.Sprintf("sticky-cache-%d", time.Now().UnixNano())
	const channelID = 91
	const cacheKey = "ck-primary"
	t.Cleanup(func() {
		RemoveStickyFor(apiKeyID, modelName, cacheKey)
		RemoveStickyFor(apiKeyID, modelName, "")
	})

	SetStickyFor(apiKeyID, modelName, cacheKey, channelID, 5, modelName)
	got := GetStickyFor(apiKeyID, modelName, cacheKey, time.Hour)
	if got == nil || got.ChannelID != channelID || got.ChannelKeyID != 5 {
		t.Fatalf("sticky round trip failed: %+v", got)
	}
	if other := GetStickyFor(apiKeyID, modelName, "ck-other", time.Hour); other != nil {
		t.Fatalf("different cache key must not hit the same sticky entry: %+v", other)
	}
	if legacy := GetStickyFor(apiKeyID, modelName, "", time.Hour); legacy != nil {
		t.Fatalf("legacy (no cache key) request must not hit cache-key sticky: %+v", legacy)
	}

	// 逃生门：近窗成功率 <0.3（样本足够）时自动解除粘性。
	recordOutcome(channelID, modelName, false, getMinSamples())
	if still := GetStickyFor(apiKeyID, modelName, cacheKey, time.Hour); still != nil {
		t.Fatalf("sticky must be evicted after success rate collapse: %+v", still)
	}
}

// TestNewIteratorStickyIsScopedToMatchingCacheKey 验证 iterator 层：
// 同一 (apiKey, model) 下，只有携带相同 prompt_cache_key 的请求才会命中粘性渠道。
func TestNewIteratorStickyIsScopedToMatchingCacheKey(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	apiKeyID := int(time.Now().UnixNano()%1_000_000) + 2000
	modelName := fmt.Sprintf("sticky-iter-%d", time.Now().UnixNano())
	const stickyChannel = 104
	const cacheKey = "ck-iter"
	t.Cleanup(func() { RemoveStickyFor(apiKeyID, modelName, cacheKey) })

	group := model.Group{
		SessionKeepTime: 3600,
		Items: []model.GroupItem{
			{ChannelID: 103, ModelName: modelName, Weight: 50},
			{ChannelID: stickyChannel, ModelName: modelName, Weight: 50},
		},
	}
	// 两个渠道都达到最小样本数（否则探索优先会把未探索渠道排到最前，
	// 无法区分「粘性提前」与「探索提前」）。
	recordOutcome(103, modelName, true, getMinSamples()*2)
	recordOutcome(stickyChannel, modelName, true, getMinSamples())

	SetStickyFor(apiKeyID, modelName, cacheKey, stickyChannel, 1, modelName)

	itHit := NewIterator(group, apiKeyID, modelName, nil, cacheKey)
	if !itHit.Next() {
		t.Fatal("iterator has no candidates")
	}
	if !itHit.HasSticky() {
		t.Fatalf("HasSticky() = false, want true for matching cache key")
	}
	if item := itHit.Item(); item.ChannelID != stickyChannel {
		t.Fatalf("cache-key request should hit sticky channel %d, got %d", stickyChannel, item.ChannelID)
	}

	// 不同 cache key 与无 cache key 的请求都不得命中粘性。
	for _, missKey := range []string{"ck-other", ""} {
		itMiss := NewIterator(group, apiKeyID, modelName, nil, missKey)
		if !itMiss.Next() {
			t.Fatal("iterator has no candidates")
		}
		if itMiss.HasSticky() {
			t.Fatalf("HasSticky() = true for non-matching cache key %q", missKey)
		}
	}
}
