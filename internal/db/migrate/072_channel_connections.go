package migrate

import (
	"encoding/json"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() { RegisterAfterAutoMigration(Migration{Version: 72, Up: addChannelConnections}) }
func addChannelConnections(database *gorm.DB) error {
	if database.Migrator().HasTable(&model.ChannelProbeResult{}) && !database.Migrator().HasColumn(&model.ChannelProbeResult{}, "EndpointID") {
		if err := database.Migrator().AddColumn(&model.ChannelProbeResult{}, "EndpointID"); err != nil {
			return err
		}
	}
	if !database.Migrator().HasTable(&model.Channel{}) {
		return nil
	}
	if !database.Migrator().HasColumn(&model.Channel{}, "ConnectionConfig") {
		if err := database.Migrator().AddColumn(&model.Channel{}, "ConnectionConfig"); err != nil {
			return err
		}
	}
	for _, column := range []string{"type", "base_urls", "upstream_protocols", "outbound_format_override"} {
		if !database.Migrator().HasColumn(&model.Channel{}, column) {
			return nil
		}
	}
	var rows []model.Channel
	if err := database.Select("id", "type", "base_urls", "upstream_protocols", "outbound_format_override").Where("connection_config IS NULL OR connection_config = '' OR connection_config = 'null'").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		// Wildcard groups may also forward media. Preserve their old connection
		// until the operator explicitly reviews its intended endpoint scope.
		if database.Migrator().HasTable("group_items") && database.Migrator().HasTable("groups") && database.Migrator().HasColumn("groups", "endpoint_type") {
			var count int64
			if err := database.Table("group_items").Joins("JOIN groups ON groups.id = group_items.group_id").Where("group_items.channel_id = ? AND groups.endpoint_type NOT IN ?", row.ID, []string{"chat", "deepseek", "mimo", "responses", "messages", "embeddings"}).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				continue
			}
		}
		preview := row.PreviewConnectionMigration()
		if !preview.Automatic {
			continue
		}
		payload, err := json.Marshal(preview.Config)
		if err != nil {
			return err
		}
		if err := database.Model(&model.Channel{}).Where("id = ? AND (connection_config IS NULL OR connection_config = '' OR connection_config = 'null')", row.ID).Update("connection_config", string(payload)).Error; err != nil {
			return err
		}
	}
	return nil
}
