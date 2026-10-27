package group

import (
	"testing"

	"github.com/lingyuins/octopus/internal/model"
)

// GroupItemUpdateRequest.RelayRetryCountOverride 的 JSON 解码契约
// （CFG-02）：字段缺失 = 保持既有值；显式 null = 清空；数值 = 设置。
// group_stability_test.go 已覆盖写库路径，这里只补解码层三态断言，
// 防止未来把 Raw 语义改丢。
func TestDecodeGroupItemUpdateRequestOverrideThreeStates(t *testing.T) {
	existing := model.GroupItem{ID: 7, Priority: 2, Weight: 1, RelayRetryCountOverride: intPtr(3)}

	missing := model.GroupItemUpdateRequest{ID: 7, Priority: 1, Weight: 1}
	missing.RelayRetryCountOverride = decodeOverrideForTest(t, existing, missing.RelayRetryCountOverrideRaw)
	if missing.RelayRetryCountOverride == nil || *missing.RelayRetryCountOverride != 3 {
		t.Fatalf("missing raw should keep existing override 3, got %v", missing.RelayRetryCountOverride)
	}

	explicitNull := model.GroupItemUpdateRequest{ID: 7, Priority: 1, Weight: 1, RelayRetryCountOverrideRaw: []byte("null")}
	explicitNull.RelayRetryCountOverride = decodeOverrideForTest(t, existing, explicitNull.RelayRetryCountOverrideRaw)
	if explicitNull.RelayRetryCountOverride != nil {
		t.Fatalf("explicit null should clear override, got %v", *explicitNull.RelayRetryCountOverride)
	}

	zero := model.GroupItemUpdateRequest{ID: 7, Priority: 1, Weight: 1, RelayRetryCountOverrideRaw: []byte("0")}
	zero.RelayRetryCountOverride = decodeOverrideForTest(t, existing, zero.RelayRetryCountOverrideRaw)
	if zero.RelayRetryCountOverride == nil || *zero.RelayRetryCountOverride != 0 {
		t.Fatalf("zero should set override 0, got %v", zero.RelayRetryCountOverride)
	}
}

func decodeOverrideForTest(t *testing.T, existing model.GroupItem, raw []byte) *int {
	t.Helper()
	item := model.GroupItemUpdateRequest{ID: existing.ID, RelayRetryCountOverrideRaw: raw}
	normalized := normalizeGroupItemUpdateRequests([]model.GroupItem{existing}, []model.GroupItemUpdateRequest{item})
	if len(normalized) != 1 {
		t.Fatal("normalizeGroupItemUpdateRequests dropped item")
	}
	return normalized[0].RelayRetryCountOverride
}

func intPtr(v int) *int { return &v }
