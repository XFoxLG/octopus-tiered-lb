package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/user"
	"github.com/lingyuins/octopus/internal/server/auth"
)

func TestIsIPAllowed_SupportsHostPortAndCIDR(t *testing.T) {
	if !isIPAllowed("127.0.0.1:54321", "127.0.0.1") {
		t.Fatal("expected host:port IP to match exact allowed IP")
	}
	if !isIPAllowed("10.1.2.3:443", "10.0.0.0/8") {
		t.Fatal("expected host:port IP to match CIDR")
	}
	if isIPAllowed("invalid:ip", "127.0.0.1") {
		t.Fatal("expected invalid client IP to be rejected")
	}
}

func TestAuthOnlyAcceptsExistingPrimaryAdministrator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("OCTOPUS_INITIAL_ADMIN_USERNAME", "")
	t.Setenv("OCTOPUS_INITIAL_ADMIN_PASSWORD", "")
	previousUser := user.GetCurrent()
	previousSecret := conf.AppConfig.Auth.JWTSecret
	conf.AppConfig.Auth.JWTSecret = "isolated-console-auth-test-secret"
	t.Cleanup(func() {
		user.SetCache(previousUser)
		conf.AppConfig.Auth.JWTSecret = previousSecret
	})
	if err := db.InitDB("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name()), false); err != nil {
		t.Fatalf("init fixture database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	primaryUser := model.User{ID: 17, Username: "original", Password: "unused-fixture-hash", Role: model.UserRoleAdmin}
	if err := db.GetDB().Create(&primaryUser).Error; err != nil {
		t.Fatalf("seed primary account: %v", err)
	}
	if err := user.Init(); err != nil {
		t.Fatalf("initialize primary account: %v", err)
	}
	engine := gin.New()
	engine.GET("/protected", Auth(), RequirePermission(auth.PermSettingsWrite), func(context *gin.Context) {
		if context.GetInt("user_id") != int(primaryUser.ID) || context.GetString("user_role") != model.UserRoleAdmin {
			t.Error("authenticated identity must come from the existing primary account")
		}
		context.Status(http.StatusNoContent)
	})
	checkToken := func(accountID uint, expectedStatus int) {
		t.Helper()
		// A secondary Passkey or a pre-upgrade token cannot bypass Auth even
		// when it carries a valid administrator role claim.
		token, _, err := auth.GenerateJWTToken(60, accountID, model.UserRoleAdmin)
		if err != nil {
			t.Fatalf("generate fixture token: %v", err)
		}
		request := httptest.NewRequest(http.MethodGet, "/protected", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != expectedStatus {
			t.Fatalf("account %d status = %d, want %d", accountID, recorder.Code, expectedStatus)
		}
	}
	checkToken(primaryUser.ID, http.StatusNoContent)
	for accountIndex, role := range []string{model.UserRoleViewer, model.UserRoleEditor, model.UserRoleAdmin} {
		legacyAccount := model.User{ID: uint(29 + accountIndex), Username: role, Password: "unused-fixture-hash", Role: role}
		if err := db.GetDB().Create(&legacyAccount).Error; err != nil {
			t.Fatalf("seed legacy account: %v", err)
		}
		checkToken(legacyAccount.ID, http.StatusUnauthorized)
	}
	if err := db.GetDB().Model(&primaryUser).Update("role", model.UserRoleViewer).Error; err != nil {
		t.Fatalf("downgrade fixture account: %v", err)
	}
	checkToken(primaryUser.ID, http.StatusUnauthorized)
}
