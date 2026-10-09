package task

import (
	"context"
	"math"
	"strconv"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/backup"
	"github.com/lingyuins/octopus/internal/op/errorlog"
	"github.com/lingyuins/octopus/internal/op/ratelimitstore"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/op/stats"
	"github.com/lingyuins/octopus/internal/price"
	"github.com/lingyuins/octopus/internal/relay"
	"github.com/lingyuins/octopus/internal/relay/balancer"
	utilsjson "github.com/lingyuins/octopus/internal/utils/json"
	"github.com/lingyuins/octopus/internal/utils/log"
)

const (
	TaskPriceUpdate       = "price_update"
	TaskStatsSave         = "stats_save"
	TaskRuntimeState      = "runtime_state_save"
	TaskRelayLogSave      = "relay_log_save"
	TaskSyncLLM           = "sync_llm"
	TaskCleanLLM          = "clean_llm"
	TaskBaseUrlDelay      = "base_url_delay"
	TaskWebDAVBackup      = "webdav_backup"
	TaskErrorLogCleanup   = "error_log_cleanup"
)

// durationOverflows 判断 v 个 unit 换算成 time.Duration 是否溢出 int64 纳秒。
func durationOverflows(v int64, unit time.Duration) bool {
	return v > math.MaxInt64/int64(unit)
}

// settingInterval 解析周期型设置项（unit 为设置值的单位）。读取失败、值 <= 0 或
// 换算溢出时回退到 DefaultSettings 登记的默认值并告警——保证任务始终被注册，
// 设置页的热更新 task.Update 才有目标；DefaultSettings 异常时再回退 fallback。
func settingInterval(key model.SettingKey, unit time.Duration, fallback time.Duration) time.Duration {
	def := fallback
	if raw, ok := model.DefaultSettingValue(key); ok {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && !durationOverflows(int64(n), unit) {
			def = time.Duration(n) * unit
		}
	}

	v, err := setting.GetInt(key)
	if err != nil {
		log.Warnf("setting %s unreadable (%v), task interval falls back to %v", key, err, def)
		return def
	}
	if v <= 0 {
		log.Warnf("setting %s = %d is not positive, task interval falls back to %v", key, v, def)
		return def
	}
	if durationOverflows(int64(v), unit) {
		log.Warnf("setting %s = %d overflows time.Duration, task interval falls back to %v", key, v, def)
		return def
	}
	return time.Duration(v) * unit
}

// defaultWebDAVBackupInterval 从 DefaultSettings 的 webdav_config JSON 解析默认
// 备份周期，失败时回退 6 小时（与默认 JSON 的 interval_hours 一致）。
func defaultWebDAVBackupInterval() time.Duration {
	const fallback = 6 * time.Hour
	raw, ok := model.DefaultSettingValue(model.SettingKeyWebDAVConfig)
	if !ok {
		return fallback
	}
	var cfg struct {
		IntervalHours int `json:"interval_hours"`
	}
	if err := utilsjson.Unmarshal([]byte(raw), &cfg); err != nil || cfg.IntervalHours <= 0 || durationOverflows(int64(cfg.IntervalHours), time.Hour) {
		return fallback
	}
	return time.Duration(cfg.IntervalHours) * time.Hour
}

func Init() {
	if db.IsSQLite() {
		db.StartSerialWriter(context.Background())
	}
	relaylog.StartFlushWorker(context.Background())
	// 注入 Key 巡检状态清理函数到 relay 包（打破 relay -> task 循环依赖）。
	relay.OnChannelDeletedKeyHealthHook = RemoveChannelKeyHealthState
	priceUpdateInterval := settingInterval(model.SettingKeyModelInfoUpdateInterval, time.Hour, 24*time.Hour)
	Register(string(model.SettingKeyModelInfoUpdateInterval), priceUpdateInterval, true, func() {
		// 出站请求必须带超时：price.UpdateLLMPrice 走 internal/client 的 default 档
		// （http.Client.Timeout=0，僵死保护只剩 Transport.ResponseHeaderTimeout，
		// 而它仅覆盖「等响应头」阶段），因此响应体读取阶段没有任何时间上限。
		// models.dev 的 10 MiB 响应体上限只防内存爆炸，不防无限慢速涓流：上游发完
		// 响应头后以极慢速率吐 body，本 goroutine 会永久挂住。后果是连锁的——
		// runOnce 的 defer entry.running.Store(false) 永不执行 ⇒ 此后每个 tick 都被
		// skipping overlapping run 跳过（价格更新功能永久停摆，直到进程重启）；
		// Shutdown 的 entry.wg.Wait() 永久阻塞 ⇒ 优雅关闭卡死，且 task.Shutdown
		// 之后的 db.StopSerialWriter / op.SaveCache 等 hook 不执行，有数据丢失风险。
		//
		// 取值 2 分钟：对齐 op/remotesite 的既有先例（该处同样是「后台自动出站」）。
		// 本任务间隔由 SettingKeyModelInfoUpdateInterval 控制（单位小时，默认 24h），
		// 2min << 间隔，不会造成任务堆叠或长期占用。
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := price.UpdateLLMPrice(ctx); err != nil {
			log.Warnf("failed to update price info: %v", err)
		}
	})

	Register(TaskBaseUrlDelay, 1*time.Hour, true, ChannelBaseUrlDelayTask)

	syncLLMInterval := settingInterval(model.SettingKeySyncLLMInterval, time.Hour, 24*time.Hour)
	Register(string(model.SettingKeySyncLLMInterval), syncLLMInterval, true, SyncModelsTask)

	statsSaveInterval := settingInterval(model.SettingKeyStatsSaveInterval, time.Minute, 10*time.Minute)
	if db.IsSQLite() {
		Register(TaskStatsSave, statsSaveInterval, false, func() {
			db.EnqueueWrite(db.WriteJob{Name: "stats_save", Fn: func(_ context.Context) error {
				stats.SaveDBTask()
				return nil
			}})
		})
		Register(TaskRuntimeState, statsSaveInterval, false, func() {
			db.EnqueueWrite(db.WriteJob{Name: "runtime_state_save", Fn: func(_ context.Context) error {
				balancer.RuntimeStateSaveDBTask()
				return nil
			}})
		})
	} else {
		Register(TaskStatsSave, statsSaveInterval, false, stats.SaveDBTask)
		Register(TaskRuntimeState, statsSaveInterval, false, balancer.RuntimeStateSaveDBTask)
	}

	Register(TaskErrorLogCleanup, 6*time.Hour, false, func() {
		if err := errorlog.Cleanup(context.Background()); err != nil {
			log.Warnf("failed to cleanup error logs: %v", err)
		}
	})

	Register(TaskRelayLogSave, 2*time.Minute, false, func() {
		// 清理过期的 SSE 流 token（issue #149 内存优化补充）
		relaylog.PurgeExpiredStreamTokens()

		// 清理过期的失败提示缓存条目
		relay.PurgeFailureHintCache()

		// 主动清理过期的流会话条目，避免仅依赖惰性触发（见 issue #46 内存暴涨）
		relay.PurgeExpiredStreamSessions()

		// 主动回收 balancer 三个全局 map 中长期空闲的条目。它们的 key 含客户端
		// 请求携带的 modelName（基数不受控），之前只在渠道/Key 删除时清理，缺少
		// 按空闲时长的周期回收，刷量/随机 model 名会导致 map 无界增长（见 issue #46）。
		const balancerIdleThreshold = time.Hour
		balancer.PurgeIdleEntries(balancerIdleThreshold)
		balancer.PurgeIdleStats(balancerIdleThreshold)
		balancer.PurgeIdleSessions(balancerIdleThreshold)
		balancer.PurgeIdleChannelRateLimits(balancerIdleThreshold)

		// 清理过期的按模型 key 冷却条目（见 issue #94）。key 维度含客户端 model 名，
		// 缺少周期回收会在刷量/随机 model 名下无界增长。
		balancer.PurgeExpiredKeyCooldowns()
		// 清理长时间未活动的可用度分数条目，与 key 冷却同维度回收。
		balancer.PurgeStaleKeyAvailability(balancerIdleThreshold)
		// 清理长时间未活动的速度 TPS 条目，与可用度同维度回收。
		balancer.PurgeStaleKeySpeed(balancerIdleThreshold)
		// 清理长时间未活动的限流 bucket。其 key 含客户端请求携带的 modelName
		// （基数不受控），与 balancer 全局 map 同维度，缺少周期回收会在刷量/随机
		// model 名下无界增长（见 issue #46 同类遗漏）。
		ratelimitstore.PurgeStaleBuckets(balancerIdleThreshold)
		// 清理渠道级容量配额的内存回退条目（阶段1）：并发计数条目 + 渠道 RPM 桶。
		// 渠道 RPM key 含 resolvedModelName（基数不受控），与上面的限流 bucket
		// 同维度回收；Redis 模式下 key 带 TTL 自动过期，函数内部直接返回 0。
		ratelimitstore.PurgeStaleChannelState(balancerIdleThreshold)
		// 清理长时间未活动的 per-model 统计条目。modelCache 的 key = FNV(channelID:
		// clientModelName)，model 名由客户端请求携带、基数不受控；此前仅测试代码
		// Clear()，无空闲回收，刷量/随机 model 名会让 map 终生驻留（见 issue #124）。
		stats.PurgeIdleModelStats(balancerIdleThreshold)
		if db.IsSQLite() {
			db.EnqueueWrite(db.WriteJob{Name: "relay_log_save", Fn: func(_ context.Context) error {
				return relaylog.RelayLogSaveDBTask(context.Background())
			}})
		} else {
			if err := relaylog.RelayLogSaveDBTask(context.Background()); err != nil {
				log.Warnf("relay log save db task failed: %v", err)
			}
		}
	})

	// WebDAV cloud backup: interval_hours from settings (issue: user reported
	// 72h setting ignored). 存量非法值按 DefaultSettings 兜底并告警，任务始终注册。
	webdavInterval := defaultWebDAVBackupInterval()
	webdavCfg, err := backup.GetWebDAVConfig()
	if err != nil {
		log.Warnf("failed to get webdav config: %v", err)
	} else if webdavCfg.IntervalHours > 0 && !durationOverflows(int64(webdavCfg.IntervalHours), time.Hour) {
		webdavInterval = time.Duration(webdavCfg.IntervalHours) * time.Hour
	} else {
		log.Warnf("webdav interval_hours %d is invalid, task interval falls back to %v", webdavCfg.IntervalHours, webdavInterval)
	}
	Register(TaskWebDAVBackup, webdavInterval, false, func() {
		if err := backup.PerformWebDAVBackup(context.Background()); err != nil {
			log.Warnf("webdav backup failed: %v", err)
		}
	})

	// Disposable channel expiry: scan every 1 minute for expired one-time channels.
	Register(TaskChannelExpire, 1*time.Minute, false, ExpireDisposableChannels)

	// 定时 Key 可用性巡检（issue #142）：按设置间隔验证渠道 Key 连通性，
	// 失败通知并标灰渠道。间隔由 SettingKeyKeyHealthCheckInterval 控制（分钟）。
	Register(TaskKeyHealthCheck, settingInterval(model.SettingKeyKeyHealthCheckInterval, time.Minute, 30*time.Minute), false, CheckKeyHealth)

}
