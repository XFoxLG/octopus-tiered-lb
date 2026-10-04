package task

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/helper"
	"github.com/lingyuins/octopus/internal/model"
	opchannel "github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/crypto"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

// ─────────────────────────────────────────────────────────────────────────────
// 纯函数单测：逐 key 回填的增量构造与并集计算。
// 这两个函数是「抓取失败绝不清空 SupportedModels」安全策略的落点，
// 单独测比只靠端到端测试更容易覆盖边界。
// ─────────────────────────────────────────────────────────────────────────────

func TestBuildKeySupportedModelUpdatesOnlyWritesPassedKeys(t *testing.T) {
	results := []helper.KeyModelResult{
		{KeyID: 1, Passed: true, Models: []string{"gpt-a", " gpt-b ", ""}},
		{KeyID: 2, Passed: false, Models: nil, Message: "upstream 429"},
		{KeyID: 3, Passed: true, Models: []string{"gpt-c"}},
	}
	current := []model.ChannelKey{
		{ID: 1, SupportedModels: ""},
		{ID: 2, SupportedModels: "old-only-key2"},
		{ID: 3, SupportedModels: "gpt-c"}, // 与抓取结果相同 → 不应产生更新项
	}

	updates := buildKeySupportedModelUpdates(results, current)

	byID := make(map[int]string, len(updates))
	for _, u := range updates {
		if u.SupportedModels == nil {
			t.Fatalf("update for key %d has nil SupportedModels (must never be nil)", u.ID)
		}
		byID[u.ID] = *u.SupportedModels
	}
	if len(byID) != 1 {
		t.Fatalf("updates = %#v, want exactly one entry (key 1)", byID)
	}
	if got := byID[1]; got != "gpt-a,gpt-b" {
		t.Fatalf("key 1 supported_models = %q, want %q (trim + drop empties)", got, "gpt-a,gpt-b")
	}
	if _, ok := byID[2]; ok {
		t.Fatal("failed key 2 must NOT be written (would clear its model isolation)")
	}
	if _, ok := byID[3]; ok {
		t.Fatal("key 3 is unchanged; no update entry should be generated")
	}
}

func TestBuildKeySupportedModelUpdatesSkipsEmptyAndOversized(t *testing.T) {
	oversized := strings.Repeat("m", keySupportedModelsColumnMax+1)
	results := []helper.KeyModelResult{
		{KeyID: 1, Passed: true, Models: nil},                 // 上游返回空列表
		{KeyID: 2, Passed: true, Models: []string{oversized}}, // 超出列容量
		{KeyID: 0, Passed: true, Models: []string{"x"}},       // KeyID 缺失（未保存的 key）
	}
	current := []model.ChannelKey{
		{ID: 1, SupportedModels: "keep-me"},
		{ID: 2, SupportedModels: "keep-me-too"},
	}

	if updates := buildKeySupportedModelUpdates(results, current); len(updates) != 0 {
		t.Fatalf("updates = %#v, want none (empty list / oversized CSV / missing KeyID must all be skipped)", updates)
	}
}

func TestBuildKeySupportedModelUpdatesNilResults(t *testing.T) {
	if updates := buildKeySupportedModelUpdates(nil, []model.ChannelKey{{ID: 1}}); updates != nil {
		t.Fatalf("updates = %#v, want nil for the single-key (non per-key) path", updates)
	}
}

func TestUnionKeyModelsKeepsFailedKeyOldValue(t *testing.T) {
	results := []helper.KeyModelResult{
		{KeyID: 1, Passed: true, Models: []string{"gpt-a", "gpt-b"}},
		{KeyID: 2, Passed: false, Message: "boom"},
		{KeyID: 3, Passed: true, Models: []string{"gpt-b", "gpt-c"}},
	}
	current := []model.ChannelKey{
		{ID: 1},
		{ID: 2, SupportedModels: "old-only-key2, gpt-a"},
		{ID: 3},
	}

	got := unionKeyModels(results, current)

	// 并集 = 成功 key 的模型 + 失败 key 的旧值；顺序按 results 顺序去重。
	want := []string{"gpt-a", "gpt-b", "old-only-key2", "gpt-c"}
	if !equalStringSlices(got, want) {
		t.Fatalf("unionKeyModels() = %v, want %v", got, want)
	}
}

func TestUnionKeyModelsIgnoresFailedKeyWithoutOldValue(t *testing.T) {
	results := []helper.KeyModelResult{
		{KeyID: 1, Passed: true, Models: []string{"gpt-a"}},
		{KeyID: 2, Passed: false},
	}
	got := unionKeyModels(results, []model.ChannelKey{{ID: 1}, {ID: 2}})
	if !equalStringSlices(got, []string{"gpt-a"}) {
		t.Fatalf("unionKeyModels() = %v, want [gpt-a]", got)
	}
}

func TestAnyKeyFetchPassed(t *testing.T) {
	if anyKeyFetchPassed(nil) {
		t.Fatal("nil results must not report a pass")
	}
	if anyKeyFetchPassed([]helper.KeyModelResult{{KeyID: 1}, {KeyID: 2}}) {
		t.Fatal("all-failed results must not report a pass")
	}
	if !anyKeyFetchPassed([]helper.KeyModelResult{{KeyID: 1}, {KeyID: 2, Passed: true}}) {
		t.Fatal("one pass must be reported")
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ─────────────────────────────────────────────────────────────────────────────
// 端到端：真实跑 SyncModelsTask，断言落库结果。
// ─────────────────────────────────────────────────────────────────────────────

// setupPerKeySyncDB 准备一个隔离的 SQLite 库 + 渠道缓存 + 依赖注入桩。
//
// 注意（AGENTS.md「依赖注入」陷阱）：internal/task 只 import op/channel 子包，
// 不会触发 internal/op 根包 init() 里的 GroupDefaultID/GroupGet 注入，
// 子包里的占位实现会返回 "not registered"。channel.Update 只在请求带 GroupID
// 时才调它们，但为了不被后续改动绊倒，这里显式注入并在 cleanup 还原。
func setupPerKeySyncDB(t *testing.T) {
	t.Helper()

	// 加密 key 字符串是跨包测试约定，勿改（见 AGENTS.md）。
	crypto.Init("octopus-test-encryption-key")

	// httptest 监听 127.0.0.1，按仓库约定临时放行私网地址。
	xurl.SetSSRFAllowPrivateForTest(true)

	dsn := filepath.Join(t.TempDir(), "sync-perkey.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	if err := db.InitLogDB("", "", false); err != nil {
		t.Fatalf("InitLogDB failed: %v", err)
	}

	origDefault := opchannel.GroupDefaultID
	origGet := opchannel.GroupGet
	origTracker := syncFailureTracker
	opchannel.GroupDefaultID = func(ctx context.Context) (int, error) { return 1, nil }
	opchannel.GroupGet = func(id int, ctx context.Context) (*model.ChannelGroup, error) {
		return &model.ChannelGroup{ID: id}, nil
	}
	// FailureTracker 是进程级全局：连续失败 3 次会进入 30 分钟冷却，
	// 跨测试串味会让后续用例静默跳过渠道。
	syncFailureTracker = NewFailureTracker()

	t.Cleanup(func() {
		syncFailureTracker = origTracker
		opchannel.GroupDefaultID = origDefault
		opchannel.GroupGet = origGet
		xurl.SetSSRFAllowPrivateForTest(false)
		// 清缓存 + 关库，避免全局单例污染同包其他测试。
		_ = opchannel.RefreshCache(context.Background())
		_ = db.Close()
	})
}

// perKeyUpstream 起一个按 Authorization Bearer token 区分返回内容的假上游。
// modelsByToken 里 token → nil 表示该 key 抓取失败（返回 500）。
func perKeyUpstream(t *testing.T, modelsByToken map[string][]string) *httptest.Server {
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
		if models == nil {
			http.Error(w, `{"error":"upstream exploded"}`, http.StatusInternalServerError)
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

// seedSyncChannel 直接落库一个渠道（含 keys），再刷新 op/channel 运行时缓存
// —— SyncModelsTask 的 channel.List 读的是缓存，不是 DB。
func seedSyncChannel(t *testing.T, ch *model.Channel) {
	t.Helper()
	if err := db.GetDB().Create(ch).Error; err != nil {
		t.Fatalf("seed channel %q failed: %v", ch.Name, err)
	}
	if err := opchannel.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache failed: %v", err)
	}
}

// loadSyncChannel 从 DB 读回渠道与其 keys（按 key ID 升序），供断言使用。
func loadSyncChannel(t *testing.T, id int) (model.Channel, []model.ChannelKey) {
	t.Helper()
	var ch model.Channel
	if err := db.GetDB().First(&ch, id).Error; err != nil {
		t.Fatalf("reload channel %d failed: %v", id, err)
	}
	var keys []model.ChannelKey
	if err := db.GetDB().Where("channel_id = ?", id).Order("id ASC").Find(&keys).Error; err != nil {
		t.Fatalf("reload keys for channel %d failed: %v", id, err)
	}
	return ch, keys
}

// modelsCSV 把 CSV 拆成排序后的切片，避免断言依赖上游返回顺序 / map 遍历顺序。
func modelsCSV(csv string) []string {
	out := make([]string, 0)
	for _, item := range strings.Split(csv, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return out
}

func TestSyncModelsTaskBackfillsSupportedModelsPerKey(t *testing.T) {
	setupPerKeySyncDB(t)
	srv := perKeyUpstream(t, map[string][]string{
		"sk-key-one": {"gpt-a", "gpt-b"},
		"sk-key-two": {"gpt-b", "gpt-c"},
	})

	ch := &model.Channel{
		Name:              "perkey-channel",
		GroupID:           1,
		Type:              outbound.OutboundTypeOpenAIChat,
		Enabled:           true,
		AutoSync:          true,
		AutoSyncKeyModels: true,
		BaseUrls:          []model.BaseUrl{{URL: srv.URL}},
		Keys: []model.ChannelKey{
			{Enabled: true, ChannelKey: "sk-key-one", Remark: "key-one"},
			{Enabled: true, ChannelKey: "sk-key-two", Remark: "key-two"},
		},
	}
	seedSyncChannel(t, ch)

	SyncModelsTask()

	got, keys := loadSyncChannel(t, ch.ID)
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	// 每个 key 各自的支持列表（顺序按 DB 主键 = 插入顺序）。
	byRemark := make(map[string]string, len(keys))
	for _, k := range keys {
		byRemark[k.Remark] = k.SupportedModels
	}
	if g := modelsCSV(byRemark["key-one"]); !equalStringSlices(g, []string{"gpt-a", "gpt-b"}) {
		t.Errorf("key-one supported_models = %v, want [gpt-a gpt-b] (raw=%q)", g, byRemark["key-one"])
	}
	if g := modelsCSV(byRemark["key-two"]); !equalStringSlices(g, []string{"gpt-b", "gpt-c"}) {
		t.Errorf("key-two supported_models = %v, want [gpt-b gpt-c] (raw=%q)", g, byRemark["key-two"])
	}
	// 渠道级 Model 仍是所有成功 key 的并集（语义与改造前一致）。
	if g := modelsCSV(got.Model); !equalStringSlices(g, []string{"gpt-a", "gpt-b", "gpt-c"}) {
		t.Errorf("channel model = %v, want union [gpt-a gpt-b gpt-c] (raw=%q)", g, got.Model)
	}
	// 渠道上的开关本身必须真的落库（迁移 / AutoMigrate 覆盖校验）。
	if !got.AutoSyncKeyModels {
		t.Error("auto_sync_key_models was not persisted as true")
	}
}

func TestSyncModelsTaskKeepsOldSupportedModelsWhenKeyFetchFails(t *testing.T) {
	setupPerKeySyncDB(t)
	// sk-bad 让上游返回 500 → 该 key 抓取失败。
	srv := perKeyUpstream(t, map[string][]string{
		"sk-good": {"gpt-a"},
		"sk-bad":  nil,
	})

	ch := &model.Channel{
		Name:              "perkey-partial-failure",
		GroupID:           1,
		Type:              outbound.OutboundTypeOpenAIChat,
		Enabled:           true,
		AutoSync:          true,
		AutoSyncKeyModels: true,
		BaseUrls:          []model.BaseUrl{{URL: srv.URL}},
		Keys: []model.ChannelKey{
			{Enabled: true, ChannelKey: "sk-good", Remark: "good"},
			{Enabled: true, ChannelKey: "sk-bad", Remark: "bad", SupportedModels: "legacy-only-model"},
		},
	}
	seedSyncChannel(t, ch)
	keyIDByName := make(map[string]int, len(ch.Keys))
	for _, k := range ch.Keys {
		keyIDByName[k.Remark] = k.ID
	}

	SyncModelsTask()

	got, keys := loadSyncChannel(t, ch.ID)
	supported := make(map[string]string, len(keys))
	for _, k := range keys {
		supported[k.Remark] = k.SupportedModels
	}

	// 关键安全断言：抓取失败的 key 旧值原样保留，绝不被清空。
	if supported["bad"] != "legacy-only-model" {
		t.Errorf("failed key supported_models = %q, want the untouched old value %q",
			supported["bad"], "legacy-only-model")
	}
	if g := modelsCSV(supported["good"]); !equalStringSlices(g, []string{"gpt-a"}) {
		t.Errorf("successful key supported_models = %v, want [gpt-a]", g)
	}
	// 渠道级 Model = 成功 key 的模型 + 失败 key 的旧值，不能把失败 key 的模型删掉
	// （删掉会连带删除 GroupItem 与价格行，并让该 key 在恢复前完全不可路由）。
	if g := modelsCSV(got.Model); !equalStringSlices(g, []string{"gpt-a", "legacy-only-model"}) {
		t.Errorf("channel model = %v, want [gpt-a legacy-only-model] (raw=%q)", g, got.Model)
	}
	if keyIDByName["bad"] == 0 {
		t.Fatal("seeded key ids are missing; test fixture is broken")
	}
}

func TestSyncModelsTaskAllKeysFailedLeavesEverythingUntouched(t *testing.T) {
	setupPerKeySyncDB(t)
	srv := perKeyUpstream(t, map[string][]string{
		"sk-bad-one": nil,
		"sk-bad-two": nil,
	})

	ch := &model.Channel{
		Name:              "perkey-all-failed",
		GroupID:           1,
		Type:              outbound.OutboundTypeOpenAIChat,
		Enabled:           true,
		AutoSync:          true,
		AutoSyncKeyModels: true,
		Model:             "existing-model",
		BaseUrls:          []model.BaseUrl{{URL: srv.URL}},
		Keys: []model.ChannelKey{
			{Enabled: true, ChannelKey: "sk-bad-one", Remark: "one", SupportedModels: "existing-model"},
			{Enabled: true, ChannelKey: "sk-bad-two", Remark: "two", SupportedModels: "other-model"},
		},
	}
	seedSyncChannel(t, ch)

	SyncModelsTask()

	got, keys := loadSyncChannel(t, ch.ID)
	if got.Model != "existing-model" {
		t.Errorf("channel model = %q, want untouched %q", got.Model, "existing-model")
	}
	for _, k := range keys {
		want := map[int]string{ch.Keys[0].ID: "existing-model", ch.Keys[1].ID: "other-model"}[k.ID]
		if k.SupportedModels != want {
			t.Errorf("key %d supported_models = %q, want untouched %q", k.ID, k.SupportedModels, want)
		}
	}
}

func TestSyncModelsTaskWithoutAutoSyncKeyModelsKeepsOldBehaviour(t *testing.T) {
	setupPerKeySyncDB(t)
	// 单 key 路径只抓成本最低的一个 key（GetChannelKey → selectKeyByCost）。
	// 两个 key 返回不同列表：渠道级 Model 应等于被选中那个 key 的列表，
	// 且**任何** key 的 SupportedModels 都不被写入。
	srv := perKeyUpstream(t, map[string][]string{
		"sk-cheap":  {"cheap-model"},
		"sk-pricey": {"pricey-model"},
	})

	ch := &model.Channel{
		Name:              "single-key-channel",
		GroupID:           1,
		Type:              outbound.OutboundTypeOpenAIChat,
		Enabled:           true,
		AutoSync:          true,
		AutoSyncKeyModels: false,
		BaseUrls:          []model.BaseUrl{{URL: srv.URL}},
		Keys: []model.ChannelKey{
			{Enabled: true, ChannelKey: "sk-cheap", Remark: "cheap", TotalCost: 0},
			{Enabled: true, ChannelKey: "sk-pricey", Remark: "pricey", TotalCost: 10},
		},
	}
	seedSyncChannel(t, ch)

	SyncModelsTask()

	got, keys := loadSyncChannel(t, ch.ID)
	if g := modelsCSV(got.Model); !equalStringSlices(g, []string{"cheap-model"}) {
		t.Errorf("channel model = %v, want [cheap-model] (lowest-cost key only), raw=%q", g, got.Model)
	}
	for _, k := range keys {
		if k.SupportedModels != "" {
			t.Errorf("key %d supported_models = %q, want empty (AutoSyncKeyModels=false must not touch it)",
				k.ID, k.SupportedModels)
		}
	}
}

func TestSyncModelsTaskPersistsOnlyKeyUpdatesWhenUnionUnchanged(t *testing.T) {
	setupPerKeySyncDB(t)
	srv := perKeyUpstream(t, map[string][]string{
		"sk-key-one": {"gpt-a", "gpt-b"},
		"sk-key-two": {"gpt-a", "gpt-b"},
	})

	// 渠道级 Model 已经是并集，但两个 key 各自还没有限定 → 只有 key 需要回填。
	ch := &model.Channel{
		Name:              "perkey-union-unchanged",
		GroupID:           1,
		Type:              outbound.OutboundTypeOpenAIChat,
		Enabled:           true,
		AutoSync:          true,
		AutoSyncKeyModels: true,
		Model:             "gpt-a,gpt-b",
		BaseUrls:          []model.BaseUrl{{URL: srv.URL}},
		Keys: []model.ChannelKey{
			{Enabled: true, ChannelKey: "sk-key-one", Remark: "one"},
			{Enabled: true, ChannelKey: "sk-key-two", Remark: "two"},
		},
	}
	seedSyncChannel(t, ch)

	SyncModelsTask()

	_, keys := loadSyncChannel(t, ch.ID)
	for _, k := range keys {
		if g := modelsCSV(k.SupportedModels); !equalStringSlices(g, []string{"gpt-a", "gpt-b"}) {
			t.Errorf("key %d supported_models = %q, want [gpt-a gpt-b] even though the channel union did not change",
				k.ID, k.SupportedModels)
		}
	}
}
