package relay

import (
	"errors"
	"testing"

	"github.com/lingyuins/octopus/internal/model"
	transformerModel "github.com/lingyuins/octopus/internal/transformer/model"
)

func TestResolveUsageState(t *testing.T) {
	usage := &transformerModel.Usage{PromptTokens: 12, CompletionTokens: 34}
	responseWithUsage := &transformerModel.InternalLLMResponse{Usage: usage}
	responseWithoutUsage := &transformerModel.InternalLLMResponse{}

	testCases := []struct {
		name string
		resp *transformerModel.InternalLLMResponse
		err  error
		want string
	}{
		// 成因优先于 usage 数据：断连/流中断时即使带了部分 usage 块也不可信。
		{"client disconnect with partial usage", responseWithUsage, errClientDisconnected, model.RelayLogUsageClientDisconnect},
		{"missing stream terminal with partial usage", responseWithUsage, errMissingStreamTerminal, model.RelayLogUsageMissingTerminal},
		{"empty output", responseWithUsage, errEmptyOutput, model.RelayLogUsageEmptyOutput},
		// 无成因时的正常判定。
		{"reported", responseWithUsage, nil, model.RelayLogUsageReported},
		{"not reported on success", responseWithoutUsage, nil, model.RelayLogUsageNotReported},
		{"not reported on nil response", nil, nil, model.RelayLogUsageNotReported},
		{"failed no response", nil, errors.New("upstream 503"), model.RelayLogUsageFailedNoResponse},
		{"failed with usage-less response", responseWithoutUsage, errors.New("upstream 503"), model.RelayLogUsageFailedNoResponse},
		// usage 块存在但 token 全零 = 上游没真回报，不算 reported。
		{"zero tokens not reported", &transformerModel.InternalLLMResponse{Usage: &transformerModel.Usage{}}, nil, model.RelayLogUsageNotReported},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := resolveUsageState(testCase.resp, testCase.err)
			if got != testCase.want {
				t.Fatalf("resolveUsageState() = %q, want %q", got, testCase.want)
			}
		})
	}
}
