package relay

import (
	"context"
	"fmt"
	"testing"

	"github.com/lingyuins/octopus/internal/relay/balancer"
)

// newCanceledContext 返回一个已取消的 context（模拟客户端已断连）。
func newCanceledContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// newLiveContext 返回一个未取消的 context（模拟客户端仍在连接）。
// cancel 绑定到 t.Cleanup，避免 vet 的 lostcancel 告警。
func newLiveContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// --- shouldRecordChannelFailure：熔断守卫纯函数 ---
//
// 回归目标（缺陷1）：客户端在转发中途断开时，attempt() 产出的 Decision 是
// ScopeAbortAll（Reason "client disconnected"），历史上 executeRelay 的守卫只看
// `Scope == ScopeNextChannel || Scope == ScopeAbortAll`，于是每次断连都给
// (channelID, keyID, model) 熔断器 +1。阈值默认 5，健康渠道被误熔断后
// HalfOpen 试探又被断连打断 → TripCount++ → 冷却翻倍至上限 →
// Auto 策略 score=-Inf 降权，最终表现为「所有 key 不可用」。
// SkipFailureAccounting 字段是该契约的执行机制。
func TestShouldRecordChannelFailure(t *testing.T) {
	tests := []struct {
		name string
		dec  RetryDecision
		want bool
	}{
		{
			// 缺陷1 核心回归：客户端断连 → 不记熔断。
			name: "client disconnect with abort_all must not count",
			dec:  RetryDecision{Scope: ScopeAbortAll, Reason: "client disconnected", Code: 200, SkipFailureAccounting: true},
			want: false,
		},
		{
			// 对照组：真的 5xx 且已写流，仍然是渠道故障。
			name: "real upstream abort_all counts",
			dec:  RetryDecision{Scope: ScopeAbortAll, Reason: "stream response already written before failure", Code: 500, IsError: true},
			want: true,
		},
		{
			name: "next_channel with skip must not count",
			dec:  RetryDecision{Scope: ScopeNextChannel, SkipFailureAccounting: true},
			want: false,
		},
		{
			name: "next_channel without skip counts",
			dec:  RetryDecision{Scope: ScopeNextChannel, Code: 503, IsError: true},
			want: true,
		},
		{
			name: "same_channel never counts",
			dec:  RetryDecision{Scope: ScopeSameChannel, Code: 429, IsError: true},
			want: false,
		},
		{
			name: "same_channel with skip never counts",
			dec:  RetryDecision{Scope: ScopeSameChannel, SkipFailureAccounting: true},
			want: false,
		},
		{
			name: "none never counts",
			dec:  RetryDecision{Scope: ScopeNone, Code: 400, IsError: true},
			want: false,
		},
		{
			name: "success never counts",
			dec:  RetryDecision{Scope: ScopeNone, Reason: "success", Code: 200},
			want: false,
		},
		{
			name: "unknown scope does not count",
			dec:  RetryDecision{Scope: RetryScope(99)},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRecordChannelFailure(tt.dec); got != tt.want {
				t.Fatalf("shouldRecordChannelFailure(%+v) = %v, want %v", tt.dec, got, tt.want)
			}
		})
	}
}

// --- markClientCancelIfGone：媒体路径断连归一化 ---
//
// 回归目标（缺陷2）：media_relay.go 原本完全没有断连豁免，写失败会把 statusCode
// 置 0，ClassifyRelayError 只能按 EPIPE→网络错误→ScopeNextChannel（记熔断 + 无谓
// 换渠道重试）或 written→ScopeAbortAll（记熔断）归类。
func TestMarkClientCancelIfGone(t *testing.T) {
	// 媒体路径写失败的两种真实归类结果。
	writtenAbort := ClassifyRelayError(0, fmt.Errorf("failed to stream binary response: write: broken pipe"), true)
	if writtenAbort.Scope != ScopeAbortAll {
		t.Fatalf("precondition: written write-failure scope = %v, want ScopeAbortAll", writtenAbort.Scope)
	}
	notWrittenNext := ClassifyRelayError(0, fmt.Errorf("failed to stream binary response: write: broken pipe"), false)
	if notWrittenNext.Scope != ScopeNextChannel {
		t.Fatalf("precondition: unwritten EPIPE scope = %v, want ScopeNextChannel", notWrittenNext.Scope)
	}

	t.Run("client gone marks abort_all as non-channel failure", func(t *testing.T) {
		ctx := newCanceledContext(t)
		dec := writtenAbort
		markClientCancelIfGone(&dec, ctx, fmt.Errorf("failed to stream binary response: write: broken pipe"))
		if !dec.SkipFailureAccounting {
			t.Fatal("SkipFailureAccounting = false, want true when client already gone")
		}
		if dec.Scope != ScopeAbortAll {
			t.Fatalf("Scope changed to %v; markClientCancelIfGone must not alter Scope", dec.Scope)
		}
		if shouldRecordChannelFailure(dec) {
			t.Fatal("shouldRecordChannelFailure = true, want false for client disconnect")
		}
	})

	t.Run("client gone marks next_channel as non-channel failure", func(t *testing.T) {
		ctx := newCanceledContext(t)
		dec := notWrittenNext
		markClientCancelIfGone(&dec, ctx, fmt.Errorf("failed to stream binary response: write: broken pipe"))
		if !dec.SkipFailureAccounting {
			t.Fatal("SkipFailureAccounting = false, want true when client already gone")
		}
		if dec.Scope != ScopeNextChannel {
			t.Fatalf("Scope changed to %v; markClientCancelIfGone must not alter Scope", dec.Scope)
		}
		if shouldRecordChannelFailure(dec) {
			t.Fatal("shouldRecordChannelFailure = true, want false; unwritten EPIPE must not trigger a pointless channel switch")
		}
	})

	t.Run("live client keeps counting", func(t *testing.T) {
		dec := notWrittenNext
		// 客户端仍在连接：这是真实的上游/网络故障，必须照旧计入熔断。
		markClientCancelIfGone(&dec, newLiveContext(t), fmt.Errorf("connection reset by peer"))
		if dec.SkipFailureAccounting {
			t.Fatal("SkipFailureAccounting = true, want false while client is still connected")
		}
		if !shouldRecordChannelFailure(dec) {
			t.Fatal("shouldRecordChannelFailure = false, want true for a genuine network failure")
		}
	})

	t.Run("nil error never marks", func(t *testing.T) {
		dec := RetryDecision{Scope: ScopeNone, Reason: "success", Code: 200}
		markClientCancelIfGone(&dec, newCanceledContext(t), nil)
		if dec.SkipFailureAccounting {
			t.Fatal("success path must not be marked SkipFailureAccounting")
		}
	})

	t.Run("nil receiver and nil context are safe", func(t *testing.T) {
		// 不 panic 即通过；这两条是防御性入参校验。
		markClientCancelIfGone(nil, newCanceledContext(t), fmt.Errorf("boom"))
		dec := writtenAbort
		markClientCancelIfGone(&dec, nil, fmt.Errorf("boom"))
		if dec.SkipFailureAccounting {
			t.Fatal("nil clientCtx must not mark the decision (nothing proves the client left)")
		}
	})
}

// --- isSSEDoneMarker：[DONE] 识别（[DONE] 早退的判定基础）---

func TestIsSSEDoneMarker(t *testing.T) {
	tests := []struct {
		data string
		want bool
	}{
		{"[DONE]", true},
		{" [DONE]", true},
		{"[DONE] ", true},
		{"\t[DONE]\n", true},
		{"", false},
		{"[]", false},
		{`{"object":"chat.completion.chunk"}`, false},
		{"[DONE] extra", false},
		{"data: [DONE]", false}, // sse.Read 已剥离 "data: " 前缀，带前缀不应匹配
	}
	for _, tt := range tests {
		if got := isSSEDoneMarker(tt.data); got != tt.want {
			t.Errorf("isSSEDoneMarker(%q) = %v, want %v", tt.data, got, tt.want)
		}
	}
}

// --- 端到端熔断计数：跳过豁免的失败永不熔断，对照组按阈值熔断 ---
//
// 复刻缺陷1 的现场：同一 (channelID, keyID, modelName) 上反复发生「客户端在转发
// 中途断连」，历史实现会在第 5 次（默认阈值）把健康渠道熔断。
// 用唯一 modelName 隔离，避免与包内其他测试共享 balancer 全局状态。
//
// 「端到端」的含义边界（诚实声明）：这里走的是 **真实的 balancer
// 全局熔断器状态机**（RecordFailure / RecordAutoFailure / IsTripped，
// 阈值与冷却取自 circuit.go），但入口不是 executeRelay：跑 executeRelay
// 需要完整 HTTP 服务 + DB 渠道/key + 候选选择，无法在单测里构造真实的
// 客户端断连时序。因此本用例直接调用 shouldRecordChannelFailure
// （= executeRelay 熔断守卫调的同一个函数）。
//
// 实测证伪结果：把 relay.go 的守卫改回历史写法
// `result.Decision.Scope == ScopeNextChannel || result.Decision.Scope == ScopeAbortAll`
// 后，**本用例仍然 PASS**（因为它不调 executeRelay），而源码文本守卫
// TestRelayGoUsesShouldRecordChannelFailureGuard 立即 FAIL。两者必须共存：
// 单看本用例会把「守卫未接线」误判为已覆盖。
func TestCircuitBreakerNotTrippedByClientDisconnects(t *testing.T) {
	const (
		channelID = 904211
		keyID     = 904212
	)
	modelName := fmt.Sprintf("cb-disconnect-%s", t.Name())

	balancer.RemoveChannelEntries(channelID)
	t.Cleanup(func() {
		balancer.RemoveChannelEntries(channelID)
		balancer.RemoveChannelStats(channelID)
		balancer.RemoveChannelKeyAvailability(channelID)
		balancer.ClearKeyCooldown(channelID, keyID, modelName)
	})

	// 断连决策：与 attempt() 的 client disconnected 分支完全一致。
	disconnect := RetryDecision{
		Scope:                 ScopeAbortAll,
		Reason:                "client disconnected",
		Code:                  200,
		SkipFailureAccounting: true,
	}

	// 阈值默认 5（circuit.go getThreshold，setting 读取失败时兜底 5）。
	// 远超阈值地重复，确保「没熔断」不是因为次数不够。
	const attempts = 12
	for i := 0; i < attempts; i++ {
		if shouldRecordChannelFailure(disconnect) {
			balancer.RecordFailure(channelID, keyID, modelName)
			balancer.RecordAutoFailure(channelID, modelName)
		}
		if tripped, _ := balancer.IsTripped(channelID, keyID, modelName); tripped {
			t.Fatalf("circuit tripped after %d client disconnects; healthy channel must never be tripped by disconnects", i+1)
		}
	}

	// 对照组：把 SkipFailureAccounting 去掉（= 真实渠道故障），必须在阈值处熔断。
	// 证明上面的「没熔断」来自豁免判定，而不是熔断器根本没接线。
	control := disconnect
	control.SkipFailureAccounting = false

	balancer.RemoveChannelEntries(channelID)
	balancer.RemoveChannelStats(channelID)

	var trippedAt int
	for i := 0; i < attempts; i++ {
		if shouldRecordChannelFailure(control) {
			balancer.RecordFailure(channelID, keyID, modelName)
			balancer.RecordAutoFailure(channelID, modelName)
		}
		if tripped, _ := balancer.IsTripped(channelID, keyID, modelName); tripped {
			trippedAt = i + 1
			break
		}
	}
	if trippedAt == 0 {
		t.Fatalf("control group never tripped after %d real failures; circuit breaker wiring is broken", attempts)
	}
	if trippedAt > 5 {
		t.Fatalf("control group tripped at attempt %d, want <= 5 (default threshold)", trippedAt)
	}
}

// attempt() 的断连豁免分支必须带 SkipFailureAccounting，否则守卫形同虚设。
//
// 覆盖范围（诚实声明）：本用例锁定的是「纯函数契约」——给定一个带
// SkipFailureAccounting 的 Decision，shouldRecordChannelFailure 必须返回 false；
// 给定不带该标记的 ScopeAbortAll，必须返回 true。它按 attempt() 的真实取值
// 手工构造 Decision，并**不调用 attempt()**（调用它需要完整的 relayAttempt：
// DB 渠道/key、Iterator、telemetry span），因此它无法发现「生产代码漏写该标记」。
// 那部分由源码文本守卫 TestRelayGoAttemptDeclaresSkipFailureAccounting 逐分支
// 校验。
func TestAttemptDisconnectDecisionSkipsAccounting(t *testing.T) {
	disconnect := RetryDecision{Scope: ScopeAbortAll, Reason: "client disconnected", Code: 200, SkipFailureAccounting: true}
	if !disconnect.SkipFailureAccounting {
		t.Fatal("client disconnected decision must carry SkipFailureAccounting")
	}
	if shouldRecordChannelFailure(disconnect) {
		t.Fatal("client disconnected decision must not be recorded as a channel failure")
	}

	// 对照组（必要）：同样的 Scope，但**不带** SkipFailureAccounting。
	// 少了这一条，上面的断言就是同义反复：shouldRecordChannelFailure
	// 必须因为该标记而改变结果，而不是因为 Scope 本身不计数。
	disconnectUnmarked := RetryDecision{Scope: ScopeAbortAll, Reason: "client disconnected", Code: 200}
	if !shouldRecordChannelFailure(disconnectUnmarked) {
		t.Fatal("未标记的 ScopeAbortAll 必须照旧计入渠道失败；否则 SkipFailureAccounting 不构成豁免的充要条件")
	}

	// 空输出重试（issue #106/#155）用 ScopeSameChannel，天然不进守卫，
	// 这里断言它没有被误加 SkipFailureAccounting 之外的语义变化。
	empty := RetryDecision{Scope: ScopeSameChannel, Reason: "empty output, try another key", Code: 200, IsError: true}
	if shouldRecordChannelFailure(empty) {
		t.Fatal("empty output retry must not count as a channel failure")
	}
}

// 确认 RetryDecision 新增字段不破坏既有零值语义：
// 全仓构造点都是 keyed literal，新 bool 字段零值 false 即「照旧计入」。
func TestRetryDecisionZeroValueKeepsLegacySemantics(t *testing.T) {
	var zero RetryDecision
	if zero.SkipFailureAccounting {
		t.Fatal("zero-value SkipFailureAccounting must be false (backward compatible)")
	}

	legacy := RetryDecision{Scope: ScopeAbortAll, Code: 500, IsError: true}
	if legacy.SkipFailureAccounting {
		t.Fatal("existing keyed literals must default SkipFailureAccounting to false")
	}
	if !shouldRecordChannelFailure(legacy) {
		t.Fatal("legacy failure decisions must still be recorded")
	}

	// String() 不受新字段影响。
	if got := (RetryDecision{Scope: ScopeAbortAll, Reason: "client disconnected"}).String(); got != "abort_all (client disconnected)" {
		t.Fatalf("String() = %q, want %q", got, "abort_all (client disconnected)")
	}
}
