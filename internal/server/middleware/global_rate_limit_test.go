package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/ratelimitstore"
	"github.com/lingyuins/octopus/internal/op/setting"
)

// Legacy settings must not restore retired downstream rate limits.
func TestGlobalRelayRateLimit_DisabledPassesThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := stubGlobalRateLimitSettings(t, "0", "0")
	defer restore()

	engine := gin.New()
	engine.Use(GlobalRelayRateLimit())
	engine.GET("/probe", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	for range 3 {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		engine.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("disabled limiter code = %d, want 200", recorder.Code)
		}
	}
}

func TestGlobalRelayRateLimit_LegacyNonzeroLimitsAreIgnored(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := stubGlobalRateLimitSettings(t, "1", "1")
	defer restore()
	ratelimitstore.PurgeGlobalState()
	t.Cleanup(ratelimitstore.PurgeGlobalState)
	// Occupy the former global slot and exhaust the former RPM bucket.
	if !ratelimitstore.AcquireGlobalSlot(1) {
		t.Fatal("could not seed global slot")
	}
	ratelimitstore.CheckGlobalRPM(1)
	engine := gin.New()
	engine.Use(GlobalRelayRateLimit())
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1/embeddings"} {
		engine.POST(path, func(c *gin.Context) { c.String(http.StatusOK, "ok") })
		for range 3 {
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("%s: got %d", path, recorder.Code)
			}
			if recorder.Header().Get("Retry-After") != "" {
				t.Fatal("retired limiter returned retry header")
			}
		}
	}
}

// stubGlobalRateLimitSettings 直接写入设置缓存,避免测试依赖 DB。
func stubGlobalRateLimitSettings(t *testing.T, rpm, maxConcurrency string) func() {
	t.Helper()
	prevRPM, rpmErr := setting.GetString(model.SettingKeyGlobalRateLimitRPM)
	prevConcurrency, concurrencyErr := setting.GetString(model.SettingKeyGlobalMaxConcurrency)
	setting.GetCache().Set(model.SettingKeyGlobalRateLimitRPM, rpm)
	setting.GetCache().Set(model.SettingKeyGlobalMaxConcurrency, maxConcurrency)
	return func() {
		if rpmErr == nil {
			setting.GetCache().Set(model.SettingKeyGlobalRateLimitRPM, prevRPM)
		}
		if concurrencyErr == nil {
			setting.GetCache().Set(model.SettingKeyGlobalMaxConcurrency, prevConcurrency)
		}
	}
}
