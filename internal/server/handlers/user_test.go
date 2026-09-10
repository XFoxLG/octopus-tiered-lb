package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	usr "github.com/lingyuins/octopus/internal/op/user"
	"github.com/lingyuins/octopus/internal/server/auth"
	"github.com/lingyuins/octopus/internal/server/middleware"
	"github.com/lingyuins/octopus/internal/server/resp"
)

func TestPrimaryAccountLoginAndOwnAccountChanges(t *testing.T) {
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

	previousSecret := conf.AppConfig.Auth.JWTSecret
	previousUser := usr.GetCurrent()
	conf.AppConfig.Auth.JWTSecret = "test-jwt-secret"
	t.Cleanup(func() {
		conf.AppConfig.Auth.JWTSecret = previousSecret
		usr.SetCache(previousUser)
		middleware.ClearLoginFailures("192.0.2.1")
	})
	admin := model.User{ID: 17, Username: "original", Password: "super-secret-123", Role: model.UserRoleAdmin}
	if err := admin.HashPassword(); err != nil {
		t.Fatalf("hash fixture password: %v", err)
	}
	if err := db.GetDB().Create(&admin).Error; err != nil {
		t.Fatalf("seed original account: %v", err)
	}
	legacyUser := model.User{ID: 29, Username: "viewer", Password: admin.Password, Role: model.UserRoleViewer}
	if err := db.GetDB().Create(&legacyUser).Error; err != nil {
		t.Fatalf("seed legacy account: %v", err)
	}
	if err := usr.Init(); err != nil {
		t.Fatalf("init existing account: %v", err)
	}

	engine := gin.New()
	engine.POST(
		"/api/v1/user/login",
		middleware.RequireJSON(),
		middleware.LoginRateLimit(),
		login,
	)

	engine.GET("/api/v1/user/status", middleware.Auth(), status)
	engine.POST("/api/v1/user/change-username", middleware.Auth(), middleware.RequireJSON(), changeUsername)
	engine.POST("/api/v1/user/change-password", middleware.Auth(), middleware.RequireJSON(), changePassword)

	loginPayload := []byte(`{"username":"original","password":"super-secret-123","expire":60}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/user/login", bytes.NewReader(loginPayload))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRecorder := httptest.NewRecorder()
	engine.ServeHTTP(loginRecorder, loginReq)

	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d; body=%s", loginRecorder.Code, http.StatusOK, loginRecorder.Body.String())
	}

	var response resp.ResponseStruct
	if err := json.Unmarshal(loginRecorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal login response: %v", err)
	}
	if response.Data == nil {
		t.Fatal("login response data is nil")
	}

	loginDataMap, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("login response data type = %T, want map[string]any", response.Data)
	}
	tokenValue, _ := loginDataMap["token"].(string)
	if strings.TrimSpace(tokenValue) == "" {
		t.Fatal("primary account login token is empty")
	}

	valid, userID, role := auth.VerifyJWTToken(tokenValue)
	if !valid {
		t.Fatal("primary account token is invalid")
	}
	if role != model.UserRoleAdmin || userID != admin.ID {
		t.Fatal("login must preserve the original primary account identity and role")
	}

	for _, testCase := range []struct {
		method string
		target string
		body   string
	}{
		{method: http.MethodGet, target: "/api/v1/user/status"},
		{method: http.MethodPost, target: "/api/v1/user/change-username", body: `{"new_username":"renamed"}`},
		{method: http.MethodPost, target: "/api/v1/user/change-password", body: `{"old_password":"super-secret-123","new_password":"replacement-secret-123"}`},
	} {
		request := httptest.NewRequest(testCase.method, testCase.target, strings.NewReader(testCase.body))
		request.Header.Set("Authorization", "Bearer "+tokenValue)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d; body=%s", testCase.target, recorder.Code, recorder.Body.String())
		}
	}
	if current, err := usr.Verify("renamed", "replacement-secret-123"); err != nil || current.ID != admin.ID {
		t.Fatalf("login after own-account changes: %v", err)
	}
	if _, err := usr.Verify("original", "super-secret-123"); err == nil {
		t.Fatal("previous credentials must not remain valid")
	}
	legacyRequest := httptest.NewRequest(http.MethodPost, "/api/v1/user/login", strings.NewReader(`{"username":"viewer","password":"super-secret-123","expire":60}`))
	legacyRequest.Header.Set("Content-Type", "application/json")
	legacyRecorder := httptest.NewRecorder()
	engine.ServeHTTP(legacyRecorder, legacyRequest)
	if legacyRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("legacy account login status = %d, want 401", legacyRecorder.Code)
	}
	var retainedUser model.User
	if err := db.GetDB().First(&retainedUser, legacyUser.ID).Error; err != nil || retainedUser != legacyUser {
		t.Fatal("own-account changes must not modify the retained legacy account")
	}
}

func TestIsTransientDatabaseErrorClassifiesSQLiteBusy(t *testing.T) {
	if !isTransientDatabaseError(fmt.Errorf("incorrect username: database is locked (5) (SQLITE_BUSY)")) {
		t.Fatal("expected SQLITE_BUSY to be classified as transient database error")
	}
	if isTransientDatabaseError(fmt.Errorf("incorrect password")) {
		t.Fatal("expected incorrect password not to be classified as transient database error")
	}
}

func TestIsCredentialErrorOnlyClassifiesCredentialFailures(t *testing.T) {
	if !isCredentialError(fmt.Errorf("incorrect password")) {
		t.Fatal("expected incorrect password to be classified as credential error")
	}
	if isCredentialError(fmt.Errorf("failed to load user: database is locked (5) (SQLITE_BUSY)")) {
		t.Fatal("expected database errors not to be classified as credential errors")
	}
}
