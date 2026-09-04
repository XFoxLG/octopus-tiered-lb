package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 54,
		Up:      addGroupRequestOptions,
	})
}

// 054: 为分组增加请求参数覆盖（param_override）与自定义请求头（custom_header），
// 与渠道级同名字段同语义（XyzenSun 移植）。既有行保持 NULL/空，由 relay 层视为未配置。
func addGroupRequestOptions(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if !database.Migrator().HasTable(&model.Group{}) {
		return nil
	}
	if !database.Migrator().HasColumn(&model.Group{}, "ParamOverride") {
		if err := database.Migrator().AddColumn(&model.Group{}, "ParamOverride"); err != nil {
			return fmt.Errorf("add groups.param_override: %w", err)
		}
	}
	if !database.Migrator().HasColumn(&model.Group{}, "CustomHeader") {
		if err := database.Migrator().AddColumn(&model.Group{}, "CustomHeader"); err != nil {
			return fmt.Errorf("add groups.custom_header: %w", err)
		}
	}
	return nil
}
