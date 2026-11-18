package backup

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	internaldb "github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	utilsjson "github.com/lingyuins/octopus/internal/utils/json"
)

func TestV6ExportOmitsAdministratorIdentities(t *testing.T) {
	if err := internaldb.InitDB("sqlite", filepath.Join(t.TempDir(), "v6-export.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = internaldb.Close() })
	if err := internaldb.GetDB().Create(&model.User{Username: "local-admin", Role: "admin"}).Error; err != nil {
		t.Fatal(err)
	}
	dump, err := ExportAll(context.Background(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if dump.Version != 6 {
		t.Fatalf("dump version = %d, want 6", dump.Version)
	}
	if len(dump.Users) != 0 {
		t.Fatalf("v6 export leaked %d administrator rows", len(dump.Users))
	}
	if len(dump.Sections) == 0 {
		t.Fatal("v6 export is missing the sections manifest")
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	if _, exists := object["users"]; exists {
		t.Fatal("v6 export still writes a users collection")
	}
	if _, exists := object["sections"]; !exists {
		t.Fatal("v6 export is missing the sections key")
	}
}

func TestV6ImportIgnoresAdministratorRows(t *testing.T) {
	for _, version := range []int{5, 6} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			target := openChannelUIDBackupTarget(t, "v6-users-"+strconv.Itoa(version)+".db")
			if err := target.Create(&model.User{Username: "target-admin", Role: "admin"}).Error; err != nil {
				t.Fatal(err)
			}
			dump := &model.DBDump{
				Version: version,
				Sections: []string{
					"channels",
				},
				Users: []model.User{{ID: 9, Username: "backup-admin", Role: "admin"}},
			}
			result, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental)
			if err != nil {
				t.Fatal(err)
			}
			var users []model.User
			if err := target.Find(&users).Error; err != nil {
				t.Fatal(err)
			}
			if len(users) != 1 || users[0].Username != "target-admin" {
				t.Fatalf("administrator rows changed: %+v", users)
			}
			if len(result.Warnings) == 0 || !strings.Contains(result.Warnings[0], "ignored") {
				t.Fatalf("missing administrator warning: %+v", result.Warnings)
			}
		})
	}
}

func TestV6FullRestoreKeepsUnlistedSections(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-sections.db")
	channel := model.Channel{ID: 41, Name: "keep-channel", Type: outbound.OutboundTypeOpenAIChat}
	if err := target.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	group := model.Group{ID: 7, Name: "clear-me", Mode: model.GroupModeFailover}
	if err := target.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"groups"},
		Groups:   []model.Group{},
	}
	if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull); err != nil {
		t.Fatal(err)
	}
	var channels int64
	if err := target.Model(&model.Channel{}).Count(&channels).Error; err != nil {
		t.Fatal(err)
	}
	if channels != 1 {
		t.Fatalf("unlisted channel section was cleared: count=%d", channels)
	}
	var groups int64
	if err := target.Model(&model.Group{}).Count(&groups).Error; err != nil {
		t.Fatal(err)
	}
	if groups != 0 {
		t.Fatalf("explicit empty groups section did not clear rows: count=%d", groups)
	}
}

func TestV6NullSectionPreservesTargetRows(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-null-section.db")
	if err := target.Create(&model.Group{ID: 7, Name: "keep-me", Mode: model.GroupModeFailover}).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"groups"},
	}
	if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := target.Model(&model.Group{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("null section cleared target rows: count=%d", count)
	}
}

func TestV6JSONDistinguishesNullFromEmptyArray(t *testing.T) {
	var empty model.DBDump
	if err := utilsjson.Unmarshal([]byte(`{"version":6,"sections":["groups"],"groups":[]}`), &empty); err != nil {
		t.Fatal(err)
	}
	if isNullSection(&empty.Groups) {
		t.Fatal("explicit empty array decoded as null")
	}
	var null model.DBDump
	if err := utilsjson.Unmarshal([]byte(`{"version":6,"sections":["groups"],"groups":null}`), &null); err != nil {
		t.Fatal(err)
	}
	if !isNullSection(&null.Groups) {
		t.Fatal("explicit null decoded as an empty array")
	}
}

func TestV6IncrementalUpdatesExplicitValuesAndIsIdempotent(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-incremental.db")
	if err := target.Create(&model.Setting{Key: model.SettingKeyProxyURL, Value: "old-value"}).Error; err != nil {
		t.Fatal(err)
	}
	channel := model.Channel{ID: 55, Name: "untouched", Type: outbound.OutboundTypeOpenAIChat}
	if err := target.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"settings"},
		Settings: []model.Setting{{Key: model.SettingKeyProxyURL, Value: ""}},
	}
	for i := 0; i < 2; i++ {
		if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental); err != nil {
			t.Fatalf("import %d: %v", i, err)
		}
	}
	var stored model.Setting
	if err := target.Where("key = ?", model.SettingKeyProxyURL).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Value != "" {
		t.Fatalf("explicit empty value not preserved: %q", stored.Value)
	}
	var channels int64
	if err := target.Model(&model.Channel{}).Count(&channels).Error; err != nil {
		t.Fatal(err)
	}
	if channels != 1 {
		t.Fatalf("unlisted channel section changed: count=%d", channels)
	}
}

func TestV6PreservesTargetWebAuthnSettings(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-webauthn.db")
	targetRows := []model.Setting{
		{Key: model.SettingKeyWebAuthnRPID, Value: "target.example"},
		{Key: model.SettingKeyWebAuthnRPName, Value: "Target"},
	}
	if err := target.Create(&targetRows).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"settings"},
		Settings: []model.Setting{
			{Key: model.SettingKeyWebAuthnRPID, Value: "source.example"},
			{Key: model.SettingKeyWebAuthnRPName, Value: "Source"},
			{Key: model.SettingKeyProxyURL, Value: "http://proxy.example"},
		},
	}
	result, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental)
	if err != nil {
		t.Fatal(err)
	}
	var rp model.Setting
	if err := target.Where("key = ?", model.SettingKeyWebAuthnRPID).First(&rp).Error; err != nil {
		t.Fatal(err)
	}
	if rp.Value != "target.example" {
		t.Fatalf("WebAuthn RP ID changed to %q", rp.Value)
	}
	var proxy model.Setting
	if err := target.Where("key = ?", model.SettingKeyProxyURL).First(&proxy).Error; err != nil {
		t.Fatal(err)
	}
	if proxy.Value != "http://proxy.example" {
		t.Fatalf("ordinary setting was not imported: %q", proxy.Value)
	}
	found := false
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "Passkey") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing Passkey identity warning: %+v", result.Warnings)
	}
}

func TestV6ChannelUIDMappingRewritesForeignKeys(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-mapping.db")
	uid := uuid.NewString()
	targetChannel := model.Channel{ID: 9, Name: "target-channel", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: uid}
	if err := target.Create(&targetChannel).Error; err != nil {
		t.Fatal(err)
	}
	group := model.Group{ID: 3, Name: "mapping-group", Mode: model.GroupModeFailover}
	if err := target.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"channels", "group_items"},
		Channels: []model.Channel{{
			ID:         5,
			Name:       "source-channel",
			Type:       outbound.OutboundTypeOpenAIChat,
			ChannelUID: uid,
		}},
		GroupItems: []model.GroupItem{{
			ID:        11,
			GroupID:   3,
			ChannelID: 5,
			ModelName: "mapped-model",
		}},
	}
	result, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ChannelMappings) != 1 ||
		result.ChannelMappings[0].SourceChannelID != 5 ||
		result.ChannelMappings[0].TargetChannelID != 9 {
		t.Fatalf("unexpected channel mappings: %+v", result.ChannelMappings)
	}
	var item model.GroupItem
	if err := target.First(&item, 11).Error; err != nil {
		t.Fatal(err)
	}
	if item.ChannelID != 9 {
		t.Fatalf("group item channel_id = %d, want rewritten target id 9", item.ChannelID)
	}
	var phantom int64
	if err := target.Model(&model.Channel{}).Where("id = ?", 5).Count(&phantom).Error; err != nil {
		t.Fatal(err)
	}
	if phantom != 0 {
		t.Fatal("source channel was inserted under a colliding numeric id")
	}
}

func TestV6NumericIDCollisionRequiresExplicitMapping(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-collision.db")
	if err := target.Create(&model.Channel{
		ID: 5, Name: "occupied", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: uuid.NewString(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"channels"},
		Channels: []model.Channel{{
			ID: 5, Name: "incoming", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: uuid.NewString(),
		}},
	}
	_, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental)
	if err == nil || !strings.Contains(err.Error(), "channel id collision") {
		t.Fatalf("collision import error = %v", err)
	}
}

func TestV6MissingUIDOnOccupiedIDRequiresMapping(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-missing-uid.db")
	if err := target.Create(&model.Channel{
		ID: 7, Name: "current", Type: outbound.OutboundTypeOpenAIChat,
	}).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"channels"},
		Channels: []model.Channel{{
			ID: 7, Name: "legacy", Type: outbound.OutboundTypeOpenAIChat,
		}},
	}
	_, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental)
	if err == nil || !strings.Contains(err.Error(), "has no channel_uid") {
		t.Fatalf("missing uid collision error = %v", err)
	}
}

func TestV6RequiresSectionsManifest(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-manifest.db")
	_, err := ImportWithModeToDB(context.Background(), target, &model.DBDump{Version: 6}, model.ImportModeIncremental)
	if err == nil || !strings.Contains(err.Error(), "sections manifest") {
		t.Fatalf("missing manifest error = %v", err)
	}
}

func TestV6PartialImportResolvesExistingChannelReference(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-partial.db")
	if err := target.Create(&model.Channel{ID: 12, Name: "existing", Type: outbound.OutboundTypeOpenAIChat}).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.Create(&model.Group{ID: 2, Name: "group", Mode: model.GroupModeFailover}).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"group_items"},
		GroupItems: []model.GroupItem{{
			ID:        21,
			GroupID:   2,
			ChannelID: 12,
			ModelName: "partial-model",
		}},
	}
	if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeIncremental); err != nil {
		t.Fatal(err)
	}
	var item model.GroupItem
	if err := target.First(&item, 21).Error; err != nil {
		t.Fatal(err)
	}
	if item.ChannelID != 12 {
		t.Fatalf("partial import channel_id = %d", item.ChannelID)
	}
}

func TestV6FullRestoreRejectsPartialChannelReplacement(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-partial-full.db")
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"channels"},
		Channels: []model.Channel{{
			ID: 1, Name: "only-channel", Type: outbound.OutboundTypeOpenAIChat,
		}},
	}
	_, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull)
	if err == nil || !strings.Contains(err.Error(), "does not own") {
		t.Fatalf("partial full restore error = %v", err)
	}
}

func TestV6PreviewMappingDoesNotWrite(t *testing.T) {
	if err := internaldb.InitDB("sqlite", filepath.Join(t.TempDir(), "v6-preview.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = internaldb.Close() })
	target := internaldb.GetDB()
	uid := uuid.NewString()
	if err := target.Create(&model.Channel{
		ID: 9, Name: "target", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: uid,
	}).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"channels"},
		Channels: []model.Channel{{
			ID: 5, Name: "source", Type: outbound.OutboundTypeOpenAIChat, ChannelUID: uid,
		}},
	}
	mappings, err := PreviewChannelMapping(context.Background(), dump, model.ImportModeIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 1 {
		t.Fatalf("preview mappings = %+v", mappings)
	}
	var count int64
	if err := target.Model(&model.Channel{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("preview wrote rows: count=%d", count)
	}
}
