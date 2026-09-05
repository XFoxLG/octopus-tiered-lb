package migrate

import (
	"encoding/json"
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 40,
		Up:      migrateAccountPool,
	})
}

// 040: 历史迁移（曾创建 account_pools / pool_accounts 表并为 channels 加 pool_id 列）。
// 号池功能已移除；表和列由后续迁移 DROP。nav_order/nav_visible 补入 "pool" 的
// 部分保留（幂等），nav 中 "pool" 条目由应用启动时的 nav 校准过滤。
func migrateAccountPool(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}

	// nav_order / nav_visible 补入 "pool"。
	appendToNavSetting(db, string(model.SettingKeyNavOrder), "pool", "channel")
	appendToNavSetting(db, string(model.SettingKeyNavVisible), "pool", "channel")

	return nil
}

// appendToNavSetting 在 nav JSON 数组中 after 元素之后插入 item（保序去重、幂等）。
func appendToNavSetting(db *gorm.DB, key, item, after string) {
	var setting model.Setting
	if err := db.Where("`key` = ?", key).First(&setting).Error; err != nil {
		return
	}
	var items []string
	if err := json.Unmarshal([]byte(setting.Value), &items); err != nil {
		return
	}
	// 已存在则跳过。
	for _, v := range items {
		if v == item {
			return
		}
	}
	// 在 after 之后插入。
	idx := len(items) // 默认追加到末尾
	for i, v := range items {
		if v == after {
			idx = i + 1
			break
		}
	}
	newItems := make([]string, 0, len(items)+1)
	newItems = append(newItems, items[:idx]...)
	newItems = append(newItems, item)
	newItems = append(newItems, items[idx:]...)
	data, err := json.Marshal(newItems)
	if err != nil {
		return
	}
	db.Model(&setting).Update("value", string(data))
}
