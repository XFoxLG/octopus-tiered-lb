package migrate

import (
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 78, Up: createChannelModelLimits})
}

// Additive only: 新表，老版本二进制不感知；回滚保留表与数据（无损）。
// v2.9.0 渠道×模型上下文上限（人工 manual 永不被学习 learned 覆盖）。
func createChannelModelLimits(database *gorm.DB) error {
	if database.Migrator().HasTable(&model.ChannelModelLimit{}) {
		return nil
	}
	if err := database.Migrator().CreateTable(&model.ChannelModelLimit{}); err != nil {
		return err
	}
	// 唯一键（channel_id, model_name）是学习写入的去重依据；个别方言下
	// CreateTable 不会为 uniqueIndex 生成索引，这里显式补建并忽略已存在错误。
	if !database.Migrator().HasIndex(&model.ChannelModelLimit{}, "uidx_channel_model_limit") {
		if err := database.Migrator().CreateIndex(&model.ChannelModelLimit{}, "uidx_channel_model_limit"); err != nil {
			return err
		}
	}
	return nil
}
