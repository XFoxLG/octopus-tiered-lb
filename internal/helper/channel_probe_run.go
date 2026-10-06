package helper

import (
	"context"
	"fmt"
	"strings"
	"time"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

// probeChannelKey 是探测选中的渠道 Key（含下标，便于审计）。
type probeChannelKey struct {
	Key   appmodel.ChannelKey
	Index int
	NoKey bool
}

// selectProbeKey 选出一个用于探测的渠道 Key。
//
// keyIndex >= 0 时按下标精确选取（前端让用户指定）；否则挑第一个启用的 Key。
// 有些渠道确实没有 Key（自建网关白名单 IP），此时返回 NoKey=true 继续探测，
// 而不是直接报错——能否成功由上游说了算。
func selectProbeKey(channel *appmodel.Channel, keyIndex int) (probeChannelKey, error) {
	if channel == nil {
		return probeChannelKey{}, fmt.Errorf("channel is nil")
	}
	if keyIndex >= 0 {
		if keyIndex >= len(channel.Keys) {
			return probeChannelKey{}, fmt.Errorf("key_index %d out of range (have %d keys)", keyIndex, len(channel.Keys))
		}
		selected := channel.Keys[keyIndex]
		if strings.TrimSpace(selected.ChannelKey) == "" {
			return probeChannelKey{Key: selected, Index: keyIndex, NoKey: true}, nil
		}
		return probeChannelKey{Key: selected, Index: keyIndex}, nil
	}
	for index := range channel.Keys {
		if channel.Keys[index].Enabled && strings.TrimSpace(channel.Keys[index].ChannelKey) != "" {
			return probeChannelKey{Key: channel.Keys[index], Index: index}, nil
		}
	}
	return probeChannelKey{NoKey: true}, nil
}

// RunChannelCapabilityProbe 执行一次完整的手动探测（协议层 + 能力层），
// 把逐行结果落库，并返回可直接渲染的进度行。
//
// 设计约束：
//   - 只在前端点击时执行，没有后台定时任务，也没有自动重试；
//   - 串行执行；公益站普遍对并发敏感，"快"不如"不误判"重要；
//   - 任何 Unknown 结论都不会被应用到渠道配置（由调用方保证）。
func RunChannelCapabilityProbe(
	ctx context.Context,
	channel *appmodel.Channel,
	request appmodel.ChannelProbeRequest,
) (*appmodel.ChannelProbeRun, error) {
	if channel == nil {
		return nil, fmt.Errorf("channel is nil")
	}
	modelName := strings.TrimSpace(request.ModelName)
	if modelName == "" {
		return nil, fmt.Errorf("model_name is required")
	}
	if channel.SkipModelTest && !request.AllowSkipModelTest {
		// 禁止测活的渠道必须先经用户显式确认：很多公益站会因测活关键词封号。
		return nil, errModelTestSkipped
	}

	selectedKey, err := selectProbeKey(channel, request.KeyIndex)
	if err != nil {
		return nil, err
	}

	select {
	case capabilityProbeSemaphore <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-capabilityProbeSemaphore }()

	run := &appmodel.ChannelProbeRun{
		ChannelID: channel.ID,
		ModelName: modelName,
		KeyID:     selectedKey.Key.ID,
		StartedAt: time.Now(),
	}
	if channel.ID <= 0 {
		// 未保存的草稿渠道：允许探测（用户在新建表单里想先验证），
		// 但不落库，避免把无主记录写进数据库。
		run.ID = 0
	}

	results := make([]appmodel.ChannelProbeResult, 0, 8)
	passedAdapters := make(map[outbound.OutboundType]bool)

	// ---- 协议层：逐条验证可用端点 ----
	for _, target := range buildProtocolProbeTargets(channel.Type, modelName) {
		if ctx.Err() != nil {
			break
		}
		outcome := sendProbeRequest(ctx, channel, selectedKey.Key.ChannelKey, target.AdapterType, target.Request)
		passedAdapters[target.AdapterType] = outcome.Verdict == appmodel.ProbeVerdictPass
		results = append(results, buildProbeResult(run, appmodel.ProbeKindProtocol, target.Item, outcome, selectedKey.Key.ChannelKey))
	}

	// ---- 能力层：5 项能力（部分渠道会直接标 unsupported） ----
	capabilityAdapter := buildCapabilityProbeAdapter(channel.Type)
	textRequest, _ := buildCapabilityProbeRequest(appmodel.ProbeItemTextGeneration, modelName)
	capabilityAdapterReady := false
	for _, adapterType := range outbound.ResolveAttemptTypesForChannelDeclared(channel.Type, textRequest, "", channel.OutboundFormatOverride, channel.UpstreamProtocols) {
		if passedAdapters[adapterType] {
			capabilityAdapter = adapterType
			capabilityAdapterReady = true
			break
		}
	}
	capabilityItems := []string{
		appmodel.ProbeItemModels,
		appmodel.ProbeItemTextGeneration,
		appmodel.ProbeItemToolCalling,
		appmodel.ProbeItemStructuredOutput,
		appmodel.ProbeItemWebSearch,
	}
	for _, item := range capabilityItems {
		if ctx.Err() != nil {
			break
		}
		if !capabilityProbeSupportedForChannel(item, channel.Type) {
			results = append(results, appmodel.ChannelProbeResult{
				RunID:     run.ID,
				ChannelID: channel.ID,
				ModelName: modelName,
				Kind:      appmodel.ProbeKindCapability,
				Item:      item,
				Verdict:   appmodel.ProbeVerdictUnsupported,
				Summary:   "该渠道类型没有统一的探测入口",
			})
			continue
		}
		// 模型列表不是生成类探测：走 fetchModel 那套单独的 HTTP 路径。
		if item == appmodel.ProbeItemModels {
			results = append(results, probeChannelModelList(ctx, channel, selectedKey.Key.ChannelKey, modelName))
			continue
		}
		if !capabilityAdapterReady {
			results = append(results, appmodel.ChannelProbeResult{ChannelID: channel.ID, ModelName: modelName, Kind: appmodel.ProbeKindCapability, Item: item, Verdict: appmodel.ProbeVerdictUnknown, Summary: "No verified protocol available for capability probe"})
			continue
		}
		probeRequest, buildErr := buildCapabilityProbeRequest(item, modelName)
		if buildErr != nil {
			results = append(results, appmodel.ChannelProbeResult{
				RunID:     run.ID,
				ChannelID: channel.ID,
				ModelName: modelName,
				Kind:      appmodel.ProbeKindCapability,
				Item:      item,
				Verdict:   appmodel.ProbeVerdictUnknown,
				Summary:   truncateProbeSummary(buildErr.Error()),
			})
			continue
		}
		probeAdapter := capabilityAdapter
		if item == appmodel.ProbeItemWebSearch && passedAdapters[outbound.OutboundTypeOpenAIResponse] {
			probeAdapter = outbound.OutboundTypeOpenAIResponse
		}
		outcome := sendProbeRequest(ctx, channel, selectedKey.Key.ChannelKey, probeAdapter, probeRequest, item)
		outcome.Verdict = judgeCapabilityResponse(item, outcome)
		results = append(results, buildProbeResult(run, appmodel.ProbeKindCapability, item, outcome, selectedKey.Key.ChannelKey))
	}

	run.EndedAt = time.Now()
	secrets := []string{selectedKey.Key.ChannelKey}
	for _, header := range channel.CustomHeader {
		secrets = append(secrets, header.HeaderValue)
	}
	for i := range results {
		results[i].Summary = redactProbeDetail(results[i].Summary, secrets...)
		results[i].Detail = redactProbeDetail(results[i].Detail, secrets...)
	}
	run.Summary = summarizeProbeRun(results)
	run.Results = results
	return run, nil
}

// buildProbeResult 把一次 HTTP 结果转成可落库的单行记录（含脱敏）。
func buildProbeResult(
	run *appmodel.ChannelProbeRun,
	kind appmodel.ProbeKind,
	item string,
	outcome *probeRequestOutcome,
	secret string,
) appmodel.ChannelProbeResult {
	result := appmodel.ChannelProbeResult{
		RunID:     run.ID,
		ChannelID: run.ChannelID,
		ModelName: run.ModelName,
		Kind:      kind,
		Item:      item,
	}
	if outcome == nil {
		result.Verdict = appmodel.ProbeVerdictUnknown
		result.Summary = "probe produced no outcome"
		return result
	}
	result.Verdict = outcome.Verdict
	result.StatusCode = outcome.StatusCode
	result.LatencyMS = outcome.LatencyMS
	result.Summary = redactProbeDetail(outcome.Summary, secret)
	result.Detail = redactProbeDetail(outcome.Body, secret)
	return result
}

// summarizeProbeRun 生成一行人类可读的整体结论（纯展示，不参与决策）。
func summarizeProbeRun(results []appmodel.ChannelProbeResult) string {
	if len(results) == 0 {
		return "no probe executed"
	}
	var passed, failed, unsupported, unknown int
	for _, result := range results {
		switch result.Verdict {
		case appmodel.ProbeVerdictPass:
			passed++
		case appmodel.ProbeVerdictFail:
			failed++
		case appmodel.ProbeVerdictUnsupported:
			unsupported++
		default:
			unknown++
		}
	}
	return fmt.Sprintf("passed=%d failed=%d unsupported=%d unknown=%d", passed, failed, unsupported, unknown)
}

// probeChannelModelList 探测 /models 列表端点。
//
// 列表端点与生成端点不是一回事：很多中转站根本不实现 /models，
// 但对话完全正常。因此 404/405 判 Unsupported 而不是 Fail，
// 并且在界面上明确标成"不适用"而不是"坏了"。
func probeChannelModelList(
	ctx context.Context,
	channel *appmodel.Channel,
	key string,
	modelName string,
) appmodel.ChannelProbeResult {
	result := appmodel.ChannelProbeResult{
		ChannelID: channel.ID,
		ModelName: modelName,
		Kind:      appmodel.ProbeKindCapability,
		Item:      appmodel.ProbeItemModels,
	}
	// 复用既有的模型列表抓取（它已经按渠道类型分别处理 OpenAI / Gemini / Anthropic）。
	// 传入的渠道对象需要带上待探测的 Key。
	probeChannel := *channel
	if strings.TrimSpace(key) != "" {
		probeChannel.Keys = []appmodel.ChannelKey{{Enabled: true, ChannelKey: key}}
	}
	models, err := FetchModelsShortTimeout(ctx, probeChannel)
	if err != nil {
		// fetchChannelModelIDs 把状态码语义丢掉了，这里保守判 Unknown：
		// 拿不到列表不代表渠道不可用，绝不据此改写配置。
		result.Verdict = appmodel.ProbeVerdictUnknown
		result.Summary = redactProbeDetail(err.Error(), key)
		return result
	}
	if len(models) == 0 {
		result.Verdict = appmodel.ProbeVerdictUnknown
		result.Summary = "列表端点返回空结果"
		return result
	}
	result.Verdict = appmodel.ProbeVerdictPass
	result.Summary = fmt.Sprintf("列表返回 %d 个模型", len(models))
	return result
}

// ApplyChannelProbeRun 把一次探测的结论写回渠道配置。
//
// 严格「只加不减」：只把探测通过的协议加进渠道声明，把明确的结论写进能力表；
// 失败的协议不会被移除。理由是上游一次限流/抽风就会让"失败"结论不可信，
// 自动删配置会让用户静默丢掉一条可用通道，且界面上看不出是谁改的。
func ApplyChannelProbeRun(
	ctx context.Context,
	channel *appmodel.Channel,
	run *appmodel.ChannelProbeRun,
) (*appmodel.ChannelProbeApplyResult, error) {
	if channel == nil {
		return nil, fmt.Errorf("channel is nil")
	}
	if run == nil {
		return nil, fmt.Errorf("probe run is nil")
	}

	applyResult := &appmodel.ChannelProbeApplyResult{}

	// 协议层：把通过验证的协议加进渠道声明（不删任何现有项）。
	passingProtocols := make([]string, 0, 4)
	for _, result := range run.Results {
		if result.Kind != appmodel.ProbeKindProtocol {
			continue
		}
		if result.Verdict != appmodel.ProbeVerdictPass {
			continue
		}
		protocol := protocolForProbeItem(result.Item)
		if protocol == "" {
			continue
		}
		passingProtocols = append(passingProtocols, protocol)
	}

	existing := channel.EffectiveUpstreamProtocols()
	merged := make([]string, 0, len(existing)+len(passingProtocols))
	merged = append(merged, existing...)
	seen := make(map[string]bool, len(merged))
	for _, protocol := range merged {
		seen[protocol] = true
	}
	for _, protocol := range passingProtocols {
		if seen[protocol] || seen[strings.TrimSuffix(protocol, "_only")] || seen["passthrough"] || seen["raw"] {
			continue
		}
		seen[protocol] = true
		merged = append(merged, protocol)
		applyResult.AddedProtocols = append(applyResult.AddedProtocols, protocol)
	}

	normalized, err := appmodel.NormalizeUpstreamProtocols(merged)
	if err != nil {
		return nil, fmt.Errorf("merge probed protocols: %w", err)
	}
	if len(applyResult.AddedProtocols) > 0 {
		channel.UpstreamProtocols = normalized
	}

	// 能力层：只写明确结论（pass / unsupported），Unknown 一律跳过。
	for _, result := range run.Results {
		if result.Kind != appmodel.ProbeKindCapability {
			continue
		}
		if _, ok := capabilityForProbeItem(result.Item); !ok {
			continue
		}
		if !result.Verdict.IsConclusive() {
			continue
		}
		applyResult.Capabilities++
	}
	if len(applyResult.AddedProtocols) == 0 {
		applyResult.AddedProtocols = []string{}
	}
	return applyResult, nil
}

// protocolForProbeItem 把协议层探测项的标识映射回协议名。
func protocolForProbeItem(item string) string {
	switch item {
	case appmodel.ProbeItemProtocolChat:
		return string(appmodel.UpstreamProtocolChatOnly)
	case appmodel.ProbeItemProtocolResponses:
		return string(appmodel.UpstreamProtocolResponsesOnly)
	case appmodel.ProbeItemProtocolMessages:
		return string(appmodel.UpstreamProtocolMessagesOnly)
	default:
		// Gemini 没有"渠道声明的协议"对应项：Gemini 渠道类型本身已经表达了协议。
		return ""
	}
}

// capabilityForProbeItem 把能力层探测项的标识映射回能力名。
func capabilityForProbeItem(item string) (appmodel.CapabilityName, bool) {
	switch item {
	case appmodel.ProbeItemToolCalling:
		return appmodel.CapabilityToolCalling, true
	case appmodel.ProbeItemStructuredOutput:
		return appmodel.CapabilityStructured, true
	case appmodel.ProbeItemWebSearch:
		return appmodel.CapabilityWebSearch, true
	default:
		return "", false
	}
}

// CapabilityVerdicts 把一次探测的能力层结论整理成"能力 → 是否支持"，
// 供 op 层写入 channel_model_capabilities（helper 不直接依赖 op）。
func CapabilityVerdicts(run *appmodel.ChannelProbeRun) map[appmodel.CapabilityName]bool {
	verdicts := make(map[appmodel.CapabilityName]bool)
	if run == nil {
		return verdicts
	}
	for _, result := range run.Results {
		if result.Kind != appmodel.ProbeKindCapability || !result.Verdict.IsConclusive() {
			continue
		}
		capability, ok := capabilityForProbeItem(result.Item)
		if !ok {
			continue
		}
		verdicts[capability] = result.Verdict == appmodel.ProbeVerdictPass
	}
	return verdicts
}

// OutboundProtocolDeclarableForChannelType 报告某渠道类型是否支持"多协议声明"。
//
// 只有 OpenAI 兼容渠道能声明并回退多个协议；原生协议渠道（Gemini / Anthropic /
// 火山 / Cloudflare / Embedding）只会说自己的原生协议，前端据此决定是否渲染
// 协议多选区。
func OutboundProtocolDeclarableForChannelType(channelType outbound.OutboundType) bool {
	switch channelType {
	case outbound.OutboundTypeOpenAIChat,
		outbound.OutboundTypeOpenAIResponse:
		return true
	default:
		return false
	}
}
