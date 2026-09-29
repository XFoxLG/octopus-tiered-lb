package client

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/utils/xurl"
)

// 本文件覆盖 v2.6.0 的一处缺陷：internal/client 把 600s 硬编码为 http.Client.Timeout。
//
// http.Client.Timeout 的语义（$GOROOT/src/net/http/client.go）包含 connection time、
// redirects **以及 reading the response body**，且计时器在 Do 返回后仍在运行、会中断
// Response.Body 的读取。因此任何超过 600s 的流式（SSE）响应都会被 client 主动砍断：
// relay 侧收到的是一个普通读取错误而非断连哨兵，长时程 agent（reasoning 模型 + 长输出）
// 在 duration ≈ 600000ms 处被截断，客户端收不到 [DONE]。
//
// 修复方式：default 档改为 Client.Timeout=0（不限）+ Transport.ResponseHeaderTimeout，
// 后者只限制「等响应头」的时间，不影响流式 body 读取；同时补建连超时。
// 分桶粒度保持两档不变（default / short），内存零增量。

// setResponseHeaderTimeoutForTest 临时覆盖 default 档的响应头超时，让测试快速完成。
// 不用 t.Setenv：responseHeaderTimeout 在 init() 里一次性读取，运行期改环境变量无效。
func setResponseHeaderTimeoutForTest(t *testing.T, d time.Duration) {
	t.Helper()
	restore := responseHeaderTimeout
	responseHeaderTimeout = d
	t.Cleanup(func() { responseHeaderTimeout = restore })
}

// resetClientCachesForTest 清空包级 client 缓存并在使用后还原。
//
// 必要性：systemDirectClient / customProxyClients 缓存的是**已构造好**的 *http.Client，
// 其 Transport.ResponseHeaderTimeout 在构造时就已经定格。若不清空，先前测试（或先前
// 包内测试函数）注入的 responseHeaderTimeout 会被静默沿用，本次断言就是在验证陈旧对象。
//
// 这也是本包测试一律不加 t.Parallel() 的原因：它们共享这些全局单例。
func resetClientCachesForTest(t *testing.T) {
	t.Helper()

	clientLock.Lock()
	restoreSystemDirect, restoreSystemProxy, restoreSystemURL := systemDirectClient, systemProxyClient, systemProxyURL
	restoreShortDirect, restoreShortProxy := shortTimeoutDirectClient, shortTimeoutProxyClient
	systemDirectClient, systemProxyClient, systemProxyURL = nil, nil, ""
	shortTimeoutDirectClient, shortTimeoutProxyClient = nil, nil
	clientLock.Unlock()

	customProxyClientsLock.Lock()
	restoreCustomProxy := customProxyClients
	customProxyClients = make(map[string]map[string]*http.Client)
	customProxyClientsLock.Unlock()

	t.Cleanup(func() {
		clientLock.Lock()
		systemDirectClient, systemProxyClient, systemProxyURL = restoreSystemDirect, restoreSystemProxy, restoreSystemURL
		shortTimeoutDirectClient, shortTimeoutProxyClient = restoreShortDirect, restoreShortProxy
		clientLock.Unlock()

		customProxyClientsLock.Lock()
		customProxyClients = restoreCustomProxy
		customProxyClientsLock.Unlock()
	})
}

// newSlowSSEServer 立即返回响应头（200 + text/event-stream），随后按 interval 缓慢
// 发送 count 条 SSE 事件。整体时长 = count*interval，可远大于响应头超时。
func newSlowSSEServer(count int, interval time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		for i := 1; i <= count; i++ {
			if r.Context().Err() != nil {
				return
			}
			if _, err := w.Write([]byte("data: event-" + strconv.Itoa(i) + "\n\n")); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(interval)
		}
	}))
}

// readSSEEvents 完整读取 SSE 流，返回收到的 data: 事件数与读取过程中的错误。
func readSSEEvents(c *http.Client, url string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	count := 0
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data:") {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		return count, err
	}
	return count, nil
}

// TestLongTaskClientDoesNotCutSlowStream 核心回归测试。
//
// 前半段断言修复后的 default 档客户端能在响应头超时（250ms）远小于整体流时长（~720ms）
// 的情况下完整读完全部事件。
//
// 后半段是**自证对照**：用修复前的语义（Client.Timeout 覆盖 body 读取）构造客户端，
// 在同一条流上必须失败。它证明本测试在未修复代码上必然失败——否则测试本身已失效。
func TestLongTaskClientDoesNotCutSlowStream(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })

	setResponseHeaderTimeoutForTest(t, 250*time.Millisecond)

	const events = 6
	const interval = 120 * time.Millisecond // 总时长 ≈ 720ms，远超 250ms 响应头超时
	server := newSlowSSEServer(events, interval)
	t.Cleanup(server.Close)

	// 修复后：default 档 = Client.Timeout 0 + Transport.ResponseHeaderTimeout。
	fixed, err := newHTTPClientNoProxyWithTimeoutConfig(longTaskTimeoutConfig())
	if err != nil {
		t.Fatalf("newHTTPClientNoProxyWithTimeoutConfig: %v", err)
	}
	if fixed.Timeout != 0 {
		t.Fatalf("long task client Timeout = %v, want 0 (no overall deadline)", fixed.Timeout)
	}

	got, readErr := readSSEEvents(fixed, server.URL)
	if readErr != nil {
		t.Fatalf("long task client was cut off while reading the SSE body: %v", readErr)
	}
	if got != events {
		t.Fatalf("received %d SSE events, want %d (stream truncated)", got, events)
	}

	// 对照：修复前的语义 —— Client.Timeout 覆盖整个 body 读取过程。
	legacy, err := newHTTPClientNoProxyWithTimeoutConfig(clientTimeoutConfig{
		overallTimeout: responseHeaderTimeout,
		headerTimeout:  0,
	})
	if err != nil {
		t.Fatalf("newHTTPClientNoProxyWithTimeoutConfig (legacy): %v", err)
	}
	if legacy.Timeout == 0 {
		t.Fatal("legacy client should carry an overall timeout")
	}

	legacyGot, legacyErr := readSSEEvents(legacy, server.URL)
	if legacyErr == nil && legacyGot == events {
		t.Fatalf("legacy Client.Timeout client unexpectedly completed the stream (%d events); "+
			"this regression test would no longer catch the bug", legacyGot)
	}
	if legacyErr == nil {
		t.Fatalf("legacy client read only %d/%d events without error", legacyGot, events)
	}
	var netErr net.Error
	if !errors.As(legacyErr, &netErr) || !netErr.Timeout() {
		t.Logf("legacy cutoff error (expected a timeout): %v", legacyErr)
	} else {
		t.Logf("legacy cutoff error as expected: %v", legacyErr)
	}
}

// TestLongTaskClientTimesOutWhenHeadersNeverArrive 反向保证：ResponseHeaderTimeout
// 仍能防住「上游连响应头都不返回」的僵死连接 —— 这正是 Client.Timeout 原本该管的事。
//
// 同时锁定错误类型：stdlib 的 timeoutError 既满足 net.Error.Timeout()==true，
// 也满足 errors.Is(err, context.DeadlineExceeded)。relay 的错误分类
// （internal/relay/type.go 的 isTimeoutError）依赖这一点，改动不能悄悄换掉错误类型。
func TestLongTaskClientTimesOutWhenHeadersNeverArrive(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })

	setResponseHeaderTimeoutForTest(t, 250*time.Millisecond)

	// handler 收到请求后长时间不写任何东西（远超响应头超时）。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)

	c, err := newHTTPClientNoProxyWithTimeoutConfig(longTaskTimeoutConfig())
	if err != nil {
		t.Fatalf("newHTTPClientNoProxyWithTimeoutConfig: %v", err)
	}

	start := time.Now()
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	resp, err := c.Do(req)
	elapsed := time.Since(start)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected a response-header timeout, got a successful response")
	}
	if elapsed > 4*time.Second {
		t.Fatalf("response header timeout not enforced: took %v", elapsed)
	}

	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("err = %v (%T), want a net.Error with Timeout()==true", err, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want errors.Is(err, context.DeadlineExceeded) == true", err)
	}
	t.Logf("stalled upstream failed as expected after %v: %v", elapsed, err)
}

// TestLongTaskConfigDisablesOverallTimeout 锁定 default 档的超时组合：
// 整体不限时（0）+ 响应头超时等于当前生效值。
func TestLongTaskConfigDisablesOverallTimeout(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })

	setResponseHeaderTimeoutForTest(t, 1234*time.Millisecond)

	c, err := newHTTPClientNoProxyWithTimeoutConfig(longTaskTimeoutConfig())
	if err != nil {
		t.Fatalf("newHTTPClientNoProxyWithTimeoutConfig: %v", err)
	}
	if c.Timeout != 0 {
		t.Fatalf("Client.Timeout = %v, want 0", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", c.Transport)
	}
	if tr.ResponseHeaderTimeout != responseHeaderTimeout {
		t.Fatalf("ResponseHeaderTimeout = %v, want %v", tr.ResponseHeaderTimeout, responseHeaderTimeout)
	}
}

// TestShortTaskConfigKeepsOverallTimeout 锁定 short 档（后台短任务）语义**未被本次修复
// 改变**：仍保留 30s 整体超时，且不额外设置 ResponseHeaderTimeout（避免重复计时）。
// short 档服务的是连通性探测 / 延迟测量 / 模型拉取等非流式短请求，整体超时是正确语义。
func TestShortTaskConfigKeepsOverallTimeout(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })

	cfg := shortTaskTimeoutConfig()
	if cfg.overallTimeout != shortTaskTimeout {
		t.Fatalf("short bucket overallTimeout = %v, want %v", cfg.overallTimeout, shortTaskTimeout)
	}
	if cfg.headerTimeout != 0 {
		t.Fatalf("short bucket headerTimeout = %v, want 0 (unchanged behavior)", cfg.headerTimeout)
	}

	c, err := newHTTPClientNoProxyWithTimeoutConfig(cfg)
	if err != nil {
		t.Fatalf("newHTTPClientNoProxyWithTimeoutConfig: %v", err)
	}
	if c.Timeout != shortTaskTimeout {
		t.Fatalf("short bucket Client.Timeout = %v, want %v", c.Timeout, shortTaskTimeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", c.Transport)
	}
	if tr.ResponseHeaderTimeout != 0 {
		t.Fatalf("short bucket ResponseHeaderTimeout = %v, want 0", tr.ResponseHeaderTimeout)
	}

	// 自定义代理路径的 short 档同样保持整体超时。
	pc, err := newHTTPClientCustomProxyWithTimeoutConfig("http://127.0.0.1:1", cfg)
	if err != nil {
		t.Fatalf("newHTTPClientCustomProxyWithTimeoutConfig: %v", err)
	}
	if pc.Timeout != shortTaskTimeout {
		t.Fatalf("custom proxy short bucket Client.Timeout = %v, want %v", pc.Timeout, shortTaskTimeout)
	}
}

// TestCustomProxyClientBucketStillTwoBuckets 锁定分桶数量与边界**没有增加**。
//
// 每多一档，customProxyClients[bucket][proxyURL] 就会为每个 proxyURL 多一个 client +
// 一份 clone 的 Transport + 一套独立空闲连接池，所以「不新增档位」是本次修复的硬约束。
// timeout<=0（转发链路）必须归入 default，与 helper.channelHTTPClient 的
// `short := timeout > 0 && timeout <= 30*time.Second` 判定保持一致。
func TestCustomProxyClientBucketStillTwoBuckets(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, bucketDefault},                                  // 转发链路：整体不限时
		{-time.Second, bucketDefault},                       // 非法值同样归入 default，绝不落进 short
		{600 * time.Second, bucketDefault},                  // 历史硬编码值仍归 default
		{12345 * time.Second, bucketDefault},                // 任意长值
		{time.Nanosecond, bucketShort},                      // 最短的短任务
		{10 * time.Second, bucketShort},                     // 自定义短超时
		{shortTaskTimeout, bucketShort},                     // 边界：等于 30s
		{shortTaskTimeout + time.Nanosecond, bucketDefault}, // 边界：刚超过 30s
	}

	seen := map[string]struct{}{}
	for _, tc := range cases {
		got := customProxyClientBucket(tc.in)
		seen[got] = struct{}{}
		if got != tc.want {
			t.Errorf("customProxyClientBucket(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("bucket count = %d (%v), want exactly 2 (memory zero-increment constraint)", len(seen), seen)
	}
	if _, ok := seen[bucketShort]; !ok {
		t.Fatalf("missing %q bucket", bucketShort)
	}
	if _, ok := seen[bucketDefault]; !ok {
		t.Fatalf("missing %q bucket", bucketDefault)
	}
}

// TestTimeoutConfigForBucketMatchesBucketSemantics 锁定「缓存 key」与「实际生效的超时
// 语义」严格一致：同一 (bucket, proxyURL) 只会构造一个 client，若按请求方的原始 timeout
// 施加超时，后来的请求会静默沿用第一个请求的超时值。
func TestTimeoutConfigForBucketMatchesBucketSemantics(t *testing.T) {
	setResponseHeaderTimeoutForTest(t, 999*time.Millisecond)

	if cfg := timeoutConfigForBucket(bucketDefault, 0); cfg.overallTimeout != 0 || cfg.headerTimeout != responseHeaderTimeout {
		t.Errorf("default/0 config = %+v, want overall 0 + header %v", cfg, responseHeaderTimeout)
	}
	if cfg := timeoutConfigForBucket(bucketDefault, 600*time.Second); cfg.overallTimeout != 0 {
		t.Errorf("default/600s config = %+v, want overall 0 (no new bucket, no overall deadline)", cfg)
	}
	if cfg := timeoutConfigForBucket(bucketShort, 10*time.Second); cfg.overallTimeout != 10*time.Second {
		t.Errorf("short/10s config = %+v, want overall 10s (caller-specified short timeout preserved)", cfg)
	}
	if cfg := timeoutConfigForBucket(bucketShort, 0); cfg.overallTimeout != 0 || cfg.headerTimeout != responseHeaderTimeout {
		t.Errorf("short/0 config = %+v, want long-task config (timeout 0 means unlimited overall)", cfg)
	}
}

// TestResolveResponseHeaderTimeout 锁定环境变量解析：空值 / 非法值 / <=0 一律回退默认，
// 绝不产生「无响应头超时」的退化值（那会让 default 档彻底失去上游僵死保护）。
func TestResolveResponseHeaderTimeout(t *testing.T) {
	fallback := 600 * time.Second
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"", fallback},
		{"   ", fallback},
		{"abc", fallback},
		{"0", fallback},
		{"-5", fallback},
		{"1.5", fallback},
		{"30", 30 * time.Second},
		{" 45 ", 45 * time.Second},
		{"3600", time.Hour},
	}
	for _, tc := range cases {
		if got := resolveResponseHeaderTimeout(tc.raw, fallback); got != tc.want {
			t.Errorf("resolveResponseHeaderTimeout(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// TestWithDialTimeoutCancelsStalledDial 验证建连超时确实生效。
//
// 必要性：Client.Timeout 改为 0 之后，「连不上但也不报错」的僵死拨号不再有任何内置上限
// （直连用的 xurl.SafeDialContext 内部是裸 &net.Dialer{}；socks5 原先用的
// socksDialer.Dial 内部走 context.Background()）。
func TestWithDialTimeoutCancelsStalledDial(t *testing.T) {
	blocking := func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	wrapped := withDialTimeout(150*time.Millisecond, blocking)
	start := time.Now()
	_, err := wrapped(context.Background(), "tcp", "127.0.0.1:1")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("stalled dial should fail once the dial timeout fires")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("dial timeout not enforced: stalled dial took %v", elapsed)
	}
	t.Logf("stalled dial failed after %v: %v", elapsed, err)
}

// TestWithDialTimeoutDoesNotExtendTighterCallerDeadline 验证包装不会放宽调用方已有的
// 更紧 deadline（否则 short 档的请求 ctx 超时会建连阶段被拉长到 30s）。
func TestWithDialTimeoutDoesNotExtendTighterCallerDeadline(t *testing.T) {
	blocking := func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	wrapped := withDialTimeout(30*time.Second, blocking)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := wrapped(ctx, "tcp", "127.0.0.1:1")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("dial should fail when the caller deadline fires")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("wrapper extended the caller's tighter deadline: took %v", elapsed)
	}
}

// TestWithDialTimeoutZeroMeansNoExtraLimit 验证 timeout<=0 时包装是纯透传。
func TestWithDialTimeoutZeroMeansNoExtraLimit(t *testing.T) {
	calls := 0
	inner := func(ctx context.Context, network, addr string) (net.Conn, error) {
		calls++
		if _, ok := ctx.Deadline(); ok {
			t.Error("timeout<=0 wrapper must not inject a deadline")
		}
		return nil, errors.New("sentinel")
	}

	wrapped := withDialTimeout(0, inner)
	if _, err := wrapped(context.Background(), "tcp", "127.0.0.1:1"); err == nil || err.Error() != "sentinel" {
		t.Fatalf("err = %v, want sentinel passthrough", err)
	}
	if calls != 1 {
		t.Fatalf("inner called %d times, want 1", calls)
	}
}

// TestWithDialTimeoutLeavesNoResidualReadDeadline 锁定本修复最关键的安全前提。
//
// 用超时 ctx 包住拨号，绝不能给后续长时程 body 读取留下残留 deadline —— 否则
// ResponseHeaderTimeout 的修复会被建连超时抵消（流仍会在 30s 处被砍断）。
// 已实证 net.Dialer.DialContext 成功返回后会清除 conn 上的 deadline（stdlib dialCtx
// 只在拨号期间用 WithDeadline 派生子 ctx），此测试把该行为钉住防回归。
func TestWithDialTimeoutLeavesNoResidualReadDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	const lateDelay = 400 * time.Millisecond
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			// 模拟慢流：建连后先沉默 lateDelay，再发一个字节。
			go func(c net.Conn) {
				time.Sleep(lateDelay)
				_, _ = c.Write([]byte("X"))
				time.Sleep(2 * time.Second)
				_ = c.Close()
			}(conn)
		}
	}()

	// 建连超时远小于首个字节的到达时间。
	wrapped := withDialTimeout(50*time.Millisecond, (&net.Dialer{}).DialContext)
	conn, err := wrapped(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("wrapped dial: %v", err)
	}
	defer conn.Close()

	// 不设任何 deadline 直接读：若拨号 ctx 的 deadline 残留在 conn 上，这里会立刻
	// i/o timeout；正确行为是阻塞到 lateDelay 后拿到数据。
	start := time.Now()
	buf := make([]byte, 1)
	n, readErr := conn.Read(buf)
	elapsed := time.Since(start)

	if readErr != nil {
		t.Fatalf("read failed after %v: %v (residual dial deadline leaked onto the conn?)", elapsed, readErr)
	}
	if n != 1 {
		t.Fatalf("read %d bytes, want 1", n)
	}
	if elapsed < lateDelay/2 {
		t.Fatalf("read returned in %v, want ~%v (data arrived too early?)", elapsed, lateDelay)
	}
}

// TestSocks5DialContextHonorsCancellation 验证 socks5 路径改为 ctx 感知拨号。
//
// 修复前用的是 socksDialer.Dial(network, addr)，其内部对代理连接用 net.Dial、
// 对握手用 context.Background()：请求取消与建连超时都传不进去，代理不响应时会一直
// 阻塞（可能泄漏 goroutine 直到 OS 超时）。x/net 的 socks Dialer 实现了
// proxy.ContextDialer，改用 DialContext 后 ctx deadline 会被转成 conn 的 SetDeadline。
func TestSocks5DialContextHonorsCancellation(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	// 假 SOCKS5 代理：接受连接但从不回应握手。
	var held []net.Conn
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			held = append(held, conn) // 保持打开，避免 RST 提前结束
		}
	}()
	t.Cleanup(func() {
		for _, c := range held {
			_ = c.Close()
		}
	})

	c, err := newHTTPClientCustomProxyWithTimeoutConfig("socks5://"+ln.Addr().String(), longTaskTimeoutConfig())
	if err != nil {
		t.Fatalf("newHTTPClientCustomProxyWithTimeoutConfig: %v", err)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", c.Transport)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	conn, dialErr := tr.DialContext(ctx, "tcp", "example.com:80")
	elapsed := time.Since(start)
	if conn != nil {
		conn.Close()
	}

	if dialErr == nil {
		t.Fatal("socks5 dial should fail when the proxy never answers the handshake")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("socks5 dial ignored the ctx deadline: took %v (%v)", elapsed, dialErr)
	}
	t.Logf("socks5 dial failed after %v as expected: %v", elapsed, dialErr)
}

// TestCustomProxyHTTPSchemeKeepsTransportDialContext 回归守卫：HTTP(S) 代理路径必须
// 保留 clonedDefaultTransport 自带的 DialContext（不能注入 SafeDialContext），否则
// 上游钉住的 IP 会被拼到代理地址上导致拨号目标错误、必超时（见 http.go 注释）。
// 本次为所有分支加了 withDialTimeout 包装，需确认没有顺带改变代理路径的拨号目标。
func TestCustomProxyHTTPSchemeKeepsTransportDialContext(t *testing.T) {
	c, err := newHTTPClientCustomProxyWithTimeoutConfig("http://127.0.0.1:18080", longTaskTimeoutConfig())
	if err != nil {
		t.Fatalf("newHTTPClientCustomProxyWithTimeoutConfig: %v", err)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", c.Transport)
	}
	if tr.DialContext == nil {
		t.Fatal("HTTP proxy transport lost its DialContext")
	}
	if tr.Proxy == nil {
		t.Fatal("HTTP proxy transport lost its Proxy func")
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	got, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy(req): %v", err)
	}
	if got == nil || got.String() != "http://127.0.0.1:18080" {
		t.Fatalf("proxy url = %v, want http://127.0.0.1:18080", got)
	}
}

// TestUnsupportedProxySchemeRejected 确认包装 DialContext 没有放宽 scheme 校验。
func TestUnsupportedProxySchemeRejected(t *testing.T) {
	for _, scheme := range []string{"ftp://127.0.0.1:1080", "gopher://127.0.0.1:1080"} {
		if _, err := newHTTPClientCustomProxyWithTimeoutConfig(scheme, longTaskTimeoutConfig()); err == nil {
			t.Errorf("scheme %q accepted, want error", scheme)
		}
	}
	if _, err := newHTTPClientCustomProxyWithTimeoutConfig("http://[::1", longTaskTimeoutConfig()); err == nil {
		t.Error("malformed proxy url accepted, want error")
	}
}

// TestPublicEntryPointsUseResponseHeaderTimeout 锁定本次修复的真正缺陷位置。
//
// v2.6.0 把 600s 硬编码为 http.Client.Timeout 的三个入口是：
//   - GetHTTPClientSystemProxy(false) -> newHTTPClientNoProxy()
//   - GetHTTPClientCustomProxy(proxyURL) -> getOrBuildCustomProxyClient(proxyURL, 600s)
//   - GetHTTPClientCustomProxyWithTimeout / newHTTPClientCustomProxy
//
// 上面的测试都只验证内部构造函数，本测试从公开入口验证：default 档拿到的是
// Client.Timeout=0 + Transport.ResponseHeaderTimeout，而不是整体超时。
//
// 全局缓存约束（故本包测试一律不加 t.Parallel）：systemDirectClient 与
// customProxyClients 都是包级单例，且按 (bucket, proxyURL) 缓存已构造的 client。
// 若并行或共用同一 proxyURL，先前测试注入的 responseHeaderTimeout 会被静默沿用，
// 因此这里用专属的 proxyURL，并靠 setResponseHeaderTimeoutForTest 的 t.Cleanup 还原。
func TestPublicEntryPointsUseResponseHeaderTimeout(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })

	setResponseHeaderTimeoutForTest(t, 4321*time.Millisecond)
	resetClientCachesForTest(t)

	assertLongTaskClient := func(name string, c *http.Client) {
		t.Helper()
		if c == nil {
			t.Fatalf("%s: nil client", name)
		}
		if c.Timeout != 0 {
			t.Errorf("%s: Client.Timeout = %v, want 0 (must not cut streaming bodies)", name, c.Timeout)
		}
		tr, ok := c.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("%s: transport type = %T, want *http.Transport", name, c.Transport)
		}
		if tr.ResponseHeaderTimeout != responseHeaderTimeout {
			t.Errorf("%s: ResponseHeaderTimeout = %v, want %v", name, tr.ResponseHeaderTimeout, responseHeaderTimeout)
		}
	}

	// 直连入口（relay direct 模式、price、update、sitesync 都走这里）。
	direct, err := GetHTTPClientSystemProxy(false)
	if err != nil {
		t.Fatalf("GetHTTPClientSystemProxy(false): %v", err)
	}
	assertLongTaskClient("GetHTTPClientSystemProxy(false)", direct)

	// 自定义代理入口（relay pool 模式 / 号池账号代理走这里）。
	const proxyURL = "socks5://127.0.0.1:39999"
	viaDefault, err := GetHTTPClientCustomProxy(proxyURL)
	if err != nil {
		t.Fatalf("GetHTTPClientCustomProxy: %v", err)
	}
	assertLongTaskClient("GetHTTPClientCustomProxy", viaDefault)

	// 历史硬编码值 600s 必须归入同一 default 档，并命中同一个缓存 client：
	// 这既证明没有新增档位（内存零增量），也证明不会为同一 proxyURL 构造出
	// 两个超时语义不同的 client。
	via600, err := GetHTTPClientCustomProxyWithTimeout(proxyURL, 600*time.Second)
	if err != nil {
		t.Fatalf("GetHTTPClientCustomProxyWithTimeout(600s): %v", err)
	}
	if via600 != viaDefault {
		t.Errorf("600s variant returned a different client than the default bucket; " +
			"bucket granularity changed (extra Transport + idle conn pool per proxyURL)")
	}

	via0, err := GetHTTPClientCustomProxyWithTimeout(proxyURL, 0)
	if err != nil {
		t.Fatalf("GetHTTPClientCustomProxyWithTimeout(0): %v", err)
	}
	if via0 != viaDefault {
		t.Error("timeout=0 variant returned a different client than the default bucket")
	}

	// short 档（30s）必须是另一个 client，且保留整体超时语义。
	short, err := GetHTTPClientCustomProxyWithTimeout(proxyURL, 30*time.Second)
	if err != nil {
		t.Fatalf("GetHTTPClientCustomProxyWithTimeout(30s): %v", err)
	}
	if short == viaDefault {
		t.Fatal("short bucket collided with the default bucket")
	}
	if short.Timeout != shortTaskTimeout {
		t.Errorf("short bucket Client.Timeout = %v, want %v", short.Timeout, shortTaskTimeout)
	}
	shortTR, ok := short.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("short bucket transport type = %T, want *http.Transport", short.Transport)
	}
	if shortTR.ResponseHeaderTimeout != 0 {
		t.Errorf("short bucket ResponseHeaderTimeout = %v, want 0 (unchanged behavior)", shortTR.ResponseHeaderTimeout)
	}

	// 缓存里恰好两个档位，没有多出来。
	customProxyClientsLock.RLock()
	buckets := len(customProxyClients)
	perBucket := make(map[string]int, buckets)
	for b, m := range customProxyClients {
		perBucket[b] = len(m)
	}
	customProxyClientsLock.RUnlock()
	if buckets != 2 {
		t.Errorf("cached bucket count = %d (%v), want exactly 2", buckets, perBucket)
	}
}

// TestEnvOverrideAppliesToDefaultBucket 验证 OCTOPUS_HTTP_RESPONSE_HEADER_TIMEOUT_SECONDS
// 的解析结果确实落到 default 档的 ResponseHeaderTimeout 上（而不只是停在解析函数里）。
func TestEnvOverrideAppliesToDefaultBucket(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })

	// init() 已在包加载时跑过，这里直接复现它的解析链：env 字符串 -> 生效值 -> 档位配置。
	for _, tc := range []struct {
		raw  string
		want time.Duration
	}{
		{"120", 120 * time.Second},
		{"", defaultResponseHeaderTimeout},
		{"-1", defaultResponseHeaderTimeout},
		{"not-a-number", defaultResponseHeaderTimeout},
	} {
		resolved := resolveResponseHeaderTimeout(tc.raw, defaultResponseHeaderTimeout)
		if resolved != tc.want {
			t.Errorf("resolveResponseHeaderTimeout(%q) = %v, want %v", tc.raw, resolved, tc.want)
			continue
		}
		setResponseHeaderTimeoutForTest(t, resolved)
		cfg := longTaskTimeoutConfig()
		if cfg.overallTimeout != 0 {
			t.Errorf("env %q: overallTimeout = %v, want 0", tc.raw, cfg.overallTimeout)
		}
		if cfg.headerTimeout != tc.want {
			t.Errorf("env %q: headerTimeout = %v, want %v", tc.raw, cfg.headerTimeout, tc.want)
		}
	}
}
