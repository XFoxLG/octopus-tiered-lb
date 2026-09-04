package relay

import (
	"strings"

	"github.com/lingyuins/octopus/internal/helper"
	dbmodel "github.com/lingyuins/octopus/internal/model"
	ch "github.com/lingyuins/octopus/internal/op/channel"
)

// splitDirectChannelModel 按首个 `/` 切分指定路由模型名。
// 名称比较严格区分大小写且不 trim，与 XyzenSun direct-channel ADR 一致。
func splitDirectChannelModel(requestModel string) (channelName, modelName string, ok bool) {
	channelName, modelName, ok = strings.Cut(requestModel, "/")
	if !ok || channelName == "" || modelName == "" {
		return "", "", false
	}
	return channelName, modelName, true
}

// resolveDirectChannelGroup 解析指定渠道路由，构造虚拟单候选分组。
// 校验：渠道存在且启用、模型在渠道已选集合内。失败返回 false（调用方回 404）。
// 虚拟分组走主流程（executeRelay），重试预算由调用方钳为单次。
func resolveDirectChannelGroup(requestModel, endpointType string) (dbmodel.Group, bool) {
	channelName, modelName, ok := splitDirectChannelModel(requestModel)
	if !ok {
		return dbmodel.Group{}, false
	}
	channel, err := ch.GetByName(channelName)
	if err != nil || !channel.Enabled {
		return dbmodel.Group{}, false
	}
	if !helper.ChannelModelInSelectedModels(channel, modelName) {
		return dbmodel.Group{}, false
	}
	return dbmodel.Group{
		Name:         requestModel,
		EndpointType: endpointType,
		Mode:         dbmodel.GroupModeFailover,
		Items: []dbmodel.GroupItem{
			{ChannelID: channel.ID, ModelName: modelName, Priority: 1, Weight: 1},
		},
	}, true
}
