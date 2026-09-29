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

// GlobalRelayRateLimit 作用域测试:两个设置默认为 0(关闭)时必须完全放行,
// 开启并发上限后超出的请求立即 429,且名额在请求结束后释放。
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

func TestGlobalRelayRateLimit_ConcurrencyRejectsAndReleases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ratelimitstore.PurgeGlobalState()
	t.Cleanup(ratelimitstore.PurgeGlobalState)
	restore := stubGlobalRateLimitSettings(t, "0", "1")
	defer restore()

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	engine := gin.New()
	engine.Use(GlobalRelayRateLimit())
	engine.GET("/hold", func(c *gin.Context) {
		entered <- struct{}{}
		<-release
		c.String(http.StatusOK, "ok")
	})

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/hold", nil)
		engine.ServeHTTP(recorder, req)
	}()

	<-entered
	// 第二个请求在名额被占用时立即被拒。
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/hold", nil))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("over-limit code = %d, want 429", recorder.Code)
	}

	close(release)
	<-firstDone
	// 名额释放后同一路由重新可进入。
	if inFlight := ratelimitstore.InFlightGlobal(); inFlight != 0 {
		t.Fatalf("in-flight after request finished = %d, want 0", inFlight)
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
