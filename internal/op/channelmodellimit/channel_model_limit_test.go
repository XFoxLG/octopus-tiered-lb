package channelmodellimit

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
)

func setupLimitTest(t *testing.T) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "channel-model-limit.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := setting.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache failed: %v", err)
	}
	resetLimitStateForTest()
	t.Cleanup(resetLimitStateForTest)
}

func resetLimitStateForTest() {
	cacheMu.Lock()
	cache = map[string]Entry{}
	cacheMu.Unlock()
	clearEvidenceDedupeForTest()
}

// clearEvidenceDedupeForTest 清空「60 秒独立证据」去重表：测试需要在不等待
// 真实时间窗的前提下构造多次独立超限。
func clearEvidenceDedupeForTest() {
	evidenceMu.Lock()
	evidenceDedupe = map[string]time.Time{}
	evidenceMu.Unlock()
}

// TestSetManualLookupDeleteAndNormalization 验证人工上限的读写与归一化：
// 大小写/空白归一为同一条记录，<=0 等价删除。
func TestSetManualLookupDeleteAndNormalization(t *testing.T) {
	setupLimitTest(t)
	ctx := context.Background()

	if err := SetManual(ctx, 1, "  GLM-4.6 ", 128000); err != nil {
		t.Fatalf("SetManual failed: %v", err)
	}
	entry, ok := Lookup(1, "glm-4.6")
	if !ok || entry.MaxContextTokens != 128000 || entry.Source != model.ChannelModelLimitSourceManual {
		t.Fatalf("Lookup = %+v ok=%v, want manual/128000", entry, ok)
	}
	if _, ok := Lookup(1, "other-model"); ok {
		t.Fatal("unknown model must not resolve a limit")
	}
	if _, ok := Lookup(2, "glm-4.6"); ok {
		t.Fatal("unknown channel must not resolve a limit")
	}

	// 大小写不敏感 + upsert 覆盖（同一条记录，不新增行）。
	if err := SetManual(ctx, 1, "glm-4.6", 64000); err != nil {
		t.Fatalf("SetManual update failed: %v", err)
	}
	entries, err := ListByChannel(ctx, 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ListByChannel = %d entries, err=%v, want 1", len(entries), err)
	}
	if entries[0].MaxContextTokens != 64000 {
		t.Fatalf("updated limit = %d, want 64000", entries[0].MaxContextTokens)
	}

	// <=0 等价删除。
	if err := SetManual(ctx, 1, "glm-4.6", 0); err != nil {
		t.Fatalf("SetManual(delete) failed: %v", err)
	}
	if _, ok := Lookup(1, "glm-4.6"); ok {
		t.Fatal("limit must be deleted after SetManual(<=0)")
	}
}

// TestManualLimitNeverOverwrittenByLearning 验证人工声明永不被学习覆盖：
// 两次独立超限证据只累计 evidence_count，源与上限值保持 manual。
func TestManualLimitNeverOverwrittenByLearning(t *testing.T) {
	setupLimitTest(t)
	ctx := context.Background()

	if err := SetManual(ctx, 2, "m", 32000); err != nil {
		t.Fatalf("SetManual failed: %v", err)
	}
	if written, evidence := RecordOverflow(ctx, 2, "m", 20000); written || evidence != 1 {
		t.Fatalf("first overflow = (%v,%d), want (false,1)", written, evidence)
	}
	clearEvidenceDedupeForTest()
	if written, evidence := RecordOverflow(ctx, 2, "m", 18000); written || evidence != 2 {
		t.Fatalf("second overflow = (%v,%d), want (false,2)", written, evidence)
	}

	entry, ok := Lookup(2, "m")
	if !ok || entry.MaxContextTokens != 32000 || entry.Source != model.ChannelModelLimitSourceManual {
		t.Fatalf("manual limit was overwritten: %+v ok=%v", entry, ok)
	}
	if entry.EvidenceCount != 2 {
		t.Fatalf("evidence count = %d, want 2 (manual 只累计证据不改值)", entry.EvidenceCount)
	}
}

// TestLearnedLimitRequiresTwoIndependentOverflowsAndKeepsMinimum 验证学习路径：
// 达阈值前不生效；60 秒内重复证据去重；写入值为观察到的最小失败估算。
func TestLearnedLimitRequiresTwoIndependentOverflowsAndKeepsMinimum(t *testing.T) {
	setupLimitTest(t)
	ctx := context.Background()

	if written, evidence := RecordOverflow(ctx, 3, "m", 30000); written || evidence != 1 {
		t.Fatalf("first overflow = (%v,%d), want (false,1)", written, evidence)
	}
	if _, ok := Lookup(3, "m"); ok {
		t.Fatal("limit must not be effective before the learn threshold")
	}

	// 60 秒内同一目标的重复证据被去重（不计入独立证据）。
	if written, _ := RecordOverflow(ctx, 3, "m", 29000); written {
		t.Fatal("duplicate evidence within the dedupe window must not write")
	}
	clearEvidenceDedupeForTest()
	if written, evidence := RecordOverflow(ctx, 3, "m", 28000); !written || evidence != 2 {
		t.Fatalf("second independent overflow = (%v,%d), want (true,2)", written, evidence)
	}
	entry, ok := Lookup(3, "m")
	if !ok || entry.MaxContextTokens != 28000 || entry.Source != model.ChannelModelLimitSourceLearned {
		t.Fatalf("learned limit = %+v ok=%v, want learned/28000", entry, ok)
	}

	// 更大的失败值不抬高上限；更小的失败值把上限收敛得更保守。
	clearEvidenceDedupeForTest()
	RecordOverflow(ctx, 3, "m", 40000)
	entry, _ = Lookup(3, "m")
	if entry.MaxContextTokens != 28000 {
		t.Fatalf("learned limit raised by a larger observation: %d, want 28000", entry.MaxContextTokens)
	}
	clearEvidenceDedupeForTest()
	RecordOverflow(ctx, 3, "m", 20000)
	entry, _ = Lookup(3, "m")
	if entry.MaxContextTokens != 20000 {
		t.Fatalf("learned limit = %d, want 20000 (minimum observed)", entry.MaxContextTokens)
	}
}

// TestLearningDisabledBySetting 验证学习开关关闭时不产生任何证据行。
func TestLearningDisabledBySetting(t *testing.T) {
	setupLimitTest(t)
	ctx := context.Background()

	if err := setting.SetString(model.SettingKeyChannelModelLimitLearnEnabled, "false"); err != nil {
		t.Fatalf("disable learning failed: %v", err)
	}
	if written, evidence := RecordOverflow(ctx, 4, "m", 10000); written || evidence != 0 {
		t.Fatalf("disabled learning = (%v,%d), want (false,0)", written, evidence)
	}
	entries, err := ListByChannel(ctx, 4)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rows = %d err=%v, want 0 (learning disabled)", len(entries), err)
	}
}

// TestInitCacheLoadsPersistedRows 验证启动加载：清空内存缓存后可从数据库重建。
func TestInitCacheLoadsPersistedRows(t *testing.T) {
	setupLimitTest(t)
	ctx := context.Background()

	if err := SetManual(ctx, 5, "a", 1000); err != nil {
		t.Fatalf("SetManual(a) failed: %v", err)
	}
	if err := SetManual(ctx, 5, "b", 2000); err != nil {
		t.Fatalf("SetManual(b) failed: %v", err)
	}

	cacheMu.Lock()
	cache = map[string]Entry{}
	cacheMu.Unlock()
	if err := InitCache(ctx); err != nil {
		t.Fatalf("InitCache failed: %v", err)
	}
	if all := LookupAll(); len(all) != 2 {
		t.Fatalf("LookupAll = %d entries, want 2", len(all))
	}
	if entry, ok := Lookup(5, "b"); !ok || entry.MaxContextTokens != 2000 {
		t.Fatalf("reloaded entry = %+v ok=%v, want 2000", entry, ok)
	}
}
