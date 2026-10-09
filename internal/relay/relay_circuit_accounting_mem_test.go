package relay

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 源码文本守卫（*_mem_test.go 既有模式）。
//
// 这组断言锁住运行时难以验证、但一旦被回退就会重新引入生产故障的三件事：
//  1. relay.go 的熔断守卫必须走 shouldRecordChannelFailure，不能退回裸 Scope 判定；
//  2. media_relay.go 必须同样带断连豁免，防止两文件逻辑漂移；
//  3. attempt() 的断连分支必须真的带上 SkipFailureAccounting: true——
//     这是契约的「声明侧」，缺少它守卫就形同虚设。

func readRelaySourceFile(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	src, err := os.ReadFile(filepath.Clean(filepath.Join(filepath.Dir(file), name)))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	// 本仓工作区在 core.autocrlf=true 下检出为 CRLF，而下方多处守卫用
	// "\n" 做边界匹配（切取函数体、反向守卫多行字面量）。统一归一为 LF，
	// 否则这些守卫会在 CRLF 工作区里静默失效（永不命中）。
	return strings.ReplaceAll(string(src), "\r\n", "\n")
}

// stripGoComments 去掉整行注释与行尾注释，保留代码结构。
//
// 源码文本守卫必须针对代码而非注释：本仓在守卫上方写了大段解释取舍的中文注释，
// 其中就包含 SkipFailureAccounting 这些字样，若不剥离注释，
// 守卫会被注释自身满足（假阳性）或被注释误触（假阴性）。
// 行尾注释仅在行内不含字符串字面量时剥离，避免误伤包含 "//" 的字符串（如 URL）。
func stripGoComments(src string) string {
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue // 整行注释
		}
		if idx := strings.Index(line, "//"); idx >= 0 {
			head := line[:idx]
			if !strings.Contains(head, `"`) && !strings.Contains(head, "`") {
				line = head
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// readRelayCode 读取并归一化源文件（LF + 去注释），用于结构性守卫。
func readRelayCode(t *testing.T, name string) string {
	t.Helper()
	return stripGoComments(readRelaySourceFile(t, name))
}

func TestRelayGoUsesShouldRecordChannelFailureGuard(t *testing.T) {
	src := readRelayCode(t, "relay.go")

	if !strings.Contains(src, "shouldRecordChannelFailure(result.Decision)") {
		t.Fatal("relay.go 必须用 shouldRecordChannelFailure(result.Decision) 作为熔断守卫；" +
			"裸 `result.Decision.Scope == ScopeNextChannel || result.Decision.Scope == ScopeAbortAll` " +
			"会把客户端断连（Decision 恰为 ScopeAbortAll）计入熔断器，导致健康渠道被误熔断")
	}

	// 反向守卫：旧的裸判定不得回来。
	legacy := "result.Decision.Scope == ScopeNextChannel || result.Decision.Scope == ScopeAbortAll"
	if strings.Contains(src, legacy) {
		t.Fatalf("relay.go 仍含旧的裸 Scope 熔断守卫：%q", legacy)
	}
}

func TestMediaRelayGoHasDisconnectExemption(t *testing.T) {
	src := readRelayCode(t, "media_relay.go")
	raw := readRelaySourceFile(t, "media_relay.go")

	// SkipFailureAccounting 在媒体侧只出现于解释豁免语义的注释里：实际写入该字段
	// 的代码在 type.go 的 markClientCancelIfGone 中。所以这一条查原始文本
	//（防两文件语义漂移：媒体侧必须明确记录断连豁免的存在），
	// 下面两条查去注释后的代码（防真正的接线被拆掉）。
	if !strings.Contains(raw, "SkipFailureAccounting") {
		t.Fatal("media_relay.go 必须包含 SkipFailureAccounting 断连豁免，否则与 relay.go 漂移：" +
			"媒体侧写失败会把 statusCode 置 0，ClassifyRelayError 只能归为 ScopeNextChannel 或 " +
			"ScopeAbortAll，把客户端主动停止误判成上游故障")
	}
	if !strings.Contains(src, "markClientCancelIfGone(&decision, c.Request.Context(), fwdErr)") {
		t.Fatal("media_relay.go 必须调 markClientCancelIfGone 并传 c.Request.Context()；" +
			"operationCtx 基于 context.Background()（context.go），查它永远看不到客户端断连")
	}
	if !strings.Contains(src, "shouldRecordChannelFailure(decision)") {
		t.Fatal("media_relay.go 必须复用 shouldRecordChannelFailure 守卫，避免两文件判定逻辑漂移")
	}

	// 反向守卫：媒体侧不得退回裸 Scope 判定。
	legacy := "if decision.Scope == ScopeNextChannel || decision.Scope == ScopeAbortAll {"
	if strings.Contains(src, legacy) {
		t.Fatal("media_relay.go 仍含旧的裸 Scope 熔断守卫，应改用 shouldRecordChannelFailure")
	}
}

// 契约的「声明侧」守卫：attempt() 的断连豁免分支必须真的带上
// SkipFailureAccounting: true。否则即使 shouldRecordChannelFailure 守卫写得再对，
// 断连仍会被计入熔断——这正是本次修复的核心。
//
// 这里逐分支切片校验，而不是只数全文出现次数：只数次数的话，
// 把标记从断连分支挪到别的分支也能让测试通过。
func TestRelayGoAttemptDeclaresSkipFailureAccounting(t *testing.T) {
	src := readRelayCode(t, "relay.go")

	// decisionSlice 切出从分支入口到其 attemptResult 构造结束之间的代码。
	decisionSlice := func(marker string) string {
		start := strings.Index(src, marker)
		if start < 0 {
			t.Fatalf("relay.go 的 attempt() 缺少分支 %q", marker)
		}
		rest := src[start:]
		end := strings.Index(rest, "attemptResult{")
		if end < 0 {
			t.Fatalf("分支 %q 之后找不到 attemptResult 构造", marker)
		}
		closeBrace := strings.Index(rest[end:], "\n\t\t}\n")
		if closeBrace < 0 {
			t.Fatalf("分支 %q 的 attemptResult 构造未正常结束", marker)
		}
		return rest[:end+closeBrace]
	}

	slice := decisionSlice("if errors.Is(fwdErr, errClientDisconnected) {")
	if !strings.Contains(slice, "SkipFailureAccounting: true") {
		t.Fatal("attempt() 的客户端断连分支缺少 SkipFailureAccounting: true；" +
			"少了它，shouldRecordChannelFailure 守卫无从识别豁免，断连会被当成渠道故障计入熔断器")
	}

	// 反向守卫：空输出重试分支用的是 ScopeSameChannel（天然不进守卫），
	// 不应也不需要带 SkipFailureAccounting；若将来有人给它加上，
	// 说明对守卫语义的理解已经漂移，需要重新评估。
	emptyStart := strings.Index(src, "if errors.Is(fwdErr, errEmptyOutput) {")
	if emptyStart < 0 {
		t.Fatal("relay.go 的 attempt() 缺少空输出重试分支")
	}
	rest := src[emptyStart:]
	if end := strings.Index(rest, "attemptResult{"); end >= 0 {
		if closeBrace := strings.Index(rest[end:], "\n\t\t}\n"); closeBrace >= 0 {
			if strings.Contains(rest[:end+closeBrace], "SkipFailureAccounting") {
				t.Fatal("空输出重试分支（ScopeSameChannel）不应带 SkipFailureAccounting；" +
					"它本来就不进熔断守卫，加上意味着对守卫语义的理解已漂移")
			}
		}
	}
}

// [DONE] 早退必须存在，且必须与 EOF 收尾共用同一套收尾逻辑（不得复制一份，
// 否则 issue #106/#155 的空输出重试语义会在两条路径上漂移）。
//
// 复刻现场：部分上游/中间代理发完 [DONE] 后并不关闭连接，继续等 EOF 会一直
// 阻塞到客户端先超时断开，把一次成功响应记成 client disconnected。
func TestRelayGoHandlesSSEDoneMarkerViaSharedFinalize(t *testing.T) {
	src := readRelayCode(t, "relay.go")

	if !strings.Contains(src, "isSSEDoneMarker(r.data)") {
		t.Fatal("relay.go 必须用 isSSEDoneMarker 识别上游的 [DONE] 标记；" +
			"缺少它时，发完 [DONE] 却不关连接的上游会让请求阻塞到客户端超时断开，" +
			"被误记为 client disconnected")
	}
	if !strings.Contains(src, "sawDoneMarker = true") {
		t.Fatal("relay.go 缺少 sawDoneMarker 标记；[DONE] 必须让该 chunk 走完正常写入" +
			"（客户端要收到入站适配器渲染的终止帧）再收尾，不能当场 return")
	}
	if !strings.Contains(src, "if sawDoneMarker {") {
		t.Fatal("relay.go 缺少 `if sawDoneMarker` 早退分支；[DONE] 之后的请求仍会阻塞等 EOF")
	}

	// [DONE] 早退与 EOF 收尾必须共用 finishAcceptedTerminal：
	// 计数 >= 2 且收尾闭包内保留空输出重试与 stream session 收尾。
	if !strings.Contains(src, "finishAcceptedTerminal := func() error {") {
		t.Fatal("relay.go 缺少 finishAcceptedTerminal 收尾闭包")
	}
	finalizeCalls := strings.Count(src, "return finishAcceptedTerminal()")
	if finalizeCalls < 3 {
		t.Fatalf("relay.go 中 `return finishAcceptedTerminal()` 出现 %d 次，want >= 3"+
			"（[DONE] 早退、ctx.Done、EOF/读失败等路径必须共用同一个收尾闭包，避免语义漂移）", finalizeCalls)
	}

	// 收尾闭包内必须保留 issue #106/#155 的空输出重试判定：
	// 收到 [DONE] 不等于有可见内容。
	finalizeStart := strings.Index(src, "finishAcceptedTerminal := func() error {")
	finalizeBody := src[finalizeStart:]
	if end := strings.Index(finalizeBody, "\n\tfor {"); end > 0 {
		finalizeBody = finalizeBody[:end]
	}
	for _, required := range []string{
		"isRetryEmptyOutputEnabled()",
		"!hasVisibleContent",
		"return errEmptyOutput",
		"ra.streamSession.Finish(nil)",
	} {
		if !strings.Contains(finalizeBody, required) {
			t.Fatalf("finishAcceptedTerminal 缺少 %q；[DONE] 早退会破坏 issue #106/#155 的空输出重试语义"+
				"或 stream session 收尾", required)
		}
	}
}

// SkipFailureAccounting 字段与守卫函数必须存在于 type.go（防止有人把字段删掉
// 却留下调用点，或反之）。
func TestTypeGoDeclaresSkipFailureAccountingContract(t *testing.T) {
	src := readRelayCode(t, "type.go")

	for _, required := range []string{
		"SkipFailureAccounting bool",
		"func shouldRecordChannelFailure(decision RetryDecision) bool",
		"func markClientCancelIfGone(decision *RetryDecision, clientCtx context.Context, err error)",
		"func isSSEDoneMarker(data string) bool",
	} {
		if !strings.Contains(src, required) {
			t.Fatalf("type.go 缺少 %q", required)
		}
	}

	// 守卫的三条判定缺一不可。
	guardBody := src[strings.Index(src, "func shouldRecordChannelFailure"):]
	if end := strings.Index(guardBody, "\n}"); end > 0 {
		guardBody = guardBody[:end]
	}
	for _, required := range []string{"!decision.SkipFailureAccounting", "ScopeNextChannel", "ScopeAbortAll"} {
		if !strings.Contains(guardBody, required) {
			t.Fatalf("shouldRecordChannelFailure 缺少 %q 判定", required)
		}
	}
}
