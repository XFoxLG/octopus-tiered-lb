package relay

import (
	"strings"

	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

// canPassthroughStream 判定本尝试的流式响应是否可以走「同协议直通」：
// 上游 SSE 事件字节不经过 inAdapter 重新序列化，直接原样写给客户端。
//
// 存在条件（全部满足才返回 true，存疑一律回退原路径）：
//   - 出站是 passthrough/raw 透传适配器（字节级改写 model，无格式转换）；
//   - 入站请求的 RawAPIFormat 已由 passthrough 委托解析，非空；
//   - 无内联 reasoning 提取副作用（本会话曾把 reasoning 从内容里抽掉的话，
//     响应字节已不再是上游原文，直通会出错）。
func (ra *relayAttempt) canPassthroughStream() bool {
	if ra == nil || ra.internalRequest == nil {
		return false
	}
	if ra.adapterType != outbound.OutboundTypePassthrough && ra.adapterType != outbound.OutboundTypeRaw {
		return false
	}
	if len(strings.TrimSpace(string(ra.internalRequest.RawAPIFormat))) == 0 {
		return false
	}
	if ra.inlineReasoningExtracted {
		return false
	}
	return true
}

// passthroughStreamEvent 把上游 SSE 事件字节封装为写给客户端的帧。
// data 为 SSE data 行内容（不含 "data: " 前缀），拼装回完整 SSE 事件字节。
//
// 为了与 writeReasoningBuffer 的既有客户端或 stream session AddPayload 保持
// 字节级一致（内部历史都是 "data: " + payload + "\n\n" 完整帧），这里沿用同一形态。
func passthroughStreamEvent(data string) []byte {
	frame := make([]byte, 0, len(data)+8)
	frame = append(frame, "data: "...)
	frame = append(frame, data...)
	frame = append(frame, '\n', '\n')
	return frame
}
