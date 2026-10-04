package op

import (
	"context"

	"github.com/lingyuins/octopus/internal/helper"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/op/stats"
)

var channelCache = channel.GetCache()
var channelKeyCache = channel.GetKeyCache()
var channelKeyCacheNeedUpdate, channelKeyCacheNeedUpdateLock = channel.GetKeyCacheNeedUpdate()

// OnChannelDeletedHooks holds optional callbacks invoked after a channel is deleted.
// External packages (e.g. relay/balancer) register cleanup hooks here at init time.
var OnChannelDeletedHooks []func(channelID int)

func init() {
	channel.GroupDefaultID = func(ctx context.Context) (int, error) {
		return ChannelGroupDefaultID(ctx)
	}
	channel.GroupGet = func(id int, ctx context.Context) (*model.ChannelGroup, error) {
		return ChannelGroupGet(id, ctx)
	}
	// 注入代理池 URL 解析器，避免 helper 反向 import op 造成循环依赖。
	helper.ProxyURLByConfigFunc = ProxyURLForConfig
	// 渠道删除统一清理：group items 缓存 + 统计 + balancer/key 运行态钩子。
	// 挂在 channel.OnDeleted 上，任何走 channel.Delete 的删除路径都自动生效
	// （含一次性渠道过期任务），不再依赖各调用方手动补清理。
	channel.OnDeleted = func(id int) {
		group.RemoveChannelItemsFromCache(id)
		stats.OnChannelDeleted(id)
		for _, hook := range OnChannelDeletedHooks {
			hook(id)
		}
	}
}

// Deprecated: Use channel.List from internal/op/channel instead.
func ChannelList(ctx context.Context) ([]model.Channel, error) { return channel.List(ctx) }

// Deprecated: Use channel.Create from internal/op/channel instead.
func ChannelCreate(ch *model.Channel, ctx context.Context) error { return channel.Create(ch, ctx) }

// Deprecated: Use channel.KeyUpdate from internal/op/channel instead.
func ChannelKeyUpdate(key model.ChannelKey) error { return channel.KeyUpdate(key) }

// Deprecated: Use channel.BaseUrlUpdate from internal/op/channel instead.
func ChannelBaseUrlUpdate(channelID int, baseUrl []model.BaseUrl) error {
	return channel.BaseUrlUpdate(channelID, baseUrl)
}

// Deprecated: Use channel.KeySaveDB from internal/op/channel instead.
func ChannelKeySaveDB(ctx context.Context) error { return channel.KeySaveDB(ctx) }

// Deprecated: Use channel.Update from internal/op/channel instead.
func ChannelUpdate(req *model.ChannelUpdateRequest, ctx context.Context) (*model.Channel, error) {
	return channel.Update(req, ctx)
}

// Deprecated: Use channel.Enabled from internal/op/channel instead.
func ChannelEnabled(id int, enabled bool, ctx context.Context) error {
	return channel.Enabled(id, enabled, ctx)
}

// ChannelDel handles deletion with cross-package stats/group cache cleanup.
func ChannelDel(id int, ctx context.Context) error {
	return channel.Delete(id, ctx)
}

// Deprecated: Use channel.LLMList from internal/op/channel instead.
func ChannelLLMList(ctx context.Context) ([]model.LLMChannel, error) { return channel.LLMList(ctx) }

// Deprecated: Use channel.Get from internal/op/channel instead.
func ChannelGet(id int, ctx context.Context) (*model.Channel, error) { return channel.Get(id, ctx) }

// channelRefreshCache is called by cache.go (same package)
func channelRefreshCache(ctx context.Context) error { return channel.RefreshCache(ctx) }

// channelRefreshCacheByID is called by group.go and ChannelDel (same package)
func channelRefreshCacheByID(id int, ctx context.Context) error {
	return channel.RefreshCacheByID(id, ctx)
}

// ChannelGroup functions are still in channel_group.go (not yet extracted)
