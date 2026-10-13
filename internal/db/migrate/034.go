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
		Version: 34,
		Up:      migrateNavAlertToNotification,
	})
}

// 034: 顶级导航项 id 由 "alert" 重命名为 "notification"（前端 nav-store / 路由
// 已使用 "notification"，但后端 nav_order / nav_visible 默认值与历史持久化行仍写
// "alert"）。后果：全新安装时 setting.RefreshCache 用旧默认 seed，前端 visibleItems
// 不含 "notification"，导航入口与铃铛"打开通知中心"均不可达，页面彻底隐藏。
//
// 本迁移把 settings 表里 nav_order / nav_visible 的 JSON 数组中的 "alert" 替换为
// "notification"（去重，保序）。已含 "notification" 或不含 "alert" 的行保持不变，
// 故幂等。在 AfterAutoMigrate 阶段运行，早于 setting.RefreshCache 加载缓存，
// 单次重启即可对存量库生效。
func migrateNavAlertToNotification(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("settings") {
		return nil
	}

	type Setting struct {
		Key   string `gorm:"primaryKey;column:key"`
		Value string `gorm:"column:value"`
	}

	for _, key := range []string{"nav_order", "nav_visible"} {
		var setting Setting
			// 同 011：用 clause.Column 走方言安全 quote（PostgreSQL 不认反引号）。
			result := db.Where(clause.Eq{Column: clause.Column{Name: "key"}, Value: key}).First(&setting)
		if result.Error != nil {
			if result.Error == gorm.ErrRecordNotFound {
				continue
			}
			return fmt.Errorf("read %s: %w", key, result.Error)
		}

		raw := strings.TrimSpace(setting.Value)
		if raw == "" {
			continue
		}

		var items []string
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			// 非合法 JSON 不在本迁移职责内（011 的 cleanup 同样跳过保留原值）。
			continue
		}

		renamed := renameNavItem(items, "alert", "notification")
		if renamed == nil {
			continue // 无需变更
		}

		newJSON, err := json.Marshal(renamed)
		if err != nil {
			return fmt.Errorf("marshal %s: %w", key, err)
		}
			if err := db.Model(&Setting{}).Where(clause.Eq{Column: clause.Column{Name: "key"}, Value: key}).Update("value", string(newJSON)).Error; err != nil {
			return fmt.Errorf("update %s: %w", key, err)
		}
	}
	return nil
}

// renameNavItem 把 items 中的 oldID 替换为 newID，保序去重。
// 若 items 不含 oldID，或已含 newID 且无 oldID 需替换，返回 nil 表示无需变更。
func renameNavItem(items []string, oldID, newID string) []string {
	hasOld := false
	for _, it := range items {
		if it == oldID {
			hasOld = true
			break
		}
	}
	if !hasOld {
		return nil
	}

	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	add := func(id string) {
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, it := range items {
		if it == oldID {
			add(newID) // 用 newID 占 oldID 的原位置
			continue
		}
		add(it)
	}
	return out
}
