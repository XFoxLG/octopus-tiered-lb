package helper

import (
	"context"
	"fmt"
	"strings"
	"time"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/xstrings"
)

// testModelSyncMaxTimeout 单次同步探针的超时上限，防止恶意构造的 timeout 挂死资源。
const testModelSyncMaxTimeout = 5 * time.Minute

// ChannelModelSyncProbeResult 是单次同步渠道模型探针的瞬时结果，不持久化。
type ChannelModelSyncProbeResult struct {
	Model      string `json:"model"`
	StatusCode int    `json:"status_code"` // 上游 HTTP 状态码；网络错误/超时（没拿到响应）时为 0
	DelayMS    int64  `json:"delay_ms"`
	Error      string `json:"error,omitempty"`
}

// TestChannelModelSync 对单个渠道上的单个模型发起一次同步真实调用并阻塞返回。
// 与异步管线（StartChannelModelTest，不重试/无进度/不写 relay 日志）不同，
// 本函数只做单次探测：首个可用 adapter、单 key、单次请求，适用于渠道编辑页的
// 即时手感验证。channel 可以是已保存的（有 ID），也可以是新建弹窗里未保存的
// 临时对象；keyIndex 是 channel.Keys 数组下标，调用方保证不越界。
// endpointType 为空时默认 chat；timeout<=0 时默认 30 秒，上限 5 分钟。
func TestChannelModelSync(
	ctx context.Context,
	channel *appmodel.Channel,
	modelName string,
	keyIndex int,
	endpointType string,
	timeout time.Duration,
) (*ChannelModelSyncProbeResult, error) {
	result := &ChannelModelSyncProbeResult{Model: modelName}

	if channel == nil {
		return nil, fmt.Errorf("channel is nil")
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return nil, fmt.Errorf("model is empty")
	}
	if channel.SkipModelTest {
		return nil, fmt.Errorf("channel skipped model test (issue #98)")
	}
	if keyIndex < 0 || keyIndex >= len(channel.Keys) {
		return nil, fmt.Errorf("key_index %d out of range (have %d keys)", keyIndex, len(channel.Keys))
	}
	key := strings.TrimSpace(channel.Keys[keyIndex].ChannelKey)
	if key == "" {
		return nil, fmt.Errorf("selected key is empty")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > testModelSyncMaxTimeout {
		timeout = testModelSyncMaxTimeout
	}

	if outbound.Get(channel.Type) == nil {
		return nil, fmt.Errorf("unsupported channel type: %d", channel.Type)
	}
	endpoint := strings.TrimSpace(endpointType)
	if endpoint == "" {
		endpoint = appmodel.EndpointTypeChat
	}
	if err := validateGroupProbeChannelType(endpoint, channel.Type); err != nil {
		return nil, err
	}

	probeRequest, err := buildGroupProbeRequest(endpoint, modelName)
	if err != nil {
		return nil, err
	}
	adapterTypes := outbound.ResolveAttemptTypes(channel.Type, probeRequest, "")
	if len(adapterTypes) == 0 {
		return nil, fmt.Errorf("no available adapter for channel type: %d", channel.Type)
	}

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	// 单次探测：只用首个 adapter，不重试；失败原因写入 Error 即时返回。
	statusCode, responseText, _, err := sendGroupProbeRequest(probeCtx, outbound.Get(adapterTypes[0]), channel, key, endpoint, modelName)
	result.DelayMS = time.Since(start).Milliseconds()
	result.StatusCode = statusCode
	if err != nil {
		result.Error = truncateSyncProbeErr(firstNonEmpty(responseText, err.Error()))
		return result, nil
	}
	return result, nil
}

// ChannelModelInSelectedModels 报告模型名是否在渠道已选模型集合（model + custom_model）内。
func ChannelModelInSelectedModels(channel *appmodel.Channel, modelName string) bool {
	if channel == nil {
		return false
	}
	for _, m := range xstrings.SplitTrimCompact(",", channel.Model, channel.CustomModel) {
		if m == modelName {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// truncateSyncProbeErr 截断上游错误摘要，避免超大 body 污染响应。
func truncateSyncProbeErr(s string) string {
	const maxLen = 200
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
