package balancer

import (
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	ch "github.com/lingyuins/octopus/internal/op/channel"
)

// seedOverrideChannel 把带覆盖配置的渠道放进渠道缓存，测试结束后清理。
// 内存构造即缓存命中（ch.Get 缓存优先），不会落库。
func seedOverrideChannel(t *testing.T, channel model.Channel) {
	t.Helper()
	ch.GetCache().Set(channel.ID, channel)
	t.Cleanup(func() { ch.GetCache().Del(channel.ID) })
}

// clearKeyCooldownsForTest 清空内存冷却表，避免用例间串扰。
func clearKeyCooldownsForTest() {
	globalKeyCooldown.Range(func(key, _ any) bool {
		globalKeyCooldown.Delete(key)
		return true
	})
}

func TestChannelCircuitThresholdOverride(t *testing.T) {
	clearCircuitBreakerForTest()
	seedOverrideChannel(t, model.Channel{ID: 701, CircuitBreakerThreshold: 2})
	// 无覆盖的渠道走全局阈值（默认 5）。
	seedOverrideChannel(t, model.Channel{ID: 702})

	if got := channelCircuitThreshold(701); got != 2 {
		t.Fatalf("channel override threshold = %d, want 2", got)
	}
	if got := channelCircuitThreshold(702); got != 5 {
		t.Fatalf("no-override channel should follow global default: got %d, want 5", got)
	}
	if got := channelCircuitThreshold(999999); got != 5 {
		t.Fatalf("missing channel should follow global default: got %d, want 5", got)
	}
}

func TestChannelCircuitCooldownOverride(t *testing.T) {
	clearCircuitBreakerForTest()
	// 覆盖：基础 10s，最大 40s。tripCount=3 → 10<<2 = 40s（未超上限）。
	seedOverrideChannel(t, model.Channel{ID: 711, CircuitBreakerCooldown: 10, CircuitBreakerMaxCooldown: 40})
	// 只覆盖基础值，最大值跟随全局（600s）。
	seedOverrideChannel(t, model.Channel{ID: 712, CircuitBreakerCooldown: 10})

	if got := channelCircuitCooldown(711, 3); got != 40*time.Second {
		t.Fatalf("channel cooldown override with backoff = %v, want 40s", got)
	}
	if got := channelCircuitCooldown(711, 20); got != 40*time.Second {
		t.Fatalf("channel max cooldown cap = %v, want 40s", got)
	}
	// 只覆盖基础值：退避上限应来自全局 600s（10<<20 会溢出保护 → 600s）。
	if got := channelCircuitCooldown(712, 20); got != 600*time.Second {
		t.Fatalf("base-only override should cap at global max: got %v, want 600s", got)
	}
	// 无覆盖渠道跟随全局默认（60s 基础）。
	if got := channelCircuitCooldown(999999, 1); got != 60*time.Second {
		t.Fatalf("missing channel should follow global: got %v, want 60s", got)
	}
}

func TestRecordFailureUsesChannelThreshold(t *testing.T) {
	clearCircuitBreakerForTest()
	seedOverrideChannel(t, model.Channel{ID: 721, CircuitBreakerThreshold: 2})

	// 第 1 次失败：低于渠道阈值 2，仍 Closed。
	RecordFailure(721, 31, "override-model")
	if tripped, _ := IsTripped(721, 31, "override-model"); tripped {
		t.Fatal("first failure should not trip with channel threshold 2")
	}
	// 第 2 次失败：达到渠道阈值，熔断（全局默认 5 时不会触发）。
	RecordFailure(721, 31, "override-model")
	tripped, _ := IsTripped(721, 31, "override-model")
	if !tripped {
		t.Fatal("second failure should trip circuit with channel threshold 2")
	}
}

func TestKeyCooldownDurationChannelOverride(t *testing.T) {
	clearKeyCooldownsForTest()
	seedOverrideChannel(t, model.Channel{
		ID:                     731,
		KeyCooldownRatelimit:   10,
		KeyCooldownAuthError:   20,
		KeyCooldownServerError: 5,
	})

	if got := keyCooldownDurationForChannel(731, 429, 0); got != 10*time.Second {
		t.Fatalf("429 with channel override = %v, want 10s", got)
	}
	if got := keyCooldownDurationForChannel(731, 401, 0); got != 20*time.Second {
		t.Fatalf("401 with channel override = %v, want 20s", got)
	}
	if got := keyCooldownDurationForChannel(731, 502, 0); got != 5*time.Second {
		t.Fatalf("502 with channel override = %v, want 5s", got)
	}
	// 无覆盖渠道回退全局默认（429 → 300s，401 → 300s，5xx → 30s）。
	if got := keyCooldownDurationForChannel(999999, 429, 0); got != 300*time.Second {
		t.Fatalf("missing channel 429 should follow global: got %v, want 300s", got)
	}
	if got := keyCooldownDurationForChannel(999999, 500, 0); got != 30*time.Second {
		t.Fatalf("missing channel 500 should follow global: got %v, want 30s", got)
	}
	// Retry-After 大于覆盖值时仍取较大者（上游约定不越过）。
	if got := keyCooldownDurationForChannel(731, 429, 60*time.Second); got != 60*time.Second {
		t.Fatalf("retry-after should win over smaller override: got %v, want 60s", got)
	}
}

func TestRecordKeyCooldownWithRetryAfterUsesChannelOverride(t *testing.T) {
	clearKeyCooldownsForTest()
	seedOverrideChannel(t, model.Channel{ID: 741, KeyCooldownRatelimit: 3})

	RecordKeyCooldownWithRetryAfter(741, 51, "override-cooldown-model", 429, 0)
	if !IsKeyOnCooldown(741, 51, "override-cooldown-model") {
		t.Fatal("cooldown should be recorded")
	}
	// 3 秒冷却：2.5 秒后仍未到期（用真实等待太慢，这里直接验证条目存在即可，
	// 到期行为由 TestIsKeyOnCooldownFalseAfterExpiry 覆盖）。
	// 其他模型不受影响（按模型粒度隔离）。
	if IsKeyOnCooldown(741, 51, "other-model") {
		t.Fatal("other model should not be affected by per-model cooldown")
	}
	ClearKeyCooldown(741, 51, "override-cooldown-model")
	if IsKeyOnCooldown(741, 51, "override-cooldown-model") {
		t.Fatal("cooldown should be cleared")
	}
}

// TestCooldownFromConfig 保护退避数学的溢出/上限行为（阶段3 重构出的纯函数）。
func TestCooldownFromConfig(t *testing.T) {
	cases := []struct {
		name            string
		baseCooldownSec int
		maxCooldownSec  int
		tripCount       int
		want            time.Duration
	}{
		{"first trip uses base", 60, 600, 1, 60 * time.Second},
		{"second trip doubles", 60, 600, 2, 120 * time.Second},
		{"cap at max", 60, 600, 10, 600 * time.Second},
		{"zero max falls back to default 600", 300, 0, 1, 300 * time.Second},
		{"overflow shift jumps to max", 64, 600, 40, 600 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cooldownFromConfig(tc.baseCooldownSec, tc.maxCooldownSec, tc.tripCount); got != tc.want {
				t.Fatalf("cooldownFromConfig(%d, %d, %d) = %v, want %v",
					tc.baseCooldownSec, tc.maxCooldownSec, tc.tripCount, got, tc.want)
			}
		})
	}
}
