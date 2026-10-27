package group

import (
	"context"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
)

// 现实 DB roundtrip 回归：Auto 分组默认思考档位经由 SQLite 真库 +
// GroupCreate/GroupGet/GroupList/GroupUpdate 全链路保留。
// 之前字段丢失故障发生在 GroupList（缓存快照层），必须覆盖列表读取。
func TestGroupDefaultReasoningEffortDBRoundtrip(t *testing.T) {
	ctx := initGroupEndpointNameTestDB(t)

	forceTrue, forceFalse := true, false
	specific := " High "
	auto := "  AUTO  "
	cases := []struct {
		name           string
		create         string
		wantCreate     string
		update         *string
		wantAfterPatch string
		force          *bool
		wantForce      bool
	}{
		{name: "auto", create: auto, wantCreate: "auto", update: &auto, wantAfterPatch: "auto", force: &forceTrue, wantForce: true},
		{name: "specific", create: specific, wantCreate: "high", update: &specific, wantAfterPatch: "high", force: &forceFalse, wantForce: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created := &model.Group{
				Name:                   "reasoning-" + tc.name,
				EndpointType:           model.EndpointTypeChat,
				Mode:                   model.GroupModeRandom,
				DefaultReasoningEffort: tc.create,
				ReasoningForceOverride: *tc.force,
			}
			if err := GroupCreate(created, ctx); err != nil {
				t.Fatalf("GroupCreate() error = %v", err)
			}
			if created.DefaultReasoningEffort != tc.wantCreate {
				t.Fatalf("created effort = %q, want %q", created.DefaultReasoningEffort, tc.wantCreate)
			}
			if created.ReasoningForceOverride != tc.wantForce {
				t.Fatalf("created force = %v, want %v", created.ReasoningForceOverride, tc.wantForce)
			}

			// 缓存读取（GroupGet/GroupList）与 DB 读回（RefreshAllCache）
			// 双路断言，覆盖此前字段丢失的列表路径。
			got, err := GroupGet(created.ID, ctx)
			if err != nil {
				t.Fatalf("GroupGet() error = %v", err)
			}
			assertRoundtripGroup(t, got, tc.wantCreate, tc.wantForce)

			listed, err := GroupList(ctx)
			if err != nil {
				t.Fatalf("GroupList() error = %v", err)
			}
			var listedGroup *model.Group
			for i := range listed {
				if listed[i].ID == created.ID {
					listedGroup = &listed[i]
					break
				}
			}
			if listedGroup == nil {
				t.Fatalf("GroupList() missing group id %d", created.ID)
			}
			assertRoundtripGroup(t, listedGroup, tc.wantCreate, tc.wantForce)

			if err := RefreshAllCache(ctx); err != nil {
				t.Fatalf("RefreshAllCache() error = %v", err)
			}
			reloaded, err := GroupGet(created.ID, ctx)
			if err != nil {
				t.Fatalf("GroupGet() after reload error = %v", err)
			}
			assertRoundtripGroup(t, reloaded, tc.wantCreate, tc.wantForce)

			// PATCH 契约：DefaultReasoningEffort 存在时归一化并写入；
			// ReasoningForceOverride 存在时写入。
			updated, err := GroupUpdate(&model.GroupUpdateRequest{
				ID:                     created.ID,
				DefaultReasoningEffort: tc.update,
				ReasoningForceOverride: tc.force,
			}, ctx)
			if err != nil {
				t.Fatalf("GroupUpdate() error = %v", err)
			}
			assertRoundtripGroup(t, updated, tc.wantAfterPatch, tc.wantForce)
			assertStoredRoundtrip(t, ctx, created.ID, tc.wantAfterPatch, tc.wantForce)
		})
	}
}

// PATCH 缺失字段契约：请求里不带 DefaultReasoningEffort / ReasoningForceOverride
// 时必须原值保留；显式空串清空档位、显式 false 关闭强制。
func TestGroupDefaultReasoningEffortPatchPresence(t *testing.T) {
	ctx := initGroupEndpointNameTestDB(t)

	created := &model.Group{
		Name:                   "reasoning-presence",
		EndpointType:           model.EndpointTypeChat,
		Mode:                   model.GroupModeRandom,
		DefaultReasoningEffort: "high",
		ReasoningForceOverride: true,
	}
	if err := GroupCreate(created, ctx); err != nil {
		t.Fatalf("GroupCreate() error = %v", err)
	}

	renamed := "reasoning-presence-renamed"
	updated, err := GroupUpdate(&model.GroupUpdateRequest{ID: created.ID, Name: &renamed}, ctx)
	if err != nil {
		t.Fatalf("GroupUpdate() error = %v", err)
	}
	assertRoundtripGroup(t, updated, "high", true)
	assertStoredRoundtrip(t, ctx, created.ID, "high", true)

	empty := ""
	forceFalse := false
	updated, err = GroupUpdate(&model.GroupUpdateRequest{
		ID:                     created.ID,
		DefaultReasoningEffort: &empty,
		ReasoningForceOverride: &forceFalse,
	}, ctx)
	if err != nil {
		t.Fatalf("GroupUpdate() clear error = %v", err)
	}
	assertRoundtripGroup(t, updated, "", false)
	assertStoredRoundtrip(t, ctx, created.ID, "", false)
}

// ReasoningForceOverride=false 的写库验证：GORM 结构体 Updates 在
// Select 显式列出字段时写 false 不会因零值跳过；该断言防止未来
// 有人改成隐式 struct update 导致 false 被 default:true 或零值跳过。
func assertStoredRoundtrip(t *testing.T, ctx context.Context, id int, effort string, force bool) {
	t.Helper()
	var stored model.Group
	if err := db.GetDB().WithContext(ctx).First(&stored, id).Error; err != nil {
		t.Fatalf("load stored group: %v", err)
	}
	assertRoundtripGroup(t, &stored, effort, force)
}

func assertRoundtripGroup(t *testing.T, got *model.Group, effort string, force bool) {
	t.Helper()
	if got.DefaultReasoningEffort != effort {
		t.Fatalf("DefaultReasoningEffort = %q, want %q", got.DefaultReasoningEffort, effort)
	}
	if got.ReasoningForceOverride != force {
		t.Fatalf("ReasoningForceOverride = %v, want %v", got.ReasoningForceOverride, force)
	}
}
