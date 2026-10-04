package apikey

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/utils/crypto"
)

func TestMain(m *testing.M) {
	crypto.Init("octopus-test-encryption-key")
	os.Exit(m.Run())
}

func setupTestDB(t *testing.T) context.Context {
	t.Helper()
	testName := strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(t.Name())
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", testName)
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	keyCache.Clear()
	keyIDMap.Clear()
	t.Cleanup(func() { _ = db.Close() })
	return context.Background()
}

// TestCreateStoresEncryptedNotHash 验证新建 key 落库为加密密文而非哈希，
// 且内存 cache 与响应持有明文。
func TestCreateStoresEncryptedNotHash(t *testing.T) {
	ctx := setupTestDB(t)

	original := "sk-octopus-my-secret-key-12345"
	key := &model.APIKey{Name: "test-key", APIKey: original, Enabled: true}
	if err := Create(key, ctx); err != nil {
		t.Fatalf("create: %v", err)
	}

	// 响应与 cache 应持有明文
	if key.APIKey != original {
		t.Fatalf("after create, APIKey = %q, want plaintext %q", key.APIKey, original)
	}

	// DB 列应存加密密文，不是明文也不是裸哈希
	var dbRow model.APIKey
	if err := db.GetDB().First(&dbRow, key.ID).Error; err != nil {
		t.Fatalf("query db: %v", err)
	}
	if dbRow.APIKey == original {
		t.Fatalf("db stores plaintext api_key = %q, want encrypted", dbRow.APIKey)
	}
	if !crypto.IsEncrypted(dbRow.APIKey) {
		t.Fatalf("db api_key = %q, want enc: prefix", dbRow.APIKey)
	}
	if dbRow.APIKeyHash != HashAPIKey(original) {
		t.Fatalf("db api_key_hash = %q, want %q", dbRow.APIKeyHash, HashAPIKey(original))
	}

	// hash 列不应等于裸密文，应是明文的确定性哈希
	decrypted, err := crypto.Decrypt(dbRow.APIKey)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if decrypted != original {
		t.Fatalf("decrypted = %q, want %q", decrypted, original)
	}
}

// TestRefreshCacheRecoversPlaintext 验证重启后（RefreshCache）能解密恢复明文，
// 修复哈希化时期重启后 cache 持哈希、显示/复制错误的 bug。
func TestRefreshCacheRecoversPlaintext(t *testing.T) {
	ctx := setupTestDB(t)

	original := "sk-octopus-restart-recovery-test"
	key := &model.APIKey{Name: "restart-key", APIKey: original, Enabled: true}
	if err := Create(key, ctx); err != nil {
		t.Fatalf("create: %v", err)
	}

	// 模拟重启：清空内存 cache 后重新从 DB 加载
	keyCache.Clear()
	keyIDMap.Clear()
	if err := RefreshCache(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	reloaded, err := Get(key.ID, ctx)
	if err != nil {
		t.Fatalf("get after refresh: %v", err)
	}
	if reloaded.APIKey != original {
		t.Fatalf("after RefreshCache, APIKey = %q, want plaintext %q", reloaded.APIKey, original)
	}
}

// TestGetByKeyAuthAfterRestart 验证重启后认证路径（GetByKey）仍能按明文定位记录。
func TestGetByKeyAuthAfterRestart(t *testing.T) {
	ctx := setupTestDB(t)

	original := "sk-octopus-auth-after-restart"
	key := &model.APIKey{Name: "auth-key", APIKey: original, Enabled: true}
	if err := Create(key, ctx); err != nil {
		t.Fatalf("create: %v", err)
	}

	// 模拟重启
	keyCache.Clear()
	keyIDMap.Clear()
	if err := RefreshCache(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	// GetByKey 应能按明文定位（经 hash 列回查 + 解密重建映射）
	found, err := GetByKey(original, ctx)
	if err != nil {
		t.Fatalf("GetByKey after restart: %v", err)
	}
	if found.ID != key.ID {
		t.Fatalf("GetByKey found ID = %d, want %d", found.ID, key.ID)
	}
	if found.APIKey != original {
		t.Fatalf("GetByKey APIKey = %q, want %q", found.APIKey, original)
	}
}

// TestUpdatePreservesPlaintext 验证更新非 key 字段时不会破坏明文 cache。
func TestUpdatePreservesPlaintext(t *testing.T) {
	ctx := setupTestDB(t)

	original := "sk-octopus-update-preserve"
	key := &model.APIKey{Name: "update-key", APIKey: original, Enabled: true}
	if err := Create(key, ctx); err != nil {
		t.Fatalf("create: %v", err)
	}

	// 更新 name，不改 api_key
	updated := &model.APIKey{ID: key.ID, Name: "renamed", APIKey: "", Enabled: true}
	if err := Update(updated, ctx); err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.APIKey != original {
		t.Fatalf("after update, APIKey = %q, want preserved %q", updated.APIKey, original)
	}

	got, _ := Get(key.ID, ctx)
	if got.APIKey != original {
		t.Fatalf("get after update, APIKey = %q, want %q", got.APIKey, original)
	}
}

// TestUpdateChangesKeyValue 验证更换 key 值后新明文落库、认证切换到新值。
func TestUpdateChangesKeyValue(t *testing.T) {
	ctx := setupTestDB(t)

	oldKey := "sk-octopus-old-value"
	key := &model.APIKey{Name: "change-key", APIKey: oldKey, Enabled: true}
	if err := Create(key, ctx); err != nil {
		t.Fatalf("create: %v", err)
	}

	newKey := "sk-octopus-new-value"
	updated := &model.APIKey{ID: key.ID, Name: "change-key", APIKey: newKey, Enabled: true}
	if err := Update(updated, ctx); err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.APIKey != newKey {
		t.Fatalf("after update, APIKey = %q, want %q", updated.APIKey, newKey)
	}

	// 旧值认证应失败
	if _, err := GetByKey(oldKey, ctx); err == nil {
		t.Fatalf("GetByKey with old key should fail")
	}
	// 新值认证应成功
	if _, err := GetByKey(newKey, ctx); err != nil {
		t.Fatalf("GetByKey with new key: %v", err)
	}
}

// TestRefreshCacheLegacyHashedKey 验证存量哈希化 key（明文不可逆）的兼容：
// 认证仍可用，但 api_key 列保留哈希（前端标记需重生）。
func TestRefreshCacheLegacyHashedKey(t *testing.T) {
	ctx := setupTestDB(t)

	original := "sk-octopus-legacy-plaintext"
	hashed := HashAPIKey(original)
	// 直接写一条哈希化时期的存量记录（api_key=哈希，无 hash 列）
	row := &model.APIKey{Name: "legacy", APIKey: hashed, Enabled: true}
	if err := db.GetDB().Create(row).Error; err != nil {
		t.Fatalf("create legacy row: %v", err)
	}

	if err := RefreshCache(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	got, err := Get(row.ID, ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// 存量哈希不可逆，cache 持哈希值（前端标记需重生）
	if got.APIKey != hashed {
		t.Fatalf("legacy hashed key APIKey = %q, want hash %q", got.APIKey, hashed)
	}
	// hash 列应被补齐为哈希值本身
	if got.APIKeyHash != hashed {
		t.Fatalf("legacy api_key_hash = %q, want %q", got.APIKeyHash, hashed)
	}
	// 认证仍可用（客户端持明文 → sha256 匹配 hash 列）
	auth, err := GetByKey(original, ctx)
	if err != nil {
		t.Fatalf("GetByKey legacy: %v", err)
	}
	if auth.ID != row.ID {
		t.Fatalf("legacy auth ID = %d, want %d", auth.ID, row.ID)
	}
}

// TestRefreshCacheLegacyPlaintextKey 验证哈希化前的存量明文 key（sk- 前缀）
// 在 RefreshCache 时加密化落库 + 补 hash 列，且显示恢复明文。
func TestRefreshCacheLegacyPlaintextKey(t *testing.T) {
	ctx := setupTestDB(t)

	original := "sk-octopus-pre-hash-era-plaintext"
	// 直接写一条明文存量记录
	row := &model.APIKey{Name: "legacy-plain", APIKey: original, Enabled: true}
	if err := db.GetDB().Create(row).Error; err != nil {
		t.Fatalf("create legacy plain row: %v", err)
	}

	if err := RefreshCache(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	got, err := Get(row.ID, ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// 明文存量应加密化，cache 持明文
	if got.APIKey != original {
		t.Fatalf("legacy plain key APIKey = %q, want plaintext %q", got.APIKey, original)
	}
	// DB 列应已加密化
	var dbRow model.APIKey
	if err := db.GetDB().First(&dbRow, row.ID).Error; err != nil {
		t.Fatalf("query db: %v", err)
	}
	if !crypto.IsEncrypted(dbRow.APIKey) {
		t.Fatalf("db api_key = %q, want encrypted after migration", dbRow.APIKey)
	}
	if dbRow.APIKeyHash != HashAPIKey(original) {
		t.Fatalf("db api_key_hash = %q, want %q", dbRow.APIKeyHash, HashAPIKey(original))
	}
	// 认证可用
	if _, err := GetByKey(original, ctx); err != nil {
		t.Fatalf("GetByKey legacy plain: %v", err)
	}
}

// TestIsLegacyHashedAPIKey 验证存量哈希判定逻辑。
func TestIsLegacyHashedAPIKey(t *testing.T) {
	tests := []struct {
		name string
		val  string
		want bool
	}{
		{"empty", "", false},
		{"plaintext", "sk-octopus-foo", false},
		{"encrypted", "enc:abc123", false},
		{"hash-64", HashAPIKey("sk-octopus-test"), true},
		{"short-hex", "abc123", false},
		{"hash-63", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLegacyHashedAPIKey(tt.val); got != tt.want {
				t.Fatalf("isLegacyHashedAPIKey(%q) = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}
