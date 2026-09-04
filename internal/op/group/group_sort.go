 package group

 import (
 	"context"
 	"fmt"
 	"sort"
 	"strings"

 	"github.com/lingyuins/octopus/internal/db"
 	"github.com/lingyuins/octopus/internal/model"
 	"github.com/lingyuins/octopus/internal/op/channel"
 	"github.com/lingyuins/octopus/internal/op/setting"
 	"github.com/lingyuins/octopus/internal/utils/log"
 )

 // 分组内成员排序策略（Seller 移植）。
 const (
 	// GroupSortNonRelayBalance 非备用按余额降序，备用沉底后按倍率升序+余额降序。
 	GroupSortNonRelayBalance = "non_relay_balance"
 	// GroupSortNonRelayMultiplier 非备用按倍率升序+余额降序，备用沉底后按倍率升序。
 	GroupSortNonRelayMultiplier = "non_relay_multiplier"
 	// GroupSortMultiplierBalance 全量按倍率升序，倍率相同按余额降序。
 	GroupSortMultiplierBalance = "multiplier_balance"
 	// GroupSortBalanceOnly 全量按余额降序。
 	GroupSortBalanceOnly = "balance_only"
 )

 // groupSortItem 排序用的富化条目：余额来自站点账号，倍率只采信 known=true 的同步值。
 type groupSortItem struct {
 	item       model.GroupItem
 	isReserve  bool
 	balance    float64
 	multiplier float64
 }

 // NormalizeGroupSortStrategy 归一化排序策略名：空/未知一律回退 non_relay_balance。
 func NormalizeGroupSortStrategy(raw string) string {
 	switch strings.ToLower(strings.TrimSpace(raw)) {
 	case GroupSortNonRelayBalance:
 		return GroupSortNonRelayBalance
 	case GroupSortNonRelayMultiplier:
 		return GroupSortNonRelayMultiplier
 	case GroupSortMultiplierBalance:
 		return GroupSortMultiplierBalance
 	case GroupSortBalanceOnly:
 		return GroupSortBalanceOnly
 	default:
 		return GroupSortNonRelayBalance
 	}
 }

 // SortEnrichedGroupItems 按策略对富化条目原地排序（纯函数，单测直接覆盖）。
 func SortEnrichedGroupItems(items []groupSortItem, strategy string) {
 	switch NormalizeGroupSortStrategy(strategy) {
 	case GroupSortNonRelayMultiplier:
 		sort.SliceStable(items, func(i, j int) bool {
 			leftTier, rightTier := groupSortTier(items[i].isReserve), groupSortTier(items[j].isReserve)
 			if leftTier != rightTier {
 				return leftTier < rightTier
 			}
 			if leftTier == 0 {
 				if items[i].multiplier != items[j].multiplier {
 					return items[i].multiplier < items[j].multiplier
 				}
 				return items[i].balance > items[j].balance
 			}
 			return items[i].multiplier < items[j].multiplier
 		})
 	case GroupSortMultiplierBalance:
 		sort.SliceStable(items, func(i, j int) bool {
 			if items[i].multiplier != items[j].multiplier {
 				return items[i].multiplier < items[j].multiplier
 			}
 			return items[i].balance > items[j].balance
 		})
 	case GroupSortBalanceOnly:
 		sort.SliceStable(items, func(i, j int) bool {
 			return items[i].balance > items[j].balance
 		})
 	default:
 		sort.SliceStable(items, func(i, j int) bool {
 			leftTier, rightTier := groupSortTier(items[i].isReserve), groupSortTier(items[j].isReserve)
 			if leftTier != rightTier {
 				return leftTier < rightTier
 			}
 			if leftTier == 0 {
 				return items[i].balance > items[j].balance
 			}
 			if items[i].multiplier != items[j].multiplier {
 				return items[i].multiplier < items[j].multiplier
 			}
 			return items[i].balance > items[j].balance
 		})
 	}
 }

 func groupSortTier(isReserve bool) int {
 	if isReserve {
 		return 1
 	}
 	return 0
 }

 // SortGroupsByStrategy 按各自分组 sort_strategy（空则跟随全局）重排成员并回写 priority。
 // 余额取绑定站点账号的 balance，倍率只采信 multiplier_known=true 的同步值（未知按 1x）。
 // 返回实际改写 priority 的条目数。
 func SortGroupsByStrategy(groupIDs []int, ctx context.Context) (int64, error) {
 	uniqueIDs := dedupeGroupSortIDs(groupIDs)
 	if len(uniqueIDs) == 0 {
 		return 0, nil
 	}
 	globalStrategy := GroupSortNonRelayBalance
 	if raw, err := setting.GetString(model.SettingKeyDefaultGroupSortStrategy); err == nil {
 		globalStrategy = NormalizeGroupSortStrategy(raw)
 	}
 	groups := make([]model.Group, 0, len(uniqueIDs))
 	for _, id := range uniqueIDs {
 		cached, ok := groupCache.Get(id)
 		if !ok || len(cached.Items) == 0 {
 			continue
 		}
 		groups = append(groups, cached)
 	}
 	if len(groups) == 0 {
 		return 0, nil
 	}
 	bindingMap := loadChannelBindings(uniqueIDs, groups)
 	balanceByAccount := loadAccountBalances(bindingMap, ctx)
 	multiplierByChannel := loadChannelMultipliers(bindingMap, ctx)
 	var totalSorted int64
 	for _, group := range groups {
 		strategy := strings.TrimSpace(group.SortStrategy)
 		if strategy == "" {
 			strategy = globalStrategy
 		}
		items := make([]groupSortItem, 0, len(group.Items))
		for _, item := range group.Items {
			multiplier, ok := multiplierByChannel[item.ChannelID]
			if !ok {
				multiplier = 1
			}
			items = append(items, groupSortItem{
				item:       item,
				isReserve:  resolveChannelIsReserve(item.ChannelID),
				balance:    balanceByAccount[bindingAccountID(bindingMap, item.ChannelID)],
				multiplier: multiplier,
			})
		}
 		SortEnrichedGroupItems(items, strategy)
 		for index, enriched := range items {
 			newPriority := index + 1
 			if enriched.item.Priority == newPriority {
 				continue
 			}
 			if err := db.GetDB().WithContext(ctx).
 				Model(&model.GroupItem{}).
 				Where("id = ?", enriched.item.ID).
 				Update("priority", newPriority).Error; err != nil {
 				return totalSorted, fmt.Errorf("update priority for item %d: %w", enriched.item.ID, err)
 			}
 			totalSorted++
 		}
 	}
 	for _, id := range uniqueIDs {
 		if err := RefreshCacheByID(id, ctx); err != nil {
 			log.Warnf("SortGroupsByStrategy: refresh group %d cache failed: %v", id, err)
 		}
 	}
 	return totalSorted, nil
 }

 func dedupeGroupSortIDs(groupIDs []int) []int {
 	seen := make(map[int]struct{}, len(groupIDs))
 	unique := make([]int, 0, len(groupIDs))
 	for _, id := range groupIDs {
 		if id <= 0 {
 			continue
 		}
 		if _, ok := seen[id]; ok {
 			continue
 		}
 		seen[id] = struct{}{}
 		unique = append(unique, id)
 	}
 	return unique
 }

 // loadChannelBindings 收集这些分组全部成员渠道的站点绑定（无绑定=自建渠道，余额/倍率为零值）。
 func loadChannelBindings(_ []int, groups []model.Group) map[int]model.SiteChannelBinding {
 	channelIDs := make([]int, 0)
 	seen := make(map[int]struct{})
 	for _, group := range groups {
 		for _, item := range group.Items {
 			if item.ChannelID <= 0 {
 				continue
 			}
 			if _, ok := seen[item.ChannelID]; ok {
 				continue
 			}
 			seen[item.ChannelID] = struct{}{}
 			channelIDs = append(channelIDs, item.ChannelID)
 		}
 	}
 	if len(channelIDs) == 0 {
 		return map[int]model.SiteChannelBinding{}
 	}
 	var bindings []model.SiteChannelBinding
 	if err := db.GetDB().Where("channel_id IN ?", channelIDs).Find(&bindings).Error; err != nil {
 		log.Warnf("SortGroupsByStrategy: load channel bindings failed: %v", err)
 		return map[int]model.SiteChannelBinding{}
 	}
 	result := make(map[int]model.SiteChannelBinding, len(bindings))
 	for _, binding := range bindings {
 		result[binding.ChannelID] = binding
 	}
 	return result
 }

 func bindingAccountID(bindingMap map[int]model.SiteChannelBinding, channelID int) int {
 	if binding, ok := bindingMap[channelID]; ok {
 		return binding.SiteAccountID
 	}
 	return 0
 }

 // loadAccountBalances 按站点账号查 balance（查不到按 0 处理，不阻断排序）。
 func loadAccountBalances(bindingMap map[int]model.SiteChannelBinding, ctx context.Context) map[int]float64 {
 	result := make(map[int]float64, len(bindingMap))
 	accountIDs := make([]int, 0, len(bindingMap))
 	seen := make(map[int]struct{})
 	for _, binding := range bindingMap {
 		if binding.SiteAccountID <= 0 {
 			continue
 		}
 		if _, ok := seen[binding.SiteAccountID]; ok {
 			continue
 		}
 		seen[binding.SiteAccountID] = struct{}{}
 		accountIDs = append(accountIDs, binding.SiteAccountID)
 	}
 	if len(accountIDs) == 0 {
 		return result
 	}
 	var rows []struct {
 		ID      int     `gorm:"column:id"`
 		Balance float64 `gorm:"column:balance"`
 	}
 	if err := db.GetDB().WithContext(ctx).Table("site_accounts").Select("id, balance").Where("id IN ?", accountIDs).Scan(&rows).Error; err != nil {
 		log.Warnf("SortGroupsByStrategy: load account balances failed: %v", err)
 		return result
 	}
 	for _, row := range rows {
 		result[row.ID] = row.Balance
 	}
 	return result
 }

 // loadChannelMultipliers 按绑定查同步的分组倍率，只采信 known=true（未知按 1x）。
 func loadChannelMultipliers(bindingMap map[int]model.SiteChannelBinding, ctx context.Context) map[int]float64 {
 	result := make(map[int]float64, len(bindingMap))
 	type accountGroupKey struct {
 		accountID int
 		groupKey  string
 	}
 	keys := make([]accountGroupKey, 0, len(bindingMap))
 	seen := make(map[accountGroupKey]struct{})
 	for _, binding := range bindingMap {
 		key := accountGroupKey{accountID: binding.SiteAccountID, groupKey: model.NormalizeSiteGroupKey(binding.GroupKey)}
 		if _, ok := seen[key]; ok {
 			continue
 		}
 		seen[key] = struct{}{}
 		keys = append(keys, key)
 	}
 	if len(keys) == 0 {
 		return result
 	}
 	var groups []model.SiteUserGroup
 	query := db.GetDB().WithContext(ctx).
 		Select("site_account_id, group_key, multiplier, multiplier_known").
 		Where("multiplier IS NOT NULL")
 	conditions := make([]string, 0, len(keys))
 	arguments := make([]any, 0, len(keys)*2)
 	for _, key := range keys {
 		conditions = append(conditions, "(site_account_id = ? AND group_key = ?)")
 		arguments = append(arguments, key.accountID, key.groupKey)
 	}
 	if err := query.Where(strings.Join(conditions, " OR "), arguments...).Find(&groups).Error; err != nil {
 		log.Warnf("SortGroupsByStrategy: load group multipliers failed: %v", err)
 		return result
 	}
 	multiplierByAccountGroup := make(map[accountGroupKey]float64, len(groups))
 	for _, group := range groups {
 		if group.Multiplier == nil || group.MultiplierKnown == nil || !*group.MultiplierKnown {
 			continue
 		}
 		if *group.Multiplier <= 0 {
 			continue
 		}
 		multiplierByAccountGroup[accountGroupKey{accountID: group.SiteAccountID, groupKey: model.NormalizeSiteGroupKey(group.GroupKey)}] = *group.Multiplier
 	}
 	for channelID, binding := range bindingMap {
 		if value, ok := multiplierByAccountGroup[accountGroupKey{accountID: binding.SiteAccountID, groupKey: model.NormalizeSiteGroupKey(binding.GroupKey)}]; ok {
 			result[channelID] = value
 		} else {
 			result[channelID] = 1
 		}
 	}
 	return result
 }

 func resolveChannelIsReserve(channelID int) bool {
 	if cached, ok := channel.GetCache().Get(channelID); ok {
 		return cached.IsReserve
 	}
 	return false
 }
