package airoute

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/utils/log"
)

// aiRouteInputKeySep 是输入模型键的分隔符（与 route.go 的 seen 键一致）。
const aiRouteInputKeySep = "\x00"

func aiRouteInputKey(channelID int, modelName string) string {
	return fmt.Sprintf("%d%s%s", channelID, aiRouteInputKeySep, strings.ToLower(modelName))
}

// computeUncoveredInputs diff 输入模型集合与 AI 返回 routes 覆盖的
// (channel_id, lowercase upstream_model)，返回漏归类的输入列表。
// 全覆盖时返回 nil。
func computeUncoveredInputs(bucket aiRoutePromptBucket, routes []model.AIRouteEntry) []aiRoutePromptModelInput {
	if len(bucket.ModelInputs) == 0 {
		return nil
	}

	covered := make(map[string]struct{}, len(bucket.ModelInputs))
	for _, route := range routes {
		for _, item := range route.Items {
			if item.ChannelID <= 0 {
				continue
			}
			upstreamModel := strings.TrimSpace(item.UpstreamModel)
			if upstreamModel == "" {
				continue
			}
			covered[aiRouteInputKey(item.ChannelID, upstreamModel)] = struct{}{}
		}
	}

	uncovered := make([]aiRoutePromptModelInput, 0)
	for _, input := range bucket.ModelInputs {
		if input.ChannelID <= 0 || strings.TrimSpace(input.Model) == "" {
			continue
		}
		if _, ok := covered[aiRouteInputKey(input.ChannelID, input.Model)]; ok {
			continue
		}
		uncovered = append(uncovered, input)
	}
	if len(uncovered) == 0 {
		return nil
	}
	return uncovered
}

// followUpUncoveredInputs 实现 AI 路由生成的自校验闭环（A1）：
// 首轮分析完成后，若仍有输入模型未被任何 route 覆盖，向同一个分析服务
// 追问一轮，只针对漏掉的模型；补充 routes 与首轮结果合并去重后返回。
//
// 约束：
//   - 每个 bucket 只追问一次（由调用方保证 routes 已是首轮结果）；
//   - 追问失败不致命：log.Warnf 后返回首轮 routes（质量退化但不中断任务）；
//   - 追问超时受全局 ctx 控制，不额外延长任务时长。
func followUpUncoveredInputs(
	ctx context.Context,
	service aiRouteService,
	bucket aiRoutePromptBucket,
	targetGroupName string,
	batchIndex int,
	firstRoutes []model.AIRouteEntry,
) []model.AIRouteEntry {
	if ctx.Err() != nil {
		return firstRoutes
	}

	uncovered := computeUncoveredInputs(bucket, firstRoutes)
	if len(uncovered) == 0 {
		return firstRoutes
	}

	log.Infof("ai route bucket %d follow-up: %d/%d inputs uncovered by first pass, asking the model to classify them",
		batchIndex, len(uncovered), len(bucket.ModelInputs))

	payload, err := json.Marshal(uncovered)
	if err != nil {
		log.Warnf("ai route bucket %d follow-up marshal failed: %v", batchIndex, err)
		return firstRoutes
	}

	systemPrompt := buildAIRouteFollowUpSystemPrompt()
	userPrompt := buildAIRouteFollowUpUserPrompt(targetGroupName, payload)

	content, callErr := callAIRouteChatCompletion(ctx, service, systemPrompt, userPrompt)
	if callErr != nil {
		log.Warnf("ai route bucket %d follow-up call failed (keeping first-pass routes): %v", batchIndex, callErr)
		return firstRoutes
	}

	routeResp, parseErr := parseAIRouteResponseContent(content)
	if parseErr != nil {
		log.Warnf("ai route bucket %d follow-up decode failed (keeping first-pass routes): %v", batchIndex, parseErr)
		return firstRoutes
	}

	supplement := normalizeAIRouteEntries(routeResp.Routes)
	if len(supplement) == 0 {
		log.Warnf("ai route bucket %d follow-up returned no routes (keeping first-pass routes)", batchIndex)
		return firstRoutes
	}

	for i := range supplement {
		supplement[i].EndpointType = bucket.GroupEndpointType
	}

	merged := normalizeAIRouteEntries(append(append([]model.AIRouteEntry(nil), firstRoutes...), supplement...))

	stillUncovered := computeUncoveredInputs(bucket, merged)
	if len(stillUncovered) > 0 {
		log.Warnf("ai route bucket %d follow-up still left %d inputs unclassified: %s",
			batchIndex, len(stillUncovered), summarizeUncoveredInputs(stillUncovered))
	} else {
		log.Infof("ai route bucket %d follow-up covered all remaining inputs", batchIndex)
	}

	return merged
}

// summarizeUncoveredInputs 生成漏归类模型的紧凑日志摘要（最多 10 个模型名）。
func summarizeUncoveredInputs(inputs []aiRoutePromptModelInput) string {
	const maxNames = 10
	names := make([]string, 0, len(inputs))
	for i, input := range inputs {
		if i >= maxNames {
			names = append(names, "...")
			break
		}
		names = append(names, fmt.Sprintf("%d:%s", input.ChannelID, input.Model))
	}
	return strings.Join(names, ", ")
}

// buildAIRouteFollowUpSystemPrompt 追问轮的 system prompt。
func buildAIRouteFollowUpSystemPrompt() string {
	return "你是一个模型路由分析器。上一轮分析中，以下模型尚未被归类。请补充归类并输出路由 JSON。\n" +
		"要求：\n" +
		"1. 只输出 JSON，不要输出任何解释、Markdown、代码块标记。\n" +
		"2. 将语义相同或同系列的模型归一到一个 requested_model。\n" +
		"3. requested_model 应尽量使用简洁、稳定、常见的名称。\n" +
		"4. items 中每个元素表示一个可用上游：\n" +
		"   - channel_id: 整数，必须来自输入列表\n" +
		"   - upstream_model: 原始模型名，必须来自输入列表中相同 channel_id 下的 model\n" +
		"   - priority: 数字，越小优先级越高\n" +
		"   - weight: 数字，默认 100\n" +
		"5. 如果一个模型名确实无法判断，可以不归类（宁缺勿滥）。\n" +
		"6. 输出格式必须严格符合：\n" +
		"{\n" +
		"  \"routes\": [\n" +
		"    {\n" +
		"      \"requested_model\": \"string\",\n" +
		"      \"items\": [\n" +
		"        {\n" +
		"          \"channel_id\": 1,\n" +
		"          \"upstream_model\": \"string\",\n" +
		"          \"priority\": 1,\n" +
		"          \"weight\": 100\n" +
		"        }\n" +
		"      ]\n" +
		"    }\n" +
		"  ]\n" +
		"}"
}

// buildAIRouteFollowUpUserPrompt 追问轮的 user prompt。
func buildAIRouteFollowUpUserPrompt(targetGroupName string, payload []byte) string {
	if strings.TrimSpace(targetGroupName) != "" {
		return fmt.Sprintf(
			"请为以下尚未归类的模型补充路由。\n本次目标分组名称为 %q，请优先输出 requested_model 为 %q 的路由。\n模型列表：\n%s",
			targetGroupName,
			targetGroupName,
			string(payload),
		)
	}
	return fmt.Sprintf("请为以下尚未归类的模型补充路由。\n模型列表：\n%s", string(payload))
}
