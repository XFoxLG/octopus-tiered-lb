package client

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/utils/xurl"
)

// acceptOnce 启动一个只 accept 一次连接的 goroutine，把结果写入 done。
// 用于验证拨号确实连到了本地 listener（而非被移到别的 host）。
func acceptOnce(t *testing.T, ln net.Listener) chan net.Conn {
	t.Helper()
	done := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- nil
			return
		}
		done <- conn
	}()
	return done
}

// TestHTTPProxyTransportIgnoresPinnedIP 回归测试（HTTP 代理 SSRF DNS-pin bug）：
// 走 HTTP 代理时，上游域名由代理端解析，客户端的 DialContext 收到的 addr 是代理
// 地址。若误用 SafeDialContext，会把 context 里钉入的上游 IP 拼上代理端口（如
// 上游IP:7890），导致拨号目标错误、必超时。此处验证 HTTP 代理 transport 即使
// context 携带钉入 IP，也仍按传入的代理 addr 拨号。
func TestHTTPProxyTransportIgnoresPinnedIP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	accepted := acceptOnce(t, ln)
	proxyAddr := ln.Addr().String()

	// 用公网 IP 作为上游 target，AssertSafeRequestWithPin 会把它钉入 context（无 DNS）。
	req, err := http.NewRequest(http.MethodPost, "https://8.8.8.8/v1/chat/completions", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	safeCtx, err := xurl.AssertSafeRequestWithPin(req)
	if err != nil {
		t.Fatalf("AssertSafeRequestWithPin: %v", err)
	}

	client, err := newHTTPClientCustomProxyWithTimeout("http://"+proxyAddr, time.Second)
	if err != nil {
		t.Fatalf("newHTTPClientCustomProxyWithTimeout: %v", err)
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}

	// Go 的 HTTP 代理路径会调用 DialContext(ctx, "tcp", 代理地址)。
	// 正确行为：连接 proxyAddr（本地 listener）；错误行为：连接 8.8.8.8:proxyPort。
	ctx, cancel := context.WithTimeout(safeCtx, 3*time.Second)
	defer cancel()
	conn, err := tr.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		t.Fatalf("HTTP proxy dial should connect to proxy addr %s, got: %v", proxyAddr, err)
	}
	conn.Close()

	select {
	case got := <-accepted:
		if got == nil {
			t.Fatal("listener closed without accepting a connection")
		}
		got.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("dial did not reach the local listener (dialed wrong host?)")
	}
}

// TestDirectTransportStillPinsIP 确保直连（无代理）路径仍然注入 SafeDialContext：
// context 携带钉入 IP 时，DialContext 应直连该 IP 而非原 addr 中的 host。
func TestDirectTransportStillPinsIP(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	defer xurl.SetSSRFAllowPrivateForTest(false)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	accepted := acceptOnce(t, ln)

	addr := ln.Addr().String()
	ip, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}

	// 钉入 listener 的 IP（测试环境放行私有地址），target 端口随意。
	req, err := http.NewRequest(http.MethodPost, "http://"+ip+":9/v1", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	safeCtx, err := xurl.AssertSafeRequestWithPin(req)
	if err != nil {
		t.Fatalf("AssertSafeRequestWithPin: %v", err)
	}

	client, err := newHTTPClientNoProxyWithTimeout(time.Second)
	if err != nil {
		t.Fatalf("newHTTPClientNoProxyWithTimeout: %v", err)
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}

	// 原 addr 用不存在的 hostname + listener 端口；SafeDialContext 应改用钉入的 IP。
	conn, err := tr.DialContext(safeCtx, "tcp", "nonexistent-host-xyz.invalid:"+port)
	if err != nil {
		t.Fatalf("direct transport should pin IP and dial it: %v", err)
	}
	conn.Close()

	select {
	case got := <-accepted:
		if got == nil {
			t.Fatal("listener closed without accepting a connection")
		}
		got.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("dial did not reach the local listener (pin lost?)")
	}
}