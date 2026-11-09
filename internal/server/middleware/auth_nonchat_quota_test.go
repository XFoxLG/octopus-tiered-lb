package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/apikey"
	"github.com/lingyuins/octopus/internal/op/stats"
)

func TestAPIKeyAuthMaxTokensCountsTotalOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := db.InitDB("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name()), false); err != nil {
		t.Fatalf("init fixture database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stats.ClearAllCachesForTest()
	t.Cleanup(stats.ClearAllCachesForTest)

	apiKey := model.APIKey{
		ID:        9,
		Name:      "nonchat-quota",
		APIKey:    "sk-octopus-test-nonchat",
		Enabled:   true,
		MaxTokens: 128,
	}
	apiKey.APIKeyHash = apikey.HashAPIKey(apiKey.APIKey)
	if err := db.GetDB().Create(&apiKey).Error; err != nil {
		t.Fatalf("seed API key: %v", err)
	}
	if err := stats.APIKeyUpdate(apiKey.ID, model.StatsMetrics{
		InputToken:  100,
		OutputToken: 20,
		NonChatMetrics: model.NonChatMetrics{
			UnattributedToken: 7,
			BilledToken:       90,
		},
	}); err != nil {
		t.Fatalf("seed usage: %v", err)
	}

	engine := gin.New()
	engine.GET("/protected", APIKeyAuth(), func(context *gin.Context) {
		context.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+apiKey.APIKey)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
	}
	if err := stats.APIKeyUpdate(apiKey.ID, model.StatsMetrics{
		NonChatMetrics: model.NonChatMetrics{UnattributedToken: 1},
	}); err != nil {
		t.Fatalf("reach quota: %v", err)
	}
	request = httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+apiKey.APIKey)
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), "max token limit") {
		t.Fatalf("total-only increment must reach quota: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
