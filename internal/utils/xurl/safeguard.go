package xurl

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
)

// ssrfAllowPrivate 是测试专用开关：设 true 后允许 loopback/私有 IP。
// 生产永远 false——SSRF 防护不得在生产路径放宽。仅在单元测试里通过
// SetSSRFAllowPrivateForTest 临时设置，测试结束恢复 false。
var ssrfAllowPrivate bool

// SetSSRFAllowPrivateForTest 设置测试专用的 SSRF 私有地址放行开关。
// 仅用于单元测试（httptest.NewServer 监听 127.0.0.1）。
func SetSSRFAllowPrivateForTest(b bool) {
	ssrfAllowPrivate = b
}

// IsDisallowedIP reports whether ip points at a host that server-side requests
// must never reach: loopback, private (RFC 1918 / ULA), link-local, multicast,
// unspecified, or the link-local metadata range (169.254.0.0/16) used by cloud
// instance metadata services such as AWS IMDS. It is the core of the SSRF
// mitigation applied to user-controlled outbound URLs.
func IsDisallowedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ssrfAllowPrivate {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		// 169.254.0.0/16 — link-local, also covers cloud metadata endpoints.
		return v4[0] == 169 && v4[1] == 254
	}
	return ip.IsPrivate()
}

// resolveSafeHost 解析 host 并返回首个安全的 IP。
// 返回的 IP 可用于 SafeDialContext 钉住连接目标，杜绝 DNS rebinding 窗口。
var lookupSafeHost = net.DefaultResolver.LookupIPAddr

func resolveSafeHost(ctx context.Context, target *url.URL) (net.IP, error) {
	if target == nil {
		return nil, fmt.Errorf("url is required")
	}
	host := strings.TrimSpace(target.Hostname())
	if host == "" {
		host = strings.TrimSpace(target.Host)
	}
	host = strings.Trim(strings.ToLower(host), ".")
	if host == "" {
		return nil, fmt.Errorf("url must have a host")
	}
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return nil, fmt.Errorf("url host is not allowed")
	}
	if ip := net.ParseIP(host); ip != nil {
		if IsDisallowedIP(ip) {
			return nil, fmt.Errorf("url host is not allowed")
		}
		return ip, nil
	}
	trace := httptrace.ContextClientTrace(ctx)
	if trace != nil && trace.DNSStart != nil {
		trace.DNSStart(httptrace.DNSStartInfo{Host: host})
	}
	ips, err := lookupSafeHost(ctx, host)
	if trace != nil && trace.DNSDone != nil {
		trace.DNSDone(httptrace.DNSDoneInfo{Addrs: ips, Err: err})
	}
	if err != nil {
		return nil, fmt.Errorf("resolve url host: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("url host did not resolve")
	}
	for _, address := range ips {
		if IsDisallowedIP(address.IP) {
			return nil, fmt.Errorf("url host resolves to disallowed address %s", address.IP)
		}
	}
	return ips[0].IP, nil
}

// AssertSafeHost validates that target.Host is safe for a server-side request.
// It rejects empty hosts, "localhost" and ".local" names, any literal IP that
// is disallowed (see IsDisallowedIP), and any hostname whose DNS records
// resolve to a disallowed address. Resolving up front mitigates — but does not
// fully prevent — DNS rebinding; use SafeDialContext to fully close the window.
func AssertSafeHost(ctx context.Context, target *url.URL) error {
	_, err := resolveSafeHost(ctx, target)
	return err
}

// AssertSafeURL parses rawURL, requires an http(s) scheme with a host, and
// validates the host via AssertSafeHost. Use it at request boundaries to guard
// server-side fetches driven by user input.
func AssertSafeURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("url must be a valid http or https url")
	}
	return AssertSafeHost(ctx, parsed)
}

// AssertSafeRequest 校验最终出站 http.Request 的 URL 是否安全。
// 用于 relay 转发主链路：渠道 Base URL 经 adapter/rewrite 拼接后，在
// httpClient.Do(req) 前对最终 URL 做最后一道 SSRF 防线。
func AssertSafeRequest(req *http.Request) error {
	if req == nil || req.URL == nil {
		return fmt.Errorf("request url is required")
	}
	if req.URL.Host == "" {
		return fmt.Errorf("request url must have a host")
	}
	return AssertSafeHost(req.Context(), req.URL)
}

// safeIPKey 是 context key，用于把校验时解析的安全 IP 传递给 SafeDialContext。
type safeIPKey struct{}

type safeHostKey struct{}

// AssertSafeRequestWithPin 校验请求 URL 并把解析到的安全 IP 钉入 context。
// 返回的新 context 供 SafeDialContext 读取，dial 时直接连该 IP，跳过二次 DNS
// 解析，彻底消除 DNS rebinding 窗口。
func AssertSafeRequestWithPin(req *http.Request) (context.Context, error) {
	if req == nil || req.URL == nil || req.URL.Host == "" {
		return nil, fmt.Errorf("request url is required")
	}
	host := strings.ToLower(req.URL.Hostname())
	if pinnedHost, _ := req.Context().Value(safeHostKey{}).(string); pinnedHost == host {
		if ip, _ := req.Context().Value(safeIPKey{}).(net.IP); ip != nil {
			return req.Context(), nil
		}
	}
	ip, err := resolveSafeHost(req.Context(), req.URL)
	if err != nil {
		return nil, err
	}
	return context.WithValue(context.WithValue(req.Context(), safeHostKey{}, host), safeIPKey{}, ip), nil
}

// SafeDialContext 是可注入 http.Transport.DialContext 的安全拨号函数。
// 若 context 携带了校验时钉入的安全 IP（AssertSafeRequestWithPin 设置），
// 则直接连该 IP，跳过二次 DNS 解析——彻底消除 DNS rebinding 窗口。
// 未携带时回退到默认拨号（向后兼容未接入校验的路径）。
func SafeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if ip, ok := ctx.Value(safeIPKey{}).(net.IP); ok && ip != nil {
		// 钉住校验过的 IP：把 addr 中的 host 替换为安全 IP，端口保留。
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		pinnedHost, _ := ctx.Value(safeHostKey{}).(string)
		if pinnedHost == "" || strings.EqualFold(host, pinnedHost) {
			return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
	}
	// 回退：未携带钉入 IP 的路径走默认拨号（管理面 AssertSafeURL 已校验，
	// rebinding 窗口已由注释说明 mitigated but not fully prevented）。
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

// CheckRedirectSafe 返回一个 http.Client 的 CheckRedirect 函数：对每一跳重定向
// 目标重新执行 AssertSafeHost 校验，并限制最大跳数。初始 URL 校验通过后，攻击者
// 域名仍可能 302 到内网地址（或利用 DNS rebinding 在解析后更换指向），逐跳校验
// 可封堵「重定向到内网」这一绕过路径。
func CheckRedirectSafe(maxRedirects int) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if maxRedirects > 0 && len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		if req.URL == nil || req.URL.Host == "" {
			return fmt.Errorf("redirect url is invalid")
		}
		return AssertSafeHost(req.Context(), req.URL)
	}
}
