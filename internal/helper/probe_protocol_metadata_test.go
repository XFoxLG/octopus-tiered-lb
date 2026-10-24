package helper

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	m "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

// probeMetadataUpstream 假上游：返回合法的生成响应，并把收到的 Authorization
// 回显进响应体，用来证明「落库脱敏」确实生效。
func probeMetadataUpstream(t *testing.T, paths *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if paths != nil {
			*paths = append(*paths, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		echo := fmt.Sprintf(`"echo":{"authorization":%q}`, r.Header.Get("Authorization"))
		switch {
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}],` + echo + `}`))
		case strings.HasSuffix(r.URL.Path, "/responses"):
			_, _ = w.Write([]byte(`{"id":"r","object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}],` + echo + `}`))
		case strings.HasSuffix(r.URL.Path, "/models"):
			_, _ = w.Write([]byte(`{"data":[{"id":"meta-model"}],` + echo + `}`))
		default:
			_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","content":[{"type":"text","text":"OK"}],` + echo + `}`))
		}
	}))
}

// fingerprintByEndpointAndItem 把一次探测里「真的发过请求」的行按接口+判据索引起来，
// 用于跨两次探测对拍请求指纹。
func fingerprintByEndpointAndItem(run *m.ChannelProbeRun) map[string]string {
	index := map[string]string{}
	for _, result := range run.Results {
		if result.RequestFingerprint == "" {
			continue
		}
		index[result.EndpointID+"|"+result.Item] = result.RequestFingerprint
	}
	return index
}

// 配置化连接：新结果必须记录实际协议/接口/地址/Key 范围/stream/budget/请求指纹，
// 且任何落库字段都不得出现凭据。
func TestConfiguredProbePersistsProtocolMetadataWithoutCredentials(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	secret := "sk-probe-metadata-secret-0123456789"
	srv := probeMetadataUpstream(t, nil)
	defer srv.Close()

	cfg := &m.ConnectionConfig{Version: 1, Selection: "configured", Catalog: m.ModelCatalog{Format: "manual"}}
	cfg.Endpoints = append(cfg.Endpoints,
		m.ChannelEndpoint{ID: "chat-one", Protocol: "chat", URL: srv.URL, URLMode: "base", Auth: "default"},
		m.ChannelEndpoint{ID: "responses-one", Protocol: "responses", URL: srv.URL, URLMode: "base", Auth: "default"},
	)
	ch := &m.Channel{
		Type:             outbound.OutboundTypeOpenAIChat,
		ConnectionConfig: cfg,
		Keys:             []m.ChannelKey{{ID: 42, Enabled: true, ChannelKey: secret}},
	}

	run, err := RunChannelCapabilityProbe(context.Background(), ch, m.ChannelProbeRequest{ModelName: "meta-model", KeyIndex: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Results) == 0 {
		t.Fatal("probe produced no results")
	}

	sent := 0
	for _, result := range run.Results {
		if result.RequestFingerprint == "" {
			continue
		}
		sent++
		wantProtocol := strings.TrimSuffix(result.EndpointID, "-one")
		if result.Protocol != wantProtocol {
			t.Errorf("actual protocol %q does not match interface %q", result.Protocol, result.EndpointID)
		}
		if !strings.HasPrefix(result.EndpointURL, srv.URL) || strings.Contains(result.EndpointURL, "?") {
			t.Errorf("endpoint URL not stored as a credential-free call target: %q", result.EndpointURL)
		}
		if result.KeyScope != "key_index:0,key_id:42" {
			t.Errorf("key scope %q does not describe the key used", result.KeyScope)
		}
		if result.RequestStream == nil || *result.RequestStream {
			t.Errorf("probe stream flag not recorded: %v", result.RequestStream)
		}
		if result.RequestBudgetMS == nil || *result.RequestBudgetMS != capabilityProbeTimeout.Milliseconds() {
			t.Errorf("probe budget not recorded: %v", result.RequestBudgetMS)
		}
		if len(result.RequestFingerprint) != 64 {
			t.Errorf("request fingerprint is not a sha256 hex digest: %q", result.RequestFingerprint)
		}
	}
	if sent == 0 {
		t.Fatal("no result recorded request metadata")
	}
	if rendered := fmt.Sprintf("%+v", run); strings.Contains(rendered, secret) {
		t.Fatal("probe run leaked the channel credential")
	}
}

// 旧式（未配置连接）渠道：协议取实际 adapter，接口 ID 为空，地址是实际调用地址。
func TestLegacyProbeRecordsAdapterProtocolAndCallTarget(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	secret := "sk-legacy-probe-secret-0123456789"
	srv := probeMetadataUpstream(t, nil)
	defer srv.Close()

	ch := &m.Channel{
		Type:     outbound.OutboundTypeOpenAIChat,
		BaseUrls: []m.BaseUrl{{URL: srv.URL}},
		Keys:     []m.ChannelKey{{ID: 7, Enabled: true, ChannelKey: secret}},
	}
	run, err := RunChannelCapabilityProbe(context.Background(), ch, m.ChannelProbeRequest{ModelName: "meta-model", KeyIndex: 0})
	if err != nil {
		t.Fatal(err)
	}

	byItem := map[string]m.ChannelProbeResult{}
	for _, result := range run.Results {
		if result.Kind == m.ProbeKindProtocol {
			byItem[result.Item] = result
		}
	}
	chat, ok := byItem[m.ProbeItemProtocolChat]
	if !ok {
		t.Fatalf("chat protocol row missing: %+v", byItem)
	}
	if chat.Protocol != "chat" {
		t.Errorf("legacy channel recorded protocol %q instead of the actual adapter", chat.Protocol)
	}
	if chat.EndpointID != "" {
		t.Errorf("legacy channel invented an interface id: %q", chat.EndpointID)
	}
	if !strings.HasSuffix(chat.EndpointURL, "/chat/completions") || !strings.HasPrefix(chat.EndpointURL, srv.URL) {
		t.Errorf("legacy call target not recorded: %q", chat.EndpointURL)
	}
	if chat.KeyScope != "key_index:0,key_id:7" {
		t.Errorf("legacy key scope wrong: %q", chat.KeyScope)
	}
	if chat.RequestStream == nil || *chat.RequestStream {
		t.Errorf("legacy stream flag not recorded: %v", chat.RequestStream)
	}
	if chat.RequestBudgetMS == nil || *chat.RequestBudgetMS != capabilityProbeTimeout.Milliseconds() {
		t.Errorf("legacy budget not recorded: %v", chat.RequestBudgetMS)
	}
	if len(chat.RequestFingerprint) != 64 {
		t.Errorf("legacy fingerprint missing: %q", chat.RequestFingerprint)
	}
}

// 同一个请求体 + 路径 + 方法必须得到同一个指纹，且指纹不含凭据：
// 换一把 Key 重跑，指纹必须逐字一致，否则对照不可复现。
func TestProbeFingerprintIdentifiesTheWireBodyNotTheCredential(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })
	srv := probeMetadataUpstream(t, nil)
	defer srv.Close()

	runOnce := func(key string) *m.ChannelProbeRun {
		t.Helper()
		cfg := &m.ConnectionConfig{Version: 1, Selection: "configured", Catalog: m.ModelCatalog{Format: "manual"}}
		cfg.Endpoints = append(cfg.Endpoints, m.ChannelEndpoint{ID: "chat-one", Protocol: "chat", URL: srv.URL, URLMode: "base", Auth: "default"})
		ch := &m.Channel{
			Type:             outbound.OutboundTypeOpenAIChat,
			ConnectionConfig: cfg,
			Keys:             []m.ChannelKey{{ID: 1, Enabled: true, ChannelKey: key}},
		}
		run, err := RunChannelCapabilityProbe(context.Background(), ch, m.ChannelProbeRequest{ModelName: "meta-model", KeyIndex: 0})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}

	first := fingerprintByEndpointAndItem(runOnce("sk-first-key-0123456789"))
	second := fingerprintByEndpointAndItem(runOnce("sk-second-key-0123456789"))
	if len(first) == 0 || len(second) == 0 {
		t.Fatalf("no fingerprints captured: %v / %v", first, second)
	}
	for key, fingerprint := range first {
		if second[key] != fingerprint {
			t.Errorf("identical requests produced different fingerprints for %s", key)
		}
	}
}
