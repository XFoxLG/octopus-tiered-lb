package relay

import (
	"strings"

	transformerModel "github.com/lingyuins/octopus/internal/transformer/model"
)

// promptCacheKeyOf 提取客户端请求里的缓存亲和键（OpenAI prompt_cache_key 等，v2.9.0）。
//
// 会话粘性据此从「Key × 模型」细化到「Key × 模型 × 缓存键」：同一段前缀会被反复
// 复用时，只有稳定落在同一渠道才能保住上游前缀缓存、避免跨账号重放历史
// （GPT-Load rc.18 / Codex-Proxy-RS #443 的外部实证）。字段缺失或空白时返回空串，
// 调用方自动退回原有的两级键行为（现有「成功率 <0.3 自动解除粘性」逃生门不变）。
func promptCacheKeyOf(req *transformerModel.InternalLLMRequest) string {
	if req == nil || req.PromptCacheKey == nil {
		return ""
	}
	return strings.TrimSpace(*req.PromptCacheKey)
}
