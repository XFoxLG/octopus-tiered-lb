package cacheconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/utils/crypto"
	"github.com/spf13/viper"
)

func preparePrecedenceTest(t *testing.T, serviceID string) string {
	t.Helper()
	originalConfiguration := conf.GetCacheConfig()
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		conf.SetCacheConfig(originalConfiguration)
	})
	t.Setenv("OCTOPUS_DATA_DIR", t.TempDir())
	t.Setenv("OCTOPUS_AUTH_JWT_SECRET", "test-secret-not-for-production")
	t.Setenv("RENDER", "")
	t.Setenv("RENDER_SERVICE_ID", serviceID)
	for _, variable := range []string{
		"OCTOPUS_CACHE_TYPE", "OCTOPUS_CACHE_REDIS_ADDR", "OCTOPUS_CACHE_REDIS_USERNAME",
		"OCTOPUS_CACHE_REDIS_PASSWORD", "OCTOPUS_CACHE_REDIS_DB", "OCTOPUS_CACHE_REDIS_POOL_SIZE",
		"OCTOPUS_CACHE_REDIS_DIAL_TIMEOUT", "OCTOPUS_CACHE_REDIS_READ_TIMEOUT",
		"OCTOPUS_CACHE_REDIS_TLS", "OCTOPUS_CACHE_REDIS_CA_FILE",
		"OCTOPUS_DATABASE_TYPE", "OCTOPUS_DATABASE_PATH", "OCTOPUS_SECURITY_ENCRYPTION_KEY",
	} {
		t.Setenv(variable, "")
		if err := os.Unsetenv(variable); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(t.TempDir(), "config.json")
}

func TestServiceRecordSupersedesInvalidFileCacheSection(t *testing.T) {
	configPath := preparePrecedenceTest(t, "srv-experimental")
	if err := os.WriteFile(configPath, []byte(`{"cache":{"type":"redis","redis":{"addr":""}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	// The file cache section is invalid, but a higher-priority service record
	// owns the configuration; file loading must not block startup.
	if err := conf.Load(configPath); err != nil {
		t.Fatalf("invalid file cache section blocked a higher-priority source: %v", err)
	}
	crypto.Init("synthetic-precedence-test-key")
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "precedence.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := Save(context.Background(), model.CacheConfigRequest{
		Type: "redis", Redis: &model.CacheRedisConfig{Addr: "redis://cache.example.test:6379"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Load(context.Background()); err != nil {
		t.Fatalf("service record load failed: %v", err)
	}
	loaded := conf.GetCacheConfig()
	if loaded.Type != "redis" || loaded.Redis.Addr != "redis://cache.example.test:6379" {
		t.Fatalf("service record did not supersede the invalid file section: %+v", loaded)
	}
}

func TestMissingServiceRecordStillRejectsInvalidFileFallback(t *testing.T) {
	configPath := preparePrecedenceTest(t, "srv-experimental")
	if err := os.WriteFile(configPath, []byte(`{"cache":{"type":"redis","redis":{"addr":""}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := conf.Load(configPath); err != nil {
		t.Fatalf("file loading should defer to the effective source: %v", err)
	}
	crypto.Init("synthetic-precedence-test-key")
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "precedence.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	err := Load(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cache.redis.addr is required") {
		t.Fatalf("invalid file fallback must fail explicitly once no record exists: %v", err)
	}
}

func TestFileSourceStillRejectsInvalidCacheSectionEagerly(t *testing.T) {
	configPath := preparePrecedenceTest(t, "")
	if err := os.WriteFile(configPath, []byte(`{"cache":{"type":"redis","redis":{"addr":""}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	err := conf.Load(configPath)
	if err == nil || !strings.Contains(err.Error(), "cache.redis.addr is required") {
		t.Fatalf("file-managed cache section must still be validated at load: %v", err)
	}
}
