package airoute

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lingyuins/octopus/internal/model"
)

func TestFollowUpUncoveredInputs_MergesSupplement(t *testing.T) {
	// 首轮漏了 (2, claude-3-5)：追问服务返回补充路由，最终 routes 覆盖全部输入。
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"routes\":[{\"requested_model\":\"claude-3-5\",\"items\":[{\"channel_id\":2,\"upstream_model\":\"claude-3-5\",\"priority\":1,\"weight\":100}]}]}"}}]}`))
	}))
	defer server.Close()

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
				{ChannelID: 1, UpstreamModel: "gpt-4o", Priority: 1, Weight: 100},
			},
		},
	}

	service := aiRouteService{Index: 0, Name: "svc", BaseURL: server.URL, APIKey: "k", Model: "m"}
	got := followUpUncoveredInputs(context.Background(), service, bucket, "", 1, firstRoutes)

	if calls != 1 {
		t.Fatalf("follow-up HTTP calls = %d, want 1", calls)
	}
	if len(got) != 2 {
		t.Fatalf("merged routes len = %d, want 2 (gpt-4o + claude-3-5)", len(got))
	}
	byModel := map[string][]model.AIRouteItemSpec{}
	for _, route := range got {
		byModel[route.RequestedModel] = route.Items
	}
	if len(byModel["claude-3-5"]) != 1 || byModel["claude-3-5"][0].ChannelID != 2 {
		t.Fatalf("claude-3-5 supplement missing or wrong: %+v", byModel["claude-3-5"])
	}
	if len(byModel["gpt-4o"]) != 1 {
		t.Fatalf("first-pass gpt-4o route lost: %+v", byModel["gpt-4o"])
	}

	// 合并后应全覆盖
	if uncovered := computeUncoveredInputs(bucket, got); uncovered != nil {
		t.Fatalf("after follow-up still uncovered: %+v", uncovered)
	}
}

func TestFollowUpUncoveredInputs_EmptySupplementKeepsFirstPass(t *testing.T) {
	// 追问返回空 routes：保留首轮结果，不追加空路由。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"routes\":[]}"}}]}`))
	}))
	defer server.Close()

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
				{ChannelID: 1, UpstreamModel: "gpt-4o", Priority: 1, Weight: 100},
			},
		},
	}

	service := aiRouteService{Index: 0, Name: "svc", BaseURL: server.URL, APIKey: "k", Model: "m"}
	got := followUpUncoveredInputs(context.Background(), service, bucket, "", 1, firstRoutes)

	if len(got) != 1 || got[0].RequestedModel != "gpt-4o" {
		t.Fatalf("empty supplement should keep first-pass routes, got %+v", got)
	}
}
