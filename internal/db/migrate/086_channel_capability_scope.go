package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

const (
	channelCapabilityLegacyIndex = "idx_channel_model_capability"
	channelCapabilityScopeIndex  = "uidx_channel_model_capability_scope"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 86,
		Up:      migrateChannelCapabilityScope,
	})
}

// 086 expands capability evidence from one coarse channel/model/capability row
// to a scoped row that records the stable channel UID, endpoint, protocol, and
// key. Existing rows keep the unknown endpoint/protocol values; they are never
// backfilled with a guessed endpoint. The old unique index is replaced so
// evidence from different endpoints or keys cannot overwrite each other.
func migrateChannelCapabilityScope(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	migrator := database.Migrator()
	if !migrator.HasTable(&model.ChannelModelCapability{}) {
		return nil
	}
	for _, column := range []string{
		"ChannelUID",
		"EndpointID",
		"Protocol",
		"ConfigurationFingerprint",
		"Stale",
		"StaleAt",
		"StaleReason",
	} {
		if migrator.HasColumn(&model.ChannelModelCapability{}, column) {
			continue
		}
		if err := migrator.AddColumn(&model.ChannelModelCapability{}, column); err != nil {
			return fmt.Errorf("add channel_model_capabilities.%s: %w", column, err)
		}
	}

	type unscopedRow struct {
		ID        int64
		ChannelID int
	}
	var unscoped []unscopedRow
	if err := database.Model(&model.ChannelModelCapability{}).
		Select("id", "channel_id").
		Where("channel_uid IS NULL OR channel_uid = ''").
		Find(&unscoped).Error; err != nil {
		return fmt.Errorf("list unscoped capability evidence: %w", err)
	}
	for _, row := range unscoped {
		uid := ""
		if migrator.HasTable(&model.Channel{}) && migrator.HasColumn(&model.Channel{}, "ChannelUID") {
			var values []string
			if err := database.Model(&model.Channel{}).
				Where("id = ?", row.ChannelID).
				Limit(1).
				Pluck("channel_uid", &values).Error; err != nil {
				return fmt.Errorf("load channel_uid for capability row %d: %w", row.ID, err)
			}
			if len(values) > 0 {
				uid = values[0]
			}
		}
		scopeUID := model.CapabilityScopeUID(uid, row.ChannelID)
		if err := database.Model(&model.ChannelModelCapability{}).
			Where("id = ? AND (channel_uid IS NULL OR channel_uid = '')", row.ID).
			Update("channel_uid", scopeUID).Error; err != nil {
			return fmt.Errorf("backfill capability channel_uid for row %d: %w", row.ID, err)
		}
	}

	if migrator.HasIndex(&model.ChannelModelCapability{}, channelCapabilityLegacyIndex) {
		if err := migrator.DropIndex(&model.ChannelModelCapability{}, channelCapabilityLegacyIndex); err != nil {
			return fmt.Errorf("drop %s: %w", channelCapabilityLegacyIndex, err)
		}
	}
	// Recreate unconditionally. AutoMigrate can create the model's current
	// index before this after-migration runs, and older development builds may
	// have created the same name with the previous column set. Dropping it
	// first makes the final definition deterministic and keeps reruns
	// idempotent.
	if migrator.HasIndex(&model.ChannelModelCapability{}, channelCapabilityScopeIndex) {
		if err := migrator.DropIndex(&model.ChannelModelCapability{}, channelCapabilityScopeIndex); err != nil {
			return fmt.Errorf("drop %s: %w", channelCapabilityScopeIndex, err)
		}
	}
	if err := migrator.CreateIndex(&model.ChannelModelCapability{}, channelCapabilityScopeIndex); err != nil {
		return fmt.Errorf("create %s: %w", channelCapabilityScopeIndex, err)
	}
	return nil
}
