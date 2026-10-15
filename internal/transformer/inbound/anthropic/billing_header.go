package anthropic

import "strings"

// billingHeaderPrefix 是 Claude Code 在 system 文本块里注入的计费元数据前缀。
// 该块形如：x-anthropic-billing-header: cc_version=…; cc_entrypoint=cli; cch=<每次请求都变>;
const billingHeaderPrefix = "x-anthropic-billing-header:"

// asciiLower 只折叠 ASCII 大写字母：字节长度不变，保证后续用下标
// 在原串上做切片不会因 Unicode 大小写折叠（如 İ→i̇）而错位。
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// isBillingHeaderText 判定文本是否为计费头（容忍首尾空白与 ASCII 大小写，
// 与 axonhub 的 isBillingHeaderText 同口径）。
func isBillingHeaderText(text string) bool {
	return strings.HasPrefix(asciiLower(strings.TrimSpace(text)), billingHeaderPrefix)
}

// stripCchFromBillingHeader 剥离计费头里的 `; cch=…` 动态字段（v2.8.15 协议层
// fix-p1）：cch 每次请求都不同，原样转发会让上游每次看到不同的 system 前缀，
// 直接破坏 prompt cache（计费与首字都受损，见 higress stripCchFromBillingHeader）。
// 语义：仅当前缀命中时处理；其余字段（cc_version/cc_entrypoint 等）保留；
// 兼容 `; cch=xxx;`（中段）与 `; cch=xxx`（末尾）两种形态；多次出现全部移除；
// 键名大小写不敏感（保持原串偏移，不做整体 ToLower）。
func stripCchFromBillingHeader(text string) string {
	if !isBillingHeaderText(text) {
		return text
	}

	result := text
	for {
		idx := strings.Index(asciiLower(result), "; cch=")
		if idx == -1 {
			return result
		}
		// result[idx+2:] 从 "cch=…" 开始；其后的第一个 ';' 是值的结束符。
		rest := result[idx+2:]
		if end := strings.Index(rest, ";"); end == -1 {
			// cch 位于末尾：截断至其前。
			result = result[:idx]
		} else {
			// 保留值结束符 ';' 及其后内容。
			result = result[:idx] + rest[end:]
		}
	}
}
