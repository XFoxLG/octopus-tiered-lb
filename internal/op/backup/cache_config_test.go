package backup

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
)

func TestBackupPreservesRetiredSettingsAndExcludesServiceCredentials(t *testing.T) {
	for _, mode := range []string{model.ImportModeIncremental, model.ImportModeFull} {
		t.Run(mode, func(t *testing.T) {
			if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "backup.db"), false); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			legacy := []model.Setting{
				{Key: model.SettingKeySemanticCacheEnabled, Value: "true"},
				{Key: model.SettingKeySemanticCacheEmbeddingAPIKey, Value: "old-embedding-credential"},
			}
			if err := db.GetDB().Create(&legacy).Error; err != nil {
				t.Fatal(err)
			}
			serviceConfig := model.ServiceCacheConfig{ServiceID: "srv-test", EncryptedConfig: "enc:synthetic-cache-ciphertext"}
			if err := db.GetDB().Create(&serviceConfig).Error; err != nil {
				t.Fatal(err)
			}
			dump, err := ExportAll(context.Background(), false, false)
			if err != nil {
				t.Fatal(err)
			}
			serialized, err := json.Marshal(dump)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(serialized), "synthetic-cache-ciphertext") || strings.Contains(string(serialized), "service_cache_configs") {
				t.Fatal("ordinary application backup must not transport Redis deployment credentials")
			}
			if !strings.Contains(string(serialized), string(model.SettingKeySemanticCacheEnabled)) {
				t.Fatal("legacy settings must remain available in historical backups")
			}
			incoming := &model.DBDump{Version: 1, Settings: []model.Setting{
				{Key: model.SettingKeySemanticCacheEnabled, Value: "false"},
				{Key: model.SettingKeySemanticCacheEmbeddingModel, Value: "legacy-embedding-model"},
				{Key: model.SettingKeyStatsSaveInterval, Value: "10"},
			}}
			if _, err := ImportWithMode(context.Background(), incoming, mode); err != nil {
				t.Fatal(err)
			}
			for _, expected := range legacy {
				var actual model.Setting
				if err := db.GetDB().First(&actual, "key = ?", expected.Key).Error; err != nil || actual != expected {
					t.Fatalf("%s import changed a shared legacy setting", mode)
				}
			}
			var imported model.Setting
			if err := db.GetDB().First(&imported, "key = ?", model.SettingKeySemanticCacheEmbeddingModel).Error; err != nil || imported.Value != "legacy-embedding-model" {
				t.Fatal("old backups should remain readable without recreating runtime functionality")
			}
			var preserved model.ServiceCacheConfig
			if err := db.GetDB().Where("service_id = ?", serviceConfig.ServiceID).Take(&preserved).Error; err != nil || preserved.EncryptedConfig != serviceConfig.EncryptedConfig {
				t.Fatal("application restore deleted or replaced deployment-owned cache configuration")
			}
		})
	}
}
