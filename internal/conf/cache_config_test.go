package conf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

func resetConfigurationForTest(t *testing.T) {
	t.Helper()
	original := AppConfig
	originalEphemeral := ephemeralJWTSecret
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		AppConfig = original
		ephemeralJWTSecret = originalEphemeral
	})
	t.Setenv("OCTOPUS_DATA_DIR", t.TempDir())
	t.Setenv("OCTOPUS_AUTH_JWT_SECRET", "test-secret-not-for-production")
	t.Setenv("RENDER", "")
	t.Setenv("RENDER_SERVICE_ID", "")
}

func TestLoadRedisEnvironmentInFreshContainer(t *testing.T) {
	resetConfigurationForTest(t)
	t.Setenv("OCTOPUS_CACHE_TYPE", "redis")
	t.Setenv("OCTOPUS_CACHE_REDIS_ADDR", "valkey.example.test:12345")
	t.Setenv("OCTOPUS_CACHE_REDIS_PASSWORD", "synthetic-password")
	t.Setenv("OCTOPUS_CACHE_REDIS_USERNAME", "service-user")
	t.Setenv("OCTOPUS_CACHE_REDIS_DB", "3")
	t.Setenv("OCTOPUS_CACHE_REDIS_POOL_SIZE", "4")
	t.Setenv("OCTOPUS_CACHE_REDIS_TLS", "true")
	t.Setenv("OCTOPUS_CACHE_REDIS_CA_FILE", "/etc/ssl/test-ca.pem")
	t.Setenv("OCTOPUS_DATABASE_POOL_MAX_OPEN_CONNS", "6")
	t.Setenv("OCTOPUS_DATABASE_LOG_POOL_MAX_OPEN_CONNS", "2")
	if err := Load(""); err != nil {
		t.Fatal(err)
	}
	createdConfig, err := os.ReadFile(defaultConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(createdConfig), "synthetic-password") || strings.Contains(string(createdConfig), "test-secret-not-for-production") {
		t.Fatal("fresh startup must not copy deployment secrets into the local config file")
	}
	configuration := AppConfig.Cache.Redis
	if configuration.Addr != "valkey.example.test:12345" || configuration.Password != "synthetic-password" || configuration.Username != "service-user" || configuration.DB != 3 || configuration.PoolSize != 4 || !configuration.TLS || configuration.CAFile != "/etc/ssl/test-ca.pem" {
		t.Fatal("environment-only Redis configuration was not fully decoded")
	}
	if AppConfig.Database.Pool.MaxOpenConns != 6 || AppConfig.Database.LogPool.MaxOpenConns != 2 {
		t.Fatal("database pool environment configuration was not decoded")
	}
	if CacheConfigSource() != "environment" {
		t.Fatal("environment configuration must not be reported as an editable file")
	}
	if err := SaveCacheConfig("redis", configuration); err == nil {
		t.Fatal("saving must not silently override deployment environment")
	}
}

func TestCacheSaveDoesNotCopyUnrelatedEnvironmentSecrets(t *testing.T) {
	resetConfigurationForTest(t)
	if err := Load(""); err != nil {
		t.Fatal(err)
	}
	if err := SaveCacheConfig("", RedisConfig{}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(defaultConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "test-secret-not-for-production") {
		t.Fatal("saving cache must not persist unrelated deployment credentials")
	}
}

func TestCacheSaveZeroDurationReloads(t *testing.T) {
	resetConfigurationForTest(t)
	if err := Load(""); err != nil {
		t.Fatal(err)
	}
	configuration := RedisConfig{Addr: "valkey.example.test:12345", TLS: true, PoolSize: 5}
	if err := SaveCacheConfig("redis", configuration); err != nil {
		t.Fatal(err)
	}
	configPath := viper.ConfigFileUsed()
	viper.Reset()
	if err := Load(configPath); err != nil {
		t.Fatalf("saved zero durations must decode after restart: %v", err)
	}
	if AppConfig.Cache.Redis.DialTimeout != 0 || AppConfig.Cache.Redis.ReadTimeout != 0 || !AppConfig.Cache.Redis.TLS {
		t.Fatal("cache configuration did not survive the round trip")
	}
}

func TestLoadRejectsUnsafeDatabasePool(t *testing.T) {
	resetConfigurationForTest(t)
	t.Setenv("OCTOPUS_DATABASE_POOL_MAX_OPEN_CONNS", "0")
	if err := Load(""); err == nil {
		t.Fatal("unbounded database pool must not be accepted")
	}
}

func TestCacheSaveRejectsNegativeTimeoutWithoutChangingFile(t *testing.T) {
	resetConfigurationForTest(t)
	if err := Load(""); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Clean(defaultConfigPath())
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveCacheConfig("redis", RedisConfig{Addr: "localhost:6379", DialTimeout: -time.Second}); err == nil {
		t.Fatal("negative timeout should fail before saving")
	}
	after, err := os.ReadFile(configPath)
	if err != nil || string(before) != string(after) {
		t.Fatal("invalid configuration changed the saved file")
	}
}
