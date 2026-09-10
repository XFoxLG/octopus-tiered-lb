package user

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/utils/log"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// dummyBcryptHash 是用户不存在时用于抹平时序差异的 dummy bcrypt 哈希
// （"password" 的标准 bcrypt 哈希，仅用于保证比较耗时一致，防用户名枚举）。
var dummyBcryptHash = []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")

var (
	adminCache   model.User
	adminCacheMu sync.RWMutex
)

const minInitialAdminPasswordLength = 12

var (
	ErrBootstrapAlreadySetUp  = errors.New("initial admin account is already set up")
	ErrBootstrapCredentials   = errors.New("invalid bootstrap credentials")
	ErrPrimaryAccountNotAdmin = errors.New("the primary console account is not an existing administrator")
)

// GetAdminCache returns the cached admin user (for backward compatibility).
func GetAdminCache() model.User {
	adminCacheMu.RLock()
	defer adminCacheMu.RUnlock()
	return adminCache
}

// SetCache sets the admin cache value (for backward compatibility with tests).
func SetCache(u model.User) {
	adminCacheMu.Lock()
	defer adminCacheMu.Unlock()
	adminCache = u
}

func Ready() bool {
	adminCacheMu.RLock()
	defer adminCacheMu.RUnlock()
	return adminCache.ID != 0
}

func BootstrapStatus() (bool, string, error) {
	if Ready() {
		return true, "", nil
	}

	var count int64
	if err := db.GetDB().Model(&model.User{}).Count(&count).Error; err != nil {
		if errors.Is(err, gorm.ErrInvalidDB) {
			return false, "database not initialized", err
		}
		return false, "failed to inspect user initialization state", fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return true, "", nil
	}
	return false, "initial admin account is not set up yet", nil
}

func Init() error {
	SetCache(model.User{})
	var primaryUser model.User
	// Keep the original upstream First() identity; never replace it using env
	// credentials or promote a legacy viewer/editor during an upgrade.
	result := db.GetDB().First(&primaryUser)
	if result.Error == nil {
		if primaryUser.Role != model.UserRoleAdmin {
			return ErrPrimaryAccountNotAdmin
		}
		SetCache(primaryUser)
		return nil
	}
	if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return result.Error
	}
	return bootstrapFromEnv()
}

func bootstrapFromEnv() error {
	username := strings.TrimSpace(os.Getenv("OCTOPUS_INITIAL_ADMIN_USERNAME"))
	password := os.Getenv("OCTOPUS_INITIAL_ADMIN_PASSWORD")

	if username == "" && password == "" {
		return nil
	}
	if username == "" || password == "" {
		return fmt.Errorf("both OCTOPUS_INITIAL_ADMIN_USERNAME and OCTOPUS_INITIAL_ADMIN_PASSWORD must be set together")
	}

	if err := BootstrapCreate(username, password); err != nil {
		// On a fully initialized DB (e.g. every container restart after the
		// first run), an admin already exists and BootstrapCreate returns
		// ErrBootstrapAlreadySetUp. OCTOPUS_INITIAL_ADMIN_* is a first-run
		// bootstrap hint, so treat this as a benign no-op instead of a fatal
		// startup error — otherwise the container loops on restart.
		if errors.Is(err, ErrBootstrapAlreadySetUp) {
			log.Infof("initial admin already set up; OCTOPUS_INITIAL_ADMIN_USERNAME=%s ignored (existing account takes precedence)", username)
			return nil
		}
		return fmt.Errorf("bootstrap admin from env: %w", err)
	}
	return nil
}

func BootstrapCreate(username, password string) error {
	if err := validateCredentials(username, password); err != nil {
		return err
	}
	username = strings.TrimSpace(username)

	var count int64
	if err := db.GetDB().Model(&model.User{}).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to inspect user state: %w", err)
	}
	if count > 0 {
		return ErrBootstrapAlreadySetUp
	}

	user := model.User{
		Username: username,
		Password: password,
		Role:     model.UserRoleAdmin,
	}
	if err := user.HashPassword(); err != nil {
		return err
	}
	if err := db.GetDB().Create(&user).Error; err != nil {
		return err
	}
	adminCacheMu.Lock()
	adminCache = user
	adminCacheMu.Unlock()
	return nil
}

func validateCredentials(username, password string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("%w: username is required", ErrBootstrapCredentials)
	}
	if password == "" {
		return fmt.Errorf("%w: password is required", ErrBootstrapCredentials)
	}
	if utf8.RuneCountInString(password) < minInitialAdminPasswordLength {
		return fmt.Errorf("%w: password must be at least %d characters long", ErrBootstrapCredentials, minInitialAdminPasswordLength)
	}
	return nil
}

func ChangePassword(userID uint, oldPassword, newPassword string) error {
	user, err := GetByID(userID, context.Background())
	if err != nil {
		return fmt.Errorf("user not found: %w", err)
	}
	// 与创建/引导流程一致：修改密码同样要求满足强度策略（≥12 位等），
	// 避免管理员把密码改成弱口令。
	if err := validateCredentials(user.Username, newPassword); err != nil {
		return err
	}
	if err := user.ComparePassword(oldPassword); err != nil {
		return fmt.Errorf("incorrect old password: %w", err)
	}

	user.Password = newPassword
	if err := user.HashPassword(); err != nil {
		return fmt.Errorf("failed to hash new password: %w", err)
	}

	if err := db.GetDB().Model(&user).Update("password", user.Password).Error; err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}
	adminCacheMu.Lock()
	if adminCache.ID == user.ID {
		adminCache.Password = user.Password
	}
	adminCacheMu.Unlock()
	return nil
}

func ChangeUsername(userID uint, newUsername string) error {
	newUsername = strings.TrimSpace(newUsername)
	if newUsername == "" {
		return fmt.Errorf("username is required")
	}

	user, err := GetByID(userID, context.Background())
	if err != nil {
		return fmt.Errorf("user not found: %w", err)
	}
	if user.Username == newUsername {
		return fmt.Errorf("new username is the same as the old username")
	}

	var count int64
	if err := db.GetDB().Model(&model.User{}).
		Where("username = ? AND id <> ?", newUsername, user.ID).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed to inspect existing users: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("username already exists")
	}

	user.Username = newUsername
	if err := db.GetDB().Model(&user).Update("username", user.Username).Error; err != nil {
		return fmt.Errorf("failed to update username: %w", err)
	}
	adminCacheMu.Lock()
	if adminCache.ID == user.ID {
		adminCache.Username = user.Username
	}
	adminCacheMu.Unlock()
	return nil
}

func Verify(username, password string) (model.User, error) {
	if !Ready() {
		return model.User{}, fmt.Errorf("user not initialized: %w", ErrBootstrapAlreadySetUp)
	}
	user, err := GetByUsername(strings.TrimSpace(username), context.Background())
	if err != nil {
		// 用户不存在时也执行一次 dummy bcrypt 比较，抹平「用户存在与否」的
		// 时序差异，防用户名枚举。
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.User{}, fmt.Errorf("incorrect username")
		}
		return model.User{}, fmt.Errorf("failed to load user: %w", err)
	}
	if err := user.ComparePassword(password); err != nil {
		return model.User{}, fmt.Errorf("incorrect password")
	}
	return user, nil
}

func GetCurrent() model.User {
	adminCacheMu.RLock()
	defer adminCacheMu.RUnlock()
	return adminCache
}

func GetByID(id uint, ctx context.Context) (model.User, error) {
	primaryUser := GetCurrent()
	if primaryUser.ID == 0 || id != primaryUser.ID {
		return model.User{}, gorm.ErrRecordNotFound
	}
	var user model.User
	if err := db.GetDB().WithContext(ctx).First(&user, id).Error; err != nil {
		return model.User{}, err
	}
	// Role is retained only for compatibility. Never turn an old token or
	// Passkey for a restricted account into console administrator access.
	if user.Role != model.UserRoleAdmin {
		return model.User{}, gorm.ErrRecordNotFound
	}
	return user, nil
}

func GetByUsername(username string, ctx context.Context) (model.User, error) {
	primaryUser := GetCurrent()
	user, err := GetByID(primaryUser.ID, ctx)
	if err != nil {
		return model.User{}, err
	}
	if username != user.Username {
		return model.User{}, gorm.ErrRecordNotFound
	}
	return user, nil
}
