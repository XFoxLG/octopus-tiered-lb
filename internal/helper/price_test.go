package helper

import (
	"context"
	"testing"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/llm"
)

// TestLLMPriceRefreshExistingModels_PreservesManualPrices 验证：手动设置过价格
// 的模型（price_manual=true，经 llm.Create/Update 创建/编辑）不参与同步刷新，
// 即使同步源未命中也不会被写 0 覆盖。
func TestLLMPriceRefreshExistingModels_PreservesManualPrices(t *testing.T) {
	setupHelperDB(t)

	llmCache := llm.GetCache()
	oldLLMs := llmCache.GetAll()
	llmCache.Clear()
	defer func() {
		llmCache.Clear()
		for k, v := range oldLLMs {
			llmCache.Set(k, v)
		}
	}()

	ctx := context.Background()
	// 手动创建：价格 123，不在任何同步源中。
	manual := model.LLMInfo{Name: "my-manual-model-xyz", LLMPrice: model.LLMPrice{Input: 123, Output: 456, CacheRead: 0.1, CacheWrite: 0.2}}
	if err := llm.Create(manual, ctx); err != nil {
		t.Fatalf("llm.Create(manual) error = %v", err)
	}
	// 同步模型：不在同步源，旧值非 0，刷新后应被写 0。
	if err := llm.BatchCreate([]model.LLMInfo{{Name: "sync-model-xyz", LLMPrice: model.LLMPrice{Input: 7, Output: 7, CacheRead: 7, CacheWrite: 7}}}, ctx); err != nil {
		t.Fatalf("llm.BatchCreate(sync) error = %v", err)
	}

	if err := LLMPriceRefreshExistingModels(ctx); err != nil {
		t.Fatalf("LLMPriceRefreshExistingModels() error = %v", err)
	}

	// 手动模型价格保留。
	got, err := llm.Get("my-manual-model-xyz")
	if err != nil {
		t.Fatalf("llm.Get(manual) error = %v", err)
	}
	if got != manual.LLMPrice {
		t.Fatalf("manual price = %+v, want %+v (preserved)", got, manual.LLMPrice)
	}
	// 同步模型被写 0。
	got, err = llm.Get("sync-model-xyz")
	if err != nil {
		t.Fatalf("llm.Get(sync) error = %v", err)
	}
	if got != (model.LLMPrice{}) {
		t.Fatalf("sync model price = %+v, want zero (overwritten)", got)
	}
}

// TestLLMPriceDeleteFromDBWithNoPrice_SkipsManualModels 验证：手动创建的模型
// （price_manual=true，即使价格为 0）不会被"删 0 价格模型"任务删除。
func TestLLMPriceDeleteFromDBWithNoPrice_SkipsManualModels(t *testing.T) {
	setupHelperDB(t)

	llmCache := llm.GetCache()
	oldLLMs := llmCache.GetAll()
	llmCache.Clear()
	defer func() {
		llmCache.Clear()
		for k, v := range oldLLMs {
			llmCache.Set(k, v)
		}
	}()

	ctx := context.Background()
	// 手动创建 0 价模型（用户明确创建，即使没填价格也不应被自动删除）。
	if err := llm.Create(model.LLMInfo{Name: "manual-zero-model-xyz"}, ctx); err != nil {
		t.Fatalf("llm.Create(manual-zero) error = %v", err)
	}
	// 同步 0 价模型：应被删除。
	if err := llm.BatchCreate([]model.LLMInfo{{Name: "sync-zero-model-xyz"}}, ctx); err != nil {
		t.Fatalf("llm.BatchCreate(sync-zero) error = %v", err)
	}

	if err := LLMPriceDeleteFromDBWithNoPrice([]string{"manual-zero-model-xyz", "sync-zero-model-xyz"}, ctx); err != nil {
		t.Fatalf("LLMPriceDeleteFromDBWithNoPrice() error = %v", err)
	}

	// 手动模型仍存在。
	if _, err := llm.Get("manual-zero-model-xyz"); err != nil {
		t.Fatalf("manual-zero model deleted, want preserved: %v", err)
	}
	// 同步模型已删除。
	if _, err := llm.Get("sync-zero-model-xyz"); err == nil {
		t.Fatal("sync-zero model still exists, want deleted")
	}
}

// TestLLMPriceRefreshExistingModels_NoPresetWritesZero 验证"同步价格"刷新
// 已有模型时的价格解析顺序：
//  1. 内置预设命中 -> 用预设价格
//  2. 均未命中 -> 写 0
//
// 峰谷计费规则（model_price_schedules）由 EffectiveLLMPrice 在计费时应用，
// 不再参与同步刷新的 DB 写价。
// 断言用 deepseek-chat（presets.go 内置、presets_manual.go 移除后仍在的条目）
// 而非 deepseek-v4-flash：presets.go 由 release 脚本从 models.dev 重新生成，
// 其内容随上游变动，不能作为断言依赖；deepseek-v4-flash 等是否被预设收录
// 与本测试的解析顺序无关。
func TestLLMPriceRefreshExistingModels_NoPresetWritesZero(t *testing.T) {
	setupHelperDB(t)

	llmCache := llm.GetCache()
	oldLLMs := llmCache.GetAll()
	llmCache.Clear()
	defer func() {
		llmCache.Clear()
		for k, v := range oldLLMs {
			llmCache.Set(k, v)
		}
	}()

	// deepseek-chat：presets.go 内置条目，旧值故意设为 0 以便断言刷新后写入预设价。
	llmCache.Set("deepseek-chat", model.LLMPrice{})
	// 完全未知的模型：外部与托底均未命中，刷新后应写 0。
	llmCache.Set("totally-unknown-model-xyz", model.LLMPrice{Input: 9, Output: 9, CacheRead: 9, CacheWrite: 9})

	if err := LLMPriceRefreshExistingModels(context.Background()); err != nil {
		t.Fatalf("LLMPriceRefreshExistingModels() error = %v", err)
	}

	// 预设命中：deepseek-chat 刷新后应写入非 0 的预设价。峰谷计费由规则表
	// （model_price_schedules）在 EffectiveLLMPrice 运行时应用，与 DB 价格无关。
	got, err := llm.Get("deepseek-chat")
	if err != nil {
		t.Fatalf("llm.Get(deepseek-chat) error = %v", err)
	}
	if got.Input == 0 {
		t.Fatalf("deepseek-chat price = %+v, want non-zero preset price (presets.go)", got)
	}

	// 均未命中：应写 0。
	got, err = llm.Get("totally-unknown-model-xyz")
	if err != nil {
		t.Fatalf("llm.Get(totally-unknown-model-xyz) error = %v", err)
	}
	if got != (model.LLMPrice{}) {
		t.Fatalf("totally-unknown-model-xyz price = %+v, want zero", got)
	}
}
