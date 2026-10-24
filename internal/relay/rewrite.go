package relay

import (
	"fmt"
	"strings"

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

	// REQ-02 attempt 隔离：请求源是所有 attempt 共享的只读副本。param_override、
	// 分组思考档位、transformer 元数据、出站适配器的就地清洗都发生在 target 上；
	// 若 target 直接指向请求源，第一次 attempt 的注入会被后续 attempt 误认为
	// 「客户端已设置」，覆盖策略随之失效。这里先做一次完整深拷贝（rewrite 启用时
	// 亦然，保证 rewrite.Apply 的产物也不与请求源共享嵌套引用），注入只落在
	// attempt 私有副本上。
	target := transmodel.CloneForAttempt(request)
	if enabled {
		rewritten, applyErr := rewrite.Apply(target, effectiveRewrite)
		if applyErr != nil {
			return nil, nil, applyErr
		}
		target = rewritten
	}

	applyParamOverride(channel, group, target)
	applyReasoningPolicy(group, target)
	attachRelayGroupEndpointMetadata(target, groupEndpointType, group)
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

func attachRelayGroupEndpointMetadata(request *transmodel.InternalLLMRequest, groupEndpointType string, group *appmodel.Group) {
	if request == nil {
		return
	}

	// The UI normalizes conversation groups to endpoint_type=chat while keeping
	// the provider selection in endpoint_provider. Both old endpoint_type values
	// and new provider values must reach outbound adapters, because DeepSeek/MiMo
	// decide reasoning controls from this metadata.
	provider := ""
	if group != nil {
		provider = strings.ToLower(strings.TrimSpace(group.EndpointProvider))
	}
	if provider == "" || provider == "auto" {
		provider = appmodel.NormalizeEndpointType(groupEndpointType)
	}
	if provider == "" {
		return
	}

	if request.TransformerMetadata == nil {
		request.TransformerMetadata = make(map[string]string)
	}
	request.TransformerMetadata[transmodel.TransformerMetadataGroupEndpointType] = provider
}

// applyReasoningPolicy 分组默认思考档位注入（Sub2API 参考，本仓库裁剪版）。
// 仅当分组配置了 DefaultReasoningEffort 且客户端「未表达任何思考意图」时，
// 把分组档位填入 request.ReasoningEffort；出向各协议自行翻译（OpenAI
// reasoning_effort / Anthropic adaptive+output_config / Gemini thinking 钳制）。
//
// 优先级：模型名后缀 > 客户端显式意图（含 enable_thinking 任一取值）>
// 分组默认。分类由 transmodel.ReasoningIntentOf 完成，避免依赖某条入站
// 路径是否恰好置过 ReasoningExplicit：
//   - 具体档位/预算/adaptive        → 尊重，绝不覆盖（force 也不生效）
//   - enable_thinking=true（显式开）→ 阻断分组默认注入（force 只覆盖显式关）
//   - 显式关（enable_thinking=false / reasoning_effort=none /
//     thinking.disabled / 空 Responses reasoning {}）→ 默认尊重；
//     分组开启 ReasoningForceOverride 时注入，并清掉 disabled 标志
//   - 未表达                        → 注入分组默认档位
//
// passthrough/raw 出站不经过本函数（relay.go 在 prepare 之前已分流），注入
// 天然不触碰原始穿透。
func applyReasoningPolicy(group *appmodel.Group, request *transmodel.InternalLLMRequest) {
	if request == nil || group == nil || request.ReasoningFromModelSuffix {
		return
	}
	defaultEffort := strings.ToLower(strings.TrimSpace(group.DefaultReasoningEffort))
	if defaultEffort == "" {
		return
	}

	switch transmodel.ReasoningIntentOf(request) {
	case transmodel.ReasoningIntentLevel:
		// 客户端已发具体档位/预算/adaptive：尊重，不覆盖。
		return
	case transmodel.ReasoningIntentOn:
		// REQ-01: enable_thinking=true 是显式「开」。客户端已表态，分组默认
		// 档位不再叠加；force 只覆盖显式关，不覆盖显式开。
		return
	case transmodel.ReasoningIntentOff:
		if !group.ReasoningForceOverride {
			return
		}
		// REQ-01: 显式关与注入的档位是矛盾控制。force 注入时清掉 disabled
		// 标志，让出站协议按已注入的档位翻译，不发送 disabled。
		request.EnableThinking = nil
	}

	request.ReasoningEffort = defaultEffort
}
