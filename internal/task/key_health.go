package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/helper"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/notification"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/utils/log"
)

const TaskKeyHealthCheck = "key_health_check"

// keyHealthState 记录某渠道的定时 Key 巡检运行态（内存，重启重置）。
type keyHealthState struct {
	mu               sync.Mutex
	consecutiveFails int       // 连续失败次数
	lastNotifyAt     time.Time // 上次发送失败通知时间（冷却去重）
	notifiedFailed   bool      // 是否已通知过失败（恢复时据此决定是否发恢复通知）
}

// globalKeyHealthState 全局 Key 巡检状态存储，key: channelID。
var globalKeyHealthState sync.Map // int -> *keyHealthState

func getKeyHealthState(channelID int) *keyHealthState {
	v, ok := globalKeyHealthState.Load(channelID)
	if ok {
		return v.(*keyHealthState)
	}
	entry := &keyHealthState{}
	actual, _ := globalKeyHealthState.LoadOrStore(channelID, entry)
	return actual.(*keyHealthState)
}

// RemoveChannelKeyHealthState 删除指定渠道的 Key 巡检状态。
// 在渠道被删除时调用，注册于 OnChannelDeletedHooks。
func RemoveChannelKeyHealthState(channelID int) {
	globalKeyHealthState.Delete(channelID)
}

// CheckKeyHealth 执行一次定时 Key 可用性巡检（issue #142）。
// 遍历所有 enabled 渠道（跳过 SkipModelTest），调用 helper.TestChannel 探测
// base_url×key 连通性，更新 Channel 的 KeyHealth 状态字段，并在连续失败达到
// 阈值时发送通知（冷却去重）；从失败恢复时发送恢复通知。
func CheckKeyHealth() {
	enabled, err := setting.GetBool(model.SettingKeyKeyHealthCheckEnabled)
	if err != nil || !enabled {
		return
	}

	failThreshold, err := setting.GetInt(model.SettingKeyKeyHealthCheckFailThreshold)
	if err != nil || failThreshold < 1 {
		failThreshold = 3
	}
	notifyEnabled, err := setting.GetBool(model.SettingKeyKeyHealthCheckNotifyEnabled)
	if err != nil {
		notifyEnabled = true
	}
	recoveryNotify, err := setting.GetBool(model.SettingKeyKeyHealthCheckRecoveryNotify)
	if err != nil {
		recoveryNotify = true
	}
	cooldownSec, err := setting.GetInt(model.SettingKeyKeyHealthCheckNotifyCooldown)
	if err != nil || cooldownSec < 1 {
		cooldownSec = 300
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	channels, err := channel.List(ctx)
	if err != nil {
		log.Warnf("key_health: failed to list channels: %v", err)
		return
	}

	for i := range channels {
		ch := &channels[i]
		if !ch.Enabled || ch.SkipModelTest {
			continue
		}
		// 只检查有可用 key 的渠道
		hasUsableKey := false
		for _, k := range ch.Keys {
			if k.Enabled && strings.TrimSpace(k.ChannelKey) != "" {
				hasUsableKey = true
				break
			}
		}
		if !hasUsableKey {
			continue
		}

		summary, testErr := helper.TestChannel(ctx, *ch)
		now := time.Now()

		if testErr != nil {
			// 探测本身出错（网络/配置），视为失败
			updateChannelKeyHealth(ch.ID, false, true, now.Unix())
			handleKeyHealthFailure(ctx, ch, failThreshold, notifyEnabled, cooldownSec, now, testErr.Error())
			continue
		}

		if summary.Passed {
			// 至少一个 key 可用
			updateChannelKeyHealth(ch.ID, true, false, now.Unix())
			handleKeyHealthRecovery(ctx, ch, recoveryNotify, now)
		} else {
			// 全部 key 失败
			updateChannelKeyHealth(ch.ID, false, true, now.Unix())
			detail := buildKeyHealthFailureDetail(summary)
			handleKeyHealthFailure(ctx, ch, failThreshold, notifyEnabled, cooldownSec, now, detail)
		}
	}
}

// updateChannelKeyHealth 更新渠道的 KeyHealth 状态字段并写回缓存+DB。
func updateChannelKeyHealth(channelID int, passed bool, allFailed bool, at int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch, err := channel.Get(channelID, ctx)
	if err != nil {
		log.Warnf("key_health: failed to get channel %d for status update: %v", channelID, err)
		return
	}

	ch.KeyHealthPassed = &passed
	ch.KeyHealthAllFailed = &allFailed
	ch.KeyHealthAt = at

	// 更新缓存（与 channel.EnableChannel 等同模式）
	channel.GetCache().Set(channelID, *ch)

	// 更新 DB（只更新 3 个字段）
	conn := db.GetDB()
	if conn != nil {
		conn.Model(&model.Channel{}).Where("id = ?", channelID).Updates(map[string]any{
			"key_health_passed":     passed,
			"key_health_all_failed": allFailed,
			"key_health_at":         at,
		})
	}
}

// handleKeyHealthFailure 处理巡检失败：递增连续失败计数，达到阈值且通过冷却时发通知。
func handleKeyHealthFailure(ctx context.Context, ch *model.Channel, failThreshold int, notifyEnabled bool, cooldownSec int, now time.Time, detail string) {
	state := getKeyHealthState(ch.ID)
	state.mu.Lock()
	state.consecutiveFails++
	fails := state.consecutiveFails
	shouldNotify := notifyEnabled && fails >= failThreshold
	if shouldNotify {
		if !state.lastNotifyAt.IsZero() && now.Sub(state.lastNotifyAt) < time.Duration(cooldownSec)*time.Second {
			shouldNotify = false
		}
	}
	if shouldNotify {
		state.lastNotifyAt = now
		state.notifiedFailed = true
	}
	state.mu.Unlock()

	if shouldNotify {
		if ctx == nil {
			ctx = context.Background()
		}
		sendKeyHealthNotification(ctx, ch, false, fails, detail)
	}
}

// handleKeyHealthRecovery 处理巡检恢复：重置计数，若之前通知过失败且开启恢复通知则发通知。
func handleKeyHealthRecovery(ctx context.Context, ch *model.Channel, recoveryNotify bool, now time.Time) {
	state := getKeyHealthState(ch.ID)
	state.mu.Lock()
	wasNotified := state.notifiedFailed
	state.consecutiveFails = 0
	state.notifiedFailed = false
	state.mu.Unlock()

	if wasNotified && recoveryNotify {
		if ctx == nil {
			ctx = context.Background()
		}
		sendKeyHealthNotification(ctx, ch, true, 0, "")
	}
}

// sendKeyHealthNotification 创建应用内通知。
func sendKeyHealthNotification(ctx context.Context, ch *model.Channel, recovered bool, fails int, detail string) {
	var key notification.NotifKey
	var severity model.NotificationSeverity
	if recovered {
		key = notification.KeyKeyHealthRecover
		severity = model.NotificationSeveritySuccess
	} else {
		key = notification.KeyKeyHealthFail
		severity = model.NotificationSeverityWarning
	}

	metadata, _ := json.Marshal(map[string]any{
		"channel_id":   ch.ID,
		"channel_name": ch.Name,
		"fails":        fails,
		"detail":       detail,
	})
	n := &model.Notification{
		Type:         model.NotificationTypeKeyHealth,
		Severity:     severity,
		Source:       "key_health_check",
		SourceID:     fmt.Sprintf("%d", ch.ID),
		DedupeKey:    fmt.Sprintf("key_health:%d:%v:%d", ch.ID, recovered, time.Now().UnixMilli()),
		MetadataJSON: string(metadata),
		Link:         "channel",
	}

	var titleArgs, contentArgs map[string]any
	var titleFmtArgs, contentFmtArgs []any
	if recovered {
		titleArgs = map[string]any{"name": ch.Name}
		contentArgs = map[string]any{"name": ch.Name, "id": ch.ID}
		titleFmtArgs = []any{ch.Name}
		contentFmtArgs = []any{ch.Name, ch.ID}
	} else {
		titleArgs = map[string]any{"name": ch.Name}
		contentArgs = map[string]any{"name": ch.Name, "id": ch.ID, "fails": fails, "detail": detail}
		titleFmtArgs = []any{ch.Name}
		contentFmtArgs = []any{ch.Name, ch.ID, fails, detail}
	}

	notification.SetMessage(n, key, key, titleArgs, contentArgs, titleFmtArgs, contentFmtArgs)
	// notification.Create 在 DB 未初始化时（测试环境）可能 panic，recover 防止崩溃。
	if err := safeCreateNotification(ctx, n); err != nil {
		log.Warnf("key_health: failed to create notification for channel %d: %v", ch.ID, err)
	}
}

// buildKeyHealthFailureDetail 从 TestChannel 结果构造失败摘要。
func buildKeyHealthFailureDetail(summary *helper.ChannelTestSummary) string {
	if summary == nil || len(summary.Results) == 0 {
		return "all keys failed"
	}
	var parts []string
	for _, r := range summary.Results {
		if !r.Passed {
			keyLabel := r.KeyRemark
			if keyLabel == "" {
				keyLabel = r.KeyMasked
			}
			if keyLabel == "" {
				keyLabel = "unknown"
			}
			msg := r.Message
			if msg == "" {
				msg = fmt.Sprintf("HTTP %d", r.StatusCode)
			}
			parts = append(parts, fmt.Sprintf("%s: %s", keyLabel, msg))
		}
	}
	if len(parts) == 0 {
		return "all keys failed"
	}
	return strings.Join(parts, "; ")
}

// safeCreateNotification 包装 notification.Create，在 DB 未初始化时（测试环境）
// recover 防止 panic。生产环境 DB 总是可用的，recover 仅用于健壮性兜底。
func safeCreateNotification(ctx context.Context, n *model.Notification) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("notification create panic: %v", r)
		}
	}()
	return notification.Create(ctx, n)
}
