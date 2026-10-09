package helper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

// modelsUpstream 起一个按 Authorization Bearer token 返回不同模型列表的假上游。
// token 不在表里 → 401（让对应 key 抓取失败）。
func modelsUpstream(t *testing.T, modelsByToken map[string][]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		models, ok := modelsByToken[token]
		if !ok {
			http.Error(w, `{"error":"unknown token"}`, http.StatusUnauthorized)
			return
		}
		data := make([]map[string]string, 0, len(models))
		for _, m := range models {
			data = append(data, map[string]string{"id": m})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestFetchModelsPerKeyReturnsKeyID 锁住 KeyModelResult.KeyID 的正确性：
// 逐 key 回填 channel_keys.supported_models 完全依赖它定位行（task.SyncModelsTask），
// 填错就会把 A key 的模型权限写到 B key 上。
func TestFetchModelsPerKeyReturnsKeyID(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })

	srv := modelsUpstream(t, map[string][]string{
		"sk-first":  {"model-1a", "model-1b"},
		"sk-second": {"model-2a"},
		// "sk-third" 故意缺席 → 401 → Passed=false，但 KeyID 仍须正确带上。
	})

	request := model.Channel{
		ID:       77,
		Type:     outbound.OutboundTypeOpenAIChat,
		BaseUrls: []model.BaseUrl{{URL: srv.URL}},
		Keys: []model.ChannelKey{
			{ID: 101, Enabled: true, ChannelKey: "sk-first", Remark: "first"},
			{ID: 202, Enabled: true, ChannelKey: "sk-second", Remark: "second"},
			{ID: 303, Enabled: true, ChannelKey: "sk-third", Remark: "third"},
			{ID: 404, Enabled: false, ChannelKey: "sk-disabled", Remark: "disabled"},
			{ID: 505, Enabled: true, ChannelKey: "   ", Remark: "blank"},
		},
	}

	result, err := FetchModelsPerKey(context.Background(), request)
	if err != nil {
		t.Fatalf("FetchModelsPerKey() error = %v", err)
	}

	// 只有「启用且非空」的 key 参与抓取：disabled / blank 都不该出现。
	if len(result.Results) != 3 {
		t.Fatalf("results len = %d, want 3 (disabled and blank keys must be skipped)", len(result.Results))
	}

	byKeyID := make(map[int]KeyModelResult, len(result.Results))
	for _, r := range result.Results {
		if r.KeyID == 0 {
			t.Errorf("result for remark=%q has KeyID=0; KeyID must carry channel_keys.id", r.KeyRemark)
		}
		if _, dup := byKeyID[r.KeyID]; dup {
			t.Errorf("duplicate KeyID %d in results", r.KeyID)
		}
		byKeyID[r.KeyID] = r
	}

	if r, ok := byKeyID[101]; !ok {
		t.Error("KeyID 101 missing from results")
	} else {
		if !r.Passed {
			t.Errorf("key 101 passed = false, want true (message=%q)", r.Message)
		}
		if len(r.Models) != 2 || r.Models[0] != "model-1a" || r.Models[1] != "model-1b" {
			t.Errorf("key 101 models = %v, want [model-1a model-1b]", r.Models)
		}
		if r.KeyRemark != "first" {
			t.Errorf("key 101 remark = %q, want %q (KeyID must line up with the right key)", r.KeyRemark, "first")
		}
	}

	if r, ok := byKeyID[202]; !ok {
		t.Error("KeyID 202 missing from results")
	} else if !r.Passed || len(r.Models) != 1 || r.Models[0] != "model-2a" {
		t.Errorf("key 202 = passed:%v models:%v, want passed:true [model-2a]", r.Passed, r.Models)
	}

	// 失败的 key 同样要带正确 KeyID，否则调用方无从判断「哪个 key 该保留旧值」。
	if r, ok := byKeyID[303]; !ok {
		t.Error("KeyID 303 missing from results")
	} else {
		if r.Passed {
			t.Errorf("key 303 passed = true, want false (upstream returns 401)")
		}
		if r.Message == "" {
			t.Error("key 303 message is empty; failed results should carry a reason")
		}
		if len(r.Models) != 0 {
			t.Errorf("key 303 models = %v, want empty for a failed fetch", r.Models)
		}
	}

	// AllModels 是所有成功 key 的并集。
	if len(result.AllModels) != 3 {
		t.Errorf("AllModels = %v, want the union of the 3 models from the two passing keys", result.AllModels)
	}
}

// TestFetchModelsPerKeyShortTimeoutUsesSameSemantics 保证后台同步用的短超时变体
// 与诊断端点用的常规变体行为一致（同样按 key 隔离请求、同样带 KeyID），
// 只是 HTTP client 超时档位不同。
func TestFetchModelsPerKeyShortTimeoutUsesSameSemantics(t *testing.T) {
	xurl.SetSSRFAllowPrivateForTest(true)
	t.Cleanup(func() { xurl.SetSSRFAllowPrivateForTest(false) })

	srv := modelsUpstream(t, map[string][]string{
		"sk-alpha": {"m1", "m2"},
		"sk-beta":  {"m2", "m3"},
	})

	request := model.Channel{
		ID:       88,
		Type:     outbound.OutboundTypeOpenAIChat,
		BaseUrls: []model.BaseUrl{{URL: srv.URL}},
		Keys: []model.ChannelKey{
			{ID: 11, Enabled: true, ChannelKey: "sk-alpha", Remark: "alpha"},
			{ID: 22, Enabled: true, ChannelKey: "sk-beta", Remark: "beta"},
		},
	}

	result, err := FetchModelsPerKeyShortTimeout(context.Background(), request)
	if err != nil {
		t.Fatalf("FetchModelsPerKeyShortTimeout() error = %v", err)
	}
	if len(result.Results) != 2 {
		t.Fatalf("results len = %d, want 2", len(result.Results))
	}
	seen := make(map[int][]string, 2)
	for _, r := range result.Results {
		if !r.Passed {
			t.Fatalf("key %d failed: %s", r.KeyID, r.Message)
		}
		seen[r.KeyID] = r.Models
	}
	if got := seen[11]; len(got) != 2 || got[0] != "m1" || got[1] != "m2" {
		t.Errorf("key 11 models = %v, want [m1 m2]", got)
	}
	if got := seen[22]; len(got) != 2 || got[0] != "m2" || got[1] != "m3" {
		t.Errorf("key 22 models = %v, want [m2 m3]", got)
	}
	if len(result.AllModels) != 3 {
		t.Errorf("AllModels = %v, want the 3-model union", result.AllModels)
	}
}
