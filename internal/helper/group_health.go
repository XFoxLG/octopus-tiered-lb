package helper

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	appmodel "github.com/lingyuins/octopus/internal/model"
	ch "github.com/lingyuins/octopus/internal/op/channel"
	grp "github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"gorm.io/gorm"
)

// ErrGroupHealthAlreadyRunning 该分组已有未完成的健康检查。
var ErrGroupHealthAlreadyRunning = errors.New("group health check already running")

// groupHealthRunLocks 按分组互斥检查执行。
var groupHealthRunLocks sync.Map

func lockGroupHealth(groupID int) func() {
	value, _ := groupHealthRunLocks.LoadOrStore(groupID, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return func() { lock.Unlock() }
}

// GroupHealthProbeResult 单候选拨测结果。
type GroupHealthProbeResult struct {
	Success      bool
	HTTPStatus   int
	DurationMS   int64
	ErrorMessage string
}

// RunGroupHealthCandidate 对单个候选发起一次真实拨测（12s 独立超时）。
// 复用 sendGroupProbeRequest（outbound adapter + custom header），与分组测试同源。
func RunGroupHealthCandidate(ctx context.Context, channel *appmodel.Channel, usedKey appmodel.ChannelKey, modelName, endpointType string) GroupHealthProbeResult {
	startedAt := time.Now()
	result := GroupHealthProbeResult{}

	adapterTypes := candidateAdapterTypes(channel, modelName, endpointType)
	if len(adapterTypes) == 0 {
		result.ErrorMessage = fmt.Sprintf("unsupported channel type: %d", channel.Type)
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}
	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	statusCode, responseText, _, err := sendGroupProbeRequest(probeCtx, outbound.Get(adapterTypes[0]), channel, strings.TrimSpace(usedKey.ChannelKey), endpointType, modelName)
	result.HTTPStatus = statusCode
	result.DurationMS = time.Since(startedAt).Milliseconds()
	if err != nil {
		if strings.TrimSpace(responseText) != "" {
			result.ErrorMessage = truncateSyncProbeErr(strings.TrimSpace(responseText))
		} else {
			result.ErrorMessage = truncateSyncProbeErr(err.Error())
		}
		return result
	}
	result.Success = true
	return result
}

func candidateAdapterTypes(channel *appmodel.Channel, modelName, endpointType string) []outbound.OutboundType {
	if channel == nil || outbound.Get(channel.Type) == nil {
		return nil
	}
	endpoint := strings.TrimSpace(endpointType)
	if endpoint == "" {
		endpoint = appmodel.EndpointTypeChat
	}
	probeRequest, err := buildGroupProbeRequest(endpoint, modelName)
	if err != nil {
		return nil
	}
	return outbound.ResolveAttemptTypes(channel.Type, probeRequest, "")
}

// resolveGroupHealthProbeMode 解析拨测模式（仅首个 full 生效，其余回 standard）。
func resolveGroupHealthProbeMode(probeModes []appmodel.GroupHealthProbeMode) appmodel.GroupHealthProbeMode {
	if len(probeModes) > 0 && probeModes[0] == appmodel.GroupHealthProbeModeFull {
		return appmodel.GroupHealthProbeModeFull
	}
	return appmodel.GroupHealthProbeModeStandard
}

// RunGroupHealth 对分组按优先级逐个候选拨测并落快照（Seller 移植）。
// failover 分组 standard 模式遇首个成功即停（余下记 skipped）；full 模式测完所有候选。
// running 是无出边兜底的状态，defer 保证中断时总有终态。
func RunGroupHealth(ctx context.Context, groupID int, probeModes ...appmodel.GroupHealthProbeMode) error {
	unlock := lockGroupHealth(groupID)
	defer unlock()

	repo := grp.NewGroupHealthRepository()
	if _, err := repo.GetRunningSnapshotByGroupID(ctx, groupID); err == nil {
		return ErrGroupHealthAlreadyRunning
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	group, ok := grp.GetCache().Get(groupID)
	if !ok {
		return fmt.Errorf("group not found")
	}

	probeMode := resolveGroupHealthProbeMode(probeModes)
	snapshot, err := repo.CreateRunningSnapshot(ctx, group, probeMode)
	if err != nil {
		return err
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		finishCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = repo.FinishSnapshot(finishCtx, snapshot.ID, appmodel.GroupHealthStatusFailed, nil,
			time.Since(snapshot.StartedAt).Milliseconds(),
			"interrupted: check did not complete", time.Now())
	}()

	items := append([]appmodel.GroupItem(nil), group.Items...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		if items[i].Weight != items[j].Weight {
			return items[i].Weight > items[j].Weight
		}
		if items[i].ChannelID != items[j].ChannelID {
			return items[i].ChannelID < items[j].ChannelID
		}
		return items[i].ID < items[j].ID
	})

	var successfulChannelID *int
	stopAfterSuccess := group.Mode == appmodel.GroupModeFailover && probeMode != appmodel.GroupHealthProbeModeFull
	successFound := false
	firstSuccessIndex := -1
	attemptedCount := 0
	successCount := 0

	for index, item := range items {
		channel, err := ch.Get(item.ChannelID, ctx)
		if err != nil {
			attemptedCount++
			if appendErr := repo.AppendAttempt(ctx, snapshot.ID, appmodel.GroupHealthAttempt{
				GroupItemID: item.ID, ChannelID: item.ChannelID,
				ChannelName:  fmt.Sprintf("channel-%d", item.ChannelID),
				ModelName:    item.ModelName,
				Priority:     item.Priority, Weight: item.Weight,
				Status:       appmodel.GroupHealthAttemptStatusFailed,
				ErrorMessage: fmt.Sprintf("failed to load channel: %v", err),
			}); appendErr != nil {
				return appendErr
			}
			continue
		}

		usedKey := channel.GetChannelKey()
		if usedKey.ID == 0 || strings.TrimSpace(usedKey.ChannelKey) == "" {
			attemptedCount++
			if appendErr := repo.AppendAttempt(ctx, snapshot.ID, appmodel.GroupHealthAttempt{
				GroupItemID: item.ID, ChannelID: item.ChannelID,
				ChannelName:  channel.Name,
				ModelName:    item.ModelName,
				Priority:     item.Priority, Weight: item.Weight,
				Status:       appmodel.GroupHealthAttemptStatusFailed,
				ErrorMessage: "no available key",
			}); appendErr != nil {
				return appendErr
			}
			continue
		}

		result := RunGroupHealthCandidate(ctx, channel, usedKey, item.ModelName, group.EndpointType)
		attemptedCount++
		attempt := appmodel.GroupHealthAttempt{
			GroupItemID: item.ID, ChannelID: item.ChannelID,
			ChannelName:  channel.Name,
			ChannelKeyID: usedKey.ID, KeyRemark: usedKey.Remark,
			ModelName:    item.ModelName,
			Priority:     item.Priority, Weight: item.Weight,
			HTTPStatus:   result.HTTPStatus, DurationMS: result.DurationMS,
			ErrorMessage: result.ErrorMessage,
			Status:       appmodel.GroupHealthAttemptStatusFailed,
		}
		if result.Success {
			attempt.Status = appmodel.GroupHealthAttemptStatusSuccess
		}
		if err := repo.AppendAttempt(ctx, snapshot.ID, attempt); err != nil {
			return err
		}

		if result.Success {
			successFound = true
			successCount++
			if firstSuccessIndex == -1 {
				firstSuccessIndex = index
				successfulChannelID = &item.ChannelID
			}
			if stopAfterSuccess {
				for _, skipped := range items[index+1:] {
					skippedName := fmt.Sprintf("channel-%d", skipped.ChannelID)
					if skippedChannel, getErr := ch.Get(skipped.ChannelID, ctx); getErr == nil {
						skippedName = skippedChannel.Name
					}
					if err := repo.AppendAttempt(ctx, snapshot.ID, appmodel.GroupHealthAttempt{
						GroupItemID: skipped.ID, ChannelID: skipped.ChannelID,
						ChannelName: skippedName,
						ModelName:   skipped.ModelName,
						Priority:    skipped.Priority, Weight: skipped.Weight,
						Status:      appmodel.GroupHealthAttemptStatusSkipped,
					}); err != nil {
						return err
					}
				}
				break
			}
		}
	}

	finalStatus, message := resolveGroupHealthFinalStatus(group.Mode, stopAfterSuccess, items, successFound, firstSuccessIndex, attemptedCount, successCount)

	finishedAt := time.Now()
	if err := repo.FinishSnapshot(ctx, snapshot.ID, finalStatus, successfulChannelID, finishedAt.Sub(snapshot.StartedAt).Milliseconds(), message, finishedAt); err != nil {
		return err
	}
	finished = true
	return nil
}

// RunAllGroupHealth 对全部分组并发执行健康检查（默认并发度 2）。
func RunAllGroupHealth(ctx context.Context, maxConcurrency int, probeModes ...appmodel.GroupHealthProbeMode) {
	if maxConcurrency <= 0 {
		maxConcurrency = 2
	}
	probeMode := resolveGroupHealthProbeMode(probeModes)
	groups, err := grp.GroupList(ctx)
	if err != nil {
		return
	}
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	for _, group := range groups {
		groupID := group.ID
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			_ = RunGroupHealth(ctx, groupID, probeMode)
		}()
	}
	wg.Wait()
}

// resolveGroupHealthFinalStatus 按拨测结果映射快照终态（纯函数，可单测）。
func resolveGroupHealthFinalStatus(groupMode appmodel.GroupMode, stopAfterSuccess bool, items []appmodel.GroupItem, successFound bool, firstSuccessIndex, attemptedCount, successCount int) (appmodel.GroupHealthStatus, string) {
	if !successFound {
		if len(items) == 0 {
			return appmodel.GroupHealthStatusFailed, "group has no items"
		}
		return appmodel.GroupHealthStatusFailed, "all candidates failed"
	}
	successName := fmt.Sprintf("candidate %d", items[firstSuccessIndex].ChannelID)
	switch {
	case stopAfterSuccess && firstSuccessIndex == 0:
		return appmodel.GroupHealthStatusSuccess, fmt.Sprintf("candidate %s succeeded", successName)
	case stopAfterSuccess:
		return appmodel.GroupHealthStatusPartial, fmt.Sprintf("candidate %s succeeded after failover", successName)
	case successCount == attemptedCount:
		return appmodel.GroupHealthStatusSuccess, fmt.Sprintf("all %d candidates succeeded", successCount)
	default:
		_ = groupMode
		return appmodel.GroupHealthStatusPartial, fmt.Sprintf("%d/%d candidates succeeded", successCount, attemptedCount)
	}
}
