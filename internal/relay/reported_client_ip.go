package relay

import (
	"net"
	"strings"

	"github.com/gin-gonic/gin"
)

// 客户端来源 IP 的展示轨（与安全轨分离）：
//
//	安全轨 = c.ClientIP()（Gin 可信代理解析），继续驱动登录限速与 API Key IP
//	         白名单；配置不变时取值不变，伪造头无法影响安全判定。
//	展示轨 = 本文件解析的 reportedClientIP，写入 relay_logs.reported_client_ip，
//	         仅用于日志展示。托管平台经代理转发时，默认不信任代理的
//	         c.ClientIP() 可能是内网地址。转发头只能作为来源线索：是否被
//	         平台正确覆盖需要实际部署验证，不能凭头名称保证真实性。

const reportedClientIPContextKey = "octopus_reported_client_ip"

// ReportedClientIPSource 标注展示 IP 的来源头。
// none 仅表示没有可用地址，不能区分缺头、无效头或内网地址。
type ReportedClientIPSource string

const (
	ReportedClientIPSourceCFConnectingIP ReportedClientIPSource = "cf-connecting-ip"
	ReportedClientIPSourceXForwardedFor  ReportedClientIPSource = "x-forwarded-for"
	ReportedClientIPSourceNone           ReportedClientIPSource = "none"
)

type reportedClientIP struct {
	IP     string
	Source ReportedClientIPSource
}

// resolveReportedClientIP 依次尝试各来源头，返回第一个可用的公网地址。
// 优先级：CF-Connecting-IP > X-Forwarded-For（右起首个公网）。
// X-Real-IP 不在展示回退范围内；以上来源也未经可信代理校验。
func resolveReportedClientIP(headerGet func(string) string) reportedClientIP {
	if headerGet == nil {
		return reportedClientIP{Source: ReportedClientIPSourceNone}
	}
	if ip := firstPublicIP(headerGet("CF-Connecting-IP")); ip != "" {
		return reportedClientIP{IP: ip, Source: ReportedClientIPSourceCFConnectingIP}
	}
	if ip := rightmostPublicForwardedIP(headerGet("X-Forwarded-For")); ip != "" {
		return reportedClientIP{IP: ip, Source: ReportedClientIPSourceXForwardedFor}
	}
	return reportedClientIP{Source: ReportedClientIPSourceNone}
}

// firstPublicIP 解析单值头（如 CF-Connecting-IP）；仅接受合法公网地址。
func firstPublicIP(raw string) string {
	ip := parseIPTight(strings.TrimSpace(raw))
	if ip == nil || isPrivateOrReservedIP(ip) {
		return ""
	}
	return ip.String()
}

// rightmostPublicForwardedIP 从 X-Forwarded-For（可能多段逗号分隔）右侧向左
// 找第一个非内网地址。这是展示用的启发式取值，不是可信代理链校验；
// 结果也可能是中间代理。全部无效时返回空。
func rightmostPublicForwardedIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// 右起遍历：strings.Split 产出左->右序列，倒序扫描取最右侧合法公网值
	candidates := strings.Split(raw, ",")
	for i := len(candidates) - 1; i >= 0; i-- {
		candidate := candidates[i]
		ip := parseIPTight(candidate)
		if ip == nil || isPrivateOrReservedIP(ip) {
			continue
		}
		return ip.String()
	}
	return ""
}

// parseIPTight 只接受纯 IP 文本；net.ParseIP 对 IPv4-mapped IPv6（::ffff:a.b.c.d）
// 统一归一化，避免同一地址出现两种写法。
func parseIPTight(raw string) net.IP {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	ip := net.ParseIP(raw)
	if ip == nil {
		return nil
	}
	return ip
}

// isPrivateOrReservedIP 判断地址是否不适合作为"客户端来源"展示：
// 回环、链路本地、IPv4 私有段、IPv6 ULA、未指定/组播地址。
// 与常见网关地址（10.x、172.16/12、192.168/16、CGNAT 100.64/10）保持一致，
// 托管平台内网代理地址不会被误当成客户端。
func isPrivateOrReservedIP(ip net.IP) bool {
	return !ip.IsGlobalUnicast() ||
		ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast() ||
		isInCIDR(ip, "10.0.0.0/8") ||
		isInCIDR(ip, "172.16.0.0/12") ||
		isInCIDR(ip, "192.168.0.0/16") ||
		isInCIDR(ip, "100.64.0.0/10") ||
		isInCIDR(ip, "fc00::/7")
}

func isInCIDR(ip net.IP, cidr string) bool {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	return network.Contains(ip)
}

// captureReportedClientIP 在请求入口计算展示轨 IP 并存入 gin context，
// 供各落盘点读取；每请求只解析一次。
func captureReportedClientIP(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	resolved := resolveReportedClientIP(c.Request.Header.Get)
	c.Set(reportedClientIPContextKey, resolved)
}

// reportedClientIPFromContext 返回入口采集的展示轨 IP；中间件未运行
// （如单测直接构造 handler）时返回空值，落盘点按"无展示 IP"处理。
func reportedClientIPFromContext(c *gin.Context) reportedClientIP {
	if c == nil {
		return reportedClientIP{Source: ReportedClientIPSourceNone}
	}
	value, exists := c.Get(reportedClientIPContextKey)
	if !exists {
		return reportedClientIP{Source: ReportedClientIPSourceNone}
	}
	resolved, _ := value.(reportedClientIP)
	return resolved
}
