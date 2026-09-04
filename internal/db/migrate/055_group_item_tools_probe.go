package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 55,
		Up:      addToolsProbeColumnsToGroupItems,
	})
}

// 055: 为 group_items 增加 tools 能力探测结论列（Seller 移植）。
// 既有行保持 NULL/空，由探测回填按证据层级写入。
func addToolsProbeColumnsToGroupItems(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if !database.Migrator().HasTable(&model.GroupItem{}) {
		return nil
	}
	for _, column := range []string{
		"SupportsTools",
		"SupportsToolsProbeKeyID",
		"SupportsToolsProbedAt",
		"SupportsToolsSource",
	} {
		if database.Migrator().HasColumn(&model.GroupItem{}, column) {
			continue
		}
		if err := database.Migrator().AddColumn(&model.GroupItem{}, column); err != nil {
			return fmt.Errorf("add group_items.%s: %w", column, err)
		}
	}
	return nil
}
