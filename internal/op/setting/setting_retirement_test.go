package setting

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
)

func TestRetiredSemanticSettingsPreserveSharedRows(test *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(test.TempDir(), "retired-settings.db"), false); err != nil {
		test.Fatalf("initialize fixture database: %v", err)
	}
	test.Cleanup(func() { _ = db.Close() })
	test.Cleanup(func() { settingCache.Clear() })
	fixtureContext := context.Background()
	retiredKeys := model.RetiredSemanticCacheSettingKeys()

	if err := RefreshCache(fixtureContext); err != nil {
		test.Fatalf("refresh fresh settings: %v", err)
	}
	var retiredRowCount int64
	if err := db.GetDB().Model(&model.Setting{}).Where("key IN ?", retiredKeys).Count(&retiredRowCount).Error; err != nil {
		test.Fatalf("count retired fixture rows: %v", err)
	}
	if retiredRowCount != 0 {
		test.Fatalf("fresh database contains %d retired settings", retiredRowCount)
	}

	legacySettings := []model.Setting{
		{Key: model.SettingKeySemanticCacheEnabled, Value: "true"},
		{Key: model.SettingKeySemanticCacheTTL, Value: " 7200 "},
		{Key: model.SettingKeySemanticCacheEmbeddingAPIKey, Value: "fixture-only-legacy-key"},
	}
	if err := db.GetDB().Create(&legacySettings).Error; err != nil {
		test.Fatalf("seed shared legacy settings: %v", err)
	}
	for _, legacySetting := range legacySettings {
		settingCache.Set(legacySetting.Key, legacySetting.Value)
	}
	for _, refresh := range []bool{false, true} {
		if refresh {
			if err := RefreshCache(fixtureContext); err != nil {
				test.Fatalf("refresh legacy settings: %v", err)
			}
		}
		visibleSettings, err := List(fixtureContext)
		if err != nil {
			test.Fatalf("list settings: %v", err)
		}
		for _, visibleSetting := range visibleSettings {
			if model.IsRetiredSemanticCacheSetting(visibleSetting.Key) {
				test.Errorf("List exposes %q (refreshed=%t)", visibleSetting.Key, refresh)
			}
		}
		for _, retiredKey := range retiredKeys {
			for _, rejectedValue := range []string{"true", "false", ""} {
				if err := SetString(retiredKey, rejectedValue); err == nil {
					test.Errorf("SetString accepted %q (refreshed=%t)", retiredKey, refresh)
				}
			}
			if err := SetInt(retiredKey, 10); err == nil {
				test.Errorf("SetInt accepted %q (refreshed=%t)", retiredKey, refresh)
			}
		}
	}
	for _, legacySetting := range legacySettings {
		var storedSetting model.Setting
		if err := db.GetDB().First(&storedSetting, "key = ?", legacySetting.Key).Error; err != nil {
			test.Fatalf("read preserved fixture setting: %v", err)
		}
		if storedSetting.Value != legacySetting.Value {
			test.Errorf("retired setting %q changed from %q to %q", legacySetting.Key, legacySetting.Value, storedSetting.Value)
		}
		if _, exists := settingCache.Get(legacySetting.Key); exists {
			test.Errorf("refresh loaded retired setting %q into the runtime cache", legacySetting.Key)
		}
	}
	if err := db.GetDB().Model(&model.Setting{}).Where("key IN ?", retiredKeys).Count(&retiredRowCount).Error; err != nil {
		test.Fatalf("count preserved fixture rows: %v", err)
	}
	if retiredRowCount != int64(len(legacySettings)) {
		test.Errorf("retired rows = %d, want exactly %d original rows", retiredRowCount, len(legacySettings))
	}
	if err := SetString(model.SettingKeyRelayRetryCount, "2"); err != nil {
		test.Fatalf("active setting write failed: %v", err)
	}
	if value, err := GetInt(model.SettingKeyRelayRetryCount); err != nil || value != 2 {
		test.Errorf("active setting = %d, %v; want 2", value, err)
	}
}
