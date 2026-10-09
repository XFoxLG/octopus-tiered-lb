package helper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/httpx"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

// uaRecord 记录一次到达假上游的请求轮廓，用于断言 User-Agent 与命中端点。
type uaRecord struct {
	Method    string
	Path      string
	UserAgent string
}

// uaGuardUpstream 是「拒绝 Go 默认 UA」的哨兵假上游。
//
// Go 标准库 transport 在请求未显式携带 User-Agent 时会以 Go-http-client/1.1
// 身份写出。生产统一 transport（internal/utils/httpx.WithUserAgent，由
// internal/client/http.go 的 applyTimeouts 注入所有渠道 client）应把空 UA 补成
// httpx.DefaultUserAgent()，并原样保留渠道 CustomHeader 里的显式 UA。
//
// 本上游把这些约定当协议来校验：空 UA 或 Go-http-client/* 一律 401（模拟做
// 客户端指纹校验的严格上游）。一旦生产 client 丢失 UA 包装（例如 applyTimeouts
// 改动后忘了包 httpx.WithUserAgent），本文件全部集成测试都会以 401 失败。
type uaGuardUpstream struct {
	server *httptest.Server

	mu      sync.Mutex
	records []uaRecord

	// modelsStatus > 0 时 /models 恒返回该状态码，模拟上游 /models 端点异常，
	// 触发 TestChannel 的真实模型调用回退（performChannelModelFallback）。
	modelsStatus int
}

// newUAGuardUpstream 启动哨兵上游；modelsStatus 传 0 表示 /models 正常返回 200。
func newUAGuardUpstream(t *testing.T, modelsStatus int) *uaGuardUpstream {
	t.Helper()
	u := &uaGuardUpstream{modelsStatus: modelsStatus}
	u.server = httptest.NewServer(http.HandlerFunc(u.serve))
	t.Cleanup(u.server.Close)
	return u
}

func (u *uaGuardUpstream) serve(w http.ResponseWriter, r *http.Request) {
	ua := r.Header.Get("User-Agent")
	u.mu.Lock()
	u.records = append(u.records, uaRecord{Method: r.Method, Path: r.URL.Path, UserAgent: ua})
	u.mu.Unlock()

	// 哨兵规则：空 UA / Go 默认 UA 一律 401，且先于任何端点逻辑生效。
	if ua == "" || strings.HasPrefix(ua, "Go-http-client") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"missing or default user-agent"}}`))
		return
	}

	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
		if u.modelsStatus > 0 {
			http.Error(w, `{"error":{"message":"models endpoint broken"}}`, u.modelsStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-a"},{"id":"model-b"}]}`))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	default:
		http.NotFound(w, r)
	}
}

// recordsSnapshot 返回已收到请求记录的快照。
func (u *uaGuardUpstream) recordsSnapshot() []uaRecord {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]uaRecord(nil), u.records...)
}

// recordsFor 返回命中指定 method + path 后缀的请求记录，一条都没有时直接失败。
func (u *uaGuardUpstream) recordsFor(t *testing.T, method, pathSuffix string) []uaRecord {
	t.Helper()
	var matched []uaRecord
	for _, rec := range u.recordsSnapshot() {
		if rec.Method == method && strings.HasSuffix(rec.Path, pathSuffix) {
			matched = append(matched, rec)
		}
	}
	if len(matched) == 0 {
		t.Fatalf("upstream received no %s %s request; got %+v", method, pathSuffix, u.recordsSnapshot())
	}
	return matched
}

// assertAppDefaultUA 断言到达上游的 UA 恰为 httpx.DefaultUserAgent()。
func assertAppDefaultUA(t *testing.T, rec uaRecord) {
	t.Helper()
	if want := httpx.DefaultUserAgent(); rec.UserAgent != want {
		t.Fatalf("User-Agent = %q, want %q (httpx.WithUserAgent must supply the app identity)", rec.UserAgent, want)
	}
}

// newUATestChannel 构造打向哨兵上游的最小 OpenAI 兼容渠道。
func newUATestChannel(u *uaGuardUpstream) appmodel.Channel {
	return appmodel.Channel{
		Type:     outbound.OutboundTypeOpenAIChat,
		BaseUrls: []appmodel.BaseUrl{{URL: u.server.URL}},
		Keys:     []appmodel.ChannelKey{{ChannelKey: "sk-test"}},
	}
}

// setupUAIntegrationTest 做两件事：关闭 dev mock（OCTOPUS_DEV_MOCK_SUCCESS 绕过
// Viper 直接读环境变量，见 conf.IsDevMockSuccess），以及按 helper 包既有约定
// （fetch_test.go 同款写法）临时放行 SSRF 私有地址，让真实渠道 client 可以打
// httptest loopback；两者都在测试结束时恢复。不使用 t.Parallel。
func setupUAIntegrationTest(t *testing.T) {
	t.Helper()
	t.Setenv("OCTOPUS_DEV_MOCK_SUCCESS", "")
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
}

// TestUAGuardUpstreamRejectsGoDefaultUserAgent 先校验哨兵本身：用不带任何包装的
// http.DefaultClient 请求，UA 是 Go-http-client/1.1，必须被 401 拒绝。防止哨兵
// 规则写错（比如永远放行）导致后续集成测试形同虚设。
func TestUAGuardUpstreamRejectsGoDefaultUserAgent(t *testing.T) {
	u := newUAGuardUpstream(t, 0)

	resp, err := http.Get(u.server.URL + "/v1/models")
	if err != nil {
		t.Fatalf("http.Get() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (guard must reject the Go default User-Agent)", resp.StatusCode)
	}

	recs := u.recordsFor(t, http.MethodGet, "/models")
	if recs[0].UserAgent == "" || !strings.HasPrefix(recs[0].UserAgent, "Go-http-client") {
		t.Fatalf("recorded User-Agent = %q, want the Go default Go-http-client/*", recs[0].UserAgent)
	}
}

// TestTestChannel_ConnectivityProbeSendsAppDefaultUserAgent 锁定 TestChannel 的
// GET /v1/models 连通性探测必须携带生产统一 UA：哨兵上游对 Go 默认 UA 拒 401，
// 探测通过即证明渠道 client 的 transport 注入了 httpx.DefaultUserAgent()。
func TestTestChannel_ConnectivityProbeSendsAppDefaultUserAgent(t *testing.T) {
	setupUAIntegrationTest(t)
	u := newUAGuardUpstream(t, 0)

	// 不配置模型：探测一旦失败不会触发模型回退，401 会原样反映在结果里。
	summary, err := TestChannel(context.Background(), newUATestChannel(u))
	if err != nil {
		t.Fatalf("TestChannel() error = %v", err)
	}
	if !summary.Passed {
		t.Fatalf("TestChannel() passed = false, want true; results = %+v", summary.Results)
	}
	if len(summary.Results) != 1 || summary.Results[0].StatusCode != http.StatusOK || summary.Results[0].Message != "ok" {
		t.Fatalf("results = %+v, want a single 200/ok result", summary.Results)
	}

	recs := u.recordsFor(t, http.MethodGet, "/models")
	if len(recs) != 1 {
		t.Fatalf("GET /models requests = %d, want 1; got %+v", len(recs), recs)
	}
	assertAppDefaultUA(t, recs[0])
}

// TestTestChannel_CustomHeaderUserAgentPreservedOnConnectivityProbe 验证渠道
// CustomHeader 显式配置的 User-Agent 原样到达上游，不被默认 UA 覆盖。
func TestTestChannel_CustomHeaderUserAgentPreservedOnConnectivityProbe(t *testing.T) {
	setupUAIntegrationTest(t)
	u := newUAGuardUpstream(t, 0)

	const customUA = "custom-probe-agent/1.2"
	channel := newUATestChannel(u)
	channel.CustomHeader = []appmodel.CustomHeader{{HeaderKey: "User-Agent", HeaderValue: customUA}}

	summary, err := TestChannel(context.Background(), channel)
	if err != nil {
		t.Fatalf("TestChannel() error = %v", err)
	}
	if !summary.Passed {
		t.Fatalf("TestChannel() passed = false, want true; results = %+v", summary.Results)
	}

	recs := u.recordsFor(t, http.MethodGet, "/models")
	if recs[0].UserAgent != customUA {
		t.Fatalf("User-Agent = %q, want %q (explicit channel UA must be preserved)", recs[0].UserAgent, customUA)
	}
	if recs[0].UserAgent == httpx.DefaultUserAgent() {
		t.Fatal("custom User-Agent was overwritten by the default user agent")
	}
}

// TestTestChannel_ModelFallbackPostSendsAppDefaultUserAgent 验证连通性探测失败
// （/models 恒 400）后的真实模型调用回退——经 outbound adapter 发出的
// POST /v1/chat/completions——同样携带生产统一 UA。
func TestTestChannel_ModelFallbackPostSendsAppDefaultUserAgent(t *testing.T) {
	setupUAIntegrationTest(t)
	u := newUAGuardUpstream(t, http.StatusBadRequest)

	channel := newUATestChannel(u)
	channel.Model = "gpt-4o-mini"

	summary, err := TestChannel(context.Background(), channel)
	if err != nil {
		t.Fatalf("TestChannel() error = %v", err)
	}
	if !summary.Passed {
		t.Fatalf("TestChannel() passed = false, want true; results = %+v", summary.Results)
	}
	if len(summary.Results) != 1 || summary.Results[0].Message != "ok (via model call)" {
		t.Fatalf("results = %+v, want single result passed via model call", summary.Results)
	}

	// POST 必须带默认 UA 通过哨兵：若 UA 包装丢失，这里会先被 401 拒绝并整体失败。
	posts := u.recordsFor(t, http.MethodPost, "/chat/completions")
	for _, rec := range posts {
		assertAppDefaultUA(t, rec)
	}
}

// TestFetchModels_SendsAppDefaultUserAgent 验证模型抓取（GET /v1/models，走
// FetchModels → 渠道 client）携带生产统一 UA，且能正常解析模型列表。
func TestFetchModels_SendsAppDefaultUserAgent(t *testing.T) {
	setupUAIntegrationTest(t)
	u := newUAGuardUpstream(t, 0)

	request := newUATestChannel(u)
	request.Keys[0].Enabled = true // FetchModels 走 GetChannelKey，要求 key 启用

	models, err := FetchModels(context.Background(), request)
	if err != nil {
		t.Fatalf("FetchModels() error = %v", err)
	}
	if len(models) != 2 || models[0] != "model-a" || models[1] != "model-b" {
		t.Fatalf("models = %v, want [model-a model-b]", models)
	}

	recs := u.recordsFor(t, http.MethodGet, "/models")
	if len(recs) != 1 {
		t.Fatalf("GET /models requests = %d, want 1; got %+v", len(recs), recs)
	}
	assertAppDefaultUA(t, recs[0])
}

// TestFetchModels_CustomHeaderUserAgentPreserved 验证模型抓取同样保留渠道
// CustomHeader 显式 UA。
func TestFetchModels_CustomHeaderUserAgentPreserved(t *testing.T) {
	setupUAIntegrationTest(t)
	u := newUAGuardUpstream(t, 0)

	const customUA = "custom-sync-agent/2.0"
	request := newUATestChannel(u)
	request.Keys[0].Enabled = true
	request.CustomHeader = []appmodel.CustomHeader{{HeaderKey: "User-Agent", HeaderValue: customUA}}

	models, err := FetchModels(context.Background(), request)
	if err != nil {
		t.Fatalf("FetchModels() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %v, want 2 entries", models)
	}

	recs := u.recordsFor(t, http.MethodGet, "/models")
	if recs[0].UserAgent != customUA {
		t.Fatalf("User-Agent = %q, want %q (explicit channel UA must be preserved)", recs[0].UserAgent, customUA)
	}
}

// TestSendGroupProbeRequest_PostSendsAppDefaultUserAgent 验证分组/渠道模型测试
// 的真实 POST 探测（sendGroupProbeRequest → outbound adapter）携带生产统一 UA。
// 该函数是 StartChannelModelTest / 分组模型测试共用的底层实现。
func TestSendGroupProbeRequest_PostSendsAppDefaultUserAgent(t *testing.T) {
	setupUAIntegrationTest(t)
	u := newUAGuardUpstream(t, 0)

	channel := newUATestChannel(u)

	statusCode, responseText, internalResp, err := sendGroupProbeRequest(
		context.Background(),
		outbound.Get(outbound.OutboundTypeOpenAIChat),
		outbound.OutboundTypeOpenAIChat,
		&channel,
		"sk-test",
		appmodel.EndpointTypeChat,
		"gpt-4o-mini",
	)
	if err != nil {
		t.Fatalf("sendGroupProbeRequest() error = %v", err)
	}
	if statusCode != http.StatusOK {
		t.Fatalf("sendGroupProbeRequest() status = %d, want 200", statusCode)
	}
	if responseText == "" {
		t.Fatal("sendGroupProbeRequest() responseText = empty, want upstream payload")
	}
	if internalResp == nil {
		t.Fatal("sendGroupProbeRequest() internalResp = nil, want parsed response")
	}

	posts := u.recordsFor(t, http.MethodPost, "/chat/completions")
	if len(posts) != 1 {
		t.Fatalf("POST /chat/completions requests = %d, want 1; got %+v", len(posts), posts)
	}
	assertAppDefaultUA(t, posts[0])
}

// TestSendGroupProbeRequest_PostCustomUserAgentPreserved 验证真实 POST 探测同样
// 保留渠道 CustomHeader 显式 UA。
func TestSendGroupProbeRequest_PostCustomUserAgentPreserved(t *testing.T) {
	setupUAIntegrationTest(t)
	u := newUAGuardUpstream(t, 0)

	const customUA = "custom-group-probe-agent/3.1"
	channel := newUATestChannel(u)
	channel.CustomHeader = []appmodel.CustomHeader{{HeaderKey: "User-Agent", HeaderValue: customUA}}

	statusCode, _, _, err := sendGroupProbeRequest(
		context.Background(),
		outbound.Get(outbound.OutboundTypeOpenAIChat),
		outbound.OutboundTypeOpenAIChat,
		&channel,
		"sk-test",
		appmodel.EndpointTypeChat,
		"gpt-4o-mini",
	)
	if err != nil {
		t.Fatalf("sendGroupProbeRequest() error = %v", err)
	}
	if statusCode != http.StatusOK {
		t.Fatalf("sendGroupProbeRequest() status = %d, want 200", statusCode)
	}

	posts := u.recordsFor(t, http.MethodPost, "/chat/completions")
	if posts[0].UserAgent != customUA {
		t.Fatalf("User-Agent = %q, want %q (explicit channel UA must be preserved)", posts[0].UserAgent, customUA)
	}
	if posts[0].UserAgent == httpx.DefaultUserAgent() {
		t.Fatal("custom User-Agent was overwritten by the default user agent")
	}
}
