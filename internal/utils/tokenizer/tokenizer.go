package tokenizer

import (
	"strings"
	"sync"

	"github.com/tiktoken-go/tokenizer/codec"
)

// codec 实例的构建成本远高于一次计数：词表虽然每个进程只初始化一次，但每个
// codec 还要编译一条大体积的 Unicode 分割正则。v2.8.13 起 Token 估算会在请求
// 收尾路径上频繁调用，因此按编码族缓存 codec，避免每次调用都重新编译正则。
//
// Count() 并发安全：只读不可变的词表，匹配状态由 regexp2 在内部互斥保护的
// runner 池里隔离。Decode() 会惰性构建 reverseVocabulary，不可并发使用；
// 网关只用 Count。
var (
	o200kCodecOnce sync.Once
	o200kCodec     *codec.Codec

	cl100kCodecOnce sync.Once
	cl100kCodec     *codec.Codec

	p50kEditCodecOnce sync.Once
	p50kEditCodec     *codec.Codec

	r50kCodecOnce sync.Once
	r50kCodec     *codec.Codec

	p50kCodecOnce sync.Once
	p50kCodec     *codec.Codec
)

type encodingFamily int

const (
	familyO200k encodingFamily = iota
	familyCl100k
	familyP50kEdit
	familyR50k
	familyP50k
)

// encodingFamilyForModel maps model name prefixes to the appropriate tokenizer
// encoding family. Models are matched by prefix to cover variants
// (e.g., "gpt-4o" matches "gpt-4o-2024-08-06").
func encodingFamilyForModel(model string) encodingFamily {
	lower := strings.ToLower(strings.TrimSpace(model))

	// o200k_base — GPT-4o, o1, o3, o4, and their variants
	if strings.HasPrefix(lower, "gpt-4o") || strings.HasPrefix(lower, "o1") ||
		strings.HasPrefix(lower, "o3") || strings.HasPrefix(lower, "o4") ||
		strings.HasPrefix(lower, "chatgpt-4o") {
		return familyO200k
	}

	// cl100k_base — GPT-4, GPT-3.5-turbo, text-embedding-ada-002
	if strings.HasPrefix(lower, "gpt-4") || strings.HasPrefix(lower, "gpt-3.5") ||
		strings.HasPrefix(lower, "text-embedding-ada") || strings.HasPrefix(lower, "text-embedding-3") {
		return familyCl100k
	}

	// p50k_edit — text-davinci-edit-001, code-davinci-edit-001
	if strings.HasPrefix(lower, "text-davinci-edit") || strings.HasPrefix(lower, "code-davinci-edit") {
		return familyP50kEdit
	}

	// r50k_base — original GPT-3 davinci models
	if strings.HasPrefix(lower, "davinci") || strings.HasPrefix(lower, "curie") ||
		strings.HasPrefix(lower, "babbage") || strings.HasPrefix(lower, "ada") ||
		strings.HasPrefix(lower, "text-similarity") || strings.HasPrefix(lower, "text-search") ||
		strings.HasPrefix(lower, "code-search") {
		return familyR50k
	}

	// p50k_base — code-cushman, code-davinci (Codex models)
	if strings.HasPrefix(lower, "code-cushman") || strings.HasPrefix(lower, "code-davinci") {
		return familyP50k
	}

	// Default: o200k_base for unknown models (most modern models use this encoding)
	return familyO200k
}

func codecForModel(model string) *codec.Codec {
	return codecForFamily(encodingFamilyForModel(model))
}

func codecForFamily(family encodingFamily) *codec.Codec {
	switch family {
	case familyCl100k:
		cl100kCodecOnce.Do(func() { cl100kCodec = codec.NewCl100kBase() })
		return cl100kCodec
	case familyP50kEdit:
		p50kEditCodecOnce.Do(func() { p50kEditCodec = codec.NewP50kEdit() })
		return p50kEditCodec
	case familyR50k:
		r50kCodecOnce.Do(func() { r50kCodec = codec.NewR50kBase() })
		return r50kCodec
	case familyP50k:
		p50kCodecOnce.Do(func() { p50kCodec = codec.NewP50kBase() })
		return p50kCodec
	default:
		o200kCodecOnce.Do(func() { o200kCodec = codec.NewO200kBase() })
		return o200kCodec
	}
}

// CountTokens returns the token count for content under the encoding family
// selected by model. Errors are reported as 0 (never negative).
func CountTokens(content, model string) int {
	enc := codecForModel(model)
	tc, err := enc.Count(content)
	if err != nil {
		return 0
	}
	return tc
}
