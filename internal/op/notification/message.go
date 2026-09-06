package notification

import (
	"encoding/json"
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
)

// NotifKey 是通知 i18n 键的前缀（不含 .title/.content 后缀）。
// 前端在 'notif' 命名空间下用 `${key}.title` / `${key}.content` 取模板，
// 并传入对应 *Args（JSON map）。新增通知类型时在此追加常量与英文回退模板。
type NotifKey string

const (
	KeyChannelExpire    NotifKey = "channel_expire"
	KeyBackupOK         NotifKey = "backup.ok"
	KeyBackupFail       NotifKey = "backup.fail"
	KeyBackupSkip       NotifKey = "backup.skip"
	KeyRestoreOK        NotifKey = "restore.ok"
	KeyRestoreFail      NotifKey = "restore.fail"
	KeyMigrationOK      NotifKey = "migration.ok"
	KeyMigrationFail    NotifKey = "migration.fail"
	KeySelfUpdateOK     NotifKey = "update.ok"
	KeySelfUpdateFail   NotifKey = "update.fail"
	KeyKeyHealthFail    NotifKey = "key_health.fail"
	KeyKeyHealthRecover NotifKey = "key_health.recover"
)

// fallbackTitle / fallbackContent 提供每个键的英文回退模板（Go fmt 占位符 %s/%d/%v）。
// 这些渲染结果写入 Title/Content 列，供搜索过滤、未来外部分发、以及无 key 的历史
// 通知混排时使用。前端有 title_key 时优先用 t() 渲染，忽略 Title/Content。
// 模板里的占位符名仅作文档；实际渲染用按位置的 Sprintf，参数顺序需与调用处一致。
var (
	fallbackTitle = map[NotifKey]string{
		KeyChannelExpire:    `Disposable channel expired`,
		KeyBackupOK:         `WebDAV backup completed`,
		KeyBackupFail:       `WebDAV backup failed`,
		KeyBackupSkip:       `WebDAV backup skipped`,
		KeyRestoreOK:        `WebDAV restore completed`,
		KeyRestoreFail:      `WebDAV restore failed`,
		KeyMigrationOK:      `Database migration completed`,
		KeyMigrationFail:    `Database migration failed`,
		KeySelfUpdateOK:     `Self update completed`,
		KeySelfUpdateFail:   `Self update failed`,
		KeyKeyHealthFail:    `Channel "%s" key verification failed`,
		KeyKeyHealthRecover: `Channel "%s" key verification recovered`,
	}
	fallbackContent = map[NotifKey]string{
		KeyChannelExpire:    `Disposable channel "%s" (ID: %d) expired at %s and has been automatically deleted.`,
		KeyBackupOK:         `Backup uploaded to %s (%d bytes).`,
		KeyBackupFail:       `%s`,
		KeyBackupSkip:       `WebDAV backup did not produce a remote file.`,
		KeyRestoreOK:        `Backup %s has been restored.`,
		KeyRestoreFail:      `%s`,
		KeyMigrationOK:      `Database migrated to %s. Restart needed: %v`,
		KeyMigrationFail:    `%s`,
		KeySelfUpdateOK:     `Octopus self-update completed successfully.`,
		KeySelfUpdateFail:   `%s`,
		KeyKeyHealthFail:    `Channel "%s" (ID: %d) consecutive %d key verification failures: %s`,
		KeyKeyHealthRecover: `Channel "%s" (ID: %d) key verification recovered.`,
	}
)

// SetMessage 为通知设置 i18n 键 + 参数，并同时把英文回退串写入 Title/Content。
// titleArgs/contentArgs 为插值参数（map，键需与 locale 模板的 {placeholder} 对应），
// 也会序列化为 JSON 存入 *Args 列供前端使用。传 nil 表示无参数。
// titleFmtArgs/contentFmtArgs 是按顺序的英文回退模板 Sprintf 参数（与 fallback 模板的
// 占位符位置对应）；若为 nil 则回退串留空模板原文（仅当无占位符时才应传 nil）。
func SetMessage(
	n *model.Notification,
	titleKey, contentKey NotifKey,
	titleArgs, contentArgs map[string]any,
	titleFmtArgs, contentFmtArgs []any,
) {
	n.TitleKey = string(titleKey)
	n.ContentKey = string(contentKey)
	if titleArgs != nil {
		if b, err := json.Marshal(titleArgs); err == nil {
			n.TitleArgs = string(b)
		}
	}
	if contentArgs != nil {
		if b, err := json.Marshal(contentArgs); err == nil {
			n.ContentArgs = string(b)
		}
	}
	// 英文回退串：供 Title/Content 列（搜索/外部分发/历史混排回退）。
	n.Title = renderFallback(fallbackTitle[titleKey], titleFmtArgs)
	n.Content = renderFallback(fallbackContent[contentKey], contentFmtArgs)
}

// renderFallback 用 Sprintf 渲染英文回退模板；无参数时返回模板原文。
func renderFallback(tmpl string, args []any) string {
	if tmpl == "" {
		return ""
	}
	if len(args) == 0 {
		return tmpl
	}
	return fmt.Sprintf(tmpl, args...)
}
