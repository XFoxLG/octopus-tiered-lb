 package group

 import (
 	"testing"

 	"github.com/lingyuins/octopus/internal/model"
 )

 func makeSortItem(id int, reserve bool, balance float64, multiplier float64) groupSortItem {
 	return groupSortItem{
 		item:       model.GroupItem{ID: id, ChannelID: id, ModelName: "gpt-4o", Priority: id},
 		isReserve:  reserve,
 		balance:    balance,
 		multiplier: multiplier,
 	}
 }

 func sortItemIDs(items []groupSortItem) []int {
 	ids := make([]int, len(items))
 	for i, item := range items {
 		ids[i] = item.item.ID
 	}
 	return ids
 }

 func equalSortIDs(got []groupSortItem, want []int) bool {
 	if len(got) != len(want) {
 		return false
 	}
 	for i, item := range got {
 		if item.item.ID != want[i] {
 			return false
 		}
 	}
 	return true
 }

 // non_relay_balance：非备用按余额降序，备用沉底后按倍率升序。
 func TestSortNonRelayBalance(t *testing.T) {
 	items := []groupSortItem{
 		makeSortItem(1, false, 10, 2),
 		makeSortItem(2, false, 50, 5),
 		makeSortItem(3, true, 999, 1),
 		makeSortItem(4, true, 1, 0.5),
 	}
 	SortEnrichedGroupItems(items, GroupSortNonRelayBalance)
 	// 非备用：2(50) > 1(10)；备用沉底按倍率：4(0.5x) > 3(1x)。
 	if !equalSortIDs(items, []int{2, 1, 4, 3}) {
 		t.Fatalf("unexpected order: %v", sortItemIDs(items))
 	}
 }

 // non_relay_multiplier：非备用按倍率升序，备用沉底。
 func TestSortNonRelayMultiplier(t *testing.T) {
 	items := []groupSortItem{
 		makeSortItem(1, false, 10, 2),
 		makeSortItem(2, false, 50, 0.5),
 		makeSortItem(3, true, 999, 0.1),
 	}
 	SortEnrichedGroupItems(items, GroupSortNonRelayMultiplier)
 	// 非备用按倍率：2(0.5x) > 1(2x)；备用 3 沉底（倍率再低也排后）。
 	if !equalSortIDs(items, []int{2, 1, 3}) {
 		t.Fatalf("unexpected order: %v", sortItemIDs(items))
 	}
 }

 // multiplier_balance：全量按倍率升序，相同按余额降序。
 func TestSortMultiplierBalance(t *testing.T) {
 	items := []groupSortItem{
 		makeSortItem(1, false, 10, 2),
 		makeSortItem(2, false, 50, 0.5),
 		makeSortItem(3, false, 99, 0.5),
 	}
 	SortEnrichedGroupItems(items, GroupSortMultiplierBalance)
 	if !equalSortIDs(items, []int{3, 2, 1}) {
 		t.Fatalf("unexpected order: %v", sortItemIDs(items))
 	}
 }

 // balance_only：全量按余额降序，备用不沉底。
 func TestSortBalanceOnly(t *testing.T) {
 	items := []groupSortItem{
 		makeSortItem(1, true, 999, 9),
 		makeSortItem(2, false, 10, 0.1),
 	}
 	SortEnrichedGroupItems(items, GroupSortBalanceOnly)
 	if !equalSortIDs(items, []int{1, 2}) {
 		t.Fatalf("unexpected order: %v", sortItemIDs(items))
 	}
 }

 // 空/未知策略回退 non_relay_balance。
 func TestSortUnknownStrategyFallsBack(t *testing.T) {
 	items := []groupSortItem{
 		makeSortItem(1, false, 10, 1),
 		makeSortItem(2, false, 50, 1),
 	}
 	SortEnrichedGroupItems(items, "not-a-strategy")
 	if !equalSortIDs(items, []int{2, 1}) {
 		t.Fatalf("unexpected order: %v", sortItemIDs(items))
 	}
 	if NormalizeGroupSortStrategy("") != GroupSortNonRelayBalance {
 		t.Fatal("empty strategy should normalize to non_relay_balance")
 	}
 }

 // 倍率上限两态：仅 known=true 且超 cap 才拦。
 func TestMultiplierBlockTwoState(t *testing.T) {
 	shouldBlock := func(known bool, multiplier float64, cap float64) bool {
 		knownPtr := known
 		group := model.SiteUserGroup{Multiplier: &multiplier, MultiplierKnown: &knownPtr}
 		return group.Multiplier != nil && ValidGroupMultiplier(*group.Multiplier) &&
 			group.MultiplierKnown != nil && *group.MultiplierKnown && *group.Multiplier > cap
 	}
 	if !shouldBlock(true, 2, 1.5) {
 		t.Fatal("known=true over cap should block")
 	}
 	if shouldBlock(false, 99, 1.5) {
 		t.Fatal("known=false should never block even at 99x")
 	}
 	// 免费分组 0x 不超 cap，不拦。
 	if shouldBlock(true, 0, 1.5) {
 		t.Fatal("free 0x group should not block")
 	}
 }

 // 设置校验：三个新键的合法/非法值。
 func TestGroupSortSettingValidation(t *testing.T) {
 	valid := []model.Setting{
 		{Key: model.SettingKeyDefaultGroupLoadBalance, Value: ""},
 		{Key: model.SettingKeyDefaultGroupLoadBalance, Value: "failover"},
 		{Key: model.SettingKeyDefaultGroupSortStrategy, Value: ""},
 		{Key: model.SettingKeyDefaultGroupSortStrategy, Value: "multiplier_balance"},
 		{Key: model.SettingKeyDefaultMultiplierCap, Value: "0"},
 		{Key: model.SettingKeyDefaultMultiplierCap, Value: "1.5"},
 	}
 	for _, setting := range valid {
 		if err := setting.Validate(); err != nil {
 			t.Fatalf("setting %s=%q should be valid: %v", setting.Key, setting.Value, err)
 		}
 	}
 	invalid := []model.Setting{
 		{Key: model.SettingKeyDefaultGroupLoadBalance, Value: "turbo"},
 		{Key: model.SettingKeyDefaultGroupSortStrategy, Value: "cheapest"},
 		{Key: model.SettingKeyDefaultMultiplierCap, Value: "-1"},
 		{Key: model.SettingKeyDefaultMultiplierCap, Value: "NaN"},
 		{Key: model.SettingKeyDefaultMultiplierCap, Value: "abc"},
 	}
 	for _, setting := range invalid {
 		if err := setting.Validate(); err == nil {
 			t.Fatalf("setting %s=%q should be invalid", setting.Key, setting.Value)
 		}
 	}
 }
