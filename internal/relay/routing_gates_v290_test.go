package relay

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

// v2.9.0 候选闸门接线的源码守卫（计划「候选闸门断言」的 relay 层）。
//
// 为什么用源码守卫：四态闸门（Key 冷却 / 额度类限流 / 渠道禁用 / 并发占满）
// 的判定内联在 executeRelay 的候选循环里；跑真实 executeRelay 需要完整
// HTTP 服务 + DB 渠道/Key，无法在单测里构造（先例：circuit_accounting_test.go
// 的 TestRelayGoUsesShouldRecordChannelFailureGuard 同策略）。
//
// 迭代器层行为断言见 balancer/gates_v290_test.go；底层组件行为已有既有用例
// （balancer/key_cooldown_test.go、op/ratelimitstore/channel_test.go）。

func readRelaySource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("relay.go")
	if err != nil {
		t.Fatalf("read relay.go: %v", err)
	}
	return string(data)
}

// TestRelayRoutingGatesWiredWithReasons：五道闸门（四态 + v2.9.0 上下文超限）
// 必须逐条接线——跳过留痕 + 紧随其后设置带原因的 lastErr。
// 缺 lastErr 时「全部候选被闸门排除」会退化为无原因的空错误，排查只能靠猜。
func TestRelayRoutingGatesWiredWithReasons(t *testing.T) {
	src := readRelaySource(t)

	gates := []struct {
		name     string
		skipAt   string // 闸门 Skip 调用（或其原因文本）的锚点
		reasonAt string // 紧随其后的 lastErr 赋值锚点
	}{
		{
			name:     "channel disabled",
			skipAt:   `routeIter.Skip(channel.ID, 0, channel.Name, "channel disabled")`,
			reasonAt: `lastErr = fmt.Errorf("channel %s disabled", channel.Name)`,
		},
		{
			name:     "channel concurrency full",
			skipAt:   `"channel concurrency limit reached (%d/%d)"`,
			reasonAt: `lastErr = fmt.Errorf("channel %s concurrency limit reached", channel.Name)`,
		},
		{
			name:     "key cooldown or exhausted",
			skipAt:   `"no available key (all keys in cooldown or disabled)"`,
			reasonAt: `lastErr = fmt.Errorf("channel %s: no available key (all keys in cooldown or disabled)", channel.Name)`,
		},
		{
			name:     "quota-class channel rate limit",
			skipAt:   `"channel rate limit active, remaining cooldown: %ds"`,
			reasonAt: `lastErr = fmt.Errorf("channel %s is temporarily rate limited", channel.Name)`,
		},
		{
			name:     "capability context over limit",
			skipAt:   `capability: context_over_limit`,
			reasonAt: `lastErr = fmt.Errorf("channel %s context limit exceeded: estimated input %d > %d"`,
		},
	}

	for _, gate := range gates {
		skipIdx := strings.Index(src, gate.skipAt)
		if skipIdx < 0 {
			t.Fatalf("gate %q: skip anchor not found in relay.go: %s", gate.name, gate.skipAt)
		}
		reasonIdx := strings.Index(src[skipIdx:], gate.reasonAt)
		if reasonIdx < 0 {
			t.Fatalf("gate %q: lastErr with reason not set after Skip (全候选被该闸门排除时会丢失原因)", gate.name)
		}
		if reasonIdx > 500 {
			t.Fatalf("gate %q: lastErr assignment is %d bytes after Skip, want <= 500 (unrelated block?)", gate.name, reasonIdx)
		}
	}
}

// TestRelayExhaustedFailsClosedWithReason：全部候选被闸门/失败排除后，
// exhausted 分支必须 (1) 把带原因的错误写进 relay log/metrics、(2) 对外只回
// 通用 502 文案（不泄漏内部渠道/冷却细节）、(3) 把 lastErr 返回给调用方且
// 不再发起任何真实转发（fail closed，不静默降级）。
func TestRelayExhaustedFailsClosedWithReason(t *testing.T) {
	src := readRelaySource(t)

	// (1) 原因入库：最近一次带原因的 lastErr 随全量决策记录落库。
	if !strings.Contains(src, "req.metrics.Save(false, lastErr, allAttempts)") {
		t.Fatal("exhausted 分支必须把带原因的 lastErr 写入 relay log（metrics.Save）")
	}

	// (2) 对外通用文案：exhausted 的客户端响应固定走 ErrorClassUnavailable 文案，
	// 不复用 lastErr（防泄漏上游内部细节）。
	const respPrefix = "resp.Error(req.c, http.StatusBadGateway,"
	const respMsg = "clientErrorMessageForClass(ErrorClassUnavailable, http.StatusBadGateway)"
	respIdx := strings.Index(src, respPrefix)
	if respIdx < 0 {
		t.Fatal("exhausted 分支必须写 502 客户端响应")
	}
	msgIdx := strings.Index(src[respIdx:], respMsg)
	if msgIdx < 0 || msgIdx > 160 {
		t.Fatal("exhausted 分支对外必须回通用 502 文案（clientErrorMessageForClass(ErrorClassUnavailable)）")
	}
	if between := src[respIdx+len(respPrefix) : respIdx+msgIdx]; strings.Contains(between, "lastErr") {
		t.Fatal("exhausted 客户端文案不得直接拼接 lastErr（可能泄漏内部渠道/冷却细节）")
	}

	// (3) 带原因错误返回调用方。
	lastErrIdx := strings.Index(src, "if lastErr != nil {")
	if lastErrIdx < 0 {
		t.Fatal("exhausted 分支缺少 lastErr 兜底返回")
	}
	lastErrEnd := lastErrIdx + 120
	if lastErrEnd > len(src) {
		lastErrEnd = len(src)
	}
	if !strings.Contains(src[lastErrIdx:lastErrEnd], "return lastErr") {
		t.Fatal("exhausted 分支必须返回非空 lastErr（带原因），否则调用方/日志只能看到通用兜底错误")
	}

	// (4) fail closed：exhausted 标签之后不得再出现任何转发/迭代调用。
	exIdx := strings.Index(src, "\nexhausted:")
	if exIdx < 0 {
		t.Fatal("exhausted label not found")
	}
	tail := src[exIdx:]
	for _, forbidden := range []string{"routeIter.Next()", "ra.attempt()", "AcquireChannelSlot(", "routeIter.StartAttempt("} {
		if strings.Contains(tail, forbidden) {
			t.Fatalf("exhausted 分支不得继续转发/迭代（发现 %s）：必须 fail closed", forbidden)
		}
	}
}

// TestExhaustedClientMessageDoesNotLeakInternalReason：对外 502 文案是固定的
// 面向用户的提示，不得包含渠道名、冷却秒数、Key 耗尽等内部原因。
func TestExhaustedClientMessageDoesNotLeakInternalReason(t *testing.T) {
	msg := clientErrorMessageForClass(ErrorClassUnavailable, http.StatusBadGateway)
	if strings.TrimSpace(msg) == "" {
		t.Fatal("clientErrorMessageForClass(ErrorClassUnavailable, 502) must not be empty")
	}
	for _, leak := range []string{"cooldown", "no available key", "concurrency", "context limit", "circuit breaker", "disabled"} {
		if strings.Contains(strings.ToLower(msg), leak) {
			t.Fatalf("exhausted client message leaks internal reason %q: %q", leak, msg)
		}
	}
}
