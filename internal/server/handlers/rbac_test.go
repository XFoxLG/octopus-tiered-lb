package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op"
	serverauth "github.com/lingyuins/octopus/internal/server/auth"
	"github.com/lingyuins/octopus/internal/server/router"
)

func TestPrimaryAccountRoleDowngradeInvalidatesConsoleAccessAcrossHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("OCTOPUS_INITIAL_ADMIN_USERNAME", "")
	t.Setenv("OCTOPUS_INITIAL_ADMIN_PASSWORD", "")

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(t.Name()))
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	conf.AppConfig.Auth.JWTSecret = "test-jwt-secret"

	if err := op.UserInit(); err != nil {
		t.Fatalf("user init: %v", err)
	}
	if err := op.UserBootstrapCreate("admin", "super-secret-123"); err != nil {
		t.Fatalf("bootstrap user: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}

	engine := gin.New()
	if err := router.RegisterAll(engine); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	for _, retiredRoute := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/v1/user/create"},
		{method: http.MethodGet, path: "/api/v1/user/list"},
		{method: http.MethodPost, path: "/api/v1/user/update-role"},
		{method: http.MethodDelete, path: "/api/v1/user/delete/17"},
	} {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(retiredRoute.method, retiredRoute.path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("retired route %s %s status = %d, want 404", retiredRoute.method, retiredRoute.path, recorder.Code)
		}
	}

	currentUser := op.UserGet()
	if currentUser.ID == 0 {
		t.Fatal("current user id is 0")
	}
	token, _, err := serverauth.GenerateJWTToken(60, currentUser.ID, model.UserRoleAdmin)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if err := db.GetDB().Model(&currentUser).Update("role", model.UserRoleViewer).Error; err != nil {
		t.Fatalf("downgrade user role: %v", err)
	}

	testCases := []struct {
		name        string
		method      string
		target      string
		body        string
		contentType string
	}{
		{name: "setting write", method: http.MethodPost, target: "/api/v1/setting/set", body: `{"key":"stats_save_interval","value":"10"}`, contentType: "application/json"},
		{name: "apikey delete", method: http.MethodDelete, target: "/api/v1/apikey/delete/1"},
		{name: "channel sync", method: http.MethodPost, target: "/api/v1/channel/sync"},
		{name: "group test", method: http.MethodPost, target: "/api/v1/group/test", body: `{}`, contentType: "application/json"},
		{name: "log clear", method: http.MethodDelete, target: "/api/v1/log/clear"},
		{name: "log clear contents", method: http.MethodDelete, target: "/api/v1/log/clear-contents"},
		{name: "error log clear", method: http.MethodDelete, target: "/api/v1/error-log/clear"},
		{name: "model price update", method: http.MethodPost, target: "/api/v1/model/update-price", body: `{}`, contentType: "application/json"},
		{name: "ai route generate", method: http.MethodPost, target: "/api/v1/route/ai-generate", body: `{}`, contentType: "application/json"},
		{name: "core update", method: http.MethodPost, target: "/api/v1/update"},
		{name: "notification delete", method: http.MethodDelete, target: "/api/v1/notification/delete/1"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var bodyReader *strings.Reader
			if tc.body != "" {
				bodyReader = strings.NewReader(tc.body)
			} else {
				bodyReader = strings.NewReader("")
			}

			req := httptest.NewRequest(tc.method, tc.target, bodyReader)
			req.Header.Set("Authorization", "Bearer "+token)
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}

			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, req)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s status = %d, want %d; body=%s", tc.method, tc.target, recorder.Code, http.StatusUnauthorized, recorder.Body.String())
			}
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/update/now-version", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("viewer read endpoint status = %d, want %d; body=%s", recorder.Code, http.StatusUnauthorized, recorder.Body.String())
	}
}
