package user

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

// initMemDB initializes an in-memory SQLite database for the user package tests.
// Each test gets an isolated DB via a unique DSN derived from the test name.
func initMemDB(t *testing.T) {
	t.Helper()
	t.Setenv("OCTOPUS_INITIAL_ADMIN_USERNAME", "")
	t.Setenv("OCTOPUS_INITIAL_ADMIN_PASSWORD", "")
	resetAdminCache(t)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared",
		strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(t.Name()))
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

// resetAdminCache simulates a cold start (container restart): the in-memory
// adminCache is empty and has not yet been loaded from the DB, so Ready()
// returns false on the next Init() call.
func resetAdminCache(t *testing.T) {
	t.Helper()
	old := GetAdminCache()
	SetCache(model.User{})
	t.Cleanup(func() { SetCache(old) })
}

// TestBootstrapFromEnvIdempotentWhenAdminExists reproduces issue #198 part two:
// OCTOPUS_INITIAL_ADMIN_USERNAME/PASSWORD are set and the DB already has an admin
// (every restart after the first run). Previously this crashed with
// "user init error: bootstrap admin from env: initial admin account is already
// set up" and caused the Docker container to loop on restart. Init() must now
// treat this as a benign no-op.
func TestBootstrapFromEnvIdempotentWhenAdminExists(t *testing.T) {
	initMemDB(t)

	// Seed an admin as if a previous first-run bootstrap had succeeded.
	if err := BootstrapCreate("admin", "super-secret-123"); err != nil {
		t.Fatalf("seed initial admin: %v", err)
	}

	// Emulate the env the operator set once for first-run bootstrap.
	t.Setenv("OCTOPUS_INITIAL_ADMIN_USERNAME", "admin")
	t.Setenv("OCTOPUS_INITIAL_ADMIN_PASSWORD", "super-secret-123")

	// Cold start: cache empty, simulating a fresh container restart.
	resetAdminCache(t)

	// This used to return ErrBootstrapAlreadySetUp and crash the process.
	if err := Init(); err != nil {
		t.Fatalf("Init() after admin exists: %v", err)
	}

	// The existing admin must not be overwritten or duplicated by the env hint.
	var count int64
	if err := db.GetDB().Model(&model.User{}).Count(&count).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Fatalf("user count = %d, want 1 (existing admin must be preserved)", count)
	}

	var loaded model.User
	if err := db.GetDB().First(&loaded).Error; err != nil {
		t.Fatalf("load admin: %v", err)
	}
	if loaded.Username != "admin" {
		t.Fatalf("admin username = %q, want admin", loaded.Username)
	}
}

// TestBootstrapFromEnvCreatesWhenDBEmpty ensures the env bootstrap path still
// creates the admin on a fresh DB (the original first-run use case), so the
// idempotency change did not break first-run initialization.
func TestBootstrapFromEnvCreatesWhenDBEmpty(t *testing.T) {
	initMemDB(t)

	t.Setenv("OCTOPUS_INITIAL_ADMIN_USERNAME", "root")
	t.Setenv("OCTOPUS_INITIAL_ADMIN_PASSWORD", "super-secret-123")
	resetAdminCache(t)

	if err := Init(); err != nil {
		t.Fatalf("Init() on empty DB with env: %v", err)
	}

	if !Ready() {
		t.Fatal("Ready() = false, want true after env bootstrap created admin")
	}
	if got := GetAdminCache(); got.Username != "root" {
		t.Fatalf("admin username = %q, want root", got.Username)
	}

	var count int64
	if err := db.GetDB().Model(&model.User{}).Count(&count).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Fatalf("user count = %d, want 1", count)
	}
}

// TestBootstrapFromEnvRejectsPartialEnv guards the "both must be set together"
// rule: setting only one of OCTOPUS_INITIAL_ADMIN_USERNAME / PASSWORD must
// still error, not silently no-op.
func TestBootstrapFromEnvRejectsPartialEnv(t *testing.T) {
	initMemDB(t)

	t.Setenv("OCTOPUS_INITIAL_ADMIN_USERNAME", "root")
	// password intentionally unset
	resetAdminCache(t)

	err := Init()
	if err == nil {
		t.Fatal("Init() with only username set: error = nil, want error")
	}
	if !strings.Contains(err.Error(), "must be set together") {
		t.Fatalf("Init() error = %q, want 'must be set together'", err.Error())
	}
}

func TestInitPreservesExistingAccountsDespiteBootstrapHints(t *testing.T) {
	testCases := []struct {
		name     string
		username string
		password string
	}{
		{name: "different credentials", username: "replacement", password: "replacement-secret-123"},
		{name: "same username different password", username: "admin", password: "replacement-secret-123"},
		{name: "partial hint", username: "replacement"},
		{name: "weak hint", username: "replacement", password: "short"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			initMemDB(t)
			primaryUser := seedAccount(t, 17, "admin", model.UserRoleAdmin)
			seedAccount(t, 29, "archived-viewer", model.UserRoleViewer)
			seedAccount(t, 43, "archived-admin", model.UserRoleAdmin)
			before := readAccounts(t)
			t.Setenv("OCTOPUS_INITIAL_ADMIN_USERNAME", testCase.username)
			t.Setenv("OCTOPUS_INITIAL_ADMIN_PASSWORD", testCase.password)

			for restart := 0; restart < 2; restart++ {
				SetCache(model.User{})
				if err := Init(); err != nil {
					t.Fatalf("Init() restart %d: %v", restart, err)
				}
				if actual := GetCurrent(); actual != primaryUser {
					t.Fatal("the existing primary account identity or bcrypt hash changed")
				}
				if !reflect.DeepEqual(readAccounts(t), before) {
					t.Fatal("account rows changed during restart")
				}
			}
			if err := BootstrapCreate("replacement", "replacement-secret-123"); !errors.Is(err, ErrBootstrapAlreadySetUp) {
				t.Fatalf("BootstrapCreate() error = %v, want already set up", err)
			}
			if !reflect.DeepEqual(readAccounts(t), before) {
				t.Fatal("a repeated bootstrap modified existing account rows")
			}
		})
	}
}

func TestInitRejectsNonAdminPrimaryWithoutSelectingAnotherAccount(t *testing.T) {
	for _, role := range []string{model.UserRoleViewer, model.UserRoleEditor, "", "unknown"} {
		t.Run("role="+role, func(t *testing.T) {
			initMemDB(t)
			primaryUser := seedAccount(t, 17, "original", model.UserRoleAdmin)
			if err := db.GetDB().Model(&primaryUser).Update("role", role).Error; err != nil {
				t.Fatalf("seed legacy role: %v", err)
			}
			seedAccount(t, 29, "other-admin", model.UserRoleAdmin)
			before := readAccounts(t)
			t.Setenv("OCTOPUS_INITIAL_ADMIN_USERNAME", "other-admin")
			t.Setenv("OCTOPUS_INITIAL_ADMIN_PASSWORD", "replacement-secret-123")

			if err := Init(); !errors.Is(err, ErrPrimaryAccountNotAdmin) {
				t.Fatalf("Init() error = %v, want primary account rejection", err)
			}
			if Ready() {
				t.Fatal("a restricted primary account must not initialize console access")
			}
			if !reflect.DeepEqual(readAccounts(t), before) {
				t.Fatal("rejecting initialization must not modify or promote any account")
			}
		})
	}
}

func TestConsoleAccountAccessRejectsLegacyAdditionalAccounts(t *testing.T) {
	initMemDB(t)
	primaryUser := seedAccount(t, 17, "original", model.UserRoleAdmin)
	additionalUsers := []model.User{
		seedAccount(t, 29, "archived-viewer", model.UserRoleViewer),
		seedAccount(t, 43, "archived-editor", model.UserRoleEditor),
		seedAccount(t, 59, "archived-admin", model.UserRoleAdmin),
	}
	before := readAccounts(t)
	if err := Init(); err != nil {
		t.Fatalf("Init(): %v", err)
	}
	if verified, err := Verify(primaryUser.Username, "account-secret-123"); err != nil || verified != primaryUser {
		t.Fatalf("primary account login error = %v", err)
	}
	for _, additionalUser := range additionalUsers {
		if _, err := Verify(additionalUser.Username, "account-secret-123"); err == nil {
			t.Fatalf("additional %s account was allowed to log in", additionalUser.Role)
		}
		if _, err := GetByID(additionalUser.ID, context.Background()); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("additional account lookup error = %v, want not found", err)
		}
		if err := ChangeUsername(additionalUser.ID, "replacement"); err == nil {
			t.Fatal("changing an additional account username must be rejected")
		}
		if err := ChangePassword(additionalUser.ID, "account-secret-123", "replacement-secret-123"); err == nil {
			t.Fatal("changing an additional account password must be rejected")
		}
	}
	if !reflect.DeepEqual(readAccounts(t), before) {
		t.Fatal("additional account rejection must leave all account data unchanged")
	}
}

func seedAccount(t *testing.T, accountID uint, username, role string) model.User {
	t.Helper()
	account := model.User{ID: accountID, Username: username, Password: "account-secret-123", Role: role}
	if err := account.HashPassword(); err != nil {
		t.Fatalf("hash fixture password: %v", err)
	}
	if err := db.GetDB().Create(&account).Error; err != nil {
		t.Fatalf("create fixture account: %v", err)
	}
	return account
}

func readAccounts(t *testing.T) []model.User {
	t.Helper()
	var accounts []model.User
	if err := db.GetDB().Order("id").Find(&accounts).Error; err != nil {
		t.Fatalf("read fixture accounts: %v", err)
	}
	return accounts
}
