package relay

import (
	"context"

	dbmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op"
	"github.com/lingyuins/octopus/internal/op/apikey"
	ch "github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/ratelimitstore"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/relay/balancer"
)

// OnChannelDeletedKeyHealthHook 由 task 包在启动时注入，用于清理 Key 巡检状态。
// relay 不能导入 task（task 已导入 relay，会循环依赖），故用函数变量解耦。
// nil 表示 Key 巡检未启用，渠道删除时不做清理。
var OnChannelDeletedKeyHealthHook func(channelID int)

func init() {
	// 注入按模型粒度的 Key 冷却查询函数，打破 model → balancer 的循环依赖。
	// model 包不能直接 import balancer，故由 relay 层在初始化时注入实现。
	dbmodel.KeyCooldownFunc = balancer.IsKeyOnCooldown
	dbmodel.KeyAvailabilityScoreFunc = balancer.GetKeyAvailabilityScore
	dbmodel.KeySpeedTPSFunc = balancer.GetKeyTPS

	// 注入一次性渠道查询函数：从 channel cache 查询 Disposable 字段。
	// 一次性渠道在路由组内绝对优先（趁未过期先用掉），由 balancer.NewIterator 调用。
	balancer.DisposableChannelFunc = func(channelID int) bool {
		channel, err := ch.Get(channelID, context.Background())
		if err != nil {
			return false
		}
		return channel.Disposable
	}

	dbmodel.GlobalKeySelectionStrategyFunc = func() string {
		v, err := setting.GetString(dbmodel.SettingKeyKeySelectionStrategy)
		if err != nil || v == "" {
			return "cost"
		}
		return v
	}

	// 注册渠道删除时的清理钩子：清除熔断器、Auto 策略统计、Key 冷却、可用度分数、
	// 速度 TPS 统计、渠道级限流状态和 Key 巡检状态中的残留条目，防止全局 map 无限增长。
	// Key 巡检状态清理通过 OnChannelDeletedKeyHealthHook 函数变量注入，避免
	// relay -> task 循环依赖（task 已导入 relay）。
	op.OnChannelDeletedHooks = append(op.OnChannelDeletedHooks, func(channelID int) {
		balancer.RemoveChannelEntries(channelID)
		balancer.RemoveChannelStats(channelID)
		balancer.RemoveChannelKeyCooldowns(channelID)
		balancer.RemoveChannelRateLimitEntries(channelID)
		balancer.RemoveChannelKeyAvailability(channelID)
		balancer.RemoveChannelKeySpeed(channelID)
		if OnChannelDeletedKeyHealthHook != nil {
			OnChannelDeletedKeyHealthHook(channelID)
		}
	})

	// 注册 API Key 删除时的清理钩子：清除粘性会话和限流 bucket 条目，
	// 防止 globalSession / requestBuckets / tokenBuckets 无限增长。
	apikey.DeleteSessionFunc = func(id int) {
		balancer.RemoveAPIKeySticky(id)
		ratelimitstore.RemoveAPIKeyBuckets(id)
	}

}
