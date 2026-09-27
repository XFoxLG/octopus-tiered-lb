package ratelimitstore

import (
	"sync"
	"testing"
)

// consume_tokens_test.go 覆盖 PR B 的 TPM 修复:CheckRateLimit 只用 tokenCount
// 做「预占」门槛,真实用量由 ConsumeTokens 事后回扣。此处验证回扣确实按真实
// token 数扣减(而非每次只算 1)。

func resetTokenBuckets(t *testing.T) {
	t.Helper()
	requestBuckets = sync.Map{}
	tokenBuckets = sync.Map{}
	t.Cleanup(func() {
		requestBuckets = sync.Map{}
		tokenBuckets = sync.Map{}
	})
}

func TestConsumeTokens_DeductsRealTokenCount(t *testing.T) {
	resetTokenBuckets(t)
	const tpm = 1000

	// 门口检查:tokenCount=0 时只预占 1 个 token(usage 尚不可知)。
	if allowed, _, _ := CheckRateLimit(1, "gpt-4o", 0, tpm, 0); !allowed {
		t.Fatal("pre-check should allow")
	}
	// 事后按真实用量回扣:剩余额度应减少接近真实 token 数,而不是 1。
	ConsumeTokens(1, "gpt-4o", tpm, 600)

	// 再扣 500 会超过 1000 的额度(1 + 600 + 500 > 1000),必须被拒。
	if allowed, _, _ := CheckRateLimit(1, "gpt-4o", 0, tpm, 500); allowed {
		t.Fatal("real-token deduction should exhaust the TPM bucket")
	}
}

func TestConsumeTokens_NoopWhenUnconfigured(t *testing.T) {
	resetTokenBuckets(t)
	// tpm<=0 或 tokenCount<=0:安全跳过,不创建 bucket。
	ConsumeTokens(2, "gpt-4o", 0, 100)
	ConsumeTokens(2, "gpt-4o", 100, 0)
	if _, ok := tokenBuckets.Load(rateLimitKey(2, "gpt-4o")); ok {
		t.Fatal("noop ConsumeTokens should not create a bucket")
	}
}
