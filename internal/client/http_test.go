package client

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/utils/httpx"
	"github.com/lingyuins/octopus/internal/utils/xurl"
	"golang.org/x/net/dns/dnsmessage"
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

	client, err := newHTTPClientCustomProxyWithTimeoutConfig("http://"+proxyAddr, clientTimeoutConfig{overallTimeout: time.Second, headerTimeout: time.Second})
	if err != nil {
		t.Fatalf("newHTTPClientCustomProxyWithTimeoutConfig: %v", err)
	}
	tr, ok := httpx.BaseTransport(client.Transport).(*http.Transport)
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

	// Resolve the real request hostname once, then make DNS unavailable. A direct
	// transport must connect to that hostname's validated IP without resolving again.
	dns, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dns.Close() })
	var lookups atomic.Int32
	go func() {
		buffer := make([]byte, 1500)
		for {
			n, peer, err := dns.ReadFrom(buffer)
			if err != nil {
				return
			}
			lookups.Add(1)
			var query dnsmessage.Message
			if query.Unpack(buffer[:n]) != nil {
				continue
			}
			answer := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true, RecursionAvailable: true}, Questions: query.Questions}
			for _, question := range query.Questions {
				if question.Type == dnsmessage.TypeA {
					v4 := net.ParseIP(ip).To4()
					answer.Answers = append(answer.Answers, dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: question.Type, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{v4[0], v4[1], v4[2], v4[3]}}})
				}
			}
			raw, _ := answer.Pack()
			_, _ = dns.WriteTo(raw, peer)
		}
	}()
	resolver := net.DefaultResolver
	previousDial, previousPreferGo := resolver.Dial, resolver.PreferGo
	resolver.PreferGo = true
	resolver.Dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", dns.LocalAddr().String())
	}
	t.Cleanup(func() { resolver.Dial, resolver.PreferGo = previousDial, previousPreferGo })
	const host = "pinned-request.example"
	req, err := http.NewRequest(http.MethodPost, "http://"+host+":9/v1", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	safeCtx, err := xurl.AssertSafeRequestWithPin(req)
	if err != nil {
		t.Fatalf("AssertSafeRequestWithPin: %v", err)
	}
	_ = dns.Close()
	resolved := lookups.Load()
	if resolved == 0 {
		t.Fatal("request hostname was not resolved")
	}

	client, err := newHTTPClientNoProxyWithTimeoutConfig(clientTimeoutConfig{overallTimeout: time.Second, headerTimeout: time.Second})
	if err != nil {
		t.Fatalf("newHTTPClientNoProxyWithTimeoutConfig: %v", err)
	}
	tr, ok := httpx.BaseTransport(client.Transport).(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}

	conn, err := tr.DialContext(safeCtx, "tcp", host+":"+port)
	if err != nil {
		t.Fatalf("direct transport should pin IP and dial it: %v", err)
	}
	conn.Close()
	if lookups.Load() != resolved {
		t.Fatal("direct transport resolved the validated hostname again")
	}

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
