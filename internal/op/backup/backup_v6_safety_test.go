package backup

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	internaldb "github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	utilsjson "github.com/lingyuins/octopus/internal/utils/json"
	"gorm.io/gorm"
)

func TestV6JSONRejectsNullRowsInImportedCollections(t *testing.T) {
	sections := make([]string, 0, len(allSections())+16)
	for _, section := range allSections() {
		sections = append(sections, section.name)
	}
	sections = append(sections,
		"stats_total", "stats_daily", "stats_hourly", "stats_model",
		"stats_channel", "stats_api_key", "stats_daily_channel",
		"stats_daily_model", "stats_daily_api_key", "stats_daily_channel_model",
		"error_logs", "audit_logs", "relay_logs", "relay_log_attempts",
		"relay_log_content_refs", "relay_log_content_blobs",
	)

	for _, section := range sections {
		t.Run(section, func(t *testing.T) {
			raw := fmt.Sprintf(`{"version":6,"sections":[%q],%q:[null]}`, section, section)
			var dump model.DBDump
			err := utilsjson.Unmarshal([]byte(raw), &dump)
			if err == nil {
				t.Fatalf("section %q accepted a JSON null row", section)
			}
			want := fmt.Sprintf("section %q row 0", section)
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("decode error = %v, want section and row index %q", err, want)
			}
		})
	}
}

func TestBackupJSONRejectsNullRowsAcrossVersionsAndFieldCasing(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{
			name:    "legacy imported rows",
			payload: `{"version":5,"settings":[null]}`,
			wantErr: true,
		},
		{
			name:    "legacy ignored users",
			payload: `{"version":5,"users":[null]}`,
		},
		{
			name:    "v6 case-insensitive collection field",
			payload: `{"Version":6,"Sections":["channels"],"Channels":[null]}`,
			wantErr: true,
		},
		{
			name:    "v6 case-insensitive ignored users",
			payload: `{"Version":6,"Users":[null]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var dump model.DBDump
			err := utilsjson.Unmarshal([]byte(tc.payload), &dump)
			if (err != nil) != tc.wantErr {
				t.Fatalf("decode error = %v, want error %t", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), `section "settings" row 0`) &&
				!strings.Contains(err.Error(), `section "channels" row 0`) {
				t.Fatalf("decode error lacks section and row location: %v", err)
			}
		})
	}
}

func TestV6JSONIgnoresNullAdministratorRows(t *testing.T) {
	target := openChannelUIDBackupTarget(t, "v6-null-users.db")
	if err := target.Create(&model.User{Username: "target-admin", Role: model.UserRoleAdmin}).Error; err != nil {
		t.Fatal(err)
	}

	raw := `{"version":6,"sections":["settings"],"settings":[],"users":[null]}`
	var dump model.DBDump
	if err := utilsjson.Unmarshal([]byte(raw), &dump); err != nil {
		t.Fatalf("decode backup with ignored null administrator row: %v", err)
	}
	result, err := ImportWithModeToDB(context.Background(), target, &dump, model.ImportModeIncremental)
	if err != nil {
		t.Fatalf("import backup with ignored null administrator row: %v", err)
	}
	if len(result.Warnings) == 0 ||
		!strings.Contains(result.Warnings[0], "administrator identity rows in the backup were ignored") {
		t.Fatalf("missing ignored administrator warning: %+v", result.Warnings)
	}
	var users []model.User
	if err := target.Find(&users).Error; err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Username != "target-admin" {
		t.Fatalf("ignored administrator rows changed target users: %+v", users)
	}
}

func TestV6FullRestoreProtectsUnownedChildCollections(t *testing.T) {
	cases := []struct {
		name          string
		parentSection string
		childSection  string
		parentTable   string
		childTable    string
		parentColumn  string
		parentID      uint
		childID       uint
		seed          func(*testing.T, *gorm.DB)
	}{
		{
			name:          "probe",
			parentSection: "channel_probe_runs",
			childSection:  "channel_probe_results",
			parentTable:   "channel_probe_runs",
			childTable:    "channel_probe_results",
			parentColumn:  "run_id",
			parentID:      101,
			childID:       201,
			seed: func(t *testing.T, target *gorm.DB) {
				t.Helper()
				if err := target.Create(&model.ChannelProbeRun{
					ID: 101, ChannelID: 1, ModelName: "model", StartedAt: time.Now(),
				}).Error; err != nil {
					t.Fatal(err)
				}
				if err := target.Create(&model.ChannelProbeResult{
					ID: 201, RunID: 101, ChannelID: 1, ModelName: "model",
					Kind: model.ProbeKindProtocol, Item: model.ProbeItemProtocolChat,
					Verdict: model.ProbeVerdictPass,
				}).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:          "group_health",
			parentSection: "group_health_snapshots",
			childSection:  "group_health_attempts",
			parentTable:   "group_health_snapshots",
			childTable:    "group_health_attempts",
			parentColumn:  "snapshot_id",
			parentID:      301,
			childID:       401,
			seed: func(t *testing.T, target *gorm.DB) {
				t.Helper()
				if err := target.AutoMigrate(&model.GroupHealthSnapshot{}, &model.GroupHealthAttempt{}); err != nil {
					t.Fatal(err)
				}
				if err := target.Create(&model.GroupHealthSnapshot{
					ID: 301, GroupID: 1, GroupName: "group", GroupMode: model.GroupModeFailover,
					RequestModel: "model", Status: model.GroupHealthStatusSuccess, StartedAt: time.Now(),
				}).Error; err != nil {
					t.Fatal(err)
				}
				if err := target.Create(&model.GroupHealthAttempt{
					ID: 401, SnapshotID: 301, ChannelID: 1, ChannelName: "channel",
					ModelName: "model", Status: model.GroupHealthAttemptStatusSuccess,
				}).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tc := range cases {
		for _, childState := range []string{"omitted", "null"} {
			t.Run(tc.name+"/"+childState, func(t *testing.T) {
				target := openChannelUIDBackupTarget(t, "v6-protected-"+tc.name+"-"+childState+".db")
				tc.seed(t, target)
				if err := target.Create(&model.Setting{
					Key: model.SettingKeyProxyURL, Value: "preserve",
				}).Error; err != nil {
					t.Fatal(err)
				}

				raw := fmt.Sprintf(
					`{"version":6,"sections":[%q],%q:[]}`,
					tc.parentSection, tc.parentSection,
				)
				if childState == "null" {
					raw = fmt.Sprintf(
						`{"version":6,"sections":[%q,%q],%q:[],%q:null}`,
						tc.parentSection, tc.childSection, tc.parentSection, tc.childSection,
					)
				}
				var dump model.DBDump
				if err := utilsjson.Unmarshal([]byte(raw), &dump); err != nil {
					t.Fatal(err)
				}

				_, err := ImportWithModeToDB(context.Background(), target, &dump, model.ImportModeFull)
				if err == nil ||
					!strings.Contains(err.Error(), fmt.Sprintf("unowned section %q", tc.childSection)) ||
					!strings.Contains(err.Error(), "still references removed parent id") {
					t.Fatalf("full restore error = %v, want unowned child dependency", err)
				}
				assertRowPresent(t, target, tc.parentTable, tc.parentID)
				assertRowPresent(t, target, tc.childTable, tc.childID)
				var setting model.Setting
				if err := target.First(&setting, "key = ?", model.SettingKeyProxyURL).Error; err != nil {
					t.Fatalf("preflight failure deleted existing setting: %v", err)
				}
				if setting.Value != "preserve" {
					t.Fatalf("preflight failure changed existing setting to %q", setting.Value)
				}
			})
		}

		t.Run(tc.name+"/explicit_empty_child", func(t *testing.T) {
			target := openChannelUIDBackupTarget(t, "v6-empty-child-"+tc.name+".db")
			tc.seed(t, target)
			dump := &model.DBDump{
				Version:  6,
				Sections: []string{tc.parentSection, tc.childSection},
			}
			switch tc.name {
			case "probe":
				dump.ChannelProbeRuns = []model.ChannelProbeRun{}
				dump.ChannelProbeResults = []model.ChannelProbeResult{}
			case "group_health":
				dump.GroupHealthSnapshots = []model.GroupHealthSnapshot{}
				dump.GroupHealthAttempts = []model.GroupHealthAttempt{}
			default:
				t.Fatalf("unhandled case %q", tc.name)
			}

			if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull); err != nil {
				t.Fatalf("explicit empty parent/child sections must replace safely: %v", err)
			}
			assertRowAbsent(t, target, tc.parentTable, tc.parentID)
			assertRowAbsent(t, target, tc.childTable, tc.childID)
		})
	}
}

func TestV6PreviewAcceptsExplicitChannelMappingWithoutWriting(t *testing.T) {
	if err := internaldb.InitDB("sqlite", filepath.Join(t.TempDir(), "v6-preview-explicit.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = internaldb.Close() })

	target := internaldb.GetDB()
	if err := target.Create(&model.Channel{
		ID: 9, Name: "target", Type: outbound.OutboundTypeOpenAIChat,
	}).Error; err != nil {
		t.Fatal(err)
	}
	var dump model.DBDump
	raw := `{"version":6,"sections":["channels"],"channels":[{"id":5,"name":"legacy"}]}`
	if err := utilsjson.Unmarshal([]byte(raw), &dump); err != nil {
		t.Fatal(err)
	}

	mapping, mappings, err := PreviewChannelMapping(
		context.Background(), &dump, model.ImportModeIncremental, map[int]int{5: 9},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := mapping[5]; got != 9 {
		t.Fatalf("resolved mapping[5] = %d, want 9", got)
	}
	if len(mappings) != 1 ||
		mappings[0].SourceChannelID != 5 ||
		mappings[0].TargetChannelID != 9 ||
		mappings[0].Reason != "explicit" {
		t.Fatalf("preview mappings = %+v, want explicit 5 -> 9", mappings)
	}
	var count int64
	if err := target.Model(&model.Channel{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("preview wrote channels: count=%d", count)
	}
	if err := target.Model(&model.Channel{}).Where("id = ?", 5).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("preview inserted source channel: count=%d", count)
	}
}

func assertRowPresent(t *testing.T, conn *gorm.DB, table string, id uint) {
	t.Helper()
	var count int64
	if err := conn.Table(table).Where("id = ?", id).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("%s row %d count = %d, want 1", table, id, count)
	}
}

func assertRowAbsent(t *testing.T, conn *gorm.DB, table string, id uint) {
	t.Helper()
	var count int64
	if err := conn.Table(table).Where("id = ?", id).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("%s row %d count = %d, want 0", table, id, count)
	}
}
