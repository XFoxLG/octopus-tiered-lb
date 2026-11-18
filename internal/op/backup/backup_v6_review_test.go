package backup

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	utilsjson "github.com/lingyuins/octopus/internal/utils/json"
)

func TestLegacyBackupPreservesTargetWebAuthnSettings(t *testing.T) {
	for _, mode := range []string{model.ImportModeIncremental, model.ImportModeFull} {
		t.Run(mode, func(t *testing.T) {
			target := openChannelUIDBackupTarget(t, "legacy-webauthn-"+mode+".db")
			targetSettings := []model.Setting{
				{Key: model.SettingKeyWebAuthnRPID, Value: "target.example"},
				{Key: model.SettingKeyWebAuthnRPName, Value: "Target"},
				{Key: model.SettingKeyWebAuthnOrigins, Value: "https://target.example"},
				{Key: model.SettingKeyProxyURL, Value: "old-proxy"},
			}
			if err := target.Create(&targetSettings).Error; err != nil {
				t.Fatal(err)
			}
			dump := &model.DBDump{
				Version: 5,
				Settings: []model.Setting{
					{Key: model.SettingKeyWebAuthnRPID, Value: "source.example"},
					{Key: model.SettingKeyWebAuthnRPName, Value: "Source"},
					{Key: model.SettingKeyWebAuthnOrigins, Value: "https://source.example"},
					{Key: model.SettingKeyProxyURL, Value: "new-proxy"},
				},
			}
			result, err := ImportWithModeToDB(context.Background(), target, dump, mode)
			if err != nil {
				t.Fatal(err)
			}
			var settings []model.Setting
			if err := target.Find(&settings).Error; err != nil {
				t.Fatal(err)
			}
			got := make(map[model.SettingKey]string, len(settings))
			for _, setting := range settings {
				got[setting.Key] = setting.Value
			}
			if got[model.SettingKeyWebAuthnRPID] != "target.example" ||
				got[model.SettingKeyWebAuthnRPName] != "Target" ||
				got[model.SettingKeyWebAuthnOrigins] != "https://target.example" ||
				got[model.SettingKeyProxyURL] != "new-proxy" {
				t.Fatalf("legacy import changed target identity or missed ordinary setting: %+v", got)
			}
			foundWarning := false
			for _, warning := range result.Warnings {
				foundWarning = foundWarning || strings.Contains(warning, "WebAuthn")
			}
			if !foundWarning {
				t.Fatalf("legacy import omitted identity warning: %+v", result.Warnings)
			}
		})
	}
}

func TestV6FullRestoreGeneratesMissingChannelUID(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-full-generates-channel-uid.db")
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"channels", "channel_keys", "group_items"},
		Channels: []model.Channel{{
			ID: 17, Name: "restored", Type: outbound.OutboundTypeOpenAIChat,
		}},
		ChannelKeys: []model.ChannelKey{},
		GroupItems:  []model.GroupItem{},
	}
	if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull); err != nil {
		t.Fatal(err)
	}
	var restored model.Channel
	if err := target.First(&restored, 17).Error; err != nil {
		t.Fatal(err)
	}
	var defaultGroup model.ChannelGroup
	if err := target.Where("is_default = ?", true).First(&defaultGroup).Error; err != nil {
		t.Fatal(err)
	}
	if restored.GroupID != defaultGroup.ID {
		t.Fatalf("restored channel group id=%d, want target default group %d", restored.GroupID, defaultGroup.ID)
	}
	if _, err := uuid.Parse(restored.ChannelUID); err != nil {
		t.Fatalf("full restore did not persist a generated channel_uid: %q (%v)", restored.ChannelUID, err)
	}
}

func TestV6IncrementalImportCannotChangeExistingChannelUID(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-channel-uid-immutable.db")
	existingUID := uuid.NewString()
	if err := target.Create(&model.Channel{
		ID: 6, Name: "channel", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: existingUID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	var patch model.DBDump
	raw := `{"version":6,"sections":["channels"],"channels":[{"id":6,"channel_uid":"` + uuid.NewString() + `"}]}`
	if err := utilsjson.Unmarshal([]byte(raw), &patch); err != nil {
		t.Fatal(err)
	}
	_, err := ImportWithModeToDB(context.Background(), target, &patch, model.ImportModeIncremental)
	if err == nil {
		t.Fatal("channel_uid patch changed an existing stable identity")
	}
	var got model.Channel
	if err := target.First(&got, 6).Error; err != nil {
		t.Fatal(err)
	}
	if got.ChannelUID != existingUID {
		t.Fatalf("rejected patch changed stable identity: %q", got.ChannelUID)
	}
}

func TestV6IncrementalImportRejectsAssigningUIDToExistingLegacyChannel(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-channel-uid-legacy-adoption.db")
	if err := target.Create(&model.Channel{
		ID: 7, Name: "legacy", Type: outbound.OutboundTypeOpenAIChat,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.Exec("UPDATE channels SET channel_uid = '' WHERE id = ?", 7).Error; err != nil {
		t.Fatal(err)
	}
	var before model.Channel
	if err := target.First(&before, 7).Error; err != nil {
		t.Fatal(err)
	}
	if before.ChannelUID != "" {
		t.Fatalf("legacy fixture channel_uid=%q, want empty", before.ChannelUID)
	}
	uid := uuid.NewString()
	var patch model.DBDump
	raw := `{"version":6,"sections":["channels"],"channels":[{"id":7,"channel_uid":"` + uid + `"}]}`
	if err := utilsjson.Unmarshal([]byte(raw), &patch); err != nil {
		t.Fatal(err)
	}
	_, err := ImportWithModeToDB(context.Background(), target, &patch, model.ImportModeIncremental)
	if err == nil || !strings.Contains(err.Error(), "channel_uid is immutable") {
		t.Fatalf("legacy channel UID patch error=%v, want immutable identity diagnostic", err)
	}
	var got model.Channel
	if err := target.First(&got, 7).Error; err != nil {
		t.Fatal(err)
	}
	if got.ChannelUID != "" {
		t.Fatalf("rejected import changed legacy channel UID: %q", got.ChannelUID)
	}
}

func TestV6UnownedAndNullSectionsAreNotMappedOrRewritten(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-ignored-channel-sections.db")
	channelUID := uuid.NewString()
	if err := target.Create(&model.Channel{
		ID: 9, Name: "target", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: channelUID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.Create(&model.Group{ID: 3, Name: "group", Mode: model.GroupModeFailover}).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"channels", "group_items", "channel_model_limits"},
		Channels: []model.Channel{{
			ID: 5, Name: "source", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: channelUID,
		}},
		ChannelKeys: []model.ChannelKey{{
			ID: 42, ChannelID: 999, ChannelKey: "ignored-section",
		}},
		GroupItems: []model.GroupItem{{
			ID: 11, GroupID: 3, ChannelID: 5, ModelName: "mapped",
		}},
		// Listed null sections are also untouched and must not participate in
		// channel ID validation or rewriting.
		ChannelModelLimits: nil,
	}
	if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental); err != nil {
		t.Fatal(err)
	}
	var item model.GroupItem
	if err := target.First(&item, 11).Error; err != nil {
		t.Fatal(err)
	}
	if item.ChannelID != 9 {
		t.Fatalf("owned group item channel_id=%d, want 9", item.ChannelID)
	}
	var keyCount int64
	if err := target.Model(&model.ChannelKey{}).Count(&keyCount).Error; err != nil {
		t.Fatal(err)
	}
	if keyCount != 0 {
		t.Fatalf("unowned channel_keys rows were imported: %d", keyCount)
	}
	var limitCount int64
	if err := target.Model(&model.ChannelModelLimit{}).Count(&limitCount).Error; err != nil {
		t.Fatal(err)
	}
	if limitCount != 0 {
		t.Fatalf("listed null channel_model_limits changed target rows: %d", limitCount)
	}
}

func TestV6ChannelMappingRewritesAllOwnedDerivedReferences(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-all-channel-references.db")
	if err := target.AutoMigrate(&model.GroupHealthSnapshot{}, &model.GroupHealthAttempt{}); err != nil {
		t.Fatal(err)
	}
	channelUID := uuid.NewString()
	if err := target.Create(&model.Channel{
		ID: 9, Name: "target", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: channelUID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 11, 19, 1, 2, 3, 0, time.UTC)
	successfulChannelID := 5
	dump := &model.DBDump{
		Version: 6,
		Sections: []string{
			"channels", "stats_channel", "stats_model", "stats_daily_channel",
			"stats_daily_channel_model", "runtime_states", "circuit_breaker_states",
			"group_health_snapshots", "group_health_attempts", "ai_route_tasks",
		},
		Channels: []model.Channel{{
			ID: 5, Name: "source", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: channelUID,
		}},
		StatsChannel: []model.StatsChannel{{ChannelID: 5}},
		StatsModel: []model.StatsModel{{
			ID: statsModelID(5, "model"), Name: "model", ChannelID: 5,
		}},
		StatsDailyChannel: []model.StatsDailyChannel{{Date: "20261119", ChannelID: 5}},
		StatsDailyChannelModel: []model.StatsDailyChannelModel{{
			Date: "20261119", ChannelID: 5, ModelName: "model",
		}},
		RuntimeStates: []model.AutoStrategyState{{
			Key: "5:model", ChannelID: 5, ModelName: " Model ",
		}},
		CircuitBreakerStates: []model.CircuitBreakerState{{
			Key: "5:2:model", ChannelID: 5, ChannelKeyID: 2, ModelName: "model",
		}},
		GroupHealthSnapshots: []model.GroupHealthSnapshot{{
			ID: 10, GroupID: 1, GroupName: "group", GroupMode: model.GroupModeFailover,
			RequestModel: "model", Status: model.GroupHealthStatusSuccess,
			StartedAt: started, SuccessfulChannelID: &successfulChannelID,
		}},
		GroupHealthAttempts: []model.GroupHealthAttempt{{
			ID: 11, SnapshotID: 10, GroupItemID: 21, ChannelID: 5, ChannelName: "source",
			ModelName: "model", Status: model.GroupHealthAttemptStatusSuccess,
		}},
		AIRouteTasks: []model.AIRouteTask{{
			ID: "task-1",
			CurrentBatch: &model.GenerateAIRouteCurrentBatch{
				ChannelIDs: []int{5},
			},
			RunningBatches: []model.GenerateAIRouteRunningBatch{{
				ChannelIDs: []int{5},
			}},
			Channels: []model.GenerateAIRouteChannelProgress{{
				ChannelID: 5,
			}},
		}},
	}
	if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental); err != nil {
		t.Fatal(err)
	}
	var statsChannel model.StatsChannel
	if err := target.First(&statsChannel, "channel_id = ?", 9).Error; err != nil {
		t.Fatal(err)
	}
	var statsModel model.StatsModel
	if err := target.First(&statsModel, "channel_id = ?", 9).Error; err != nil {
		t.Fatal(err)
	}
	if statsModel.ID != statsModelID(9, "model") {
		t.Fatalf("stats model primary key was not regenerated: got=%d", statsModel.ID)
	}
	for _, query := range []struct {
		name  string
		model any
		where string
		args  []any
	}{
		{"daily channel", &model.StatsDailyChannel{}, "channel_id = ?", []any{9}},
		{"daily channel model", &model.StatsDailyChannelModel{}, "channel_id = ?", []any{9}},
		{"runtime state", &model.AutoStrategyState{}, "channel_id = ? AND key = ?", []any{9, "9:model"}},
		{"circuit state", &model.CircuitBreakerState{}, "channel_id = ? AND key = ?", []any{9, "9:2:model"}},
		{"health attempt", &model.GroupHealthAttempt{}, "channel_id = ?", []any{9}},
	} {
		var count int64
		if err := target.Model(query.model).Where(query.where, query.args...).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("%s rewritten rows=%d, want 1", query.name, count)
		}
	}
	var snapshot model.GroupHealthSnapshot
	if err := target.First(&snapshot, 10).Error; err != nil {
		t.Fatal(err)
	}
	if snapshot.SuccessfulChannelID == nil || *snapshot.SuccessfulChannelID != 9 {
		t.Fatalf("health snapshot channel id was not remapped: %+v", snapshot.SuccessfulChannelID)
	}
	var task model.AIRouteTask
	if err := target.First(&task, "id = ?", "task-1").Error; err != nil {
		t.Fatal(err)
	}
	if task.CurrentBatch == nil || len(task.CurrentBatch.ChannelIDs) != 1 || task.CurrentBatch.ChannelIDs[0] != 9 ||
		len(task.RunningBatches) != 1 || task.RunningBatches[0].ChannelIDs[0] != 9 ||
		len(task.Channels) != 1 || task.Channels[0].ChannelID != 9 {
		t.Fatalf("AI route task references were not remapped: %+v", task)
	}
}

func TestV6FullRestoreRejectsChangedChannelsReferencedByUnownedRows(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-full-unowned-channel-reference.db")
	oldUID := uuid.NewString()
	if err := target.Create(&model.Channel{
		ID: 4, Name: "target", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: oldUID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.Create(&model.StatsChannel{
		ChannelID: 4, StatsMetrics: model.StatsMetrics{RequestSuccess: 8},
	}).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"channels", "channel_keys", "group_items"},
		Channels: []model.Channel{{
			ID: 4, Name: "replacement", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: uuid.NewString(),
		}},
		ChannelKeys: []model.ChannelKey{},
		GroupItems:  []model.GroupItem{},
	}
	_, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull)
	if err == nil || !strings.Contains(err.Error(), `unowned section "stats_channel"`) {
		t.Fatalf("full restore error=%v, want unowned stats reference diagnostic", err)
	}
	var channel model.Channel
	if err := target.First(&channel, 4).Error; err != nil {
		t.Fatal(err)
	}
	if channel.ChannelUID != oldUID {
		t.Fatalf("failed restore changed channel identity: %+v", channel)
	}
	var stats model.StatsChannel
	if err := target.First(&stats, "channel_id = ?", 4).Error; err != nil {
		t.Fatal(err)
	}
	if stats.RequestSuccess != 8 {
		t.Fatalf("failed restore changed unowned stats: %+v", stats)
	}
}

func TestV6FullRestoreValidatesGroupItemDependencies(t *testing.T) {
	t.Run("incoming item requires target group", func(t *testing.T) {
		target := openChannelUIDBackupTarget(t, "v6-item-missing-group.db")
		if err := target.Create(&model.Channel{ID: 8, Name: "channel", Type: outbound.OutboundTypeOpenAIChat}).Error; err != nil {
			t.Fatal(err)
		}
		dump := &model.DBDump{
			Version:  6,
			Sections: []string{"group_items"},
			GroupItems: []model.GroupItem{{
				ID: 2, GroupID: 99, ChannelID: 8, ModelName: "model",
			}},
		}
		_, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull)
		if err == nil || !strings.Contains(err.Error(), "absent from the target") {
			t.Fatalf("full restore error=%v, want missing target group diagnostic", err)
		}
	})

	t.Run("replaced groups cannot strand unowned items", func(t *testing.T) {
		target := openChannelUIDBackupTarget(t, "v6-groups-remove-without-items.db")
		if err := target.Create(&model.Group{ID: 7, Name: "old", Mode: model.GroupModeFailover}).Error; err != nil {
			t.Fatal(err)
		}
		if err := target.Create(&model.GroupItem{ID: 4, GroupID: 7, ChannelID: 8, ModelName: "model"}).Error; err != nil {
			t.Fatal(err)
		}
		dump := &model.DBDump{
			Version:  6,
			Sections: []string{"groups"},
			Groups:   []model.Group{},
		}
		_, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull)
		if err == nil || !strings.Contains(err.Error(), "include group_items") {
			t.Fatalf("full restore error=%v, want unowned group item diagnostic", err)
		}
	})
}

func TestV6FullRestoreRejectsRemovingChannelGroupUsedByUnownedChannels(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-channel-group-used-by-unowned-channel.db")
	channelGroup := model.ChannelGroup{Name: "preserved"}
	if err := target.Create(&channelGroup).Error; err != nil {
		t.Fatal(err)
	}
	channel := model.Channel{
		ID: 31, Name: "channel", GroupID: channelGroup.ID, Type: outbound.OutboundTypeOpenAIChat,
	}
	if err := target.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}

	dump := &model.DBDump{
		Version:       6,
		Sections:      []string{"channel_groups"},
		ChannelGroups: []model.ChannelGroup{},
	}
	_, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull)
	if err == nil || !strings.Contains(err.Error(), "unowned target channels") {
		t.Fatalf("full restore error=%v, want unowned channel-group reference diagnostic", err)
	}
	var gotGroup model.ChannelGroup
	if err := target.First(&gotGroup, channelGroup.ID).Error; err != nil {
		t.Fatalf("preflight failure deleted channel group: %v", err)
	}
}

func TestV6FullRestoreValidatesIncomingChannelGroupReferences(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-channel-group-missing-in-backup.db")
	dump := &model.DBDump{
		Version:       6,
		Sections:      []string{"channels", "channel_keys", "group_items", "channel_groups"},
		Channels:      []model.Channel{{ID: 31, Name: "channel", GroupID: 99, Type: outbound.OutboundTypeOpenAIChat}},
		ChannelKeys:   []model.ChannelKey{},
		GroupItems:    []model.GroupItem{},
		ChannelGroups: []model.ChannelGroup{},
	}
	_, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull)
	if err == nil || !strings.Contains(err.Error(), "absent from the imported channel_groups section") {
		t.Fatalf("full restore error=%v, want missing channel group diagnostic", err)
	}
	var count int64
	if err := target.Model(&model.Channel{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("preflight failure imported channels: %d", count)
	}
}

func TestV6FullRestoreRejectsSparseChannelReferencesBeforeDeletion(t *testing.T) {
	requiredSections := []string{
		"channel_keys", "group_items", "channel_model_capabilities", "channel_model_limits",
		"channel_probe_runs", "channel_probe_results",
		"stats_channel", "stats_model", "stats_daily_channel", "stats_daily_channel_model",
		"runtime_states", "circuit_breaker_states", "group_health_attempts", "relay_log_attempts",
	}
	for _, section := range requiredSections {
		t.Run(section, func(t *testing.T) {
			target := openChannelUIDBackupTarget(t, "v6-full-sparse-channel-"+section+".db")
			if err := target.Create(&model.Setting{Key: model.SettingKeyProxyURL, Value: "preserve"}).Error; err != nil {
				t.Fatal(err)
			}
			raw := fmt.Sprintf(
				`{"version":6,"sections":["%s","settings"],"%s":[{}],"settings":[]}`,
				section, section,
			)
			var dump model.DBDump
			if err := utilsjson.Unmarshal([]byte(raw), &dump); err != nil {
				t.Fatal(err)
			}
			_, err := ImportWithModeToDB(context.Background(), target, &dump, model.ImportModeFull)
			if err == nil || !strings.Contains(err.Error(), "row 0: channel_id is required") {
				t.Fatalf("full restore error=%v, want located missing channel_id error", err)
			}
			var setting model.Setting
			if err := target.First(&setting, "key = ?", model.SettingKeyProxyURL).Error; err != nil {
				t.Fatalf("preflight failure deleted existing setting: %v", err)
			}
			if setting.Value != "preserve" {
				t.Fatalf("preflight failure changed existing setting to %q", setting.Value)
			}
		})
	}

	target := openChannelUIDBackupTarget(t, "v6-full-null-channel-reference.db")
	if err := target.Create(&model.Setting{Key: model.SettingKeyProxyURL, Value: "preserve"}).Error; err != nil {
		t.Fatal(err)
	}
	var dump model.DBDump
	raw := `{"version":6,"sections":["group_items","settings"],"group_items":[{"id":1,"channel_id":null}],"settings":[]}`
	if err := utilsjson.Unmarshal([]byte(raw), &dump); err != nil {
		t.Fatal(err)
	}
	_, err := ImportWithModeToDB(context.Background(), target, &dump, model.ImportModeFull)
	if err == nil || !strings.Contains(err.Error(), "group_items row 0: channel_id is required") {
		t.Fatalf("full restore error=%v, want located null channel_id error", err)
	}
	var setting model.Setting
	if err := target.First(&setting, "key = ?", model.SettingKeyProxyURL).Error; err != nil {
		t.Fatalf("null-field preflight failure deleted existing setting: %v", err)
	}
}

func TestV6FullRestoreChecksIncomingChannelsAgainstUnownedTargetGroups(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-channel-group-missing-in-target.db")
	oldUID := uuid.NewString()
	if err := target.Create(&model.Channel{
		ID: 31, Name: "existing", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: oldUID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:     6,
		Sections:    []string{"channels", "channel_keys", "group_items"},
		Channels:    []model.Channel{{ID: 31, Name: "replacement", GroupID: 99, Type: outbound.OutboundTypeOpenAIChat, ChannelUID: uuid.NewString()}},
		ChannelKeys: []model.ChannelKey{},
		GroupItems:  []model.GroupItem{},
	}
	_, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull)
	if err == nil || !strings.Contains(err.Error(), "absent from target channel_groups collection") {
		t.Fatalf("full restore error=%v, want missing target channel group diagnostic", err)
	}
	var channel model.Channel
	if err := target.First(&channel, 31).Error; err != nil {
		t.Fatalf("failed preflight removed target channel: %v", err)
	}
	if channel.ChannelUID != oldUID {
		t.Fatalf("failed preflight changed target identity: %q", channel.ChannelUID)
	}
}

func TestV6IncrementalPatchDoesNotRemapOmittedOrNullChannelID(t *testing.T) {
	for _, channelField := range []string{"", `,"channel_id":null`} {
		name := "omitted"
		if channelField != "" {
			name = "null"
		}
		t.Run(name, func(t *testing.T) {
			target := openChannelUIDBackupTarget(t, "v6-group-item-sparse-channel-"+name+".db")
			channelUID := uuid.NewString()
			if err := target.Create(&model.Channel{
				ID: 9, Name: "target", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: channelUID,
			}).Error; err != nil {
				t.Fatal(err)
			}
			if err := target.Create(&model.Group{ID: 3, Name: "group", Mode: model.GroupModeFailover}).Error; err != nil {
				t.Fatal(err)
			}
			if err := target.Create(&model.GroupItem{
				ID: 11, GroupID: 3, ChannelID: 9, ModelName: "model", Priority: 1,
			}).Error; err != nil {
				t.Fatal(err)
			}

			var dump model.DBDump
			raw := `{"version":6,"sections":["channels","group_items"],"channels":[{"id":5,"channel_uid":"` +
				channelUID + `"}],"group_items":[{"id":11` + channelField + `,"priority":7}]}`
			if err := utilsjson.Unmarshal([]byte(raw), &dump); err != nil {
				t.Fatal(err)
			}
			if _, err := ImportWithModeToDB(context.Background(), target, &dump, model.ImportModeIncremental); err != nil {
				t.Fatal(err)
			}
			var got model.GroupItem
			if err := target.First(&got, 11).Error; err != nil {
				t.Fatal(err)
			}
			if got.ChannelID != 9 || got.Priority != 7 {
				t.Fatalf("sparse patch changed preserved channel reference or missed explicit field: %+v", got)
			}
		})
	}
}

func TestV6IncrementalPatchRemapsDerivedKeysWithoutModelFields(t *testing.T) {
	for _, channelField := range []string{"", `,"channel_id":null`} {
		name := "omitted"
		if channelField != "" {
			name = "null"
		}
		t.Run(name, func(t *testing.T) {
			target := openChannelUIDBackupTarget(t, "v6-derived-key-sparse-"+name+".db")
			channelUID := uuid.NewString()
			if err := target.Create(&model.Channel{
				ID: 9, Name: "target", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: channelUID,
			}).Error; err != nil {
				t.Fatal(err)
			}
			if err := target.Create(&model.AutoStrategyState{
				Key: "9:model", ChannelID: 9, ModelName: "model", UpdatedAt: 1,
			}).Error; err != nil {
				t.Fatal(err)
			}
			if err := target.Create(&model.CircuitBreakerState{
				Key: "9:2:model", ChannelID: 9, ChannelKeyID: 2, ModelName: "model", State: 1,
			}).Error; err != nil {
				t.Fatal(err)
			}

			var dump model.DBDump
			raw := `{"version":6,"sections":["channels","runtime_states","circuit_breaker_states"],
				"channels":[{"id":5,"channel_uid":"` + channelUID + `"}],
				"runtime_states":[{"key":"5:model"` + channelField + `,"updated_at":2}],
				"circuit_breaker_states":[{"key":"5:2:model"` + channelField + `,"state":2}]}`
			if err := utilsjson.Unmarshal([]byte(raw), &dump); err != nil {
				t.Fatal(err)
			}
			if _, err := ImportWithModeToDB(context.Background(), target, &dump, model.ImportModeIncremental); err != nil {
				t.Fatal(err)
			}
			var runtime model.AutoStrategyState
			if err := target.First(&runtime, "key = ?", "9:model").Error; err != nil {
				t.Fatal(err)
			}
			if runtime.ChannelID != 9 || runtime.ModelName != "model" || runtime.UpdatedAt != 2 {
				t.Fatalf("runtime patch lost the existing model identity: %+v", runtime)
			}
			var circuit model.CircuitBreakerState
			if err := target.First(&circuit, "key = ?", "9:2:model").Error; err != nil {
				t.Fatal(err)
			}
			if circuit.ChannelID != 9 || circuit.ChannelKeyID != 2 || circuit.ModelName != "model" || circuit.State != 2 {
				t.Fatalf("circuit breaker patch lost the existing model identity: %+v", circuit)
			}
		})
	}
}

func TestV6DerivedStateKeysResolveSparseChannelReferences(t *testing.T) {
	var dump model.DBDump
	raw := `{"version":6,"sections":["runtime_states","circuit_breaker_states"],
		"runtime_states":[{"key":"5:model"}],
		"circuit_breaker_states":[{"key":"7:2:model","channel_id":null}]}`
	if err := utilsjson.Unmarshal([]byte(raw), &dump); err != nil {
		t.Fatal(err)
	}
	authority, err := authoritativeSections(&dump)
	if err != nil {
		t.Fatal(err)
	}
	required := requiredReferencedChannelIDs(&dump, authority)
	for _, id := range []int{5, 7} {
		if !required[id] {
			t.Errorf("derived state key channel id %d is not required for identity resolution", id)
		}
	}
	referenced := referencedChannelIDs(&dump, authority)
	for _, id := range []int{5, 7} {
		if !containsInt(referenced, id) {
			t.Errorf("derived state key channel id %d is absent from references %v", id, referenced)
		}
	}
}

func TestV6IncrementalStatsModelMappingRequiresModelName(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-stats-model-sparse-remap.db")
	channelUID := uuid.NewString()
	if err := target.Create(&model.Channel{
		ID: 9, Name: "target", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: channelUID,
	}).Error; err != nil {
		t.Fatal(err)
	}

	var dump model.DBDump
	raw := `{"version":6,"sections":["channels","stats_model"],
		"channels":[{"id":5,"channel_uid":"` + channelUID + `"}],
		"stats_model":[{"id":123,"channel_id":5}]}`
	if err := utilsjson.Unmarshal([]byte(raw), &dump); err != nil {
		t.Fatal(err)
	}
	_, err := ImportWithModeToDB(context.Background(), target, &dump, model.ImportModeIncremental)
	if err == nil || !strings.Contains(err.Error(), "stats_model row 0") || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("sparse stats model import error=%v, want located rekey diagnostic", err)
	}
	var count int64
	if err := target.Model(&model.StatsModel{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed stats model remap wrote rows: %d", count)
	}
}

func TestV6ChannelMappingRewritesAuthoritativeRelayLogReferences(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-relay-log-channel-map.db")
	channelUID := uuid.NewString()
	if err := target.Create(&model.Channel{
		ID: 9, Name: "target", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: channelUID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:     6,
		IncludeLogs: true,
		Sections:    []string{"channels", "relay_logs", "relay_log_attempts"},
		Channels:    []model.Channel{{ID: 5, Name: "source", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: channelUID}},
		RelayLogs: []model.RelayLog{{
			ID: 8001, ChannelId: 5, Attempts: []model.ChannelAttempt{{ChannelID: 5, ChannelName: "source"}},
		}},
		RelayLogAttempts: []model.RelayLogAttempt{{
			RelayLogID: 8001, AttemptNum: 1, ChannelID: 5, ChannelName: "source",
		}},
	}
	if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental); err != nil {
		t.Fatal(err)
	}
	var log model.RelayLog
	if err := target.First(&log, 8001).Error; err != nil {
		t.Fatal(err)
	}
	if log.ChannelId != 9 || len(log.Attempts) != 1 || log.Attempts[0].ChannelID != 9 {
		t.Fatalf("relay log channel references were not mapped: %+v", log)
	}
	var attempt model.RelayLogAttempt
	if err := target.First(&attempt, "relay_log_id = ? AND attempt_num = ?", 8001, 1).Error; err != nil {
		t.Fatal(err)
	}
	if attempt.ChannelID != 9 {
		t.Fatalf("relay log attempt channel_id=%d, want 9", attempt.ChannelID)
	}
}

func TestV6RelayLogAttemptSectionCanImportAgainstExistingParent(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-attempt-section-existing-parent.db")
	const relayLogID int64 = 8101
	if err := target.Create(&model.RelayLog{ID: relayLogID, RequestModelName: "parent"}).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"relay_log_attempts"},
		RelayLogAttempts: []model.RelayLogAttempt{{
			RelayLogID: relayLogID, AttemptNum: 2, ChannelID: 9,
			ChannelName: "target", ModelName: "model", Status: string(model.AttemptSuccess),
		}},
	}
	if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental); err != nil {
		t.Fatal(err)
	}
	var attempt model.RelayLogAttempt
	if err := target.First(&attempt, "relay_log_id = ? AND attempt_num = ?", relayLogID, 2).Error; err != nil {
		t.Fatal(err)
	}
	if attempt.ChannelID != 9 || attempt.ModelName != "model" {
		t.Fatalf("attempt-only section was not imported against its existing parent: %+v", attempt)
	}
}

func TestV6RelayLogAttemptSectionReportsMissingParent(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-attempt-section-missing-parent.db")
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"relay_log_attempts"},
		RelayLogAttempts: []model.RelayLogAttempt{{
			RelayLogID: 8201, AttemptNum: 1, ChannelID: 9, ChannelName: "target",
		}},
	}
	_, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental)
	if err == nil || !strings.Contains(err.Error(), "relay_log_attempts row 0 references missing target relay_log id 8201") {
		t.Fatalf("attempt import error=%v, want explicit missing-parent diagnostic", err)
	}
	var count int64
	if err := target.Model(&model.RelayLogAttempt{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("missing-parent import wrote attempts: %d", count)
	}
}

func TestV6ExplicitEmptyAttemptSectionDoesNotDeriveNestedAttempts(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-empty-attempt-section.db")
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"relay_logs", "relay_log_attempts"},
		RelayLogs: []model.RelayLog{{
			ID: 8301, RequestModelName: "model",
			Attempts: []model.ChannelAttempt{{
				ChannelID: 9, AttemptNum: 1, ModelName: "model", Status: model.AttemptSuccess,
			}},
		}},
		RelayLogAttempts: []model.RelayLogAttempt{},
	}
	if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental); err != nil {
		t.Fatal(err)
	}
	var parent model.RelayLog
	if err := target.First(&parent, 8301).Error; err != nil {
		t.Fatal(err)
	}
	if len(parent.Attempts) != 1 {
		t.Fatalf("parent nested attempts were not preserved: %+v", parent.Attempts)
	}
	var count int64
	if err := target.Model(&model.RelayLogAttempt{}).Where("relay_log_id = ?", 8301).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("explicit empty attempts section derived %d relational attempts", count)
	}
}

func TestV6FullRestoreRejectsChangedChannelsWithUnownedRelayLogReferences(t *testing.T) {
	for _, reference := range []string{"parent", "nested_attempt", "attempt_row"} {
		t.Run(reference, func(t *testing.T) {
			target := openChannelUIDBackupTarget(t, "v6-unowned-relay-reference-"+reference+".db")
			oldUID := uuid.NewString()
			if err := target.Create(&model.Channel{
				ID: 4, Name: "old", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: oldUID,
			}).Error; err != nil {
				t.Fatal(err)
			}
			relayLog := model.RelayLog{ID: 8401, RequestModelName: "model"}
			switch reference {
			case "parent":
				relayLog.ChannelId = 4
			case "nested_attempt":
				relayLog.Attempts = []model.ChannelAttempt{{ChannelID: 4, AttemptNum: 1, ModelName: "model"}}
			case "attempt_row":
				if err := target.Create(&model.RelayLogAttempt{
					RelayLogID: 8401, AttemptNum: 1, ChannelID: 4, ModelName: "model",
				}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := target.Create(&relayLog).Error; err != nil {
				t.Fatal(err)
			}

			dump := &model.DBDump{
				Version:     6,
				Sections:    []string{"channels", "channel_keys", "group_items"},
				Channels:    []model.Channel{{ID: 4, Name: "new", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: uuid.NewString()}},
				ChannelKeys: []model.ChannelKey{},
				GroupItems:  []model.GroupItem{},
			}
			_, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull)
			if err == nil || !strings.Contains(err.Error(), `unowned section "relay_`) {
				t.Fatalf("full restore error=%v, want unowned relay-log reference diagnostic", err)
			}
			var channel model.Channel
			if err := target.First(&channel, 4).Error; err != nil {
				t.Fatalf("failed preflight removed target channel: %v", err)
			}
			if channel.ChannelUID != oldUID {
				t.Fatalf("failed preflight changed target channel identity to %q", channel.ChannelUID)
			}
		})
	}
}

func TestV6ChannelMappingRewritesLegacyCapabilityScope(t *testing.T) {
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"channel_model_capabilities"},
		ChannelModelCapabilities: []model.ChannelModelCapability{{
			ChannelID: 5, ChannelUID: "legacy-channel-5", ModelName: "model",
			Capability: model.CapabilityToolCalling, ProbeKeyID: 2, Source: "probe",
		}},
	}
	if err := rewriteChannelForeignKeys(dump, map[int]int{5: 9}, map[string]bool{"channel_model_capabilities": true}); err != nil {
		t.Fatal(err)
	}
	got := dump.ChannelModelCapabilities[0]
	if got.ChannelID != 9 || got.ChannelUID != "legacy-channel-9" {
		t.Fatalf("legacy capability scope was not moved with its channel ID: %+v", got)
	}
}

func TestV6FullRestoreRejectsZeroForRequiredChannelReferences(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-zero-group-item-channel.db")
	if err := target.Create(&model.Setting{Key: model.SettingKeyProxyURL, Value: "preserve"}).Error; err != nil {
		t.Fatal(err)
	}
	var dump model.DBDump
	raw := `{"version":6,"sections":["group_items","settings"],"group_items":[{"channel_id":0}],"settings":[]}`
	if err := utilsjson.Unmarshal([]byte(raw), &dump); err != nil {
		t.Fatal(err)
	}
	_, err := ImportWithModeToDB(context.Background(), target, &dump, model.ImportModeFull)
	if err == nil || !strings.Contains(err.Error(), "group_items row 0: channel_id must be positive, got 0") {
		t.Fatalf("full restore error=%v, want a located zero channel_id diagnostic", err)
	}
	var setting model.Setting
	if err := target.First(&setting, "key = ?", model.SettingKeyProxyURL).Error; err != nil {
		t.Fatalf("zero-reference preflight failure deleted target settings: %v", err)
	}
}

func TestV6FullRestorePreservesZeroChannelStatsModelAggregate(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-zero-stats-model-aggregate.db")
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"stats_model"},
		StatsModel: []model.StatsModel{{
			ID: statsModelID(0, "shared-model"), Name: "shared-model", ChannelID: 0,
			StatsMetrics: model.StatsMetrics{RequestSuccess: 4},
		}},
	}
	if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull); err != nil {
		t.Fatalf("restore zero-channel aggregate: %v", err)
	}
	var restored model.StatsModel
	if err := target.First(&restored, "id = ?", dump.StatsModel[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if restored.ChannelID != 0 || restored.RequestSuccess != 4 {
		t.Fatalf("aggregate changed during restore: %+v", restored)
	}
}

func TestV6AttemptChannelPresenceUsesCompactMetadata(t *testing.T) {
	var dump model.DBDump
	raw := `{"version":6,"sections":["relay_log_attempts"],"relay_log_attempts":[{"channel_id":9},{"channel_id":null},{}]}`
	if err := utilsjson.Unmarshal([]byte(raw), &dump); err != nil {
		t.Fatal(err)
	}
	for index, expected := range []bool{true, false, false} {
		got, tracked := dump.RelayLogAttemptChannelIDNonNull(index)
		if !tracked || got != expected {
			t.Errorf("attempt row %d channel presence=(%t,%t), want (%t,true)", index, got, tracked, expected)
		}
		if _, tracked := dump.RowFieldPresence("relay_log_attempts", index); tracked {
			t.Errorf("attempt row %d retained the heavyweight general presence map", index)
		}
	}
}

func TestV6FullRestoreDoesNotOrphanUnownedRelayLogChildren(t *testing.T) {
	for _, child := range []string{"relay_log_attempts", "relay_log_content_refs"} {
		t.Run(child, func(t *testing.T) {
			target := openChannelUIDBackupTarget(t, "v6-remove-relay-log-with-"+child+".db")
			seedBackupTestLogFamily(t, target, 8601, []byte(`{"ok":true}`))
			if child == "relay_log_attempts" {
				if err := target.Where("relay_log_id = ?", 8601).Delete(&model.RelayLogContentRef{}).Error; err != nil {
					t.Fatal(err)
				}
			} else {
				if err := target.Where("relay_log_id = ?", 8601).Delete(&model.RelayLogAttempt{}).Error; err != nil {
					t.Fatal(err)
				}
			}
			dump := &model.DBDump{
				Version: 6, Sections: []string{"relay_logs"},
				RelayLogs: []model.RelayLog{},
			}
			_, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull)
			if err == nil || !strings.Contains(err.Error(), `unowned section "`+child+`" still references removed relay_log id 8601`) {
				t.Fatalf("full restore error=%v, want dependency error for %s", err, child)
			}
			assertRelayLogParentPresence(t, target, 8601, true)
		})
	}
}

func TestV6FullRestoreDoesNotOrphanUnownedReadyContentRefs(t *testing.T) {
	for _, includeReferencedBlob := range []bool{false, true} {
		name := "missing-referenced-blob"
		if includeReferencedBlob {
			name = "preserves-referenced-blob"
		}
		t.Run(name, func(t *testing.T) {
			target := openChannelUIDBackupTarget(t, "v6-replace-blobs-"+name+".db")
			blob := seedBackupTestLogFamily(t, target, 8701, []byte(`{"content":"kept"}`))
			dump := &model.DBDump{
				Version: 6, Sections: []string{"relay_log_content_blobs"},
				RelayLogContentBlobs: []model.RelayLogContentBlobDump{},
			}
			if includeReferencedBlob {
				dump.RelayLogContentBlobs = []model.RelayLogContentBlobDump{{
					Digest: blob.Digest, Encoding: blob.Encoding, OriginalSize: blob.OriginalSize,
					StoredSize: blob.StoredSize, Payload: blob.Payload, CreatedAt: blob.CreatedAt,
				}}
			}
			_, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull)
			if includeReferencedBlob {
				if err != nil {
					t.Fatalf("replace blobs while preserving referenced digest: %v", err)
				}
				assertBackupLogFamilyCounts(t, target, 1, 1, 1, 1)
			} else {
				if err == nil || !strings.Contains(err.Error(), "unowned ready relay_log_content_refs") {
					t.Fatalf("full restore error=%v, want an unowned-reference/blob dependency error", err)
				}
				assertBackupLogFamilyCounts(t, target, 1, 1, 1, 1)
			}
		})
	}
}

func TestV6IncrementalContentRefsCanReuseTargetBlob(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-ref-existing-target-blob.db")
	blob := seedBackupTestLogFamily(t, target, 8801, []byte(`{"content":"existing"}`))
	dump := &model.DBDump{
		Version: 6, Sections: []string{"relay_log_content_refs"},
		RelayLogContentRefs: []model.RelayLogContentRef{{
			RelayLogID: 8801, AttemptNum: 1, Boundary: model.RelayLogBoundaryClientEgress,
			Slot: 1, Kind: "body", State: model.RelayLogContentStateReady, BlobDigest: blob.Digest,
		}},
	}
	if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental); err != nil {
		t.Fatalf("import ref to existing target blob: %v", err)
	}
	assertBackupLogFamilyCounts(t, target, 1, 1, 2, 1)
}

func TestV6UnownedBlobPayloadIsIgnoredAndOwnedUnusedBlobsAreImported(t *testing.T) {
	t.Run("unowned malformed blob is ignored", func(t *testing.T) {
		target := openChannelUIDBackupTarget(t, "v6-ignore-unowned-blob.db")
		if err := target.Create(&model.RelayLog{ID: 8901, RequestModelName: "existing"}).Error; err != nil {
			t.Fatal(err)
		}
		dump := &model.DBDump{
			Version: 6, Sections: []string{"relay_log_content_refs"},
			RelayLogContentRefs: []model.RelayLogContentRef{{
				RelayLogID: 8901, AttemptNum: 1, Boundary: model.RelayLogBoundaryClientIngress,
				Slot: 0, Kind: "body", State: model.RelayLogContentStatePending,
			}},
			RelayLogContentBlobs: []model.RelayLogContentBlobDump{{Digest: "invalid", Encoding: "unknown"}},
		}
		if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental); err != nil {
			t.Fatalf("unowned blob affected refs-only import: %v", err)
		}
		var blobCount int64
		if err := target.Model(&model.RelayLogContentBlob{}).Count(&blobCount).Error; err != nil {
			t.Fatal(err)
		}
		if blobCount != 0 {
			t.Fatalf("unowned blob section imported %d rows", blobCount)
		}
	})

	t.Run("owned unused blob is imported", func(t *testing.T) {
		target := openChannelUIDBackupTarget(t, "v6-import-owned-unused-blob.db")
		if err := target.Create(&model.RelayLog{ID: 8902, RequestModelName: "existing"}).Error; err != nil {
			t.Fatal(err)
		}
		blob := buildBackupTestBlob([]byte(`{"unused":true}`), 12)
		dump := &model.DBDump{
			Version: 6, Sections: []string{"relay_log_content_blobs"},
			RelayLogContentBlobs: []model.RelayLogContentBlobDump{{
				Digest: blob.Digest, Encoding: blob.Encoding, OriginalSize: blob.OriginalSize,
				StoredSize: blob.StoredSize, Payload: blob.Payload, CreatedAt: blob.CreatedAt,
			}},
		}
		if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental); err != nil {
			t.Fatalf("import authoritative blob section: %v", err)
		}
		var restored model.RelayLogContentBlob
		if err := target.First(&restored, "digest = ?", blob.Digest).Error; err != nil {
			t.Fatal(err)
		}
		if string(restored.Payload) != string(blob.Payload) {
			t.Fatalf("owned blob payload changed: %q", restored.Payload)
		}
	})
}

func TestV6NestedRelayAttemptsRemapWhenParentHasNoChannel(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-nested-attempt-channel-map.db")
	channelUID := uuid.NewString()
	if err := target.Create(&model.Channel{
		ID: 9, Name: "target", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: channelUID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"channels", "relay_logs"},
		Channels: []model.Channel{{
			ID: 5, Name: "source", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: channelUID,
		}},
		RelayLogs: []model.RelayLog{{
			ID: 9001, ChannelId: 0,
			Attempts: []model.ChannelAttempt{{ChannelID: 5, AttemptNum: 1, ChannelName: "source"}},
		}},
	}
	if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental); err != nil {
		t.Fatal(err)
	}
	var restored model.RelayLog
	if err := target.First(&restored, 9001).Error; err != nil {
		t.Fatal(err)
	}
	if restored.ChannelId != 0 || len(restored.Attempts) != 1 || restored.Attempts[0].ChannelID != 9 {
		t.Fatalf("nested attempt channel was not independently remapped: %+v", restored)
	}
}

func TestV6IncrementalParentSectionRestoresChildrenForExistingParent(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-existing-parent-with-children.db")
	if err := target.Create(&model.RelayLog{
		ID: 9101, Time: 1, RequestModelName: "already-present",
	}).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"relay_logs", "relay_log_attempts", "relay_log_content_refs"},
		RelayLogs: []model.RelayLog{{
			ID: 9101, Time: 2, RequestModelName: "source-parent",
		}},
		RelayLogAttempts: []model.RelayLogAttempt{{
			RelayLogID: 9101, AttemptNum: 1, ChannelID: 9,
			ChannelName: "channel", ModelName: "model", Status: string(model.AttemptSuccess),
		}},
		RelayLogContentRefs: []model.RelayLogContentRef{{
			RelayLogID: 9101, AttemptNum: 1, Boundary: model.RelayLogBoundaryClientIngress,
			Slot: 0, Kind: "body", State: model.RelayLogContentStatePending,
		}},
	}
	if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental); err != nil {
		t.Fatalf("incremental import with existing parent: %v", err)
	}
	var parentCount, attemptCount, refCount int64
	if err := target.Model(&model.RelayLog{}).Where("id = ?", 9101).Count(&parentCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.Model(&model.RelayLogAttempt{}).Where("relay_log_id = ?", 9101).Count(&attemptCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.Model(&model.RelayLogContentRef{}).Where("relay_log_id = ?", 9101).Count(&refCount).Error; err != nil {
		t.Fatal(err)
	}
	if parentCount != 1 || attemptCount != 1 || refCount != 1 {
		t.Fatalf("restored parent/attempt/ref counts = %d/%d/%d, want 1/1/1", parentCount, attemptCount, refCount)
	}
}
