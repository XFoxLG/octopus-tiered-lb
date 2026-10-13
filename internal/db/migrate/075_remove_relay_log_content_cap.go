package migrate

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 075 删除「单条日志正文上限」设置项。该机制会把超过上限的请求/响应正文
// 整条抹空（老默认 2MiB），长上下文原文永久丢失；现在正文一律经 Gzip 压缩
// 落库 blob，内存与存储预算由 pending 队列预算和内容保留策略兜底，
// 因此删除该设置行，恢复与原版一致的「不截断、不抹空」行为。
func init() { RegisterAfterAutoMigration(Migration{Version: 75, Up: removeRelayLogContentCapSetting}) }

// relayLogMaxContentSizeSettingKey 是被删除的设置项 key。
const relayLogMaxContentSizeSettingKey = "relay_log_max_content_size_mb"

func removeRelayLogContentCapSetting(database *gorm.DB) error {
	if database == nil {
		return errors.New("db is nil")
	}
	if !database.Migrator().HasTable("settings") {
		return nil
	}
	return relayLogContentCapSettingDelete(database).Error
}

// relayLogContentCapSettingDelete 构造删除该设置行的语句。
//
// 必须用 clause.Column 让 GORM 按方言自动 quote 列名（MySQL 反引号 /
// PostgreSQL 双引号）：v2.8.6 上线时这里直接写了反引号 `key`，PostgreSQL 把
// 反引号当成自定义操作符解析，执行时报 syntax error at or near "=" (42601)，
// 迁移 075 失败 → 进程启动即退出 → Render 部署失败。
// 同款坑在迁移 047 已经踩过一次（见 047 的注释），故此处保留 DryRun 单测断言。
//
// 单独抽成函数是为了让单测能在 PostgreSQL 方言的 DryRun 会话上断言生成的 SQL。
func relayLogContentCapSettingDelete(database *gorm.DB) *gorm.DB {
	return database.Table("settings").
		Where(clause.Eq{Column: clause.Column{Name: "key"}, Value: relayLogMaxContentSizeSettingKey}).
		Delete(nil)
}
