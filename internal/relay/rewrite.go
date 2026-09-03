package relay

import (
	"fmt"

	appmodel "github.com/lingyuins/octopus/internal/model"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/rewrite"
	"github.com/lingyuins/octopus/internal/utils/log"
)

func prepareInternalRequestForOutbound(channel *appmodel.Channel, request *transmodel.InternalLLMRequest, groupEndpointType string, group *appmodel.Group) (*transmodel.InternalLLMRequest, *rewrite.EffectiveConfig, error) {
	if channel == nil {
		return nil, nil, fmt.Errorf("channel is nil")
	}
	if request == nil {
		return nil, nil, fmt.Errorf("request is nil")
	}

	effectiveRewrite, enabled, err := rewrite.Resolve(channel.Type, channel.RequestRewrite)
	if err != nil {
		return nil, nil, err
	}

	var target *transmodel.InternalLLMRequest
	if !enabled {
		target = request
	} else {
		rewritten, applyErr := rewrite.Apply(request, effectiveRewrite)
		if applyErr != nil {
			return nil, nil, applyErr
		}
		target = rewritten
	}

	applyParamOverride(channel, group, target)
	attachRelayGroupEndpointMetadata(target, groupEndpointType)
	return target, effectiveRewrite, nil
}

// applyParamOverride merges group-level and channel-level param_override JSON
// into the outbound request (XyzenSun 移植：分组先合并，渠道覆盖同名键）。
// Only overrides fields that are not already set by the client request (client takes precedence).
func applyParamOverride(channel *appmodel.Channel, group *appmodel.Group, request *transmodel.InternalLLMRequest) {
	if request == nil {
		return
	}
	var groupOverrides, channelOverrides map[string]RawMessage
	if group != nil && group.ParamOverride != nil && *group.ParamOverride != "" {
		if err := jsonAPI.Unmarshal([]byte(*group.ParamOverride), &groupOverrides); err != nil {
			log.Warnf("param_override: invalid JSON for group %d: %v", group.ID, err)
		}
	}
	if channel == nil || channel.ParamOverride == nil || *channel.ParamOverride == "" {
		if len(groupOverrides) == 0 {
			return
		}
	} else if err := jsonAPI.Unmarshal([]byte(*channel.ParamOverride), &channelOverrides); err != nil {
		log.Warnf("param_override: invalid JSON for channel %d: %v", channel.ID, err)
		if len(groupOverrides) == 0 {
			return
		}
	}

	mergeParamOverrideField(groupOverrides, channelOverrides, "max_tokens", &request.MaxTokens)
	mergeParamOverrideField(groupOverrides, channelOverrides, "max_completion_tokens", &request.MaxCompletionTokens)
	mergeParamOverrideFloatField(groupOverrides, channelOverrides, "temperature", &request.Temperature)
	mergeParamOverrideFloatField(groupOverrides, channelOverrides, "top_p", &request.TopP)
}

// mergeParamOverrideField 按「客户端 > 渠道 > 分组」优先级填充 int64 指针字段。
// 仅当 request 字段为 nil（客户端未设置）时才用覆盖值；渠道值优先于分组值。
func mergeParamOverrideField(groupOverrides, channelOverrides map[string]RawMessage, key string, target **int64) {
	if target == nil || *target != nil {
		return
	}
	if v, ok := channelOverrides[key]; ok {
		var val int64
		if err := jsonAPI.Unmarshal(v, &val); err == nil {
			*target = &val
			return
		}
	}
	if v, ok := groupOverrides[key]; ok {
		var val int64
		if err := jsonAPI.Unmarshal(v, &val); err == nil {
			*target = &val
		}
	}
}

// mergeParamOverrideFloatField 按「客户端 > 渠道 > 分组」优先级填充 float64 指针字段。
func mergeParamOverrideFloatField(groupOverrides, channelOverrides map[string]RawMessage, key string, target **float64) {
	if target == nil || *target != nil {
		return
	}
	if v, ok := channelOverrides[key]; ok {
		var val float64
		if err := jsonAPI.Unmarshal(v, &val); err == nil {
			*target = &val
			return
		}
	}
	if v, ok := groupOverrides[key]; ok {
		var val float64
		if err := jsonAPI.Unmarshal(v, &val); err == nil {
			*target = &val
		}
	}
}

func attachRelayGroupEndpointMetadata(request *transmodel.InternalLLMRequest, groupEndpointType string) {
	if request == nil {
		return
	}

	normalizedEndpointType := appmodel.NormalizeEndpointType(groupEndpointType)
	if normalizedEndpointType == "" {
		return
	}

	if request.TransformerMetadata == nil {
		request.TransformerMetadata = make(map[string]string)
	}
	request.TransformerMetadata[transmodel.TransformerMetadataGroupEndpointType] = normalizedEndpointType
}
