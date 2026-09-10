package op

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
)

func TestUserInitPreservesExistingAccountWithNonDefaultID(t *testing.T) {
	oldUserCache := userCache
	userCache = model.User{}
	t.Setenv("OCTOPUS_INITIAL_ADMIN_USERNAME", "alice")
	t.Setenv("OCTOPUS_INITIAL_ADMIN_PASSWORD", "replacement-secret-123")
	t.Cleanup(func() {
		userCache = oldUserCache
	})

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(t.Name()))
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	legacy := model.User{
		ID:       17,
		Username: "admin",
		Password: "legacy-secret-123",
		Role:     model.UserRoleAdmin,
	}
	if err := legacy.HashPassword(); err != nil {
		t.Fatalf("hash legacy password: %v", err)
	}
	if err := db.GetDB().Create(&legacy).Error; err != nil {
		t.Fatalf("create legacy user: %v", err)
	}

	if err := UserInit(); err != nil {
		t.Fatalf("user init: %v", err)
	}
	activeUser := UserGet()
	if activeUser != legacy {
		t.Fatal("initialization must preserve the existing account and bcrypt hash")
	}
	if _, err := UserVerify("admin", "legacy-secret-123"); err != nil {
		t.Fatalf("existing account login: %v", err)
	}
}

func TestUserVerifyRejectsNonCachedLegacyUsers(t *testing.T) {
	oldUserCache := userCache
	userCache = model.User{}
	t.Setenv("OCTOPUS_INITIAL_ADMIN_USERNAME", "")
	t.Setenv("OCTOPUS_INITIAL_ADMIN_PASSWORD", "")
	t.Cleanup(func() {
		userCache = oldUserCache
	})

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(t.Name()))
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := UserBootstrapCreate("admin", "super-secret-123"); err != nil {
		t.Fatalf("bootstrap user: %v", err)
	}
	legacyViewer := model.User{
		Username: "viewer",
		Password: "viewer-secret-123",
		Role:     model.UserRoleViewer,
	}
	if err := legacyViewer.HashPassword(); err != nil {
		t.Fatalf("hash legacy viewer password: %v", err)
	}
	if err := db.GetDB().Create(&legacyViewer).Error; err != nil {
		t.Fatalf("seed legacy viewer: %v", err)
	}

	if _, err := UserVerify("viewer", "viewer-secret-123"); err == nil {
		t.Fatal("legacy viewer must not authenticate to the console")
	}
	var retainedViewer model.User
	if err := db.GetDB().First(&retainedViewer, legacyViewer.ID).Error; err != nil {
		t.Fatalf("load retained legacy viewer: %v", err)
	}
	if retainedViewer != legacyViewer {
		t.Fatal("legacy viewer data must remain unchanged for rollback")
	}
}
