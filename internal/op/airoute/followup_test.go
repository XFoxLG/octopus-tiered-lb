package airoute

import (
	"context"
	"testing"

	"github.com/lingyuins/octopus/internal/model"
)

func TestComputeUncoveredInputs_AllCovered(t *testing.T) {
	bucket := aiRoutePromptBucket{
		ModelInputs: []aiRoutePromptModelInput{
			{ChannelID: 1, Model: "gpt-4o"},
			{ChannelID: 2, Model: "gpt-4o-2024-08-06"},
		},
	}
	routes := []model.AIRouteEntry{
		{
			RequestedModel: "gpt-4o",
			Items: []model.AIRouteItemSpec{
				{ChannelID: 1, UpstreamModel: "gpt-4o"},
				{ChannelID: 2, UpstreamModel: "gpt-4o-2024-08-06"},
			},
		},
	}

	if got := computeUncoveredInputs(bucket, routes); got != nil {
		t.Fatalf("computeUncoveredInputs(all covered) = %+v, want nil", got)
	}
}

func TestComputeUncoveredInputs_PartialCover(t *testing.T) {
	bucket := aiRoutePromptBucket{
		ModelInputs: []aiRoutePromptModelInput{
			{ChannelID: 1, Model: "gpt-4o"},
			{ChannelID: 2, Model: "claude-3-5"},
			{ChannelID: 3, Model: "gemini-1.5-pro"},
		},
	}
	routes := []model.AIRouteEntry{
		{
			RequestedModel: "gpt-4o",
			Items: []model.AIRouteItemSpec{
				{ChannelID: 1, UpstreamModel: "gpt-4o"},
			},
		},
	}

	got := computeUncoveredInputs(bucket, routes)
	if len(got) != 2 {
		t.Fatalf("computeUncoveredInputs(partial) len = %d, want 2", len(got))
	}
	if got[0].ChannelID != 2 || got[0].Model != "claude-3-5" {
		t.Fatalf("got[0] = %+v, want {2 claude-3-5}", got[0])
	}
	if got[1].ChannelID != 3 || got[1].Model != "gemini-1.5-pro" {
		t.Fatalf("got[1] = %+v, want {3 gemini-1.5-pro}", got[1])
	}
}

func TestComputeUncoveredInputs_CaseInsensitiveModelMatch(t *testing.T) {
	bucket := aiRoutePromptBucket{
		ModelInputs: []aiRoutePromptModelInput{
			{ChannelID: 1, Model: "GPT-4O"},
		},
	}
	routes := []model.AIRouteEntry{
		{
			RequestedModel: "gpt-4o",
			Items: []model.AIRouteItemSpec{
				{ChannelID: 1, UpstreamModel: "gpt-4o"},
			},
		},
	}

	if got := computeUncoveredInputs(bucket, routes); got != nil {
		t.Fatalf("computeUncoveredInputs(case-insensitive) = %+v, want nil", got)
	}
}

func TestComputeUncoveredInputs_EmptyRoutes(t *testing.T) {
	bucket := aiRoutePromptBucket{
		ModelInputs: []aiRoutePromptModelInput{
			{ChannelID: 1, Model: "gpt-4o"},
		},
	}

	got := computeUncoveredInputs(bucket, nil)
	if len(got) != 1 {
		t.Fatalf("computeUncoveredInputs(nil routes) len = %d, want 1", len(got))
	}
}

func TestComputeUncoveredInputs_UnknownChannelInRoutes(t *testing.T) {
	// routes 里出现输入列表不存在的 channel_id：覆盖判断按 (channel_id, model) 精确匹配，
	// 不影响输入集合的漏归类判定。
	bucket := aiRoutePromptBucket{
		ModelInputs: []aiRoutePromptModelInput{
			{ChannelID: 1, Model: "gpt-4o"},
		},
	}
	routes := []model.AIRouteEntry{
		{
			RequestedModel: "gpt-4o",
			Items: []model.AIRouteItemSpec{
				{ChannelID: 99, UpstreamModel: "gpt-4o"},
			},
		},
	}

	got := computeUncoveredInputs(bucket, routes)
	if len(got) != 1 {
		t.Fatalf("computeUncoveredInputs(unknown channel) len = %d, want 1 (channel 1 still uncovered)", len(got))
	}
}

func TestFollowUpUncoveredInputs_AllCoveredSkipsCall(t *testing.T) {
	bucket := aiRoutePromptBucket{
		ModelInputs: []aiRoutePromptModelInput{
			{ChannelID: 1, Model: "gpt-4o"},
		},
	}
	routes := []model.AIRouteEntry{
		{
			RequestedModel: "gpt-4o",
			Items: []model.AIRouteItemSpec{
				{ChannelID: 1, UpstreamModel: "gpt-4o"},
			},
		},
	}

	// 全覆盖：不应发生任何 HTTP 调用（无可用服务时也应原样返回）
	got := followUpUncoveredInputs(context.Background(), aiRouteService{Index: 0, Name: "svc", BaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "m"}, bucket, "", 1, routes)
	if len(got) != 1 || got[0].RequestedModel != "gpt-4o" {
		t.Fatalf("followUpUncoveredInputs(all covered) = %+v, want original routes", got)
	}
}

func TestFollowUpUncoveredInputs_CallFailureKeepsFirstPass(t *testing.T) {
	bucket := aiRoutePromptBucket{
		ModelInputs: []aiRoutePromptModelInput{
			{ChannelID: 1, Model: "gpt-4o"},
			{ChannelID: 2, Model: "claude-3-5"},
		},
	}
	firstRoutes := []model.AIRouteEntry{
		{
			RequestedModel: "gpt-4o",
			Items: []model.AIRouteItemSpec{
				{ChannelID: 1, UpstreamModel: "gpt-4o"},
			},
		},
	}

	// ctx 已取消：追问快速失败，不致命，返回首轮结果
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := followUpUncoveredInputs(ctx, aiRouteService{Index: 0, Name: "svc", BaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "m"}, bucket, "", 1, firstRoutes)
	if len(got) != 1 || got[0].RequestedModel != "gpt-4o" {
		t.Fatalf("followUpUncoveredInputs(call failed) = %+v, want first-pass routes", got)
	}
}

func TestAIRouteI18nError_InterfaceImplemented(t *testing.T) {
	// 编译期断言：aiRouteCallError 与 aiRouteError 都实现 AIRouteI18nError。
	var _ AIRouteI18nError = &aiRouteCallError{
		Message:     "msg",
		MessageKey:  "group.aiRoute.progress.runtime.noService",
		MessageArgs: map[string]any{"batch_index": 1},
	}
	var _ AIRouteI18nError = errAIRouteNoService(1)
}

func TestAIRouteI18nError_ErrorMessage(t *testing.T) {
	err := errAIRouteNoService(7)
	if got := err.Error(); got != "No AI route analysis service available (batch 7)" {
		t.Fatalf("errAIRouteNoService(7).Error() = %q, want batch-indexed message", got)
	}
	if key := err.I18nMessageKey(); key != I18nKeyAIRouteNoService {
		t.Fatalf("I18nMessageKey() = %q, want %q", key, I18nKeyAIRouteNoService)
	}
	if err.I18nMessageArgs()["batch_index"] != 7 {
		t.Fatalf("I18nMessageArgs()[batch_index] = %v, want 7", err.I18nMessageArgs()["batch_index"])
	}
}

func TestAIRouteI18nError_KeyConstantsExist(t *testing.T) {
	// 错误 key 常量必须与前端 locale 的 group.aiRoute.progress.runtime.* 同名。
	for key, want := range map[string]string{
		I18nKeyAIRouteNoService:       "group.aiRoute.progress.runtime.noService",
		I18nKeyAIRouteEmptyResult:     "group.aiRoute.progress.runtime.emptyResult",
		I18nKeyAIRouteInvalidJSON:     "group.aiRoute.progress.runtime.invalidJSON",
		I18nKeyAIRouteRateLimited:     "group.aiRoute.progress.runtime.rateLimited",
		I18nKeyAIRouteUpstreamTimeout: "group.aiRoute.progress.runtime.upstreamTimeout",
		I18nKeyAIRouteUnavailable:     "group.aiRoute.progress.runtime.unavailable",
		I18nKeyAIRouteUpstreamStatus:  "group.aiRoute.progress.runtime.upstreamStatus",
	} {
		if key != want {
			t.Fatalf("key constant = %q, want %q", key, want)
		}
	}
}
