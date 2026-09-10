package op

import (
	"context"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/relaylog"
)

// relayLogCacheReadTokens is kept lowercase for backward compatibility with tests.
func relayLogCacheReadTokens(responseContent string) int {
	return relaylog.RelayLogCacheReadTokens(responseContent)
}

func RelayLogStreamTokenCreate() (string, error) { return relaylog.RelayLogStreamTokenCreate() }

func RelayLogStreamTokenVerify(token string) bool { return relaylog.RelayLogStreamTokenVerify(token) }

func RelayLogStreamTokenRevoke(token string) { relaylog.RelayLogStreamTokenRevoke(token) }

func RelayLogSubscribe() chan model.RelayLogListItem { return relaylog.RelayLogSubscribe() }

func RelayLogUnsubscribe(ch chan model.RelayLogListItem) { relaylog.RelayLogUnsubscribe(ch) }

func RelayLogAdd(ctx context.Context, relayLog model.RelayLog) (int64, error) {
	return relaylog.RelayLogAdd(ctx, relayLog)
}

func RelayLogSaveDBTask(ctx context.Context) error { return relaylog.RelayLogSaveDBTask(ctx) }

func RelayLogList(ctx context.Context, filter relaylog.LogFilter, page, pageSize int) ([]model.RelayLogListItem, error) {
	return relaylog.RelayLogList(ctx, filter, page, pageSize)
}

func RelayLogClear(ctx context.Context) error { return relaylog.RelayLogClear(ctx) }

// RelayLogClearContents 清空所有历史日志的请求/响应内容大字段，保留元数据。
func RelayLogClearContents(ctx context.Context) error {
	return relaylog.RelayLogClearContents(ctx)
}

// RelayLogApplyKeepEnabled 在「保留历史日志」开关变更后调整独立日志库连接：
// 关闭时断开日志库连接（释放资源），重新开启时重连。共用主库时为空操作。
func RelayLogApplyKeepEnabled(ctx context.Context, enabled bool) error {
	return relaylog.ApplyKeepEnabledChange(ctx, enabled)
}

func RelayLogGetByID(ctx context.Context, id int64) (*model.RelayLog, error) {
	return relaylog.RelayLogGetByID(ctx, id)
}
