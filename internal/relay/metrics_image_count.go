package relay

import (
	transformerModel "github.com/lingyuins/octopus/internal/transformer/model"
)

// countRequestImages 统计客户端请求内的图片数量，供日志「含图 ×N」标注与
// has_image 筛选使用。各入站适配器都把图片归一为 MessageContentPart
// {Type:"image_url"} 承载在 messages[].content 数组里（legacy Message.Images
// 一并计入）。这里只做计数，不改动任何请求内容；与日志内容留存开关无关，
// 沿用 v2.8.13 usage_estimate.go 的「独立文件 + 单点接线」模式（v2.8.14）。
func countRequestImages(req *transformerModel.InternalLLMRequest) int {
	if req == nil {
		return 0
	}

	count := 0
	for i := range req.Messages {
		msg := &req.Messages[i]
		for _, part := range msg.Content.MultipleContent {
			if part.Type == "image_url" && part.ImageURL != nil && part.ImageURL.URL != "" {
				count++
			}
		}
		for _, img := range msg.Images {
			if img.ImageURL != nil && img.ImageURL.URL != "" {
				count++
			}
		}
	}
	return count
}
