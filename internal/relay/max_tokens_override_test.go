package relay

import (
	"strconv"
	"testing"

	appmodel "github.com/lingyuins/octopus/internal/model"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func paramOverridePtr(value string) *string { return &value }

func TestApplyParamOverrideMaxTokenAliases(t *testing.T) {
	invalid := "{invalid"

	cases := []struct {
		name             string
		channelOverride  string
		groupOverride    string
		request          *transmodel.InternalLLMRequest
		wantMaxTokens    *int64
		wantCompletion   *int64
		wantSource       string
		wantSourceExists bool
	}{
		{
			name:             "no client limit takes channel max_tokens before its alias",
			channelOverride:  `{"max_tokens":201,"max_completion_tokens":202}`,
			groupOverride:    `{"max_tokens":101,"max_completion_tokens":102}`,
			request:          &transmodel.InternalLLMRequest{},
			wantMaxTokens:    int64Ptr(201),
			wantCompletion:   nil,
			wantSource:       transmodel.OutboundMaxTokensSourceChannelMaxTokens,
			wantSourceExists: true,
		},
		{
			name:             "channel max_completion_tokens beats group max_tokens",
			channelOverride:  `{"max_completion_tokens":202}`,
			groupOverride:    `{"max_tokens":101}`,
			request:          &transmodel.InternalLLMRequest{},
			wantMaxTokens:    nil,
			wantCompletion:   int64Ptr(202),
			wantSource:       transmodel.OutboundMaxTokensSourceChannelMaxCompletionTokens,
			wantSourceExists: true,
		},
		{
			name:             "group max_tokens wins after channel alias is absent",
			channelOverride:  `{"temperature":0.2}`,
			groupOverride:    `{"max_tokens":101,"max_completion_tokens":102}`,
			request:          &transmodel.InternalLLMRequest{},
			wantMaxTokens:    int64Ptr(101),
			wantCompletion:   nil,
			wantSource:       transmodel.OutboundMaxTokensSourceGroupMaxTokens,
			wantSourceExists: true,
		},
		{
			name:             "group max_completion_tokens is the last fallback",
			groupOverride:    `{"max_completion_tokens":102}`,
			request:          &transmodel.InternalLLMRequest{},
			wantMaxTokens:    nil,
			wantCompletion:   int64Ptr(102),
			wantSource:       transmodel.OutboundMaxTokensSourceGroupMaxCompletionTokens,
			wantSourceExists: true,
		},
		{
			name:            "client max_tokens blocks the sibling override",
			channelOverride: `{"max_completion_tokens":202}`,
			groupOverride:   `{"max_tokens":101}`,
			request: &transmodel.InternalLLMRequest{
				MaxTokens: int64Ptr(10),
			},
			wantMaxTokens:    int64Ptr(10),
			wantCompletion:   nil,
			wantSource:       transmodel.OutboundMaxTokensSourceClientMaxTokens,
			wantSourceExists: true,
		},
		{
			name:            "client max_completion_tokens blocks the sibling override",
			channelOverride: `{"max_tokens":202}`,
			groupOverride:   `{"max_completion_tokens":101}`,
			request: &transmodel.InternalLLMRequest{
				MaxCompletionTokens: int64Ptr(20),
			},
			wantMaxTokens:    nil,
			wantCompletion:   int64Ptr(20),
			wantSource:       transmodel.OutboundMaxTokensSourceClientMaxCompletionTokens,
			wantSourceExists: true,
		},
		{
			name:            "responses max_output_tokens keeps its protocol field name",
			channelOverride: `{"max_tokens":202}`,
			request: &transmodel.InternalLLMRequest{
				MaxCompletionTokens: int64Ptr(20),
				RawAPIFormat:        transmodel.APIFormatOpenAIResponse,
			},
			wantMaxTokens:    nil,
			wantCompletion:   int64Ptr(20),
			wantSource:       transmodel.OutboundMaxTokensSourceClientMaxOutputTokens,
			wantSourceExists: true,
		},
		{
			name: "responses max_output_tokens is recorded without overrides",
			request: &transmodel.InternalLLMRequest{
				MaxCompletionTokens: int64Ptr(20),
				RawAPIFormat:        transmodel.APIFormatOpenAIResponse,
			},
			wantCompletion:   int64Ptr(20),
			wantSource:       transmodel.OutboundMaxTokensSourceClientMaxOutputTokens,
			wantSourceExists: true,
		},
		{
			name:            "client leaves both aliases untouched",
			channelOverride: `{"max_tokens":202,"max_completion_tokens":203}`,
			request: &transmodel.InternalLLMRequest{
				MaxTokens:           int64Ptr(10),
				MaxCompletionTokens: int64Ptr(20),
			},
			wantMaxTokens:    int64Ptr(10),
			wantCompletion:   int64Ptr(20),
			wantSource:       transmodel.OutboundMaxTokensSourceClientMaxTokens,
			wantSourceExists: true,
		},
		{
			name:             "malformed values fall through like other overrides",
			channelOverride:  invalid,
			groupOverride:    `{"max_tokens":101}`,
			request:          &transmodel.InternalLLMRequest{},
			wantMaxTokens:    int64Ptr(101),
			wantCompletion:   nil,
			wantSource:       transmodel.OutboundMaxTokensSourceGroupMaxTokens,
			wantSourceExists: true,
		},
		{
			name:             "explicit small client max_tokens wins over large group limit",
			groupOverride:    `{"max_tokens":32768}`,
			request:          &transmodel.InternalLLMRequest{MaxTokens: int64Ptr(4)},
			wantMaxTokens:    int64Ptr(4),
			wantCompletion:   nil,
			wantSource:       transmodel.OutboundMaxTokensSourceClientMaxTokens,
			wantSourceExists: true,
		},
		{
			name:             "large channel max_tokens fills an absent client limit",
			channelOverride:  `{"max_tokens":32768}`,
			request:          &transmodel.InternalLLMRequest{},
			wantMaxTokens:    int64Ptr(32768),
			wantCompletion:   nil,
			wantSource:       transmodel.OutboundMaxTokensSourceChannelMaxTokens,
			wantSourceExists: true,
		},
		{
			name:             "large group max_completion_tokens is used after channel aliases are absent",
			channelOverride:  `{"temperature":0.2}`,
			groupOverride:    `{"max_completion_tokens":32768}`,
			request:          &transmodel.InternalLLMRequest{},
			wantMaxTokens:    nil,
			wantCompletion:   int64Ptr(32768),
			wantSource:       transmodel.OutboundMaxTokensSourceGroupMaxCompletionTokens,
			wantSourceExists: true,
		},
		{
			name:             "explicit large client max_completion_tokens wins over large group max_tokens",
			channelOverride:  `{}`,
			groupOverride:    `{"max_tokens":32768}`,
			request:          &transmodel.InternalLLMRequest{MaxCompletionTokens: int64Ptr(32768)},
			wantMaxTokens:    nil,
			wantCompletion:   int64Ptr(32768),
			wantSource:       transmodel.OutboundMaxTokensSourceClientMaxCompletionTokens,
			wantSourceExists: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			channel := &appmodel.Channel{
				Type:          outbound.OutboundTypeOpenAIChat,
				ParamOverride: paramOverridePtr(tc.channelOverride),
			}
			group := &appmodel.Group{}
			if tc.groupOverride != "" {
				group.ParamOverride = paramOverridePtr(tc.groupOverride)
			}

			applyParamOverride(channel, group, tc.request)

			assertMaxTokenPointer(t, "max_tokens", tc.request.MaxTokens, tc.wantMaxTokens)
			assertMaxTokenPointer(t, "max_completion_tokens", tc.request.MaxCompletionTokens, tc.wantCompletion)
			assertMaxTokenMetadata(t, tc.request, tc.wantSource, tc.wantSourceExists, false)
		})
	}
}

func assertMaxTokenPointer(t *testing.T, name string, got, want *int64) {
	t.Helper()
	if (got == nil) != (want == nil) {
		t.Fatalf("%s = %#v, want %#v", name, got, want)
	}
	if got != nil && *got != *want {
		t.Fatalf("%s = %d, want %d", name, *got, *want)
	}
}

func assertMaxTokenMetadata(t *testing.T, request *transmodel.InternalLLMRequest, source string, exists, adjusted bool) {
	t.Helper()
	if !exists {
		for _, key := range []string{
			transmodel.TransformerMetadataOutboundMaxTokensValue,
			transmodel.TransformerMetadataOutboundMaxTokensSource,
			transmodel.TransformerMetadataOutboundMaxTokensAdjusted,
		} {
			if _, ok := request.TransformerMetadata[key]; ok {
				t.Fatalf("metadata %s unexpectedly set: %q", key, request.TransformerMetadata[key])
			}
		}
		return
	}

	if got := request.TransformerMetadata[transmodel.TransformerMetadataOutboundMaxTokensSource]; got != source {
		t.Fatalf("metadata source = %q, want %q", got, source)
	}
	if request.TransformerMetadata[transmodel.TransformerMetadataOutboundMaxTokensAdjusted] != strconv.FormatBool(adjusted) {
		t.Fatalf("metadata adjusted = %q, want %t",
			request.TransformerMetadata[transmodel.TransformerMetadataOutboundMaxTokensAdjusted], adjusted)
	}
}
