package task

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	ch "github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/notification"
	st "github.com/lingyuins/octopus/internal/op/stats"
	"github.com/lingyuins/octopus/internal/utils/log"
)

const TaskChannelExpire = "channel_expire"

// ExpireDisposableChannels 扫描已过期的一次性渠道，发送通知后自动删除。
// 一次性渠道（disposable=true）设置 expire_at 后，到期即由本任务清理。
func ExpireDisposableChannels() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 直接查 DB（不经过缓存），找出所有已过期的一次性渠道。
	var channels []model.Channel
	if err := db.GetDB().WithContext(ctx).
		Where("disposable = ? AND expire_at IS NOT NULL AND expire_at <= ?", true, time.Now()).
		Find(&channels).Error; err != nil {
		log.Errorf("channel expire: failed to query expired channels: %v", err)
		return
	}
	if len(channels) == 0 {
		return
	}

	log.Infof("channel expire: found %d expired disposable channels", len(channels))

	for _, channel := range channels {
		deleteExpiredChannel(ctx, channel)
	}
}

func deleteExpiredChannel(ctx context.Context, channel model.Channel) {
	// 先删除渠道，删除成功后再发通知。
	// 反过来（先通知后删除）会在删除失败时导致下一轮重复发送通知（issue #126 修复项）。
	// channel 是局部变量，删除后其字段仍在内存中，通知内容（name/id/expire_at）不受影响。
	if err := ch.Delete(channel.ID, ctx); err != nil {
		log.Errorf("channel expire: failed to delete channel %d (%s): %v (will retry next cycle)", channel.ID, channel.Name, err)
		return
	}
	st.OnChannelDeleted(channel.ID)
	log.Infof("channel expire: deleted expired disposable channel %d (%s)", channel.ID, channel.Name)

	// 删除成功后发送应用内通知。
	createChannelExpireNotification(ctx, channel, "in_app", "")
}

func createChannelExpireNotification(ctx context.Context, channel model.Channel, deliveryStatus, deliveryDetail string) {
	metadata, _ := json.Marshal(map[string]any{
		"channel_id":      channel.ID,
		"channel_name":    channel.Name,
		"expire_at":       channel.ExpireAt,
		"delivery_status": deliveryStatus,
		"delivery_detail": deliveryDetail,
	})
	n := &model.Notification{
		Type:         model.NotificationTypeChannelExpire,
		Severity:     model.NotificationSeverityWarning,
		Source:       "channel",
		SourceID:     fmt.Sprintf("%d", channel.ID),
		DedupeKey:    fmt.Sprintf("channel_expire:%d:%d", channel.ID, time.Now().UnixMilli()),
		MetadataJSON: string(metadata),
		Link:         "channel",
	}
	// 应用内通知走 i18n 键。
	expireStr := ""
	if channel.ExpireAt != nil {
		expireStr = channel.ExpireAt.Format(time.RFC3339)
	}
	notification.SetMessage(n, notification.KeyChannelExpire, notification.KeyChannelExpire,
		nil,
		map[string]any{"name": channel.Name, "id": channel.ID, "expire_at": expireStr},
		nil,
		[]any{channel.Name, channel.ID, expireStr})
	if err := notification.Create(ctx, n); err != nil {
		log.Warnf("notification: failed to create channel expiration notification for channel %d: %v", channel.ID, err)
	}
}
