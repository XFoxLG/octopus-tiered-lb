package migrate

import (
	"gorm.io/gorm"
)

// 075 删除「单条日志正文上限」设置项。该机制会把超过上限的请求/响应正文
// 整条抹空（老默认 2MiB），长上下文原文永久丢失；现在正文一律经 Gzip 压缩
// 落库 blob，内存与存储预算由 pending 队列预算和内容保留策略兜底，
// 因此删除该设置行，恢复与原版一致的「不截断、不抹空」行为。
func init() { RegisterAfterAutoMigration(Migration{Version: 75, Up: removeRelayLogContentCapSetting}) }

func removeRelayLogContentCapSetting(database *gorm.DB) error {
	return database.Exec("DELETE FROM settings WHERE `key` = ?", "relay_log_max_content_size_mb").Error
}
