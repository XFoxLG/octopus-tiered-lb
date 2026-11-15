package xurl

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"testing"
)

func TestIsDisallowedIP(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"loopback v4", "127.0.0.1", true},
		{"loopback v4 high", "127.255.255.255", true},
		{"loopback v6", "::1", true},
		{"private 10/8", "10.0.0.1", true},
		{"private 172.16/12", "172.16.0.1", true},
		{"private 192.168/16", "192.168.1.1", true},
		{"link-local metadata 169.254.169.254", "169.254.169.254", true},
		{"link-local 169.254 other", "169.254.0.1", true},
		{"link-local v6 fe80", "fe80::1", true},
		{"unspecified v4", "0.0.0.0", true},
		{"unspecified v6", "::", true},
		{"multicast v4", "224.0.0.1", true},
		{"multicast v6 ff02", "ff02::1", true},
		{"public 8.8.8.8", "8.8.8.8", false},
		{"public 1.1.1.1", "1.1.1.1", false},
		{"public v6", "2606:4700:4700::1111", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("invalid test ip %q", tt.ip)
			}
			if got := IsDisallowedIP(ip); got != tt.want {
				t.Errorf("IsDisallowedIP(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestSafePinReusesOnlySameHostAndRecordsRealDNS(t *testing.T) {
	original := lookupSafeHost
	t.Cleanup(func() { lookupSafeHost = original })
	lookups, starts, ends := 0, 0, 0
	lookupSafeHost = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		lookups++
		if host == "blocked.example" {
			return []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}}, nil
	}
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{DNSStart: func(httptrace.DNSStartInfo) { starts++ }, DNSDone: func(httptrace.DNSDoneInfo) { ends++ }})
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://safe.example/v1", nil)
	pinned, err := AssertSafeRequestWithPin(request)
	if err != nil {
		t.Fatal(err)
	}
	request = request.WithContext(pinned)
	if _, err := AssertSafeRequestWithPin(request); err != nil {
		t.Fatal(err)
	}
	if lookups != 1 || starts != 1 || ends != 1 {
		t.Fatalf("same host re-resolved/traced: %d/%d/%d", lookups, starts, ends)
	}
	changed, _ := http.NewRequestWithContext(pinned, http.MethodGet, "https://blocked.example/v1", nil)
	if _, err := AssertSafeRequestWithPin(changed); err == nil || lookups != 2 {
		t.Fatal("old pin bypassed different host validation")
	}
	ipRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://192.0.2.2/v1", nil)
	if _, err := AssertSafeRequestWithPin(ipRequest); err != nil {
		t.Fatal(err)
	}
	if starts != 2 || ends != 2 {
		t.Fatal("literal IP fabricated DNS")
	}
}

func TestIsDisallowedIP_Nil(t *testing.T) {
	if !IsDisallowedIP(nil) {
		t.Error("IsDisallowedIP(nil) = false, want true")
	}
}

func TestAssertSafeURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"empty", "", true},
		{"file scheme", "file:///etc/passwd", true},
		{"ftp scheme", "ftp://example.com/", true},
		{"gopher scheme", "gopher://127.0.0.1/", true},
		{"no scheme", "example.com/", true},
		{"loopback ip", "http://127.0.0.1/", true},
		{"metadata ip", "http://169.254.169.254/latest/meta-data/", true},
		{"private ip", "http://10.0.0.1/", true},
		{"localhost", "http://localhost/", true},
		{"dotlocal", "http://foo.local/", true},
		// Literal public IPs are validated without DNS, so this stays stable.
		{"public ip", "http://8.8.8.8/", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AssertSafeURL(context.Background(), tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("AssertSafeURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestAssertSafeHost_Nil(t *testing.T) {
	if err := AssertSafeHost(context.Background(), nil); err == nil {
		t.Error("AssertSafeHost(nil) = nil, want error")
	}
}

func TestAssertSafeRequest(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"nil request", "", true},
		{"loopback", "http://127.0.0.1/v1/chat", true},
		{"metadata", "http://169.254.169.254/latest/meta-data/", true},
		{"private", "http://10.0.0.1/v1/chat", true},
		{"public ip", "http://8.8.8.8/v1/chat", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.url != "" {
				var err error
				req, err = http.NewRequest(http.MethodPost, tt.url, nil)
				if err != nil {
					t.Fatalf("new request: %v", err)
				}
			}
			err := AssertSafeRequest(req)
			if (err != nil) != tt.wantErr {
				t.Errorf("AssertSafeRequest(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestAssertSafeRequestWithPin_PinsIP(t *testing.T) {
	// 公网 IP 不需 DNS 解析，AssertSafeRequestWithPin 应返回携带钉入 IP 的 context。
	req, err := http.NewRequest(http.MethodPost, "http://8.8.8.8/v1/chat", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	safeCtx, err := AssertSafeRequestWithPin(req)
	if err != nil {
		t.Fatalf("AssertSafeRequestWithPin: %v", err)
	}

	ip, ok := safeCtx.Value(safeIPKey{}).(net.IP)
	if !ok || ip == nil {
		t.Fatal("expected safe IP pinned in context")
	}
	if ip.String() != "8.8.8.8" {
		t.Fatalf("pinned IP = %s, want 8.8.8.8", ip)
	}
}

func TestAssertSafeRequestWithPin_RejectsMetadata(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://169.254.169.254/latest/meta-data/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if _, err := AssertSafeRequestWithPin(req); err == nil {
		t.Fatal("expected error for metadata endpoint, got nil")
	}
}

func TestAssertSafeRequestWithPinResolvesOnceAndPinsPublicAddress(t *testing.T) {
	originalLookup := lookupSafeHost
	t.Cleanup(func() { lookupSafeHost = originalLookup })
	lookups := 0
	lookupSafeHost = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		lookups++
		if host != "public.example" {
			t.Fatalf("lookup host = %q", host)
		}
		if ctx == nil {
			t.Fatal("lookup context is nil")
		}
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}

	req, err := http.NewRequest(http.MethodPost, "https://public.example/v1/chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	safeCtx, err := AssertSafeRequestWithPin(req)
	if err != nil {
		t.Fatalf("AssertSafeRequestWithPin: %v", err)
	}
	if lookups != 1 {
		t.Fatalf("DNS lookups = %d, want exactly one", lookups)
	}
	pinned, ok := safeCtx.Value(safeIPKey{}).(net.IP)
	if !ok || pinned.String() != "93.184.216.34" {
		t.Fatalf("pinned address = %v", pinned)
	}
}

func TestAssertSafeRequestWithPinRejectsMixedPrivateAnswers(t *testing.T) {
	originalLookup := lookupSafeHost
	t.Cleanup(func() { lookupSafeHost = originalLookup })
	lookupSafeHost = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{
			{IP: net.ParseIP("93.184.216.34")},
			{IP: net.ParseIP("10.0.0.7")},
		}, nil
	}

	req, err := http.NewRequest(http.MethodPost, "https://mixed.example/v1/chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AssertSafeRequestWithPin(req); err == nil {
		t.Fatal("mixed public/private DNS answer was accepted")
	}
}

func TestAssertSafeRequestWithPinUsesCancelableLookupContext(t *testing.T) {
	originalLookup := lookupSafeHost
	t.Cleanup(func() { lookupSafeHost = originalLookup })
	lookupSafeHost = func(ctx context.Context, _ string) ([]net.IPAddr, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slow.example/v1/chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AssertSafeRequestWithPin(req); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled lookup error = %v, want context.Canceled", err)
	}
}

func TestSafeDialContext_UsesPinnedIP(t *testing.T) {
	// 携带钉入 IP 时，SafeDialContext 应连该 IP 而非原 addr 中的 host。
	// 用一个本地 listener 验证：钉入的 IP 指向 listener，原 addr 用不存在的
	// host + listener 的端口。若未用钉入 IP 会 DNS 失败。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, _ := ln.Accept()
		if conn != nil {
			conn.Close()
		}
	}()

	addr := ln.Addr().String()
	ip, port, _ := net.SplitHostPort(addr)
	pinnedIP := net.ParseIP(ip)

	ctx := context.WithValue(context.Background(), safeIPKey{}, pinnedIP)
	// addr 用不存在的 hostname + listener 端口；钉入 IP 会被使用，端口保留。
	conn, err := SafeDialContext(ctx, "tcp", "nonexistent-host-xyz.invalid:"+port)
	if err != nil {
		t.Fatalf("SafeDialContext with pinned IP: %v", err)
	}
	conn.Close()
}
func TestSafeDialContext_FallbackNoPin(t *testing.T) {
	// 未携带钉入 IP 时回退默认拨号；用不存在的 host 应 DNS 失败（非 panic）。
	ctx := context.Background()
	_, err := SafeDialContext(ctx, "tcp", "nonexistent-host-xyz.invalid:80")
	if err == nil {
		t.Fatal("expected DNS failure for unpinned nonexistent host")
	}
}
