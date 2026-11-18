package backup

import (
	"bytes"
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
	"github.com/lingyuins/octopus/internal/utils/apikeyhash"
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

func TestV6ExportWritesZeroValuesForIncrementalPatch(t *testing.T) {
	if err := internaldb.InitDB("sqlite", filepath.Join(t.TempDir(), "v6-export-zero.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = internaldb.Close() })
	source := internaldb.GetDB()
	if err := source.Create(&model.Group{
		ID:                      7,
		Name:                    "source-zero",
		EndpointType:            "chat",
		Mode:                    model.GroupModeFailover,
		HedgeDelayMs:            0,
		StreamKeepaliveEnabled:  false,
		DefaultReasoningEffort:  "",
		ReasoningForceOverride:  false,
		ReasoningBufferStrategy: "",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Model(&model.Group{}).Where("id = ?", 7).UpdateColumn("hedge_delay_ms", 0).Error; err != nil {
		t.Fatal(err)
	}
	dump, err := ExportAll(context.Background(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := utilsjson.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		`"hedge_delay_ms":0`,
		`"stream_keepalive_enabled":false`,
		`"default_reasoning_effort":""`,
		`"reasoning_force_override":false`,
		`"reasoning_buffer_strategy":""`,
	} {
		if !bytes.Contains(raw, []byte(field)) {
			t.Fatalf("canonical v6 export omitted %s", field)
		}
	}

	target := openChannelUIDBackupTarget(t, "v6-export-zero-target.db")
	if err := target.Create(&model.Group{
		ID:                      7,
		Name:                    "target-nonzero",
		EndpointType:            "chat",
		Mode:                    model.GroupModeFailover,
		HedgeDelayMs:            321,
		StreamKeepaliveEnabled:  true,
		DefaultReasoningEffort:  "high",
		ReasoningForceOverride:  true,
		ReasoningBufferStrategy: "immediate",
	}).Error; err != nil {
		t.Fatal(err)
	}
	var decoded model.DBDump
	if err := utilsjson.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportWithModeToDB(context.Background(), target, &decoded, model.ImportModeIncremental); err != nil {
		t.Fatal(err)
	}
	var got model.Group
	if err := target.First(&got, 7).Error; err != nil {
		t.Fatal(err)
	}
	if got.HedgeDelayMs != 0 || got.StreamKeepaliveEnabled ||
		got.DefaultReasoningEffort != "" || got.ReasoningForceOverride ||
		got.ReasoningBufferStrategy != "" {
		t.Fatalf("canonical export did not restore explicit zero values: %+v", got)
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

func TestV6IncrementalJSONPatchPreservesMissingAndNullFields(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-incremental-patch.db")
	group := model.Group{
		ID:                      7,
		Name:                    "keep-name",
		EndpointType:            "chat",
		Mode:                    model.GroupModeFailover,
		HedgeDelayMs:            321,
		StreamKeepaliveEnabled:  true,
		DefaultReasoningEffort:  "high",
		ReasoningForceOverride:  true,
		ReasoningBufferStrategy: "immediate",
	}
	if err := target.Create(&group).Error; err != nil {
		t.Fatal(err)
	}

	var nullPatch model.DBDump
	if err := utilsjson.Unmarshal([]byte(`{
		"version":6,
		"sections":["groups"],
		"groups":[{"id":7,"hedge_delay_ms":null}]
	}`), &nullPatch); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportWithModeToDB(context.Background(), target, &nullPatch, model.ImportModeIncremental); err != nil {
		t.Fatal(err)
	}
	var got model.Group
	if err := target.First(&got, 7).Error; err != nil {
		t.Fatal(err)
	}
	if got.HedgeDelayMs != 321 || !got.StreamKeepaliveEnabled ||
		got.DefaultReasoningEffort != "high" || !got.ReasoningForceOverride ||
		got.ReasoningBufferStrategy != "immediate" {
		t.Fatalf("null/missing fields changed target row: %+v", got)
	}

	var zeroPatch model.DBDump
	if err := utilsjson.Unmarshal([]byte(`{
		"version":6,
		"sections":["groups"],
		"groups":[{
			"id":7,
			"hedge_delay_ms":0,
			"stream_keepalive_enabled":false,
			"default_reasoning_effort":"",
			"reasoning_force_override":false,
			"reasoning_buffer_strategy":""
		}]
	}`), &zeroPatch); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := ImportWithModeToDB(context.Background(), target, &zeroPatch, model.ImportModeIncremental); err != nil {
			t.Fatalf("patch import %d: %v", i, err)
		}
	}
	if err := target.First(&got, 7).Error; err != nil {
		t.Fatal(err)
	}
	if got.Name != "keep-name" || got.EndpointType != "chat" || got.Mode != model.GroupModeFailover {
		t.Fatalf("missing identity/semantic fields changed target row: %+v", got)
	}
	if got.HedgeDelayMs != 0 || got.StreamKeepaliveEnabled ||
		got.DefaultReasoningEffort != "" || got.ReasoningForceOverride ||
		got.ReasoningBufferStrategy != "" {
		t.Fatalf("explicit zero values were not applied: %+v", got)
	}
}

func TestV6IncrementalJSONPatchRequiresPresentKey(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-incremental-missing-key.db")
	if err := target.Create(&model.Setting{Key: model.SettingKeyProxyURL, Value: "keep"}).Error; err != nil {
		t.Fatal(err)
	}
	var patch model.DBDump
	if err := utilsjson.Unmarshal([]byte(`{
		"version":6,
		"sections":["settings"],
		"settings":[{"value":"must-not-change"}]
	}`), &patch); err != nil {
		t.Fatal(err)
	}
	_, err := ImportWithModeToDB(context.Background(), target, &patch, model.ImportModeIncremental)
	if err == nil || !strings.Contains(err.Error(), `non-null key field "key"`) {
		t.Fatalf("missing key patch error = %v", err)
	}
	var stored model.Setting
	if err := target.Where("key = ?", model.SettingKeyProxyURL).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Value != "keep" {
		t.Fatalf("missing-key patch changed target value to %q", stored.Value)
	}
}

func TestV6FieldPresenceSurvivesEnvelopeDecode(t *testing.T) {
	var envelope struct {
		model.DBDump
		Code int `json:"code"`
	}
	if err := utilsjson.Unmarshal([]byte(`{
		"code":200,
		"version":6,
		"sections":["settings"],
		"settings":[{"key":"proxy_url","value":null}]
	}`), &envelope); err != nil {
		t.Fatal(err)
	}
	presence, ok := envelope.RowFieldPresence("settings", 0)
	if !ok {
		t.Fatal("embedded DBDump lost row field presence")
	}
	if !presence.Present["value"] || presence.NonNull["value"] {
		t.Fatalf("unexpected value presence: %+v", presence)
	}
	if !presence.Present["key"] || !presence.NonNull["key"] {
		t.Fatalf("unexpected key presence: %+v", presence)
	}
}

func TestV6SettingsPatchPresenceFollowsFilteredRows(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-settings-presence-filter.db")
	if err := target.Create(&[]model.Setting{
		{Key: model.SettingKeyProxyURL, Value: "keep"},
		{Key: model.SettingKeyWebAuthnRPID, Value: "target.example"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	var patch model.DBDump
	if err := utilsjson.Unmarshal([]byte(`{
		"version":6,
		"sections":["settings"],
		"settings":[
			{"key":"webauthn_rp_id","value":"source.example"},
			{"key":"proxy_url","value":null}
		]
	}`), &patch); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportWithModeToDB(context.Background(), target, &patch, model.ImportModeIncremental); err != nil {
		t.Fatal(err)
	}
	var proxy model.Setting
	if err := target.Where("key = ?", model.SettingKeyProxyURL).First(&proxy).Error; err != nil {
		t.Fatal(err)
	}
	if proxy.Value != "keep" {
		t.Fatalf("filtered settings row reused the wrong presence metadata: %q", proxy.Value)
	}
	var rp model.Setting
	if err := target.Where("key = ?", model.SettingKeyWebAuthnRPID).First(&rp).Error; err != nil {
		t.Fatal(err)
	}
	if rp.Value != "target.example" {
		t.Fatalf("WebAuthn RP ID changed to %q", rp.Value)
	}
}

func TestV6APIKeyPatchRefreshesDerivedHash(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-apikey-derived-hash.db")
	if err := target.Create(&model.APIKey{
		ID:         14,
		Name:       "keep-name",
		APIKey:     "old-key",
		APIKeyHash: "old-hash",
		Enabled:    true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	var patch model.DBDump
	if err := utilsjson.Unmarshal([]byte(`{
		"version":6,
		"sections":["api_keys"],
		"api_keys":[{"id":14,"api_key":"new-key"}]
	}`), &patch); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportWithModeToDB(context.Background(), target, &patch, model.ImportModeIncremental); err != nil {
		t.Fatal(err)
	}
	var got model.APIKey
	if err := target.First(&got, 14).Error; err != nil {
		t.Fatal(err)
	}
	if got.Name != "keep-name" || !got.Enabled {
		t.Fatalf("unrelated API key fields changed: %+v", got)
	}
	if got.APIKeyHash != apikeyhash.Sum("new-key") {
		t.Fatalf("derived api_key_hash was not refreshed: %q", got.APIKeyHash)
	}
}

func TestV6ChannelPatchCanOmitStableUID(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-channel-patch-omits-uid.db")
	uid := uuid.NewString()
	if err := target.Create(&model.Channel{
		ID:         9,
		Name:       "keep-channel",
		Type:       outbound.OutboundTypeOpenAIChat,
		ChannelUID: uid,
		Enabled:    true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	var patch model.DBDump
	if err := utilsjson.Unmarshal([]byte(`{
		"version":6,
		"sections":["channels"],
		"channels":[{"id":9,"enabled":false}]
	}`), &patch); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportWithModeToDB(context.Background(), target, &patch, model.ImportModeIncremental); err != nil {
		t.Fatal(err)
	}
	var got model.Channel
	if err := target.First(&got, 9).Error; err != nil {
		t.Fatal(err)
	}
	if got.ChannelUID != uid || got.Name != "keep-channel" || got.Type != outbound.OutboundTypeOpenAIChat {
		t.Fatalf("missing channel fields changed: %+v", got)
	}
	if got.Enabled {
		t.Fatal("explicit enabled=false was not applied")
	}
}

func TestV6SparseChannelInsertGeneratesStableUID(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-channel-patch-insert-uid.db")
	var patch model.DBDump
	if err := utilsjson.Unmarshal([]byte(`{
		"version":6,
		"sections":["channels"],
		"channels":[{"id":11,"name":"new-channel","enabled":true}]
	}`), &patch); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportWithModeToDB(context.Background(), target, &patch, model.ImportModeIncremental); err != nil {
		t.Fatal(err)
	}
	var got model.Channel
	if err := target.First(&got, 11).Error; err != nil {
		t.Fatal(err)
	}
	if got.ChannelUID == "" {
		t.Fatal("sparse channel insert did not generate channel_uid")
	}
	if _, err := uuid.Parse(got.ChannelUID); err != nil {
		t.Fatalf("generated channel_uid is not a UUID: %q: %v", got.ChannelUID, err)
	}
	if got.Name != "new-channel" || !got.Enabled {
		t.Fatalf("explicit sparse insert fields changed: %+v", got)
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

func TestV6FullRestorePreservesTargetWebAuthnSettings(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-webauthn-full.db")
	if err := target.Create(&[]model.Setting{
		{Key: model.SettingKeyWebAuthnRPID, Value: "target.example"},
		{Key: model.SettingKeyWebAuthnRPName, Value: "Target"},
		{Key: model.SettingKeyProxyURL, Value: "old-proxy"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	dump := &model.DBDump{
		Version:  6,
		Sections: []string{"settings"},
		Settings: []model.Setting{
			{Key: model.SettingKeyWebAuthnRPID, Value: "source.example"},
			{Key: model.SettingKeyWebAuthnRPName, Value: "Source"},
			{Key: model.SettingKeyProxyURL, Value: "new-proxy"},
		},
	}
	if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull); err != nil {
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
		got[model.SettingKeyProxyURL] != "new-proxy" {
		t.Fatalf("full restore changed identity settings or missed ordinary setting: %+v", got)
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
	mapping, mappings, err := PreviewChannelMapping(context.Background(), dump, model.ImportModeIncremental, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := mapping[5]; got != 9 {
		t.Fatalf("preview resolved mapping[5] = %d, want 9", got)
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
