package migrate

import (
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 77, Up: addRelayLogImageCount})
}

// Additive only: 老二进制仍可读写日志；回滚后该列留在原处不影响任何路径。
// 0 = 无图（或迁移前历史行），>0 = 客户端请求内的图片数量（v2.8.14 含图标注）。
func addRelayLogImageCount(database *gorm.DB) error {
	if !database.Migrator().HasTable(&model.RelayLog{}) {
		return nil
	}
	if !database.Migrator().HasColumn(&model.RelayLog{}, "ImageCount") {
		return database.Migrator().AddColumn(&model.RelayLog{}, "ImageCount")
	}
	return nil
}
