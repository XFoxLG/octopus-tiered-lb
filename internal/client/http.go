package client

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
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
)

var (
	systemDirectClient       *http.Client
	systemProxyClient        *http.Client
	systemProxyURL           string
	shortTimeoutDirectClient *http.Client
	shortTimeoutProxyClient  *http.Client
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
	return getOrBuildCustomProxyClient(proxyURL, 600*time.Second)
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
// customProxyClientBucket(timeout) 决定，当前仅 "default"(600s) 与 "short"(30s) 两档。
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
	client, err := newHTTPClientCustomProxyWithTimeout(proxyURL, timeout)
	if err != nil {
		return nil, err
	}
	customProxyClients[bucket][proxyURL] = client
	return client, nil
}

// customProxyClientBucket 把 timeout 映射为缓存分桶 key。同 timeout 复用同一 map，
// 避免不同 timeout 的 client 互相覆盖。当前仅 600s（转发）与 30s（后台任务）两档。
func customProxyClientBucket(timeout time.Duration) string {
	if timeout <= shortTaskTimeout {
		return "short"
	}
	return "default"
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
		if shortTimeoutProxyClient != nil && systemProxyURL == currentProxyURL {
			clientLock.RUnlock()
			return shortTimeoutProxyClient, nil
		}
		clientLock.RUnlock()

		clientLock.Lock()
		defer clientLock.Unlock()

		if shortTimeoutProxyClient != nil && systemProxyURL == currentProxyURL {
			return shortTimeoutProxyClient, nil
		}

		client, err := newHTTPClientCustomProxyWithTimeout(currentProxyURL, shortTaskTimeout)
		if err != nil {
			return nil, err
		}
		shortTimeoutProxyClient = client
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
	client, err := newHTTPClientNoProxyWithTimeout(shortTaskTimeout)
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
	// 错误、必超时。故仅直连路径在 newHTTPClientNoProxyWithTimeout 中注入；
	// socks/ss/vmess 代理路径在各自分支自设 DialContext 覆盖。
	return cloned, nil
}

func newHTTPClientNoProxy() (*http.Client, error) {
	return newHTTPClientNoProxyWithTimeout(600 * time.Second)
}

func newHTTPClientNoProxyWithTimeout(timeout time.Duration) (*http.Client, error) {
	cloned, err := clonedDefaultTransport()
	if err != nil {
		return nil, err
	}
	cloned.Proxy = nil
	// 直连场景注入 SafeDialContext：从 context 读 AssertSafeRequestWithPin 钉入的
	// 安全 IP，直连该 IP 彻底消除 DNS rebinding 窗口；未携带时回退默认拨号。
	cloned.DialContext = xurl.SafeDialContext
	return &http.Client{Transport: cloned, Timeout: timeout}, nil
}

func newHTTPClientCustomProxy(proxyURLStr string) (*http.Client, error) {
	return newHTTPClientCustomProxyWithTimeout(proxyURLStr, 600*time.Second)
}

func newHTTPClientCustomProxyWithTimeout(proxyURLStr string, timeout time.Duration) (*http.Client, error) {
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
	case "socks", "socks5":
		socksDialer, err := proxy.FromURL(proxyURL, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("invalid socks proxy: %w", err)
		}
		cloned.Proxy = nil
		cloned.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return socksDialer.Dial(network, addr)
		}
	case "ss", "vmess", "vless", "trojan":
		dialContext, err := proxyx.NewDialContext(proxyURLStr)
		if err != nil {
			return nil, err
		}
		cloned.Proxy = nil
		cloned.DialContext = dialContext
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s", proxyURL.Scheme)
	}

	return &http.Client{Transport: cloned, Timeout: timeout}, nil
}
