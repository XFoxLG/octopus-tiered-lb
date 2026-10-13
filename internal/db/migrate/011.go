package migrate

import (
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 11,
		Up:      migrateNavOrderCleanup,
	})
}

// validNavItems is the canonical set of top-level navigation items.
// Items like "checkin", "credential", "announcement" have been merged
// into the Hub module as tabs and should no longer appear as standalone
// nav entries.
var validNavItems = map[string]struct{}{
	"home":         {},
	"hub":          {},
	"channel":      {},
	"group":        {},
	"model":        {},
	"analytics":    {},
	"log":          {},
	"notification": {},
	"ops":          {},
	"apikey":       {},
	"setting":      {},
	"user":         {},
}

// defaultNavOrder is the canonical default order for top-level nav items.
var defaultNavOrder = []string{
	"home", "hub", "channel", "group", "model",
	"analytics", "log", "notification", "ops", "apikey", "setting", "user",
}

func migrateNavOrderCleanup(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("settings") {
		return nil
	}

	keys := []string{"nav_order", "nav_visible"}
	for _, key := range keys {
		if err := cleanupNavSetting(db, key); err != nil {
			return fmt.Errorf("cleanup %s: %w", key, err)
		}
	}
	return nil
}

func cleanupNavSetting(db *gorm.DB, key string) error {
	type Setting struct {
		Key   string `gorm:"primaryKey"`
		Value string
	}

	var setting Setting
	// clause.Column 让 GORM 按方言 quote 列名：反引号 `key` 在 PostgreSQL 下是
	// 语法错误（backtick 被解析成操作符），会让全新 PostgreSQL 部署的迁移失败。
	result := db.Where(clause.Eq{Column: clause.Column{Name: "key"}, Value: key}).First(&setting)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil
		}
		return result.Error
	}

	raw := strings.TrimSpace(setting.Value)
	if raw == "" {
		return nil
	}

	var items []string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		// 非法 JSON：保留原值并跳过。该值可能是用户手工编辑出错——
		// 用默认值覆盖会静默重置用户导航配置；前端正则化会兜底非法值。
		return nil
	}

	// Filter to only valid items, preserving input order
	filtered := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if _, ok := validNavItems[item]; !ok {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		filtered = append(filtered, item)
	}

	// Append any missing defaults
	for _, item := range defaultNavOrder {
		if _, ok := seen[item]; ok {
			continue
		}
		filtered = append(filtered, item)
	}

	// Check if anything changed
	newJSON, err := json.Marshal(filtered)
	if err != nil {
		return err
	}

	if string(newJSON) == raw {
		return nil // nothing to update
	}

	return db.Model(&Setting{}).Where(clause.Eq{Column: clause.Column{Name: "key"}, Value: key}).Update("value", string(newJSON)).Error
}
