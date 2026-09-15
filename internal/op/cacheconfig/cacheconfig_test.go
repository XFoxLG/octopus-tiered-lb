package cacheconfig

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/store"
	"github.com/lingyuins/octopus/internal/utils/crypto"
	"gorm.io/gorm"
)

func openCacheConfigTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	crypto.Init("synthetic-cache-config-test-key")
	database, err := db.OpenStandalone("sqlite", filepath.Join(t.TempDir(), "cache.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if err := database.AutoMigrate(&model.ServiceCacheConfig{}, &model.Setting{}); err != nil {
		t.Fatal(err)
	}
	return database
}

func readStoredCacheConfig(t *testing.T, database *gorm.DB, serviceID string) model.ServiceCacheConfig {
	t.Helper()
	var record model.ServiceCacheConfig
	if err := database.Where("service_id = ?", serviceID).Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	return record
}

func TestServiceConfigurationsAreEncryptedScopedAndAdditive(t *testing.T) {
	database := openCacheConfigTestDatabase(t)
	ctx := context.Background()
	legacy := model.Setting{Key: model.SettingKeySemanticCacheEnabled, Value: "true"}
	if err := database.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	experimental := conf.Cache{Type: "redis", Redis: conf.RedisConfig{Addr: "rediss://user:experimental-secret@first.example.test:6379/3"}}
	production := conf.Cache{Type: "redis", Redis: conf.RedisConfig{Addr: "redis://:production-secret@second.example.test:6379"}}
	for serviceID, configuration := range map[string]conf.Cache{"srv-experimental": experimental, "srv-production": production} {
		if err := saveConfiguration(ctx, database, serviceID, configuration); err != nil {
			t.Fatal(err)
		}
		record := readStoredCacheConfig(t, database, serviceID)
		if !crypto.IsEncrypted(record.EncryptedConfig) || strings.Contains(record.EncryptedConfig, "secret") || strings.Contains(record.EncryptedConfig, "example.test") {
			t.Fatal("connection details were not encrypted at rest")
		}
		serialized, err := json.Marshal(record)
		if err != nil || string(serialized) != "{}" {
			t.Fatal("persistence model exposed deployment configuration through JSON")
		}
		loaded, found, err := loadConfiguration(ctx, database, serviceID)
		if err != nil || !found || loaded != configuration {
			t.Fatalf("service configuration did not round trip: found=%t error=%v", found, err)
		}
	}
	productionBefore := readStoredCacheConfig(t, database, "srv-production")
	if err := saveConfiguration(ctx, database, "srv-experimental", conf.Cache{}); err != nil {
		t.Fatal(err)
	}
	if readStoredCacheConfig(t, database, "srv-production") != productionBefore {
		t.Fatal("saving an experimental configuration changed the production row")
	}
	loaded, found, err := loadConfiguration(ctx, database, "srv-experimental")
	if err != nil || !found || loaded.Type != "" {
		t.Fatal("memory selection must survive as an explicit saved record")
	}
	if _, found, err := loadConfiguration(ctx, database, "srv-unknown"); err != nil || found {
		t.Fatal("unknown service inherited another service's configuration")
	}
	var preserved model.Setting
	if err := database.First(&preserved, "key = ?", legacy.Key).Error; err != nil || preserved != legacy {
		t.Fatal("cache persistence modified shared application settings")
	}
}

func TestInvalidSaveDoesNotOverwriteWorkingConfiguration(t *testing.T) {
	database := openCacheConfigTestDatabase(t)
	ctx := context.Background()
	configuration := conf.Cache{Type: "redis", Redis: conf.RedisConfig{Addr: "localhost:6379", Password: "synthetic-secret"}}
	if err := saveConfiguration(ctx, database, "srv-test", configuration); err != nil {
		t.Fatal(err)
	}
	before := readStoredCacheConfig(t, database, "srv-test")
	configuration.Redis.Addr = "rediss://:synthetic-secret@:6379"
	if err := saveConfiguration(ctx, database, "srv-test", configuration); err == nil || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("invalid secret-bearing configuration must fail with a safe error")
	}
	if readStoredCacheConfig(t, database, "srv-test") != before {
		t.Fatal("invalid save modified an existing encrypted record")
	}
	if err := saveConfiguration(ctx, database, "", conf.Cache{}); !errors.Is(err, ErrServiceIdentity) {
		t.Fatal("saving without a service owner must fail")
	}
	if err := saveConfiguration(ctx, nil, "srv-test", conf.Cache{}); err == nil {
		t.Fatal("an unavailable database must not report a successful save")
	}
}

func TestInvalidEncryptedRecordsFailWithoutMutation(t *testing.T) {
	database := openCacheConfigTestDatabase(t)
	ctx := context.Background()
	wrongOwner, err := crypto.Encrypt(`{"service_id":"srv-other","cache":{"Type":""}}`)
	if err != nil {
		t.Fatal(err)
	}
	foreignKey := sha256.Sum256([]byte("a-different-synthetic-key"))
	block, err := aes.NewCipher(foreignKey[:])
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	// Fixed nonce is only for this synthetic, never-persisted-as-live fixture.
	nonce := make([]byte, sealer.NonceSize())
	wrongKey := "enc:" + base64.StdEncoding.EncodeToString(sealer.Seal(nonce, nonce, []byte(`{"service_id":"srv-test","cache":{"Type":""}}`), nil))
	for _, ciphertext := range []string{"plaintext-synthetic-secret", "enc:invalid", wrongOwner, wrongKey} {
		record := model.ServiceCacheConfig{ServiceID: "srv-test", EncryptedConfig: ciphertext}
		if err := database.Save(&record).Error; err != nil {
			t.Fatal(err)
		}
		if _, _, err := loadConfiguration(ctx, database, record.ServiceID); err == nil || strings.Contains(err.Error(), ciphertext) {
			t.Fatal("invalid encrypted record must produce an explicit secret-free error")
		}
		if readStoredCacheConfig(t, database, record.ServiceID).EncryptedConfig != ciphertext {
			t.Fatal("reading a corrupt record changed it")
		}
	}
}

func TestNewURLReplacesIdentityButTuningRetainsUneditedConnection(t *testing.T) {
	current := conf.Cache{Type: "redis", Redis: conf.RedisConfig{
		Addr: "old.example.test:6379", Password: "old-secret", Username: "old-user", TLS: true,
		DB: 4, PoolSize: 5, DialTimeout: time.Second, ReadTimeout: time.Second,
	}}
	retained, err := Prepare(model.CacheConfigRequest{
		Type: "redis", Tuning: &model.CacheRedisTuning{PoolSize: 2, DialTimeout: "2s", ReadTimeout: "4s"},
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	if retained.Redis.Addr != current.Redis.Addr || retained.Redis.Password != current.Redis.Password || retained.Redis.TLS != current.Redis.TLS || retained.Redis.PoolSize != 2 {
		t.Fatal("tuning-only updates must preserve the unedited connection")
	}
	for _, replacement := range []string{"redis://new.example.test:6379", "new.example.test:6379"} {
		prepared, err := Prepare(model.CacheConfigRequest{Type: "redis", Redis: &model.CacheRedisConfig{Addr: replacement}}, current)
		if err != nil {
			t.Fatal(err)
		}
		options, err := store.BuildRedisOptions(prepared.Redis)
		if err != nil || options.Password != "" || options.Username != "" || options.TLSConfig != nil || options.DB != 0 {
			t.Fatal("new target inherited the previous connection identity")
		}
	}
	prepared, err := Prepare(model.CacheConfigRequest{Type: "redis", Redis: &model.CacheRedisConfig{
		Addr: "redis://new.example.test:6379/2", Username: "stale-user", Password: "stale-password", TLS: true, DB: 4,
	}}, current)
	if err != nil {
		t.Fatal(err)
	}
	options, err := store.BuildRedisOptions(prepared.Redis)
	if err != nil || options.Password != "" || options.Username != "" || options.TLSConfig != nil || options.DB != 2 {
		t.Fatal("URL replacement must not use separate stale authentication/TLS fields")
	}
	if _, err := Prepare(model.CacheConfigRequest{Type: "redis", Redis: &model.CacheRedisConfig{}}, current); err == nil {
		t.Fatal("empty replacement must not silently keep the old target")
	}
}

func TestConnectionSummaryContainsOnlySafeMetadata(t *testing.T) {
	summary, hasPassword, err := Describe(conf.RedisConfig{Addr: "rediss://secret-user:secret-password@host.example.test:6379/3?pool_size=2"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPassword || summary.Addr != "host.example.test:6379" || summary.DB != 3 || !summary.TLS || summary.PoolSize != 2 {
		t.Fatal("safe summary did not use the parser's effective options")
	}
	serialized, err := json.Marshal(summary)
	if err != nil || strings.Contains(string(serialized), "secret") || strings.Contains(string(serialized), "rediss://") {
		t.Fatal("display metadata contains connection credentials")
	}
}

func TestEnvironmentOwnershipSkipsDatabaseAndRejectsSave(t *testing.T) {
	original := conf.GetCacheConfig()
	t.Cleanup(func() { conf.SetCacheConfig(original) })
	t.Setenv("RENDER_SERVICE_ID", "srv-experimental")
	t.Setenv("OCTOPUS_CACHE_TYPE", "")
	conf.SetCacheConfig(conf.Cache{})
	if err := Load(context.Background()); err != nil {
		t.Fatalf("environment configuration unexpectedly attempted a database load: %v", err)
	}
	if _, err := Save(context.Background(), model.CacheConfigRequest{Type: "redis", Redis: &model.CacheRedisConfig{Addr: "localhost:6379"}}); !errors.Is(err, ErrEnvironmentManaged) {
		t.Fatal("environment-managed save must fail explicitly")
	}
	if conf.GetCacheConfig() != (conf.Cache{}) {
		t.Fatal("failed save changed desired configuration")
	}
}
