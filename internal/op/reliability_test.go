package op

import (
	"context"
	"errors"
	"testing"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/group"
)

// TestChannelDeleteCleansGroupCacheAndHooks 锁住删渠道的统一清理契约：
// 任何走 channel.Delete 的路径都必须清掉分组缓存中的残留条目并触发删除钩子，
// 避免已删除的渠道仍被路由到（上游 c4bb0ad8 同款修复）。
func TestChannelDeleteCleansGroupCacheAndHooks(t *testing.T) {
	ctx := initChannelGroupTestDB(t)

	ch := &model.Channel{
		Name:      "delete-target",
		Type:      0,
		Enabled:   true,
		BaseUrls:  []model.BaseUrl{{URL: "https://example.com", Delay: 0}},
		Model:     "gpt-4o",
		AutoGroup: model.AutoGroupTypeNone,
		Keys:      []model.ChannelKey{{Enabled: true, ChannelKey: "sk-delete-test"}},
	}
	if err := ChannelCreate(ch, ctx); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	g := &model.Group{
		Name:         "delete-group",
		EndpointType: model.EndpointTypeChat,
		Mode:         model.GroupModeRoundRobin,
		Items:        []model.GroupItem{{ChannelID: ch.ID, ModelName: "test", Priority: 1, Weight: 1}},
	}
	if err := GroupCreate(g, ctx); err != nil {
		t.Fatalf("create group: %v", err)
	}

	oldHooks := OnChannelDeletedHooks
	calls := 0
	OnChannelDeletedHooks = append(append([]func(int){}, oldHooks...), func(id int) {
		if id == ch.ID {
			calls++
		}
	})
	t.Cleanup(func() {
		OnChannelDeletedHooks = oldHooks
		group.GetCache().Clear()
		group.GetNameMap().Clear()
		group.RebuildIndexes()
	})

	if err := channel.Delete(ch.ID, ctx); err != nil {
		t.Fatalf("delete channel: %v", err)
	}

	cached, err := GroupGet(g.ID, ctx)
	if err != nil || len(cached.Items) != 0 {
		t.Fatalf("cached group after deletion = %+v, err = %v", cached, err)
	}
	routed, err := GroupGetEnabledMapByEndpoint(model.EndpointTypeChat, g.Name, ctx)
	if err == nil && len(routed.Items) != 0 {
		t.Fatalf("routing index still has deleted channel: %+v", routed.Items)
	}
	if calls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", calls)
	}
	if _, err := channel.Get(ch.ID, ctx); err == nil {
		t.Fatal("deleted channel remains cached")
	}
}

// TestSaveCacheContinuesAfterErrors 锁住 SaveCache 的聚合语义：
// 单个缓存保存失败不得跳过其余缓存，错误需通过 errors.Is 可查（上游 c4bb0ad8 同款修复）。
func TestSaveCacheContinuesAfterErrors(t *testing.T) {
	old := cacheSaveFuncs
	t.Cleanup(func() { cacheSaveFuncs = old })
	first := errors.New("first save failed")
	last := errors.New("last save failed")
	calls := 0
	cacheSaveFuncs = []CacheSaveFunc{
		func(context.Context) error { calls++; return first },
		func(context.Context) error { calls++; return nil },
		func(context.Context) error { calls++; return last },
	}
	err := SaveCache()
	if calls != 3 || !errors.Is(err, first) || !errors.Is(err, last) {
		t.Fatalf("calls = %d, err = %v; want all saves and both errors", calls, err)
	}
}
