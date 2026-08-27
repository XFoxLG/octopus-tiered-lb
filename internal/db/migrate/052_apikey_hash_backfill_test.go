package migrate

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/utils/crypto"
	"gorm.io/gorm"
)

// 052 迁移测试：回填存量空 api_key_hash，幂等。
// 覆盖三种存量形态：enc: 加密、64hex 旧哈希、sk- 明文。
func TestMigrateAPIKeyHashBackfill(t *testing.T) {
	crypto.Init("test-encryption-key-052")

	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	gormDB, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	sqlDB, err := gormDB.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	// 模拟旧版表（无 api_key_hash 列）
	if err := gormDB.Exec(`CREATE TABLE api_keys (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		api_key TEXT NOT NULL,
		enabled INTEGER DEFAULT 1,
		expire_at INTEGER,
		max_cost REAL,
		max_tokens INTEGER DEFAULT 0,
		supported_models TEXT,
		allowed_group_categories TEXT,
		rate_limit_rpm INTEGER DEFAULT 0,
		rate_limit_tpm INTEGER DEFAULT 0,
		per_model_quota_json TEXT,
		allowed_ips TEXT,
		tags TEXT,
		excluded_channels TEXT
	)`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}

	// 三种存量形态，各用不同的明文，避免回填后 hash 冲突
	keyEnc := "sk-octopus-encrypted"
	keyLegacy := "sk-octopus-legacy-hash"
	keyPlain := "sk-octopus-plaintext"

	encrypted, err := crypto.Encrypt(keyEnc)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	legacyHash := sha256Hex(keyLegacy) // 64hex 旧哈希

	rows := []model.APIKey{
		{Name: "encrypted-key", APIKey: encrypted},
		{Name: "legacy-hash-key", APIKey: legacyHash},
		{Name: "plaintext-key", APIKey: keyPlain},
	}
	for i := range rows {
		if err := gormDB.Exec(
			`INSERT INTO api_keys (name, api_key, enabled) VALUES (?, ?, 1)`,
			rows[i].Name, rows[i].APIKey,
		).Error; err != nil {
			t.Fatalf("insert %s: %v", rows[i].Name, err)
		}
	}

	// 第一遍：加列 + 回填
	if err := migrateAPIKeyHashBackfill(gormDB); err != nil {
		t.Fatalf("migrateAPIKeyHashBackfill: %v", err)
	}

	if !gormDB.Migrator().HasColumn(&model.APIKey{}, "APIKeyHash") {
		t.Fatalf("api_keys missing api_key_hash column after migrate")
	}

	// 验证三行 hash 都已回填且等于各自的预期值
	wantHashes := []string{
		sha256Hex(keyEnc),    // encrypted -> decrypt -> sha256(plaintext)
		sha256Hex(keyLegacy), // legacy hash -> hash itself (== sha256(plaintext))
		sha256Hex(keyPlain),  // plaintext -> sha256(plaintext)
	}
	var got []model.APIKey
	if err := gormDB.Order("id").Find(&got).Error; err != nil {
		t.Fatalf("query after backfill: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(got))
	}
	for i, k := range got {
		if k.APIKeyHash != wantHashes[i] {
			t.Errorf("id=%d name=%q api_key_hash=%q want %q",
				k.ID, k.Name, k.APIKeyHash, wantHashes[i])
		}
		if k.APIKeyHash == "" {
			t.Errorf("id=%d name=%q api_key_hash is empty", k.ID, k.Name)
		}
	}

	// 幂等：第二遍不应报错
	if err := migrateAPIKeyHashBackfill(gormDB); err != nil {
		t.Fatalf("re-run migrateAPIKeyHashBackfill: %v", err)
	}

	// 唯一索引可创建（无 '' 冲突）
	if err := gormDB.Migrator().CreateIndex(&model.APIKey{}, "APIKeyHash"); err != nil {
		t.Fatalf("create unique index after backfill: %v", err)
	}
}

func TestComputeAPIKeyHash(t *testing.T) {
	crypto.Init("test-encryption-key-052-compute")

	plaintext := "sk-secret"
	want := sha256Hex(plaintext)

	// enc: 加密密文
	enc, _ := crypto.Encrypt(plaintext)
	if got := computeAPIKeyHash(enc); got != want {
		t.Errorf("encrypted: got %q want %q", got, want)
	}

	// 64hex 旧哈希
	if got := computeAPIKeyHash(want); got != want {
		t.Errorf("legacy hash: got %q want %q", got, want)
	}

	// sk- 明文
	if got := computeAPIKeyHash(plaintext); got != want {
		t.Errorf("plaintext: got %q want %q", got, want)
	}

	// 空串
	if got := computeAPIKeyHash(""); got != "" {
		t.Errorf("empty: got %q want empty", got)
	}
}
