package client

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/utils/proxyx"
	"github.com/lingyuins/octopus/internal/utils/xurl"
	"golang.org/x/net/proxy"
)

const (
	// shortTaskTimeout 后台任务使用的短超时时间（延迟探测、模型同步）
	shortTaskTimeout = 30 * time.Second

	// defaultResponseHeaderTimeout 是 default 档（转发 / 长任务）等待响应头的默认超时。
	// 可被 httpHeaderTimeoutEnv 覆盖，实际生效值见 responseHeaderTimeout。
	defaultResponseHeaderTimeout = 600 * time.Second

	// defaultDialTimeout 是建连（TCP 拨号 / 代理握手）超时，对所有档位生效。
	// 与 http.DefaultTransport 自带的 net.Dialer{Timeout: 30s} 取值一致；
	// 直连路径注入的 xurl.SafeDialContext 用的是裸 &net.Dialer{}（无 Timeout），
	// socks5 路径原先用的 socksDialer.Dial 内部走 context.Background()（既不受
	// ctx 取消控制、也没有建连超时），两者都靠这层包装补齐。
	defaultDialTimeout = 30 * time.Second
)

// httpHeaderTimeoutEnv 覆盖 defaultResponseHeaderTimeout（单位：秒）。
//
// 与 OCTOPUS_RELAY_UPSTREAM_TIMEOUT_SECONDS / OCTOPUS_RELAY_MAX_SSE_EVENT_SIZE
// 同属「绕过 Viper、直接 os.Getenv」的一批开关（见 AGENTS.md「配置与环境变量」），
// 在 init() 里一次性读取。取值 <=0 或非法时保持默认值。
//
// 前缀与 conf.APP_NAME 一致，但这里有意不 import internal/conf：conf 目前不在
// client 的依赖树内，引入它会把 viper 以及 version.go 里 init() 的 git 子进程
// 带进来（AGENTS.md「版本号漂移」提到该 init 会 fork git），对本包无收益。
const httpHeaderTimeoutEnv = "OCTOPUS_HTTP_RESPONSE_HEADER_TIMEOUT_SECONDS"

// responseHeaderTimeout 是 default 档实际生效的响应头超时。
var responseHeaderTimeout = defaultResponseHeaderTimeout

func init() {
	responseHeaderTimeout = resolveResponseHeaderTimeout(os.Getenv(httpHeaderTimeoutEnv), defaultResponseHeaderTimeout)
}

// resolveResponseHeaderTimeout 解析环境变量覆盖值（单位：秒）。
// 空值 / 非法数字 / <=0 一律回退到 fallback，绝不产生「无响应头超时」的退化值。
func resolveResponseHeaderTimeout(raw string, fallback time.Duration) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

var (
	systemDirectClient       *http.Client
	systemProxyClient        *http.Client
	systemProxyURL           string
	shortTimeoutDirectClient *http.Client
	shortTimeoutProxyClient  *http.Client
	shortTimeoutProxyURL     string
	clientLock               sync.RWMutex

	// customProxyClients 缓存按 (timeout 分桶, proxyURL) 复用的 *http.Client。
	// 之前每次调用 GetHTTPClientCustomProxy* 都新建 client + 新 clone transport + 新
	// 空闲连接池，transport/连接不重用、闲置 90s 才 GC，在带 channel_proxy 的转发请求
	// 上会造成内存与连接数持续增长（见 issue #124）。proxyURL 集合受限于用户配置的
	// channel 数量，基数可控，无需 LRU。外层 key 为超时分桶（customProxyClientBucket），
	// 内层 key 为 proxyURL。
	customProxyClients     = make(map[string]map[string]*http.Client)
	customProxyClientsLock sync.RWMutex
)

// GetHTTPClientSystemProxy returns a cached http.Client.
// - useProxy=false: bypass proxy
// - useProxy=true: use proxy settings from system/app settings (setting key: proxy_url)
func GetHTTPClientSystemProxy(useProxy bool) (*http.Client, error) {
	if useProxy {
		currentProxyURL, err := setting.GetString(model.SettingKeyProxyURL)
		if err != nil {
			return nil, err
		}
		if currentProxyURL == "" {
			return nil, fmt.Errorf("proxy url is empty")
		}

		clientLock.RLock()
		if systemProxyClient != nil && systemProxyURL == currentProxyURL {
			clientLock.RUnlock()
			return systemProxyClient, nil
		}
		clientLock.RUnlock()

		clientLock.Lock()
		defer clientLock.Unlock()

		// Re-check after acquiring write lock.
		if systemProxyClient != nil && systemProxyURL == currentProxyURL {
			return systemProxyClient, nil
		}

		client, err := newHTTPClientCustomProxy(currentProxyURL)
		if err != nil {
			return nil, err
		}
		if systemProxyClient != nil {
			systemProxyClient.CloseIdleConnections()
		}
		systemProxyClient = client
		systemProxyURL = currentProxyURL
		return systemProxyClient, nil
	}

	clientLock.RLock()
	if !useProxy && systemDirectClient != nil {
		clientLock.RUnlock()
		return systemDirectClient, nil
	}
	clientLock.RUnlock()

	clientLock.Lock()
	defer clientLock.Unlock()

	if systemDirectClient != nil {
		return systemDirectClient, nil
	}
	client, err := newHTTPClientNoProxy()
	if err != nil {
		return nil, err
	}
	systemDirectClient = client
	return systemDirectClient, nil
}

// GetHTTPClientCustomProxy returns a cached http.Client for the given proxy URL.
// 缓存以 (proxyURL, timeout-bucket) 为 key（issue #124）：此前每次调用都新建
// *http.Client + 新 clone 的 http.Transport + 新空闲连接池，transport/连接不重用、
// 闲置 90s 才 GC，在带 per-channel 代理的转发请求上会造成显著的内存与 GC 压力。
// proxyURL 集合受限于用户配置的 channel 数量，基数可控，无需 LRU。
// proxyURL supports: http, https, socks, socks5.
func GetHTTPClientCustomProxy(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		return nil, fmt.Errorf("proxy url is empty")
	}
	// 传 0 表示「不设整体超时」：转发链路可能是长时程流式响应（reasoning 模型 +
	// 长输出），http.Client.Timeout 覆盖整个 body 读取过程，会在 600s 处把正在
	// 输出的 SSE 流从中砍断（客户端收不到 [DONE]）。僵死保护改由
	// Transport.ResponseHeaderTimeout + 建连超时承担（见 timeoutConfigForBucket）。
	return getOrBuildCustomProxyClient(proxyURL, 0)
}

// GetHTTPClientCustomProxyWithTimeout returns a cached http.Client with the given
// proxy URL and timeout. 同 GetHTTPClientCustomProxy 按 (proxyURL, timeout-bucket)
// 缓存；超时变体与默认超时变体分别缓存，避免超时被覆盖。
func GetHTTPClientCustomProxyWithTimeout(proxyURL string, timeout time.Duration) (*http.Client, error) {
	if proxyURL == "" {
		return nil, fmt.Errorf("proxy url is empty")
	}
	return getOrBuildCustomProxyClient(proxyURL, timeout)
}

// getOrBuildCustomProxyClient 是 GetHTTPClientCustomProxy(/WithTimeout) 的缓存核心。
// 双检锁：先 RLock 查 (bucket, proxyURL)，未命中再 Lock 构造并写入。bucket 由
// customProxyClientBucket(timeout) 决定，当前仅 "default"（长任务，整体不限时）与 "short"(30s) 两档。
func getOrBuildCustomProxyClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	bucket := customProxyClientBucket(timeout)
	customProxyClientsLock.RLock()
	if m, ok := customProxyClients[bucket]; ok {
		if c, ok := m[proxyURL]; ok {
			customProxyClientsLock.RUnlock()
			return c, nil
		}
	}
	customProxyClientsLock.RUnlock()

	customProxyClientsLock.Lock()
	defer customProxyClientsLock.Unlock()
	if m, ok := customProxyClients[bucket]; ok {
		if c, ok := m[proxyURL]; ok {
			return c, nil
		}
	} else {
		customProxyClients[bucket] = make(map[string]*http.Client)
	}
	client, err := newHTTPClientCustomProxyWithTimeoutConfig(proxyURL, timeoutConfigForBucket(bucket, timeout))
	if err != nil {
		return nil, err
	}
	customProxyClients[bucket][proxyURL] = client
	return client, nil
}

// bucketShort/bucketDefault 是 customProxyClients 的外层 key。
const (
	bucketShort   = "short"
	bucketDefault = "default"
)

// customProxyClientBucket 把 timeout 映射为缓存分桶 key。同 timeout 复用同一 map，
// 避免不同 timeout 的 client 互相覆盖。当前仅两档：default（转发，整体不限时）
// 与 30s（后台短任务）。timeout<=0 归入 default，与 helper.channelHTTPClient 的
// `short := timeout > 0 && timeout <= 30s` 判定保持一致。
//
// 不新增档位是硬约束：每多一档，customProxyClients[bucket][proxyURL] 就会为每个
// proxyURL 多一个 client + 一份 clone 的 Transport + 一套独立空闲连接池。
func customProxyClientBucket(timeout time.Duration) string {
	if timeout > 0 && timeout <= shortTaskTimeout {
		return bucketShort
	}
	return bucketDefault
}

// timeoutConfigForBucket 由档位反推超时配置，保证「缓存 key」与「实际生效的超时
// 语义」严格一致：同一 (bucket, proxyURL) 只会构造一个 client，若直接按请求方的
// 原始 timeout 施加超时，后来的请求会静默沿用第一个请求的超时值。
//
// short 档保留整体超时（值取请求方的 timeout，兼容 <30s 的自定义短超时）；
// default 档整体不限时，只限响应头。
func timeoutConfigForBucket(bucket string, requested time.Duration) clientTimeoutConfig {
	if bucket == bucketShort && requested > 0 {
		return clientTimeoutConfig{overallTimeout: requested, headerTimeout: 0}
	}
	return longTaskTimeoutConfig()
}

// GetHTTPClientShortTimeout returns a cached http.Client with short timeout (30s).
// Used for background tasks like delay probing and model syncing to avoid
// goroutine/connection accumulation when endpoints are unreachable.
// - useProxy=false: bypass proxy
// - useProxy=true: use proxy settings from system/app settings
func GetHTTPClientShortTimeout(useProxy bool) (*http.Client, error) {
	if useProxy {
		currentProxyURL, err := setting.GetString(model.SettingKeyProxyURL)
		if err != nil {
			return nil, err
		}
		if currentProxyURL == "" {
			return nil, fmt.Errorf("proxy url is empty")
		}

		clientLock.RLock()
		if shortTimeoutProxyClient != nil && shortTimeoutProxyURL == currentProxyURL {
			clientLock.RUnlock()
			return shortTimeoutProxyClient, nil
		}
		clientLock.RUnlock()

		clientLock.Lock()
		defer clientLock.Unlock()

		if shortTimeoutProxyClient != nil && shortTimeoutProxyURL == currentProxyURL {
			return shortTimeoutProxyClient, nil
		}

		client, err := newHTTPClientCustomProxyWithTimeoutConfig(currentProxyURL, shortTaskTimeoutConfig())
		if err != nil {
			return nil, err
		}
		if shortTimeoutProxyClient != nil {
			shortTimeoutProxyClient.CloseIdleConnections()
		}
		shortTimeoutProxyClient = client
		shortTimeoutProxyURL = currentProxyURL
		return shortTimeoutProxyClient, nil
	}

	clientLock.RLock()
	if shortTimeoutDirectClient != nil {
		clientLock.RUnlock()
		return shortTimeoutDirectClient, nil
	}
	clientLock.RUnlock()

	clientLock.Lock()
	defer clientLock.Unlock()

	if shortTimeoutDirectClient != nil {
		return shortTimeoutDirectClient, nil
	}
	client, err := newHTTPClientNoProxyWithTimeoutConfig(shortTaskTimeoutConfig())
	if err != nil {
		return nil, err
	}
	shortTimeoutDirectClient = client
	return shortTimeoutDirectClient, nil
}

// httpTransportPoolLimits 显式设定连接池上限，覆盖 http.DefaultTransport 的默认值
// （MaxIdleConns=0 无界、MaxIdleConnsPerHost=2）。所有派生 client 共享同一组上限，
// 避免多上游 channel 场景下空闲连接无界累积（见 issue #124）。
const (
	httpMaxIdleConns        = 100
	httpMaxIdleConnsPerHost = 20
	httpIdleConnTimeout     = 90 * time.Second
)

func clonedDefaultTransport() (*http.Transport, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default transport is not *http.Transport")
	}
	cloned := transport.Clone()
	// 显式设定连接池上限与空闲超时，避免默认 MaxIdleConns=0（无界）带来的内存累积。
	cloned.MaxIdleConns = httpMaxIdleConns
	cloned.MaxIdleConnsPerHost = httpMaxIdleConnsPerHost
	cloned.IdleConnTimeout = httpIdleConnTimeout
	// 此处不再无条件注入 SafeDialContext：SafeDialContext 会读 context 里
	// AssertSafeRequestWithPin 钉入的上游安全 IP，并把拨号地址的 host 替换为它。
	// HTTP 代理路径里 DialContext 收到的 addr 是「代理服务器地址」而非上游地址，
	// 若沿用 SafeDialContext 会把上游 IP 拼上代理端口（上游IP:7890）导致拨号目标
	// 错误、必超时。故仅直连路径在 newHTTPClientNoProxyWithTimeoutConfig 中注入；
	// socks/ss/vmess 代理路径在各自分支自设 DialContext 覆盖。
	return cloned, nil
}

// clientTimeoutConfig 描述一个档位应该怎样施加超时。
//
// overallTimeout > 0（short 档）：http.Client.Timeout 覆盖建连、重定向与 body
// 读取全过程，对非流式短请求（连通性探测、模型拉取）是正确语义。
//
// overallTimeout == 0（default 档）：Client.Timeout 置 0（不限），改用
// Transport.ResponseHeaderTimeout 限制「等响应头」的时间。该字段只覆盖到
// 收到响应头为止，不影响后续 body 读取，因此长时程流式响应不会被中途砍断；
// 同时仍能防住「上游连响应头都不返回」的僵死连接。
//
// headerTimeout > 0 时写入 ResponseHeaderTimeout（仅 default 档）；为 0 时不写
// （short 档靠整体超时，无需重复计时）。
type clientTimeoutConfig struct {
	overallTimeout time.Duration
	headerTimeout  time.Duration
}

// shortTaskTimeoutConfig 是 short 档（30s）的超时配置：保持整体超时语义不变。
//
// 这里有意**不**设 ResponseHeaderTimeout：short 档服务的都是非流式短请求
// （连通性探测、延迟测量、模型拉取），Client.Timeout=30s 已经覆盖了建连 +
// body 读取全过程，再加一层响应头超时只会重复计时，不带来新保护。
// 保持 headerTimeout=0 可让 short 档的行为与本次修复前完全一致
// （只多了 withDialTimeout 包装，而它在 30s 整体上限下是 no-op）。
func shortTaskTimeoutConfig() clientTimeoutConfig {
	return clientTimeoutConfig{overallTimeout: shortTaskTimeout, headerTimeout: 0}
}

// longTaskTimeoutConfig 是 default 档（转发 / 长任务）的超时配置：整体不限时，只限响应头。
//
// overallTimeout=0 是本次修复的核心：http.Client.Timeout 覆盖建连、重定向**与 body
// 读取全过程**，且计时器在 Do 返回后仍在运行、会中断 Response.Body 的读取
// （见 $GOROOT/src/net/http/client.go 的 Client.Timeout 文档与 cancelTimerBody.Read）。
// 长时程流式响应（reasoning 模型 + 长输出）一旦超过该值就会被客户端从流中间砍断，
// relay 侧收到的是普通读取错误而非断连哨兵，客户端拿不到 [DONE]，且会计入熔断失败。
//
// headerTimeout=responseHeaderTimeout 接管僵死保护：只限制「等响应头」的时间，
// 收到响应头后即停止计时，不影响后续 body 读取。
//
// 注意：对 stream:false 的转发请求，上游通常在生成完成后才发响应头，因此这类请求的
// 有效上限仍是 responseHeaderTimeout（默认 600s，与修复前的 Client.Timeout 一致，
// 无回归）；需要更长时可用 OCTOPUS_HTTP_RESPONSE_HEADER_TIMEOUT_SECONDS 调整。
func longTaskTimeoutConfig() clientTimeoutConfig {
	return clientTimeoutConfig{overallTimeout: 0, headerTimeout: responseHeaderTimeout}
}

// applyTimeouts 把超时配置写入 transport 与 client。
func applyTimeouts(transport *http.Transport, cfg clientTimeoutConfig) *http.Client {
	if cfg.headerTimeout > 0 {
		transport.ResponseHeaderTimeout = cfg.headerTimeout
	}
	return &http.Client{Transport: transport, Timeout: cfg.overallTimeout}
}

// withDialTimeout 给一个 DialContext 包上建连超时（对所有代理模式生效）。
//
// 必要性：Client.Timeout 改为 0 之后，「连不上但也不报错」的僵死拨号不再有任何
// 内置上限。具体到各分支：
//   - 直连：xurl.SafeDialContext 内部用裸 &net.Dialer{}（无 Timeout）；
//   - socks/socks5：x/net socks Dialer 内部用裸 net.Dialer + context.Background()，
//     既不受请求 ctx 取消控制，也没有建连超时；
//   - ss/vmess/vless/trojan：proxyx.dialTCP 已有 20s，但握手阶段无上限。
//
// 安全性：已验证 net.Dialer.DialContext 成功返回后会清除 conn 上的 deadline
// （stdlib dialCtx 只在拨号期间用 WithDeadline 派生子 ctx），因此用超时 ctx
// 包住拨号不会给后续长时程 body 读取留下残留 deadline。
func withDialTimeout(timeout time.Duration, dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if timeout <= 0 {
			return dial(ctx, network, addr)
		}
		if existing, ok := ctx.Deadline(); ok && !existing.IsZero() && time.Until(existing) < timeout {
			// 调用方已给出更紧的 deadline（如 short 档的请求 ctx），不覆盖。
			return dial(ctx, network, addr)
		}
		dialCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return dial(dialCtx, network, addr)
	}
}

func newHTTPClientNoProxy() (*http.Client, error) {
	return newHTTPClientNoProxyWithTimeoutConfig(longTaskTimeoutConfig())
}

// newHTTPClientNoProxyWithTimeoutConfig 构造直连（无代理）client。
func newHTTPClientNoProxyWithTimeoutConfig(cfg clientTimeoutConfig) (*http.Client, error) {
	cloned, err := clonedDefaultTransport()
	if err != nil {
		return nil, err
	}
	cloned.Proxy = nil
	// 直连场景注入 SafeDialContext：从 context 读 AssertSafeRequestWithPin 钉入的
	// 安全 IP，直连该 IP 彻底消除 DNS rebinding 窗口；未携带时回退默认拨号。
	cloned.DialContext = withDialTimeout(defaultDialTimeout, xurl.SafeDialContext)
	return applyTimeouts(cloned, cfg), nil
}

func newHTTPClientCustomProxy(proxyURLStr string) (*http.Client, error) {
	return newHTTPClientCustomProxyWithTimeoutConfig(proxyURLStr, longTaskTimeoutConfig())
}

// newHTTPClientCustomProxyWithTimeoutConfig 构造走自定义代理的 client。
func newHTTPClientCustomProxyWithTimeoutConfig(proxyURLStr string, cfg clientTimeoutConfig) (*http.Client, error) {
	cloned, err := clonedDefaultTransport()
	if err != nil {
		return nil, err
	}

	proxyURL, err := url.Parse(proxyURLStr)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy url: %w", err)
	}

	switch proxyURL.Scheme {
	case "http", "https":
		// HTTP(S) 代理由代理端解析并连接上游域名，客户端只需连到代理地址。
		// 这里保留 clonedDefaultTransport 的默认 DialContext（不注入 SafeDialContext），
		// 避免上游钉住的 IP 被拼到代理地址上导致拨号目标错误。
		cloned.Proxy = http.ProxyURL(proxyURL)
		cloned.DialContext = withDialTimeout(defaultDialTimeout, cloned.DialContext)
	case "socks", "socks5":
		socksDialer, err := proxy.FromURL(proxyURL, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("invalid socks proxy: %w", err)
		}
		cloned.Proxy = nil
		// 与上游 e8c6a0ef 对齐：保留 ContextDialer 断言（fail-fast 而非静默回退），
		// 同时用 withDialTimeout 包上 30s 建连超时。
		contextDialer, ok := socksDialer.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("SOCKS proxy must support cancellable dialing")
		}
		cloned.DialContext = withDialTimeout(defaultDialTimeout, func(ctx context.Context, network, addr string) (net.Conn, error) {
			return contextDialer.DialContext(ctx, network, addr)
		})
	case "ss", "vmess", "vless", "trojan":
		dialContext, err := proxyx.NewDialContext(proxyURLStr)
		if err != nil {
			return nil, err
		}
		cloned.Proxy = nil
		cloned.DialContext = withDialTimeout(defaultDialTimeout, dialContext)
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s", proxyURL.Scheme)
	}

	return applyTimeouts(cloned, cfg), nil
}
