package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

const channelUIDUniqueIndex = "uidx_channels_channel_uid"

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 85,
		Up:      addChannelUID,
	})
}

// 085 assigns a stable identity to channels that predate the column. The
// operation is additive and idempotent: existing identifiers are never
// rewritten, and a duplicate non-empty identifier fails the unique index
// instead of being silently replaced.
func addChannelUID(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	migrator := database.Migrator()
	if !migrator.HasTable(&model.Channel{}) {
		return nil
	}
	if !migrator.HasColumn(&model.Channel{}, "ChannelUID") {
		if err := migrator.AddColumn(&model.Channel{}, "ChannelUID"); err != nil {
			return fmt.Errorf("add channels.channel_uid: %w", err)
		}
	}

	var ids []int
	if err := database.Model(&model.Channel{}).
		Where("channel_uid IS NULL OR channel_uid = ''").
		Order("id").
		Pluck("id", &ids).Error; err != nil {
		return fmt.Errorf("list channels without channel_uid: %w", err)
	}
	for _, id := range ids {
		uid := model.NewChannelUID()
		// The model marks channel_uid as create-only, so a normal GORM Update
		// would omit the column. This is a deliberate repair of persisted rows.
		result := database.Exec(
			"UPDATE channels SET channel_uid = ? WHERE id = ? AND (channel_uid IS NULL OR channel_uid = '')",
			uid, id,
		)
		if result.Error != nil {
			return fmt.Errorf("backfill channels.channel_uid for id %d: %w", id, result.Error)
		}
	}

	if !migrator.HasIndex(&model.Channel{}, channelUIDUniqueIndex) {
		if err := migrator.CreateIndex(&model.Channel{}, channelUIDUniqueIndex); err != nil {
			return fmt.Errorf("create %s (duplicate non-empty channel_uid values require administrator review): %w", channelUIDUniqueIndex, err)
		}
	}
	return nil
}
