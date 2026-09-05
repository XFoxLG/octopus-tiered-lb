package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 22,
		Up:      migrateChannelDisposable,
	})
}

// 022: 为 channels 增加一次性渠道支持：disposable / expire_at。
//
// disposable 标记渠道为一次性，到期（expire_at）后由后台任务自动删除并通知。
// gorm AutoMigrate 通常也会加列，这里幂等兜底，确保跨方言（SQLite/MySQL/Postgres）
// 以及运行时切换 DB 类型后该列存在。
func migrateChannelDisposable(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable(&model.Channel{}) {
		return nil
	}
	if !db.Migrator().HasColumn(&model.Channel{}, "Disposable") {
		if err := db.Migrator().AddColumn(&model.Channel{}, "Disposable"); err != nil {
			return fmt.Errorf("add column disposable: %w", err)
		}
	}
	if !db.Migrator().HasColumn(&model.Channel{}, "ExpireAt") {
		if err := db.Migrator().AddColumn(&model.Channel{}, "ExpireAt"); err != nil {
			return fmt.Errorf("add column expire_at: %w", err)
		}
	}
	return nil
}
