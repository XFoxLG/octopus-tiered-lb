package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/cacheconfig"
	"github.com/lingyuins/octopus/internal/store"
	"github.com/lingyuins/octopus/internal/utils/crypto"
)

func prepareCacheHandlerTest(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	original := conf.GetCacheConfig()
	store.ResetForTest()
	conf.SetCacheConfig(conf.Cache{})
	t.Cleanup(func() {
		store.ResetForTest()
		conf.SetCacheConfig(original)
	})
	for _, variable := range []string{
		"OCTOPUS_CACHE_TYPE", "OCTOPUS_CACHE_REDIS_ADDR", "OCTOPUS_CACHE_REDIS_USERNAME",
		"OCTOPUS_CACHE_REDIS_PASSWORD", "OCTOPUS_CACHE_REDIS_DB", "OCTOPUS_CACHE_REDIS_POOL_SIZE",
		"OCTOPUS_CACHE_REDIS_DIAL_TIMEOUT", "OCTOPUS_CACHE_REDIS_READ_TIMEOUT",
		"OCTOPUS_CACHE_REDIS_TLS", "OCTOPUS_CACHE_REDIS_CA_FILE",
	} {
		t.Setenv(variable, "")
		if err := os.Unsetenv(variable); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("RENDER", "true")
	t.Setenv("RENDER_SERVICE_ID", "srv-experimental")
}

func callCacheHandler(t *testing.T, handler gin.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	requestContext, _ := gin.CreateTestContext(recorder)
	requestContext.Request = httptest.NewRequest(http.MethodPost, "/api/v1/setting/cache/test", strings.NewReader(body))
	requestContext.Request.Header.Set("Content-Type", "application/json")
	requestContext.Set("user_role", "admin")
	handler(requestContext)
	return recorder
}

func TestCacheGetAndPreviewNeverReturnConnectionSecrets(t *testing.T) {
	prepareCacheHandlerTest(t)
	conf.SetCacheConfig(conf.Cache{Type: "redis", Redis: conf.RedisConfig{
		Addr: "rediss://private-user:private-password@host.example.test:6379/2?pool_size=3",
	}})
	for _, handler := range []gin.HandlerFunc{getCacheConfig, previewCacheConnection} {
		recorder := callCacheHandler(t, handler, `{"type":"redis"}`)
		if recorder.Code != http.StatusOK {
			t.Fatalf("safe configuration response failed: %s", recorder.Body.String())
		}
		for _, forbidden := range []string{"private-user", "private-password", "rediss://", `"password":`, `"username":`, `"encrypted_config":`} {
			if strings.Contains(recorder.Body.String(), forbidden) {
				t.Fatalf("configuration response exposed %q", forbidden)
			}
		}
		if !strings.Contains(recorder.Body.String(), `"addr":"host.example.test:6379"`) || !strings.Contains(recorder.Body.String(), `"tls":true`) {
			t.Fatal("configuration preview omitted effective safe metadata")
		}
	}
	if store.Enabled() {
		t.Fatal("preview must not activate the Redis backend")
	}
}

func TestCacheConnectionTestNeverReusesOldCredentialsForNewURL(t *testing.T) {
	prepareCacheHandlerTest(t)
	redisServer := miniredis.RunT(t)
	redisServer.RequireAuth("saved-password")
	conf.SetCacheConfig(conf.Cache{Type: "redis", Redis: conf.RedisConfig{
		Addr: redisServer.Addr(), Password: "saved-password",
	}})
	unchanged := callCacheHandler(t, testCacheConnection, `{"type":"redis"}`)
	if unchanged.Code != http.StatusOK {
		t.Fatalf("testing unchanged connection failed: %s", unchanged.Body.String())
	}
	replacement, err := json.Marshal(model.CacheConfigRequest{Type: "redis", Redis: &model.CacheRedisConfig{Addr: "redis://" + redisServer.Addr()}})
	if err != nil {
		t.Fatal(err)
	}
	changed := callCacheHandler(t, testCacheConnection, string(replacement))
	if changed.Code != http.StatusBadRequest || strings.Contains(changed.Body.String(), "saved-password") {
		t.Fatal("a new URL must not authenticate using the saved password or expose it")
	}
	if store.Enabled() || len(redisServer.Keys()) != 0 {
		t.Fatal("PING must neither activate the backend nor write application keys")
	}
}

func TestCacheSaveSurvivesReloadWithoutSwitchingRuntime(t *testing.T) {
	prepareCacheHandlerTest(t)
	crypto.Init("synthetic-handler-cache-key")
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "cache.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	production := model.ServiceCacheConfig{ServiceID: "srv-production", EncryptedConfig: "untouched-production-record"}
	if err := db.GetDB().Create(&production).Error; err != nil {
		t.Fatal(err)
	}
	redisServer := miniredis.RunT(t)
	redisServer.RequireAuth("saved-password")
	request := model.CacheConfigRequest{Type: "redis", Redis: &model.CacheRedisConfig{
		Addr: "redis://:saved-password@" + redisServer.Addr(),
	}}
	requestBody, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	// Browser-supplied ownership must have no effect on the server's scope.
	spoofedBody := strings.TrimSuffix(string(requestBody), "}") + `,"service_id":"srv-production"}`
	recorder := callCacheHandler(t, saveCacheConfig, spoofedBody)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"restart_needed":true`) {
		t.Fatalf("save did not report pending configuration: %s", recorder.Body.String())
	}
	if store.Enabled() || len(redisServer.Keys()) != 0 {
		t.Fatal("save must not connect or switch the runtime backend")
	}
	var record model.ServiceCacheConfig
	if err := db.GetDB().Where("service_id = ?", "srv-experimental").Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	if !crypto.IsEncrypted(record.EncryptedConfig) || strings.Contains(record.EncryptedConfig, "saved-password") {
		t.Fatal("save did not encrypt the complete connection")
	}
	var unchangedProduction model.ServiceCacheConfig
	if err := db.GetDB().Where("service_id = ?", "srv-production").Take(&unchangedProduction).Error; err != nil || unchangedProduction.EncryptedConfig != production.EncryptedConfig {
		t.Fatal("browser input modified another service's row")
	}
	conf.SetCacheConfig(conf.Cache{})
	if err := cacheconfig.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	loaded := conf.GetCacheConfig()
	if loaded.Type != "redis" || loaded.Redis.Addr != request.Redis.Addr || store.Enabled() {
		t.Fatal("startup loading must recover the saved config without activating it")
	}
	if err := store.Init(loaded.Redis); err != nil {
		t.Fatal(err)
	}
	activeClient := store.Get()
	recorder = callCacheHandler(t, saveCacheConfig, `{"type":""}`)
	if recorder.Code != http.StatusOK || store.Get() != activeClient || !store.Enabled() {
		t.Fatal("saving memory selection must not close active Redis connections")
	}
	conf.SetCacheConfig(conf.Cache{Type: "redis", Redis: loaded.Redis})
	if err := cacheconfig.Load(context.Background()); err != nil || conf.GetCacheConfig().Type != "" {
		t.Fatal("explicit memory selection was not persisted")
	}
	before := conf.GetCacheConfig()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	failed := callCacheHandler(t, saveCacheConfig, string(requestBody))
	if failed.Code == http.StatusOK || conf.GetCacheConfig() != before {
		t.Fatal("failed persistence must not change desired configuration")
	}
}

func TestCacheSaveRejectsEnvironmentOverridesAndMissingIdentity(t *testing.T) {
	prepareCacheHandlerTest(t)
	t.Setenv("OCTOPUS_CACHE_TYPE", "")
	recorder := callCacheHandler(t, saveCacheConfig, `{"type":""}`)
	if recorder.Code != http.StatusConflict {
		t.Fatal("environment override must not report a successful database save")
	}
	if err := os.Unsetenv("OCTOPUS_CACHE_TYPE"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RENDER_SERVICE_ID", "")
	recorder = callCacheHandler(t, saveCacheConfig, `{"type":""}`)
	if recorder.Code != http.StatusConflict {
		t.Fatal("missing service identity must not select a shared default record")
	}
}
