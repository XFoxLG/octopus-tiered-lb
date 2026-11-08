package helper

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lingyuins/octopus/internal/conf"
	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/cacheusage"
	grp "github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/price"
	"github.com/lingyuins/octopus/internal/store"
	"github.com/lingyuins/octopus/internal/transformer"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/log"
)

var errModelTestSkipped = errors.New("channel skipped model test (issue #98)")

// ErrModelTestSkipped 暴露给上层 handler：把"渠道禁止测活、需要用户确认"
// 与普通参数错误区分开，前端据此弹确认框而不是报错。
func ErrModelTestSkipped() error { return errModelTestSkipped }

type GroupModelTestRequest struct {
	GroupID int `json:"group_id" binding:"required"`
}

type GroupModelDraftTestItem struct {
	ClientID                string `json:"client_id" binding:"required"`
	ItemID                  int    `json:"item_id,omitempty"`
	ChannelID               int    `json:"channel_id" binding:"required"`
	ModelName               string `json:"model_name" binding:"required"`
	Priority                int    `json:"priority,omitempty"`
	Weight                  int    `json:"weight,omitempty"`
	RelayRetryCountOverride *int   `json:"relay_retry_count_override,omitempty"`
}

type GroupModelDraftTestRequest struct {
	Name                    string                    `json:"name,omitempty"`
	Category                string                    `json:"category,omitempty"`
	EndpointType            string                    `json:"endpoint_type" binding:"required"`
	EndpointProvider        string                    `json:"endpoint_provider,omitempty"`
	OutboundFormat          string                    `json:"outbound_format,omitempty"`
	ForwardMode             string                    `json:"forward_mode,omitempty"`
	Mode                    appmodel.GroupMode        `json:"mode,omitempty"`
	FirstTokenTimeOut       int                       `json:"first_token_time_out,omitempty"`
	AttemptTimeOut          int                       `json:"attempt_time_out,omitempty"`
	StreamIdleTimeout       int                       `json:"stream_idle_timeout,omitempty"`
	RequestTimeoutSeconds   int                       `json:"request_timeout_seconds,omitempty"`
	StreamKeepaliveEnabled  bool                      `json:"stream_keepalive_enabled,omitempty"`
	AssistantPrefillCompat  bool                      `json:"assistant_prefill_compat,omitempty"`
	HedgeEnabled            bool                      `json:"hedge_enabled,omitempty"`
	HedgeDelayMs            int                       `json:"hedge_delay_ms,omitempty"`
	SessionKeepTime         int                       `json:"session_keep_time,omitempty"`
	Condition               string                    `json:"condition,omitempty"`
	ReasoningBufferStrategy string                    `json:"reasoning_buffer_strategy,omitempty"`
	DefaultReasoningEffort  string                    `json:"default_reasoning_effort,omitempty"`
	ReasoningForceOverride  bool                      `json:"reasoning_force_override,omitempty"`
	ParamOverride           *string                   `json:"param_override,omitempty"`
	CustomHeader            []appmodel.CustomHeader   `json:"custom_header,omitempty"`
	RelayRetryCount         int                       `json:"relay_retry_count,omitempty"`
	RelayRouteRetries       int                       `json:"relay_route_retries,omitempty"`
	Items                   []GroupModelDraftTestItem `json:"items" binding:"required"`
}

type GroupModelTestResult struct {
	EndpointID   string `json:"endpoint_id,omitempty"`
	Protocol     string `json:"protocol,omitempty"`
	Source       string `json:"source,omitempty"`
	SnapshotHash string `json:"snapshot_hash,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ItemID       int    `json:"item_id"`
	ChannelID    int    `json:"channel_id"`
	ChannelName  string `json:"channel_name"`
	ModelName    string `json:"model_name"`
	Passed       bool   `json:"passed"`
	Attempts     int    `json:"attempts"`
	StatusCode   int    `json:"status_code"`
	ResponseText string `json:"response_text,omitempty"`
	Message      string `json:"message,omitempty"`
}

type GroupModelTestSummary struct {
	Passed    bool                   `json:"passed"`
	Completed int                    `json:"completed"`
	Total     int                    `json:"total"`
	Results   []GroupModelTestResult `json:"results"`
}

type GroupModelTestProgress struct {
	ID           string                 `json:"id"`
	Source       string                 `json:"source,omitempty"`
	SnapshotHash string                 `json:"snapshot_hash,omitempty"`
	Passed       bool                   `json:"passed"`
	Completed    int                    `json:"completed"`
	Total        int                    `json:"total"`
	Done         bool                   `json:"done"`
	Results      []GroupModelTestResult `json:"results"`
	Message      string                 `json:"message,omitempty"`
}

type groupModelTestProgressEntry struct {
	progress  GroupModelTestProgress
	expiresAt time.Time
}

var groupProbeProgress sync.Map

var groupProbeProgressTTL = 10 * time.Minute

// groupProbeKVKeyPrefix 是分组探测进度在 KVStore 中的子系统前缀。
// 完整 Redis key 形如 octopus:groupprobe:{id}。
const groupProbeKVKeyPrefix = "groupprobe:"

func groupProbeKVKey(id string) string { return groupProbeKVKeyPrefix + id }

const maxConcurrentGroupModelTests = 6

func TestGroupModels(ctx context.Context, group *appmodel.Group, channels map[int]appmodel.Channel) (*GroupModelTestSummary, error) {
	if conf.IsDevMockSuccess() {
		return buildDevMockGroupTestSummary(group)
	}
	progress := &GroupModelTestProgress{Total: len(group.Items)}
	return runGroupModelTest(ctx, group, channels, progress)
}

// recordGroupTestStatus 回写分组最近一次测试的成败状态到 DB（issue #113/#119）。
// 草稿测试（StartDraftGroupModelTest）不回写，因为 group 无持久化 ID。
// allFailed 区分全部失败与部分失败（issue #119），仅当 passed=false 时有意义。
// 回写失败仅记日志，不影响测试结果本身。
func recordGroupTestStatus(groupID int, passed bool, allFailed bool) {
	if groupID <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := grp.UpdateGroupTestStatus(groupID, passed, allFailed, ctx); err != nil {
		log.Errorf("failed to record group test status: group_id=%d passed=%v allFailed=%v err=%v", groupID, passed, allFailed, err)
	}
}

// groupTestAnyPassed 检查 summary 中是否有任意一条测试结果通过。
// 用于区分全部失败与部分失败（issue #119）。
func groupTestAnyPassed(summary *GroupModelTestSummary) bool {
	for _, r := range summary.Results {
		if r.Passed {
			return true
		}
	}
	return false
}

func StartGroupModelTest(group *appmodel.Group, channels map[int]appmodel.Channel) (*GroupModelTestProgress, error) {
	if group == nil {
		return nil, fmt.Errorf("group is nil")
	}
	if len(group.Items) == 0 {
		return nil, fmt.Errorf("group has no items")
	}
	if conf.IsDevMockSuccess() {
		summary, err := buildDevMockGroupTestSummary(group)
		if err != nil {
			return nil, err
		}
		progress := &GroupModelTestProgress{
			ID:        uuid.NewString(),
			Passed:    summary.Passed,
			Completed: summary.Completed,
			Total:     summary.Total,
			Done:      true,
			Results:   append([]GroupModelTestResult(nil), summary.Results...),
			Message:   "dev mock success",
		}
		storeGroupModelProgress(progress)
		recordGroupTestStatus(group.ID, summary.Passed, false)
		log.Infof("dev mock group test success: group=%s total=%d", group.Name, len(group.Items))
		return progress, nil
	}

	id := uuid.NewString()
	progress := &GroupModelTestProgress{
		ID:      id,
		Source:  "saved",
		Total:   len(group.Items),
		Results: make([]GroupModelTestResult, 0, len(group.Items)),
	}
	storeGroupModelProgress(progress)
	log.Infof("start group test: group=%s progress_id=%s items=%d channels=%d", group.Name, id, len(group.Items), len(channels))

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("group model test panic: group=%s progress_id=%s err=%v", group.Name, id, r)
				failed := cloneGroupModelProgress(progress)
				failed.Done = true
				failed.Passed = false
				failed.Message = fmt.Sprintf("internal error: %v", r)
				storeGroupModelProgress(&failed)
				recordGroupTestStatus(group.ID, false, true)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		summary, err := runGroupModelTest(ctx, group, channels, progress)
		if err != nil {
			log.Errorf("group model test failed: group=%s progress_id=%s err=%v", group.Name, id, err)
			failed := cloneGroupModelProgress(progress)
			failed.Done = true
			failed.Message = err.Error()
			storeGroupModelProgress(&failed)
			recordGroupTestStatus(group.ID, false, true)
			return
		}
		// 正常完成：summary 携带最终 Passed，回写分组测试状态（issue #113/#119）。
		allFailed := !summary.Passed && !groupTestAnyPassed(summary)
		recordGroupTestStatus(group.ID, summary.Passed, allFailed)
		log.Infof("group model test completed: group=%s progress_id=%s", group.Name, id)
	}()

	cloned := cloneGroupModelProgress(progress)
	return &cloned, nil
}

func StartDraftGroupModelTest(endpointType string, items []GroupModelDraftTestItem, channels map[int]appmodel.Channel) (*GroupModelTestProgress, error) {
	return StartDraftGroupModelTestRequest(GroupModelDraftTestRequest{
		EndpointType: endpointType,
		Items:        items,
	}, channels)
}

func StartDraftGroupModelTestRequest(request GroupModelDraftTestRequest, channels map[int]appmodel.Channel) (*GroupModelTestProgress, error) {
	if len(request.Items) == 0 {
		return nil, fmt.Errorf("group has no items")
	}
	if len(request.Items) > 64 {
		return nil, fmt.Errorf("too many draft test items: %d", len(request.Items))
	}
	snapshotJSON, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal draft snapshot: %w", err)
	}
	snapshotHash := fmt.Sprintf("%x", sha256.Sum256(snapshotJSON))
	endpointType := appmodel.NormalizeEndpointType(request.EndpointType)
	log.Infof("start draft group test: endpoint_type=%s items=%d channels=%d snapshot=%s", endpointType, len(request.Items), len(channels), snapshotHash[:12])

	paramOverride := request.ParamOverride
	if paramOverride != nil && strings.TrimSpace(*paramOverride) == "" {
		paramOverride = nil
	}
	groupName := strings.TrimSpace(request.Name)
	if groupName == "" {
		groupName = "draft-group-test"
	}
	forwardMode := strings.ToLower(strings.TrimSpace(request.ForwardMode))
	if forwardMode == "" {
		forwardMode = "inherit"
	}
	group := &appmodel.Group{
		Name:                    groupName,
		Category:                strings.TrimSpace(request.Category),
		EndpointType:            endpointType,
		EndpointProvider:        strings.TrimSpace(request.EndpointProvider),
		OutboundFormat:          strings.TrimSpace(request.OutboundFormat),
		ForwardMode:             forwardMode,
		Mode:                    request.Mode,
		FirstTokenTimeOut:       request.FirstTokenTimeOut,
		AttemptTimeOut:          request.AttemptTimeOut,
		StreamIdleTimeout:       request.StreamIdleTimeout,
		RequestTimeoutSeconds:   request.RequestTimeoutSeconds,
		StreamKeepaliveEnabled:  request.StreamKeepaliveEnabled,
		AssistantPrefillCompat:  request.AssistantPrefillCompat,
		HedgeEnabled:            request.HedgeEnabled,
		HedgeDelayMs:            request.HedgeDelayMs,
		SessionKeepTime:         request.SessionKeepTime,
		Condition:               request.Condition,
		ReasoningBufferStrategy: request.ReasoningBufferStrategy,
		DefaultReasoningEffort:  strings.TrimSpace(request.DefaultReasoningEffort),
		ReasoningForceOverride:  request.ReasoningForceOverride,
		ParamOverride:           paramOverride,
		CustomHeader:            append([]appmodel.CustomHeader(nil), request.CustomHeader...),
		RelayRetryCount:         request.RelayRetryCount,
		RelayRouteRetries:       request.RelayRouteRetries,
		Items:                   make([]appmodel.GroupItem, 0, len(request.Items)),
	}
	clientIDs := make(map[int]string, len(request.Items))

	for index, item := range request.Items {
		log.Infof("draft group test item: index=%d channel_id=%d model=%s", index, item.ChannelID, strings.TrimSpace(item.ModelName))
		itemID := item.ItemID
		if itemID <= 0 {
			itemID = index + 1
		}
		priority := item.Priority
		if priority <= 0 {
			priority = index + 1
		}
		weight := item.Weight
		if weight <= 0 {
			weight = 1
		}
		group.Items = append(group.Items, appmodel.GroupItem{
			ID:                      itemID,
			ChannelID:               item.ChannelID,
			ModelName:               strings.TrimSpace(item.ModelName),
			Priority:                priority,
			Weight:                  weight,
			RelayRetryCountOverride: item.RelayRetryCountOverride,
		})
		clientIDs[itemID] = strings.TrimSpace(item.ClientID)
	}

	id := uuid.NewString()
	progress := &GroupModelTestProgress{
		ID:           id,
		Source:       "draft",
		SnapshotHash: snapshotHash,
		Total:        len(group.Items),
		Results:      make([]GroupModelTestResult, 0, len(group.Items)),
	}
	storeGroupModelProgress(progress)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("draft group model test panic: progress_id=%s err=%v", id, r)
				failed := cloneGroupModelProgress(progress)
				failed.Done = true
				failed.Passed = false
				failed.Message = fmt.Sprintf("internal error: %v", r)
				storeGroupModelProgress(&failed)
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		summary, err := runGroupModelTest(ctx, group, channels, progress)
		if err != nil {
			log.Errorf("draft group model test failed: progress_id=%s err=%v", id, err)
			failed := cloneGroupModelProgress(progress)
			failed.Done = true
			failed.Message = err.Error()
			for i := range failed.Results {
				if clientID, ok := clientIDs[failed.Results[i].ItemID]; ok {
					failed.Results[i].ClientID = clientID
				}
				failed.Results[i].Source = "draft"
				failed.Results[i].SnapshotHash = snapshotHash
			}
			storeGroupModelProgress(&failed)
			return
		}

		final := cloneGroupModelProgress(progress)
		final.Passed = summary.Passed
		final.Completed = summary.Completed
		final.Total = summary.Total
		final.Done = true
		final.Results = append([]GroupModelTestResult(nil), summary.Results...)
		for i := range final.Results {
			if clientID, ok := clientIDs[final.Results[i].ItemID]; ok {
				final.Results[i].ClientID = clientID
			}
			final.Results[i].Source = "draft"
			final.Results[i].SnapshotHash = snapshotHash
		}
		storeGroupModelProgress(&final)
	}()

	cloned := cloneGroupModelProgress(progress)
	return &cloned, nil
}

func GetGroupModelTestProgress(id string) (*GroupModelTestProgress, bool) {
	// Redis 后端：直接读 KVStore，TTL 过期由 Redis 保证。降级（err/未找到）回退内存。
	if store.Enabled() {
		data, found, err := store.GetKV().Get(context.Background(), groupProbeKVKey(id))
		if err == nil && found {
			var p GroupModelTestProgress
			if json.Unmarshal(data, &p) == nil {
				return &p, true
			}
		}
	}

	cleanupExpiredGroupModelProgress(time.Now())

	value, ok := groupProbeProgress.Load(id)
	if !ok {
		return nil, false
	}

	entry, ok := value.(groupModelTestProgressEntry)
	if !ok {
		return nil, false
	}

	cloned := cloneGroupModelProgress(&entry.progress)
	return &cloned, true
}

func runGroupModelTest(ctx context.Context, group *appmodel.Group, channels map[int]appmodel.Channel, progress *GroupModelTestProgress) (*GroupModelTestSummary, error) {
	if group == nil {
		return nil, fmt.Errorf("group is nil")
	}
	if len(group.Items) == 0 {
		return nil, fmt.Errorf("group has no items")
	}

	summary := &GroupModelTestSummary{Total: len(group.Items), Results: make([]GroupModelTestResult, 0, len(group.Items))}
	workerCount := int(math.Min(float64(len(group.Items)), maxConcurrentGroupModelTests))
	if workerCount < 1 {
		workerCount = 1
	}

	type indexedResult struct {
		index  int
		result GroupModelTestResult
	}

	resultsByIndex := make([]GroupModelTestResult, len(group.Items))
	jobs := make(chan int)
	results := make(chan indexedResult, len(group.Items))
	var wg sync.WaitGroup

	for workerIndex := 0; workerIndex < workerCount; workerIndex++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				results <- indexedResult{index: index, result: testGroupModelItemWithGroup(ctx, group, group.Items[index], channels)}
			}
		}()
	}

	go func() {
		for index := range group.Items {
			if ctx.Err() != nil {
				break
			}
			jobs <- index
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	for indexed := range results {
		resultsByIndex[indexed.index] = indexed.result
	}

	for _, result := range resultsByIndex {
		appendGroupTestResult(summary, progress, result)
	}

	// 所有 item 处理完毕后，统一计算整体是否通过：仅当全部完成且无任何失败才算通过。
	// 之前在 appendGroupTestResult 中用 "任一通过即 true" 的增量逻辑，会导致
	// 只要有一个渠道可用就误报整体可用（issue #89）。
	summary.Passed = summary.Completed == summary.Total
	for _, r := range summary.Results {
		if !r.Passed {
			summary.Passed = false
			break
		}
	}

	if progress != nil {
		progress.Passed = summary.Passed
		finalProgress := cloneGroupModelProgress(progress)
		finalProgress.Done = true
		storeGroupModelProgress(&finalProgress)
	}

	return summary, nil
}

func testGroupModelItem(ctx context.Context, endpointType string, item appmodel.GroupItem, channels map[int]appmodel.Channel) GroupModelTestResult {
	return testGroupModelItemWithGroup(ctx, &appmodel.Group{EndpointType: endpointType}, item, channels)
}

func testGroupModelItemWithGroup(ctx context.Context, group *appmodel.Group, item appmodel.GroupItem, channels map[int]appmodel.Channel) GroupModelTestResult {
	result := GroupModelTestResult{
		ItemID:    item.ID,
		ChannelID: item.ChannelID,
		ModelName: item.ModelName,
		Attempts:  1,
		Source:    "saved",
	}
	endpointType := appmodel.EndpointTypeChat
	if group != nil {
		endpointType = appmodel.NormalizeEndpointType(group.EndpointType)
	}

	channel, ok := channels[item.ChannelID]
	if !ok {
		result.Message = "channel not found"
		recordTestLog(ctx, endpointType, item, result, channel, nil, 0, nil, nil)
		return result
	}
	result.ChannelName = channel.Name
	if !channel.Enabled {
		result.Message = "channel disabled"
		recordTestLog(ctx, endpointType, item, result, channel, nil, 0, nil, nil)
		return result
	}
	if channel.SkipModelTest {
		// issue #98：部分上游渠道会因低字节请求扣额度/封禁，允许按渠道排除可用性测试。
		result.Message = "channel skipped model test (issue #98)"
		result.Passed = false
		recordTestLog(ctx, endpointType, item, result, channel, nil, 0, nil, nil)
		return result
	}

	usedKey := channel.GetChannelKeyWithCooldown(item.ModelName, 300)
	if strings.TrimSpace(usedKey.ChannelKey) == "" {
		result.Message = "no available key"
		recordTestLog(ctx, endpointType, item, result, channel, nil, 0, nil, nil)
		return result
	}

	if endpointType == appmodel.EndpointTypeRerank {
		return testRerankGroupItem(ctx, group, item, &channel, usedKey, result)
	}
	if outbound.Get(channel.Type) == nil {
		result.Message = fmt.Sprintf("unsupported channel type: %d", channel.Type)
		recordTestLog(ctx, endpointType, item, result, channel, nil, 0, nil, nil)
		return result
	}

	if err := validateGroupProbeChannelType(endpointType, channel.Type); err != nil && channel.ConnectionConfig == nil {
		result.Message = err.Error()
		recordTestLog(ctx, endpointType, item, result, channel, nil, 0, nil, nil)
		return result
	}

	// 构建探测请求，用于日志记录和出站 adapter 回退解析
	probeRequest, probeErr := buildGroupProbeRequest(endpointType, item.ModelName)
	if probeErr == nil && group != nil {
		probeRequest = applyGroupProbeSettings(group, probeRequest)
	}
	var requestJSON []byte
	if probeErr == nil && probeRequest != nil {
		requestJSON, _ = json.Marshal(probeRequest)
	}

	// 解析出站 adapter 回退列表。对 OpenAI 类型渠道，默认先尝试 Chat
	// Completions (/v1/chat/completions)，失败再回退 Responses API
	// (/v1/responses)。这与 relay 主流程的 outboundAttemptTypes 一致，
	// 避免 type=1 渠道在只支持 chat completions 的上游上探测失败（issue #187）。
	// 渠道级协议覆盖与主流程同规则：覆盖非空时禁用回退，探测与转发一致。
	groupFormat := ""
	if group != nil {
		groupFormat = group.OutboundFormat
	}
	plans := channel.ResolveConnectionPlans(probeRequest, groupFormat)
	if len(plans) > 1 {
		plans = plans[:1]
	}

	startTime := time.Now()
	var logAttempts []appmodel.ChannelAttempt
	var lastInternalResp *transmodel.InternalLLMResponse

	attemptNum := 0
	probeCtx, probeCancel := context.WithTimeout(ctx, 25*time.Second)
	defer probeCancel()
	for _, plan := range plans {
		adapterType := plan.AdapterType
		outAdapter := plan.Adapter()
		if outAdapter == nil {
			continue
		}

		for attempt := 1; attempt <= 1; attempt++ {
			attemptNum++
			if attemptNum > 1 && probeCtx.Err() != nil {
				result.Message = probeCtx.Err().Error()
				break
			}
			attemptStart := time.Now()
			statusCode, responseText, internalResp, err := sendGroupProbeRequestWithGroup(probeCtx, outAdapter, adapterType, channel.WithConnectionPlan(plan), usedKey.ChannelKey, endpointType, item.ModelName, group)
			attemptDuration := int(time.Since(attemptStart).Milliseconds())

			result.Protocol = probeProtocolForAdapter(adapterType)
			if plan.Endpoint != nil {
				result.EndpointID = plan.Endpoint.ID
				result.Protocol = plan.Endpoint.Protocol
			}
			result.StatusCode = statusCode
			result.ResponseText = responseText
			if internalResp != nil {
				lastInternalResp = internalResp
			}

			attemptStatus := appmodel.AttemptFailed
			attemptMsg := result.Message
			if err == nil {
				attemptStatus = appmodel.AttemptSuccess
				attemptMsg = "ok"
			} else {
				attemptMsg = err.Error()
			}

			logAttempts = append(logAttempts, appmodel.ChannelAttempt{
				ChannelID:   channel.ID,
				ChannelName: channel.Name,
				ModelName:   item.ModelName,
				AttemptNum:  attemptNum,
				Status:      attemptStatus,
				Duration:    attemptDuration,
				Msg:         attemptMsg,
			})

			if err == nil {
				result.Passed = true
				result.Attempts = attemptNum
				result.Message = "ok"
				break
			}
			result.Attempts = attemptNum
			result.Message = err.Error()
		}
	}

	useTimeMs := int(time.Since(startTime).Milliseconds())
	recordTestLog(ctx, endpointType, item, result, channel, logAttempts, useTimeMs, requestJSON, lastInternalResp)

	return result
}

// recordTestLog 将分组模型测试结果记录到日志系统（issue #82：测试模型可显示日志）。
// 测试日志以 is_test=true 标记，与正常转发日志区分；RequestAPIKeyName 设为 "[test]"。
// issue #90：从上游响应解析 usage 并回填 InputTokens/OutputTokens/CacheReadTokens/Cost，
// 使测试日志的输入/输出/费用不再显示为「未知」。
func recordTestLog(ctx context.Context, endpointType string, item appmodel.GroupItem, result GroupModelTestResult, channel appmodel.Channel, attempts []appmodel.ChannelAttempt, useTimeMs int, requestJSON []byte, internalResp *transmodel.InternalLLMResponse) {
	normalizedEndpointType := appmodel.NormalizeEndpointType(endpointType)

	channelID := channel.ID
	channelName := channel.Name
	// 渠道未找到时，使用 item 中的渠道 ID
	if channelID == 0 {
		channelID = item.ChannelID
	}

	relayLog := appmodel.RelayLog{
		Time:              time.Now().Unix(),
		RequestModelName:  item.ModelName,
		RequestAPIKeyName: "[test]",
		ClientIP:          "system",
		EndpointType:      normalizedEndpointType,
		ChannelId:         channelID,
		ChannelName:       channelName,
		ActualModelName:   item.ModelName,
		UseTime:           useTimeMs,
		Attempts:          attempts,
		TotalAttempts:     len(attempts),
		IsTest:            true,
	}

	if requestJSON != nil {
		relayLog.RequestContent = string(requestJSON)
	}
	if result.ResponseText != "" {
		relayLog.ResponseContent = result.ResponseText
	}
	if !result.Passed {
		relayLog.Error = result.Message
	}
	if normalizedEndpointType == appmodel.EndpointTypeRerank {
		relayLog.UsageState = appmodel.RelayLogUsageNotReported
		if !result.Passed {
			relayLog.UsageState = appmodel.RelayLogUsageFailedNoResponse
		}
		if input, output, reported := ParseRerankTokenUsage([]byte(result.ResponseText)); reported {
			relayLog.InputTokens, relayLog.OutputTokens = input, output
			relayLog.UsageState = appmodel.RelayLogUsageReported
		}
	}

	// Usage / Cost：复用正常转发流程的计算口径（见 relay/metrics.go SetInternalResponse）。
	// 测试请求不累加到全站统计，只落日志，因此这里仅填充日志字段，不调用 stats.*Update。
	if internalResp != nil && internalResp.Usage != nil {
		usage := internalResp.Usage
		relayLog.InputTokens = int(usage.PromptTokens)
		relayLog.OutputTokens = int(usage.CompletionTokens)

		modelPrice := price.GetLLMPrice(item.ModelName)
		if modelPrice != nil {
			if usage.PromptTokensDetails == nil {
				usage.PromptTokensDetails = &transmodel.PromptTokensDetails{
					CachedTokens: 0,
				}
			}
			var inputCost float64
			if usage.AnthropicUsage {
				inputCost = (float64(usage.PromptTokensDetails.CachedTokens)*modelPrice.CacheRead +
					float64(usage.PromptTokens)*modelPrice.Input +
					float64(usage.CacheCreationInputTokens)*modelPrice.CacheWrite) * 1e-6
			} else {
				inputCost = (float64(usage.PromptTokensDetails.CachedTokens)*modelPrice.CacheRead +
					float64(usage.PromptTokens-usage.PromptTokensDetails.CachedTokens)*modelPrice.Input) * 1e-6
			}
			outputCost := float64(usage.CompletionTokens) * modelPrice.Output * 1e-6
			relayLog.Cost = inputCost + outputCost
		}

		// 提供方提示缓存命中 Token（与正常日志一致，从响应内容解析）。
		if !relayLog.SemanticCacheHit {
			relayLog.CacheReadTokens = opRelayLogCacheReadTokens(relayLog.ResponseContent)
		}
	}

	if _, logErr := relaylog.RelayLogAdd(ctx, relayLog); logErr != nil {
		log.Warnf("failed to save test log: %v", logErr)
	}
}

// opRelayLogCacheReadTokens 与 internal/relay/metrics.go 中的同名实现一致，
// 从响应内容解析提供方提示缓存命中 Token。helper 包不能 import internal/relay（会循环），
// 故在此内联一份等价实现。
func opRelayLogCacheReadTokens(responseContent string) int {
	signals, ok := cacheusage.ParseProviderPromptCacheUsageSignals(responseContent)
	if !ok || signals.SemanticCacheHit || signals.CachedTokens <= 0 {
		return 0
	}
	return int(signals.CachedTokens)
}

func appendGroupTestResult(summary *GroupModelTestSummary, progress *GroupModelTestProgress, result GroupModelTestResult) {
	if progress != nil {
		result.Source = progress.Source
		result.SnapshotHash = progress.SnapshotHash
	}
	log.Infof("group test result: item_id=%d channel_id=%d model=%s passed=%t status=%d message=%s", result.ItemID, result.ChannelID, result.ModelName, result.Passed, result.StatusCode, result.Message)
	summary.Results = append(summary.Results, result)
	summary.Completed = len(summary.Results)
	// summary.Passed 的最终值在 runGroupModelTest 末尾统一计算，
	// 这里不再增量设置，避免 "任一通过即 true" 的误报语义（issue #89）。

	if progress == nil {
		return
	}

	// 同步累积 progress.Results，确保中间与最终 store 的进度都包含全部已完成结果。
	// 之前只 clone 后 append 当前单个 result，导致 progress.Results 始终为空，
	// 最终 done=true 但 results=[]，前端误报 "均可用"（issue #89）。
	progress.Results = append(progress.Results, result)
	progress.Completed = len(progress.Results)

	next := cloneGroupModelProgress(progress)
	storeGroupModelProgress(&next)
}

func storeGroupModelProgress(progress *GroupModelTestProgress) {
	storeGroupModelProgressAt(progress, time.Now())
}

func storeGroupModelProgressAt(progress *GroupModelTestProgress, now time.Time) {
	if progress == nil || progress.ID == "" {
		return
	}

	// Redis 后端：序列化 progress 写入 KVStore，TTL 原生过期，无需维护内存 map 与主动清理。
	// 降级（序列化/写入失败）回退内存路径，保证探测进度不丢（容错）。
	if store.Enabled() {
		if data, err := json.Marshal(progress); err == nil {
			if err := store.GetKV().Set(context.Background(),
				groupProbeKVKey(progress.ID), data, groupProbeProgressTTL); err == nil {
				return
			}
		}
	}

	cleanupExpiredGroupModelProgress(now)
	groupProbeProgress.Store(progress.ID, groupModelTestProgressEntry{
		progress:  cloneGroupModelProgress(progress),
		expiresAt: now.Add(groupProbeProgressTTL),
	})
}

func cleanupExpiredGroupModelProgress(now time.Time) {
	// Redis 模式下 TTL 自动过期，无需主动清理。
	if store.Enabled() {
		return
	}
	groupProbeProgress.Range(func(key, value any) bool {
		entry, ok := value.(groupModelTestProgressEntry)
		if !ok || (!entry.expiresAt.IsZero() && !now.Before(entry.expiresAt)) {
			groupProbeProgress.Delete(key)
		}
		return true
	})
}

func cloneGroupModelProgress(progress *GroupModelTestProgress) GroupModelTestProgress {
	if progress == nil {
		return GroupModelTestProgress{}
	}

	cloned := *progress
	if progress.Results != nil {
		cloned.Results = append([]GroupModelTestResult(nil), progress.Results...)
	}
	return cloned
}

// sendGroupProbeRequest 用给定 adapter 发一次探测请求。adapterType 只用于挑选
// base URL：多协议渠道可以把不同协议绑到不同地址，探测必须与真实转发用同一套
// 地址选择规则，否则会出现「探测失败但转发正常」的假阴性。
func sendGroupProbeRequest(ctx context.Context, outAdapter transmodel.Outbound, adapterType outbound.OutboundType, channel *appmodel.Channel, key, endpointType, modelName string) (int, string, *transmodel.InternalLLMResponse, error) {
	return sendGroupProbeRequestWithGroup(ctx, outAdapter, adapterType, channel, key, endpointType, modelName, nil)
}

func sendGroupProbeRequestWithGroup(ctx context.Context, outAdapter transmodel.Outbound, adapterType outbound.OutboundType, channel *appmodel.Channel, key, endpointType, modelName string, group *appmodel.Group) (int, string, *transmodel.InternalLLMResponse, error) {
	if channel == nil {
		return 0, "", nil, fmt.Errorf("channel is nil")
	}
	if channel.SkipModelTest {
		return 0, "", nil, errModelTestSkipped
	}

	httpClient, err := ChannelHttpClient(channel)
	if err != nil {
		return 0, "", nil, err
	}

	probeRequest, err := buildGroupProbeRequest(endpointType, modelName)
	if err != nil {
		return 0, "", nil, err
	}
	if group != nil {
		probeRequest = applyGroupProbeSettings(group, probeRequest)
	}

	// 地址按本次探测实际使用的 adapter 协议挑选：多协议渠道可以把不同协议
	// 绑到不同 base URL，探测与真实转发保持同一套地址选择规则。
	plan, err := channel.ConnectionPlanFor(adapterType)
	if err != nil {
		return 0, "", nil, err
	}
	if plan.Endpoint != nil {
		outAdapter = plan.Adapter()
	}
	probeRequest, err = prepareConnectionProbeRequest(ctx, channel, plan, probeRequest, key)
	if err != nil {
		return 0, "", nil, err
	}
	req, err := channel.BuildConnectionRequest(ctx, plan, outAdapter, probeRequest, key)
	if err != nil {
		return 0, "", nil, err
	}

	if group != nil {
		for _, header := range group.CustomHeader {
			if strings.TrimSpace(header.HeaderKey) != "" {
				req.Header.Set(header.HeaderKey, header.HeaderValue)
			}
		}
	}
	channel.ApplyConnectionHeaders(req, plan, key)

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()

	// Parse the complete bounded response. A display-preview limit truncates
	// ordinary embedding vectors and turns a healthy endpoint into a false failure.
	body, err := transformer.ReadResponseBody(resp)
	if err != nil {
		return resp.StatusCode, "", nil, err
	}
	bodyText := strings.TrimSpace(string(body))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if bodyText == "" {
			bodyText = resp.Status
		}
		return resp.StatusCode, bodyText, nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
	}

	internalResp, err := outAdapter.TransformResponse(ctx, &http.Response{
		Status:        resp.Status,
		StatusCode:    resp.StatusCode,
		Header:        resp.Header.Clone(),
		Body:          io.NopCloser(strings.NewReader(bodyText)),
		ContentLength: int64(len(bodyText)),
	})
	if err != nil {
		return resp.StatusCode, bodyText, nil, err
	}

	return resp.StatusCode, bodyText, internalResp, nil
}

func buildGroupProbeRequest(endpointType, modelName string) (*transmodel.InternalLLMRequest, error) {
	stream := false
	normalizedEndpointType := normalizeGroupProbeEndpointType(endpointType)

	switch {
	case normalizedEndpointType == appmodel.EndpointTypeEmbeddings:
		return &transmodel.InternalLLMRequest{
			Model:          modelName,
			EmbeddingInput: &transmodel.EmbeddingInput{Single: stringPtr("hi")},
		}, nil
	case normalizedEndpointType == appmodel.EndpointTypeAll || appmodel.IsConversationEndpointType(normalizedEndpointType):
		return &transmodel.InternalLLMRequest{
			Model:        modelName,
			RawAPIFormat: transmodel.APIFormatOpenAIChatCompletion,
			Messages: []transmodel.Message{{
				Role: "user",
				Content: transmodel.MessageContent{
					Content: stringPtr("hi"),
				},
			}},
			Stream: &stream,
		}, nil
	default:
		return nil, fmt.Errorf("group probe does not support endpoint type: %s", normalizedEndpointType)
	}
}

func applyGroupProbeSettings(group *appmodel.Group, request *transmodel.InternalLLMRequest) *transmodel.InternalLLMRequest {
	if group == nil || request == nil {
		return request
	}
	cloned := transmodel.CloneForAttempt(request)
	if cloned == nil {
		return request
	}

	provider := strings.ToLower(strings.TrimSpace(group.EndpointProvider))
	if provider == "" || provider == "auto" {
		provider = appmodel.NormalizeEndpointType(group.EndpointType)
	}
	if provider != "" {
		if cloned.TransformerMetadata == nil {
			cloned.TransformerMetadata = make(map[string]string)
		}
		cloned.TransformerMetadata[transmodel.TransformerMetadataGroupEndpointType] = provider
	}

	if effort := strings.ToLower(strings.TrimSpace(group.DefaultReasoningEffort)); effort != "" && effort != "auto" {
		if transmodel.ReasoningIntentOf(cloned) == transmodel.ReasoningIntentUnspecified {
			cloned.ReasoningEffort = effort
		}
	}

	if group.ParamOverride == nil || strings.TrimSpace(*group.ParamOverride) == "" {
		return cloned
	}
	var overrides map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*group.ParamOverride), &overrides); err != nil {
		log.Warnf("draft group probe: invalid param_override: %v", err)
		return cloned
	}
	if cloned.MaxTokens == nil {
		if raw, ok := overrides["max_tokens"]; ok {
			var value int64
			if json.Unmarshal(raw, &value) == nil {
				cloned.MaxTokens = &value
			}
		}
	}
	if cloned.MaxCompletionTokens == nil {
		if raw, ok := overrides["max_completion_tokens"]; ok {
			var value int64
			if json.Unmarshal(raw, &value) == nil {
				cloned.MaxCompletionTokens = &value
			}
		}
	}
	if cloned.Temperature == nil {
		if raw, ok := overrides["temperature"]; ok {
			var value float64
			if json.Unmarshal(raw, &value) == nil {
				cloned.Temperature = &value
			}
		}
	}
	if cloned.TopP == nil {
		if raw, ok := overrides["top_p"]; ok {
			var value float64
			if json.Unmarshal(raw, &value) == nil {
				cloned.TopP = &value
			}
		}
	}
	return cloned
}

func validateGroupProbeChannelType(endpointType string, channelType outbound.OutboundType) error {
	normalizedEndpointType := normalizeGroupProbeEndpointType(endpointType)

	switch normalizedEndpointType {
	case appmodel.EndpointTypeEmbeddings:
		if !outbound.IsEmbeddingChannelType(channelType) {
			return fmt.Errorf("channel type %d does not support endpoint type %s", channelType, appmodel.EndpointTypeEmbeddings)
		}
		return nil
	case appmodel.EndpointTypeAll:
		if !outbound.IsChatChannelType(channelType) {
			return fmt.Errorf("channel type %d does not support endpoint type %s", channelType, appmodel.EndpointTypeAll)
		}
		return nil
	default:
		if appmodel.IsConversationEndpointType(normalizedEndpointType) {
			if !outbound.IsChatChannelType(channelType) {
				return fmt.Errorf("channel type %d does not support endpoint type %s", channelType, normalizedEndpointType)
			}
			return nil
		}
		return fmt.Errorf("group probe does not support endpoint type: %s", normalizedEndpointType)
	}
}

func normalizeGroupProbeEndpointType(endpointType string) string {
	return appmodel.NormalizeEndpointType(endpointType)
}

func stringPtr(value string) *string {
	return &value
}

func buildDevMockGroupTestSummary(group *appmodel.Group) (*GroupModelTestSummary, error) {
	if group == nil {
		return nil, fmt.Errorf("group is nil")
	}
	if len(group.Items) == 0 {
		return nil, fmt.Errorf("group has no items")
	}

	results := make([]GroupModelTestResult, 0, len(group.Items))
	for _, item := range group.Items {
		results = append(results, GroupModelTestResult{
			ItemID:       item.ID,
			ChannelID:    item.ChannelID,
			ChannelName:  "dev-mock-channel",
			ModelName:    item.ModelName,
			Passed:       true,
			Attempts:     1,
			StatusCode:   http.StatusOK,
			ResponseText: devMockText,
			Message:      "ok",
		})
	}
	return &GroupModelTestSummary{
		Passed:    true,
		Completed: len(results),
		Total:     len(results),
		Results:   results,
	}, nil
}
