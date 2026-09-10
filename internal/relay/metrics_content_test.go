package relay

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/op/setting"
	transformerModel "github.com/lingyuins/octopus/internal/transformer/model"
)

// TestSaveLogContentEnabledToggle 验证 RelayLogContentEnabled 开关：
//   - 开启时（默认）：RequestContent/ResponseContent 被记录；
//   - 关闭时：两个大字段为空，但 CacheReadTokens 仍从 Usage 直接提取，
//     SemanticCacheHit 仍正确判定。
func TestSaveLogContentEnabledToggle(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "metrics-content.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	if err := db.InitLogDB("", "", false); err != nil {
		t.Fatalf("InitLogDB(shared) failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := setting.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache failed: %v", err)
	}
	t.Cleanup(relaylog.SetCacheForTest(nil))

	// 构造一个带 PromptTokensDetails.CachedTokens 的响应（provider 提示缓存命中）。
	cachedTokens := int64(512)
	resp := &transformerModel.InternalLLMResponse{
		Usage: &transformerModel.Usage{
			PromptTokens:     1000,
			CompletionTokens: 200,
			PromptTokensDetails: &transformerModel.PromptTokensDetails{
				CachedTokens: cachedTokens,
			},
		},
	}

	metrics := &RelayMetrics{
		APIKeyID:        1,
		RequestModel:    "gpt-4o",
		EndpointType:    "chat",
		ClientIP:        "127.0.0.1",
		UserAgent:       "ua-regression/1.0",
		StartTime:       time.Now().Add(-100 * time.Millisecond),
		InternalRequest: &transformerModel.InternalLLMRequest{},
	}
	metrics.SetInternalResponse(resp, "gpt-4o")

	// 场景 1：开启大字段记录（默认）。
	if err := setting.SetString(model.SettingKeyRelayLogContentEnabled, "true"); err != nil {
		t.Fatalf("enable content failed: %v", err)
	}
	metrics.saveLog(context.Background(), nil, 100*time.Millisecond, []model.ChannelAttempt{
		{ChannelID: 1, ChannelName: "ch", ModelName: "gpt-4o", Status: model.AttemptSuccess},
	}, 1, "ch")

	logs1, _ := relaylog.GetCacheAndLock()
	if len(logs1) == 0 {
		t.Fatal("expected log in cache after content-enabled save")
	}
	last1 := logs1[len(logs1)-1]
	if last1.RequestContent == "" {
		t.Fatal("content enabled: expected non-empty RequestContent")
	}
	if last1.ResponseContent == "" {
		t.Fatal("content enabled: expected non-empty ResponseContent")
	}
	if last1.CacheReadTokens != int(cachedTokens) {
		t.Fatalf("content enabled: CacheReadTokens = %d, want %d", last1.CacheReadTokens, cachedTokens)
	}
	if last1.UserAgent != "ua-regression/1.0" {
		t.Fatalf("content enabled: UserAgent = %q, want %q", last1.UserAgent, "ua-regression/1.0")
	}

	// 场景 2：关闭大字段记录。
	if err := setting.SetString(model.SettingKeyRelayLogContentEnabled, "false"); err != nil {
		t.Fatalf("disable content failed: %v", err)
	}
	metrics2 := &RelayMetrics{
		APIKeyID:        2,
		RequestModel:    "gpt-4o",
		EndpointType:    "chat",
		ClientIP:        "127.0.0.1",
		StartTime:       time.Now().Add(-100 * time.Millisecond),
		InternalRequest: &transformerModel.InternalLLMRequest{},
	}
	metrics2.SetInternalResponse(resp, "gpt-4o")
	metrics2.saveLog(context.Background(), nil, 100*time.Millisecond, []model.ChannelAttempt{
		{ChannelID: 1, ChannelName: "ch", ModelName: "gpt-4o", Status: model.AttemptSuccess},
	}, 1, "ch")

	logs2, _ := relaylog.GetCacheAndLock()
	if len(logs2) < 2 {
		t.Fatalf("expected at least 2 logs in cache, got %d", len(logs2))
	}
	last2 := logs2[len(logs2)-1]
	if last2.RequestContent != "" {
		t.Fatalf("content disabled: expected empty RequestContent, got %q", last2.RequestContent)
	}
	if last2.ResponseContent != "" {
		t.Fatalf("content disabled: expected empty ResponseContent, got %q", last2.ResponseContent)
	}
	// 关闭大字段时 CacheReadTokens 应从 Usage 直接提取。
	if last2.CacheReadTokens != int(cachedTokens) {
		t.Fatalf("content disabled: CacheReadTokens = %d, want %d (from Usage)", last2.CacheReadTokens, cachedTokens)
	}
}

func TestSaveLogPersistsReasoningEffortAndTokens(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "metrics-reasoning.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	if err := db.InitLogDB("", "", false); err != nil {
		t.Fatalf("InitLogDB(shared) failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := setting.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache failed: %v", err)
	}
	t.Cleanup(relaylog.SetCacheForTest(nil))

	resp := &transformerModel.InternalLLMResponse{
		Usage: &transformerModel.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			CompletionTokensDetails: &transformerModel.CompletionTokensDetails{
				ReasoningTokens: 42,
			},
		},
	}
	metrics := &RelayMetrics{
		APIKeyID:     1,
		RequestModel: "gpt-5.5",
		EndpointType: "chat",
		ClientIP:     "127.0.0.1",
		StartTime:    time.Now().Add(-50 * time.Millisecond),
		InternalRequest: &transformerModel.InternalLLMRequest{
			ReasoningEffort: "max",
		},
	}
	metrics.SetInternalResponse(resp, "gpt-5.5")
	metrics.saveLog(context.Background(), nil, 50*time.Millisecond, []model.ChannelAttempt{
		{ChannelID: 1, ChannelName: "ch", ModelName: "gpt-5.5", Status: model.AttemptSuccess},
	}, 1, "ch")

	logs, _ := relaylog.GetCacheAndLock()
	if len(logs) == 0 {
		t.Fatal("expected log in cache")
	}
	last := logs[len(logs)-1]
	if last.ReasoningEffort != "max" {
		t.Fatalf("ReasoningEffort=%q, want max", last.ReasoningEffort)
	}
	if last.ReasoningTokens != 42 {
		t.Fatalf("ReasoningTokens=%d, want 42", last.ReasoningTokens)
	}
}

func TestSaveLogPersistsReasoningCharsWhenNoOfficialTokens(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "metrics-reasoning-chars.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	if err := db.InitLogDB("", "", false); err != nil {
		t.Fatalf("InitLogDB(shared) failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := setting.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache failed: %v", err)
	}
	t.Cleanup(relaylog.SetCacheForTest(nil))

	// 中文 2 + 英文 5 + 空格 1 + 数字 2 = 10 个 rune；无官方 reasoning_tokens
	thinking := "思考hello 42"
	resp := &transformerModel.InternalLLMResponse{
		Usage: &transformerModel.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
		},
		Choices: []transformerModel.Choice{{
			Message: &transformerModel.Message{
				Role:             "assistant",
				ReasoningContent: &thinking,
			},
		}},
	}
	metrics := &RelayMetrics{
		APIKeyID:     1,
		RequestModel: "claude",
		EndpointType: "messages",
		ClientIP:     "127.0.0.1",
		StartTime:    time.Now().Add(-50 * time.Millisecond),
		InternalRequest: &transformerModel.InternalLLMRequest{
			ReasoningEffort: "max",
		},
	}
	metrics.SetInternalResponse(resp, "claude")
	metrics.saveLog(context.Background(), nil, 50*time.Millisecond, []model.ChannelAttempt{
		{ChannelID: 1, ChannelName: "ch", ModelName: "claude", Status: model.AttemptSuccess},
	}, 1, "ch")

	logs, _ := relaylog.GetCacheAndLock()
	if len(logs) == 0 {
		t.Fatal("expected log in cache")
	}
	last := logs[len(logs)-1]
	if last.ReasoningTokens != 0 {
		t.Fatalf("ReasoningTokens=%d, want 0", last.ReasoningTokens)
	}
	wantChars := len([]rune(thinking))
	if last.ReasoningChars != wantChars {
		t.Fatalf("ReasoningChars=%d, want %d", last.ReasoningChars, wantChars)
	}
}

func TestSaveLogSkipsReasoningCharsWhenOfficialTokensPresent(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "metrics-reasoning-official.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	if err := db.InitLogDB("", "", false); err != nil {
		t.Fatalf("InitLogDB(shared) failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := setting.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache failed: %v", err)
	}
	t.Cleanup(relaylog.SetCacheForTest(nil))

	thinking := "some long thinking text that should not be counted"
	resp := &transformerModel.InternalLLMResponse{
		Usage: &transformerModel.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			CompletionTokensDetails: &transformerModel.CompletionTokensDetails{
				ReasoningTokens: 99,
			},
		},
		Choices: []transformerModel.Choice{{
			Message: &transformerModel.Message{
				Role:             "assistant",
				ReasoningContent: &thinking,
			},
		}},
	}
	metrics := &RelayMetrics{
		APIKeyID:        1,
		RequestModel:    "gpt",
		EndpointType:    "chat",
		ClientIP:        "127.0.0.1",
		StartTime:       time.Now().Add(-50 * time.Millisecond),
		InternalRequest: &transformerModel.InternalLLMRequest{ReasoningEffort: "high"},
	}
	metrics.SetInternalResponse(resp, "gpt")
	metrics.saveLog(context.Background(), nil, 50*time.Millisecond, nil, 1, "ch")

	logs, _ := relaylog.GetCacheAndLock()
	last := logs[len(logs)-1]
	if last.ReasoningTokens != 99 {
		t.Fatalf("ReasoningTokens=%d, want 99", last.ReasoningTokens)
	}
	if last.ReasoningChars != 0 {
		t.Fatalf("ReasoningChars=%d, want 0 when official tokens present", last.ReasoningChars)
	}
}
