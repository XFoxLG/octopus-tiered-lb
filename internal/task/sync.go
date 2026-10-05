package task

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/lingyuins/octopus/internal/helper"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/op/llm"

	"github.com/lingyuins/octopus/internal/utils/diff"
	"github.com/lingyuins/octopus/internal/utils/log"
	"github.com/lingyuins/octopus/internal/utils/xstrings"
)

// syncFetchConcurrency bounds how many channels are probed for their model
// list in parallel during SyncModelsTask. Each probe is a network request with
// a short timeout, so a bounded pool keeps the batch wall-clock near the
// slowest single probe without opening an unbounded number of connections.
func syncFetchConcurrency() int {
	if n := runtime.GOMAXPROCS(0) * 2; n > 8 {
		return n
	}
	return 8
}

var lastSyncModelsTime = time.Now()

// syncFailureTracker 模型同步任务的失败追踪器（进程生命周期内有效）
var syncFailureTracker = NewFailureTracker()

// SyncModelsTask 同步模型任务
func SyncModelsTask() {
	log.Debugf("sync models task started")
	startTime := time.Now()
	defer func() {
		syncFailureTracker.Cleanup()
		log.Debugf("sync models task finished, sync time: %s", time.Since(startTime))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	channels, err := channel.List(ctx)
	if err != nil {
		log.Errorf("failed to list channels: %v", err)
		return
	}
	totalNewModels := make([]string, 0, 128)
	seenTotalNewModels := make(map[string]struct{}, 128)

	// 阶段一：并发抓取各 channel 的模型列表（网络 IO）。抓取彼此独立，
	// 串行执行会让单轮耗时随 channel 数线性累加；用有界 worker pool 并发抓取，
	// 单轮耗时接近最慢的单个 channel。FailureTracker 自身用 mutex 保护，可安全并发。
	type fetchResult struct {
		ch model.Channel
		// fetchModels 是写回渠道级 channels.model 的模型列表；逐 key 路径下它是
		// 所有抓取成功的 key 的有序并集。
		fetchModels []string
		// keyResults 仅在渠道开启 AutoSyncKeyModels 时非 nil：逐 key 抓取的原始结果
		// （含 KeyID / Passed / Models），阶段二据此生成 SupportedModels 回填增量。
		keyResults []helper.KeyModelResult
	}
	var (
		fetchMu      sync.Mutex
		fetchResults = make([]fetchResult, 0, len(channels))
	)
	fg, fgctx := errgroup.WithContext(ctx)
	fg.SetLimit(syncFetchConcurrency())
	for _, ch := range channels {
		ch := ch
		if !ch.Enabled || !ch.AutoSync {
			continue
		}
		if syncFailureTracker.ShouldSkip(ch.ID) {
			log.Debugf("skipping channel %s (id=%d) — in cooldown", ch.Name, ch.ID)
			continue
		}
		fg.Go(func() error {
			// 开启「按 key 隔离模型」的渠道：逐个 key 抓取，结果回填到各 key 的
			// SupportedModels；渠道级 Model 仍写所有成功 key 的并集（语义与原来一致）。
			// 与单 key 路径同样使用短超时 client，避免不可达 endpoint 拖垮整轮同步。
			if ch.AutoSyncKeyModels {
				perKey, err := helper.FetchModelsPerKeyShortTimeout(fgctx, ch)
				if err != nil {
					log.Warnf("failed to fetch models per key for channel %s: %v", ch.Name, err)
					syncFailureTracker.RecordFailure(ch.ID, ch.Name)
					return nil
				}
				if !anyKeyFetchPassed(perKey.Results) {
					// 所有 key 抓取都失败：整个渠道跳过，不碰 DB。
					// 绝不能把 channels.model 或 key 的 SupportedModels 清空，
					// 否则一次上游抖动就会解除全部模型隔离 / 删光渠道模型。
					log.Warnf("all keys failed to fetch models for channel %s, skipping (keeping existing models)", ch.Name)
					syncFailureTracker.RecordFailure(ch.ID, ch.Name)
					return nil
				}
				union := unionKeyModels(perKey.Results, ch.Keys)
				if len(union) == 0 {
					// 有 key 抓取成功但上游返回空列表，且没有可用的旧值：同样跳过，
					// 不写空 Model（写空会连带删除 GroupItem 与价格行）。
					log.Warnf("channel %s fetched an empty model list, skipping", ch.Name)
					syncFailureTracker.RecordFailure(ch.ID, ch.Name)
					return nil
				}
				syncFailureTracker.RecordSuccess(ch.ID)
				fetchMu.Lock()
				fetchResults = append(fetchResults, fetchResult{
					ch:          ch,
					fetchModels: union,
					keyResults:  perKey.Results,
				})
				fetchMu.Unlock()
				return nil
			}

			fetchModels, err := helper.FetchModelsShortTimeout(fgctx, ch)
			if err != nil {
				log.Warnf("failed to fetch models for channel %s: %v", ch.Name, err)
				syncFailureTracker.RecordFailure(ch.ID, ch.Name)
				return nil
			}
			syncFailureTracker.RecordSuccess(ch.ID)
			fetchMu.Lock()
			fetchResults = append(fetchResults, fetchResult{ch: ch, fetchModels: fetchModels})
			fetchMu.Unlock()
			return nil
		})
	}
	_ = fg.Wait()

	// 阶段二：串行处理抓取结果（DB 更新、自动分组、totalNewModels 累加），
	// 避免对共享状态的并发写。
	for _, fr := range fetchResults {
		ch := fr.ch
		fetchModels := fr.fetchModels
		oldModels := xstrings.SplitTrimCompact(",", ch.Model)
		newModels := xstrings.TrimCompact(fetchModels)
		for _, m := range newModels {
			m = strings.TrimSpace(m)
			if m == "" {
				continue
			}
			m = strings.ToLower(m)
			if _, ok := seenTotalNewModels[m]; ok {
				continue
			}
			seenTotalNewModels[m] = struct{}{}
			totalNewModels = append(totalNewModels, m)
		}
		deletedModels, addedModels := diff.Diff(oldModels, newModels)
		modelChanged := len(deletedModels) > 0 || len(addedModels) > 0
		// 逐 key 回填增量：仅开启 AutoSyncKeyModels 的渠道非空。
		keyUpdates := buildKeySupportedModelUpdates(fr.keyResults, ch.Keys)
		// 渠道级 Model 无变化但某个 key 的模型集变了时，仍须落库（只带 KeysToUpdate），
		// 否则“并集不变、分布改变”的场景永远同步不上。
		if modelChanged || len(keyUpdates) > 0 {
			updateReq := &model.ChannelUpdateRequest{ID: ch.ID}
			if modelChanged {
				fetchModelStr := strings.Join(newModels, ",")
				updateReq.Model = &fetchModelStr
			}
			if len(keyUpdates) > 0 {
				updateReq.KeysToUpdate = keyUpdates
			}
			if _, err := channel.Update(updateReq, ctx); err != nil {
				log.Errorf("failed to update channel %s: %v", ch.Name, err)
				continue
			}
		}
		// 批量删除消失的模型对应的 GroupItem
		if len(deletedModels) > 0 {
			log.Infof("deleted channel %s models: %v", ch.Name, deletedModels)
			keys := make([]model.GroupIDAndLLMName, len(deletedModels))
			for i, m := range deletedModels {
				keys[i] = model.GroupIDAndLLMName{ChannelID: ch.ID, ModelName: m}
			}
			if err := group.GroupItemBatchDelByChannelAndModels(keys, ctx); err != nil {
				log.Errorf("failed to batch delete group items for channel %s: %v", ch.Name, err)
			}
		}

		// 自动分组
		if len(newModels) > 0 {
			helper.ChannelAutoGroup(&ch, ctx)
		}
	}
	llmPrice, err := llm.List(ctx)
	if err != nil {
		log.Errorf("failed to list models price: %v", err)
		return
	}
	llmPriceNames := make([]string, 0, len(llmPrice))
	for _, price := range llmPrice {
		llmPriceNames = append(llmPriceNames, price.Name)
	}

	deletedNorm, addedNorm := diff.Diff(llmPriceNames, totalNewModels)
	if len(deletedNorm) > 0 {
		if err := helper.LLMPriceDeleteFromDBWithNoPrice(deletedNorm, ctx); err != nil {
			log.Errorf("failed to batch delete models price: %v", err)
		}
	}
	if len(addedNorm) > 0 {
		if err := helper.LLMPriceAddToDB(addedNorm, ctx); err != nil {
			log.Errorf("failed to add models price: %v", err)
		}
	}
	lastSyncModelsTime = time.Now()
}

func GetLastSyncModelsTime() time.Time {
	return lastSyncModelsTime
}

// keySupportedModelsColumnMax 是自动回填愿意写入的最大字节数。
// 迁移 070 已把 channel_keys.supported_models 从 varchar(512) 放宽为 TEXT。
// MySQL 的 TEXT 上限是 65535 字节，超过后严格模式会拒写，并回滚整个渠道更新
// （连 channels.model 一起丢）。截断到模型边界会让该 key 比实际上更严格，
// 错误地排除本可用的模型，所以超限时跳过回填、保留旧值。
// 512 是旧列宽，公益站一百多个模型就会超过它，导致开关打开却永远不落库。
const keySupportedModelsColumnMax = 60000

// anyKeyFetchPassed 报告是否至少有一个 key 抓取成功。全部失败时渠道整体跳过，
// 既不写 channels.model 也不碰任何 key 的 SupportedModels。
func anyKeyFetchPassed(results []helper.KeyModelResult) bool {
	for _, r := range results {
		if r.Passed {
			return true
		}
	}
	return false
}

// unionKeyModels 计算写回渠道级 channels.model 的模型列表：
//
//   - 所有抓取成功的 key 的模型并集；
//   - 加上抓取失败的 key 已记录的 SupportedModels（保留旧值）。
//
// 第二项是必须的：若只用成功 key 的并集，一次局部上游抖动就会把“只有失败 key
// 支持”的模型从 channels.model 里删掉，连带删除 GroupItem 与价格行（阶段二
// 的 deletedModels 分支），并且那个 key 在下轮成功前完全不可路由。这与
// “抓取失败的 key 保留旧值”的安全策略一致：权限未知时保留既有事实，不做推断。
//
// 不直接用 helper.FetchModelsPerKeyResult.AllModels：后者由 map 遍历得到、
// 顺序不确定，会让 CSV 每轮“伪变化”而反复写库。
func unionKeyModels(results []helper.KeyModelResult, currentKeys []model.ChannelKey) []string {
	supportedByKey := make(map[int]string, len(currentKeys))
	for _, k := range currentKeys {
		supportedByKey[k.ID] = k.SupportedModels
	}

	seen := make(map[string]struct{})
	union := make([]string, 0, 32)
	add := func(models []string) {
		for _, m := range xstrings.TrimCompact(models) {
			if _, ok := seen[m]; ok {
				continue
			}
			seen[m] = struct{}{}
			union = append(union, m)
		}
	}

	for _, r := range results {
		if r.Passed {
			add(r.Models)
			continue
		}
		if old, ok := supportedByKey[r.KeyID]; ok && old != "" {
			add(xstrings.SplitTrimCompact(",", old))
		}
	}
	return union
}

// buildKeySupportedModelUpdates 把逐 key 抓取结果转成 channel_keys 的回填增量。
//
// 安全约束（重要）：
//   - 只处理 Passed=true 的结果；抓取失败的 key 一律跳过，绝不写入、更不清空
//     它的 SupportedModels——上游一次 429/超时就清掉模型隔离会把请求打到
//     不支持该模型的 key 上（上游回 model_not_found）。
//   - 与现值相同时不生成更新项，避免无意义的 DB 写与缓存刷新。
//   - CSV 超过 MySQL TEXT 安全上限时跳过并告警（见 keySupportedModelsColumnMax）。
//
// currentKeys 用于读回旧值做比较；传 nil 时退化为“无条件写入”（仅测试便利）。
func buildKeySupportedModelUpdates(results []helper.KeyModelResult, currentKeys []model.ChannelKey) []model.ChannelKeyUpdateRequest {
	if len(results) == 0 {
		return nil
	}
	oldByID := make(map[int]string, len(currentKeys))
	for _, k := range currentKeys {
		oldByID[k.ID] = k.SupportedModels
	}

	updates := make([]model.ChannelKeyUpdateRequest, 0, len(results))
	for _, r := range results {
		if !r.Passed || r.KeyID <= 0 {
			continue
		}
		csv := strings.Join(xstrings.TrimCompact(r.Models), ",")
		if csv == "" {
			// 抓取“成功”但上游一个模型都没返回：多半是上游 /models 端点异常。
			// 写空串等于“不限”（model.ModelMatches 对空串返回 true），会把隔离
			// 解除掉，比保留旧值危险得多 → 跳过，保留现值。
			log.Warnf("key %d (remark=%q) returned no models, skipping backfill (keeping existing supported_models)",
				r.KeyID, r.KeyRemark)
			continue
		}
		if len(csv) > keySupportedModelsColumnMax {
			log.Warnf("skip per-key model backfill for channel key %d (remark=%q): "+
				"supported_models CSV is %d chars, exceeds column capacity %d; keeping existing value",
				r.KeyID, r.KeyRemark, len(csv), keySupportedModelsColumnMax)
			continue
		}
		if old, ok := oldByID[r.KeyID]; ok && old == csv {
			continue
		}
		supported := csv
		updates = append(updates, model.ChannelKeyUpdateRequest{
			ID:              r.KeyID,
			SupportedModels: &supported,
		})
	}
	return updates
}
