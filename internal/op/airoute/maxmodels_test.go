package airoute

import (
	"testing"

	"github.com/lingyuins/octopus/internal/model"
)

// TestGetAIRouteMaxModelsPerRequestDefault A4：设置项未配置（包测试无 DB setting）
// 时回退默认 120。
func TestGetAIRouteMaxModelsPerRequestDefault(t *testing.T) {
	got := getAIRouteMaxModelsPerRequest()
	if got != defaultAIRouteMaxModelsPerRequest {
		t.Fatalf("getAIRouteMaxModelsPerRequest() = %d, want default %d (setting not configured)", got, defaultAIRouteMaxModelsPerRequest)
	}
	if defaultAIRouteMaxModelsPerRequest != 120 {
		t.Fatalf("defaultAIRouteMaxModelsPerRequest = %d, want 120 (legacy constant)", defaultAIRouteMaxModelsPerRequest)
	}
}

// TestSplitAIRoutePromptBucketRespectsLimit A4：小桶不切分（<= 上限直接返回）。
func TestSplitAIRoutePromptBucketRespectsLimit(t *testing.T) {
	bucket := aiRoutePromptBucket{
		PromptEndpointType: model.EndpointTypeChat,
		GroupEndpointType:  model.EndpointTypeChat,
		ModelInputs: []aiRoutePromptModelInput{
			{ChannelID: 1, Model: "gpt-4o"},
			{ChannelID: 2, Model: "gpt-4o-2024-08-06"},
		},
	}

	got := splitAIRoutePromptBucket(bucket)
	if len(got) != 1 {
		t.Fatalf("splitAIRoutePromptBucket(small bucket) len = %d, want 1 (no split under limit)", len(got))
	}
}
