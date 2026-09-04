 package group

 import (
 	"context"
 	"fmt"
 	"math"
 	"strconv"
 	"strings"
 	"sync"
 	"time"

 	"github.com/lingyuins/octopus/internal/db"
 	"github.com/lingyuins/octopus/internal/model"
 	"github.com/lingyuins/octopus/internal/op/setting"
 )

 // multiplierCapMu 串行化 EnforceMultiplierCap 的读-判定-写周期（Seller 移植）。
 // 并发触发点（同步完成/设置变更/手动应用）各自做全表决策，交错执行会基于过期
 // 快照互相覆盖 policy_blocked 终态。低频全量操作，串行化无性能代价。
 var multiplierCapMu sync.Mutex

 // ApplyGroupDefaultsResult 一键应用默认的计数结果（Seller 移植）。
 type ApplyGroupDefaultsResult struct {
 	GroupsUpdated   int64 `json:"groups_updated"`
 	GroupsSuspended int64 `json:"groups_suspended"`
 	GroupsRecovered int64 `json:"groups_recovered"`
 	ItemsBlocked    int64 `json:"items_blocked"`
 	ItemsSorted     int64 `json:"items_sorted"`
 }

 // ConfiguredMultiplierCap 读取倍率上限：0/空/非法=不启用（返回 false）。
 func ConfiguredMultiplierCap() (float64, bool) {
 	raw, err := setting.GetString(model.SettingKeyDefaultMultiplierCap)
 	if err != nil {
 		return 0, false
 	}
 	capValue, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
 	if err != nil || math.IsNaN(capValue) || math.IsInf(capValue, 0) || capValue <= 0 {
 		return 0, false
 	}
 	return capValue, true
 }

 // ValidGroupMultiplier 倍率有效性：有限正数。
 func ValidGroupMultiplier(value float64) bool {
 	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
 }

 // ApplyGroupDefaults 一键应用分组默认：负载模式全量覆盖（空=不动）+ 全部分组按策略重排 +
 // 倍率上限阻断重算（Seller 移植）。
 func ApplyGroupDefaults(ctx context.Context) (*ApplyGroupDefaultsResult, error) {
 	result := &ApplyGroupDefaultsResult{}
 	if mode, ok := configuredDefaultGroupMode(); ok {
 		res := db.GetDB().WithContext(ctx).
 			Model(&model.Group{}).
 			Where("mode != ?", mode).
 			Update("mode", mode)
 		if res.Error != nil {
 			return nil, fmt.Errorf("apply default load balance: %w", res.Error)
 		}
 		result.GroupsUpdated = res.RowsAffected
 	}
 	if strategy, ok := configuredDefaultSortStrategy(); ok {
 		if err := db.GetDB().WithContext(ctx).
 			Model(&model.Group{}).
 			Where("sort_strategy != ?", strategy).
 			Update("sort_strategy", strategy).Error; err != nil {
 			return nil, fmt.Errorf("apply default sort strategy: %w", err)
 		}
 	}
 	// 排序前刷新缓存拿到最新 sort_strategy，再全量重排。
 	if err := refreshAllGroupCaches(ctx); err != nil {
 		return nil, err
 	}
 	groupIDs := make([]int, 0)
 	for _, cached := range groupCache.GetAll() {
 		groupIDs = append(groupIDs, cached.ID)
 	}
 	sorted, err := SortGroupsByStrategy(groupIDs, ctx)
 	if err != nil {
 		return nil, err
 	}
 	result.ItemsSorted = sorted
 	capValue, capEnabled := ConfiguredMultiplierCap()
 	if capEnabled {
 		result.ItemsBlocked = countOverCapItems(ctx, capValue)
 	}
 	blocked, recovered, err := EnforceMultiplierCap(ctx)
 	if err != nil {
 		return nil, err
 	}
 	result.GroupsSuspended = blocked
 	result.GroupsRecovered = recovered
 	return result, nil
 }

 func configuredDefaultGroupMode() (model.GroupMode, bool) {
 	raw, err := setting.GetString(model.SettingKeyDefaultGroupLoadBalance)
 	if err != nil {
 		return 0, false
 	}
 	switch strings.ToLower(strings.TrimSpace(raw)) {
 	case "round_robin":
 		return model.GroupModeRoundRobin, true
 	case "random":
 		return model.GroupModeRandom, true
 	case "failover":
 		return model.GroupModeFailover, true
 	case "weighted":
 		return model.GroupModeWeighted, true
 	case "auto":
 		return model.GroupModeAuto, true
 	default:
 		return 0, false
 	}
 }

 func configuredDefaultSortStrategy() (string, bool) {
 	raw, err := setting.GetString(model.SettingKeyDefaultGroupSortStrategy)
 	if err != nil {
 		return "", false
 	}
 	trimmed := strings.ToLower(strings.TrimSpace(raw))
 	if trimmed == "" {
 		return "", false
 	}
 	switch trimmed {
 	case GroupSortNonRelayBalance, GroupSortNonRelayMultiplier, GroupSortMultiplierBalance, GroupSortBalanceOnly:
 		return trimmed, true
 	default:
 		return "", false
 	}
 }

 func refreshAllGroupCaches(ctx context.Context) error {
 	var groups []model.Group
 	if err := db.GetDB().WithContext(ctx).Preload("Items").Find(&groups).Error; err != nil {
 		return fmt.Errorf("reload groups: %w", err)
 	}
 	groupCache.Clear()
 	for _, group := range groups {
 		group = normalizeGroup(group)
 		groupCache.Set(group.ID, group)
 	}
 	RebuildIndexes()
 	return nil
 }

 // countOverCapItems 统计 known=true 且超 cap 的同步分组数（展示用，不写库）。
 func countOverCapItems(ctx context.Context, capValue float64) int64 {
 	var groups []model.SiteUserGroup
 	if err := db.GetDB().WithContext(ctx).
 		Select("id", "multiplier", "multiplier_known").
 		Where("multiplier IS NOT NULL").
 		Find(&groups).Error; err != nil {
 		return 0
 	}
 	var count int64
 	for _, group := range groups {
 		if group.Multiplier == nil || group.MultiplierKnown == nil || !*group.MultiplierKnown {
 			continue
 		}
 		if !ValidGroupMultiplier(*group.Multiplier) {
 			continue
 		}
 		if *group.Multiplier > capValue {
 			count++
 		}
 	}
 	return count
 }

 // EnforceMultiplierCap 重算倍率上限阻断（Seller 移植）。
 // 两态规则：仅 known=true 且超 cap 才拦；其余一律放行并解阻。
 // 返回 (blocked, recovered)。
 func EnforceMultiplierCap(ctx context.Context) (int64, int64, error) {
 	multiplierCapMu.Lock()
 	defer multiplierCapMu.Unlock()
 	capValue, capEnabled := ConfiguredMultiplierCap()
 	var groups []model.SiteUserGroup
 	if err := db.GetDB().WithContext(ctx).
 		Select("id", "multiplier", "multiplier_known", "policy_blocked", "policy_block_reason").
 		Find(&groups).Error; err != nil {
 		return 0, 0, fmt.Errorf("load multiplier policy groups: %w", err)
 	}
 	var blocked, recovered int64
 	for _, group := range groups {
 		shouldBlock := capEnabled && group.Multiplier != nil && ValidGroupMultiplier(*group.Multiplier) &&
 			group.MultiplierKnown != nil && *group.MultiplierKnown && *group.Multiplier > capValue
 		if shouldBlock {
 			reason := fmt.Sprintf("multiplier exceeds cap (%.4g > %.4g)", *group.Multiplier, capValue)
 			if group.PolicyBlocked && group.PolicyBlockReason == reason {
 				continue
 			}
 			now := time.Now()
 			if err := db.GetDB().WithContext(ctx).Model(&model.SiteUserGroup{}).
 				Where("id = ?", group.ID).
 				Updates(map[string]any{
 					"policy_blocked":      true,
 					"policy_block_reason": reason,
 					"policy_blocked_at":   &now,
 				}).Error; err != nil {
 				return blocked, recovered, fmt.Errorf("block multiplier policy for group %d: %w", group.ID, err)
 			}
 			blocked++
 			continue
 		}
 		if !group.PolicyBlocked {
 			continue
 		}
 		if err := db.GetDB().WithContext(ctx).Model(&model.SiteUserGroup{}).
 			Where("id = ?", group.ID).
 			Updates(map[string]any{
 				"policy_blocked":      false,
 				"policy_block_reason": "",
 				"policy_blocked_at":   nil,
 			}).Error; err != nil {
 			return blocked, recovered, fmt.Errorf("recover multiplier policy for group %d: %w", group.ID, err)
 		}
 		recovered++
 	}
 	return blocked, recovered, nil
 }
