package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/utils/crypto"
	"gorm.io/gorm"
)

func init() {
	RegisterBeforeAutoMigration(Migration{
		Version: 52,
		Up:      migrateAPIKeyHashBackfill,
	})
}

// 052: 在 AutoMigrate 创建 api_key_hash 唯一索引前，回填存量空 hash 值。
//
// 背景：APIKey.APIKeyHash 带 uniqueIndex struct tag。启动序列为
// BeforeAutoMigrate → AutoMigrate → AfterAutoMigrate。存量库升级时，
// AutoMigrate 新增 NOT NULL 列 api_key_hash（存量行得 ”），随后尝试
// 创建唯一索引——多行 ” 冲突导致 MySQL Error 1062，服务无法启动。
//
// 本迁移在 AutoMigrate 之前执行：
//  1. 若 api_key_hash 列不存在，先加列（NOT NULL，存量行得 ”）。
//  2. 回填所有 api_key_hash = ” 的行，按 api_key 列值分类：
//     - enc: 加密密文 → 解密得明文 → SHA-256(明文)
//     - 64 hex 旧哈希（哈希化时期写入）→ 哈希值本身
//     - sk- 明文 → SHA-256(明文)
//  3. AutoMigrate 随后创建唯一索引，此时所有 hash 值已唯一，成功。
//
// crypto.Init 在 db.Migrate 之前已由 start.go 完成调用。
// 幂等：HasColumn 守卫 + 只回填空值，重复执行安全。
func migrateAPIKeyHashBackfill(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable(&model.APIKey{}) {
		return nil
	}

	// 步骤 1：列不存在时先加列。AutoMigrate 也会加，但此处提前加列
	// 确保回填能写入——列必须先存在才能 UPDATE。
	if !db.Migrator().HasColumn(&model.APIKey{}, "APIKeyHash") {
		if err := db.Migrator().AddColumn(&model.APIKey{}, "APIKeyHash"); err != nil {
			return fmt.Errorf("add column api_keys.api_key_hash: %w", err)
		}
	}

	// 步骤 2：回填 api_key_hash = '' 的行。
	var apiKeys []model.APIKey
	if err := db.Where("api_key_hash = ?", "").Find(&apiKeys).Error; err != nil {
		return fmt.Errorf("query api_keys with empty hash: %w", err)
	}

	for i := range apiKeys {
		hash := computeAPIKeyHash(apiKeys[i].APIKey)
		if hash == "" {
			// 无法计算 hash（空 api_key 等），跳过。空 api_key 的行
			// 会留 ''，可能导致唯一索引创建失败——但 NOT NULL + UNIQUE
			// 的 api_key 列本就禁止空 api_key，属于数据异常。
			continue
		}
		if err := db.Model(&model.APIKey{}).
			Where("id = ?", apiKeys[i].ID).
			Update("api_key_hash", hash).Error; err != nil {
			return fmt.Errorf("backfill api_key_hash for id=%d: %w", apiKeys[i].ID, err)
		}
	}

	return nil
}

// computeAPIKeyHash 根据 api_key 列的存量值计算确定性哈希。
// 返回空串表示无法计算（如 api_key 为空）。
//
// 三种存量形态（与 op/apikey.RefreshCache 逻辑一致）：
//   - enc: 加密密文 → 解密得明文 → SHA-256(明文)
//   - 64 hex 旧哈希 → 哈希值本身（它就是 SHA-256(明文)）
//   - sk- 明文 → SHA-256(明文)
func computeAPIKeyHash(stored string) string {
	if stored == "" {
		return ""
	}
	if crypto.IsEncrypted(stored) {
		plaintext, err := crypto.Decrypt(stored)
		if err != nil {
			// 解密失败（密钥变更等）：无法计算确定性 hash。
			// 保留空 hash，RefreshCache 会在运行时兜底处理。
			return ""
		}
		return sha256Hex(plaintext)
	}
	if isLegacyHashedAPIKey(stored) {
		return stored // 旧哈希本身就是 SHA-256(明文)
	}
	return sha256Hex(stored) // sk- 明文或其他明文
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// isLegacyHashedAPIKey 判断是否为哈希化时期写入的存量哈希。
// 64 位 hex、不带 sk- 前缀、不带 enc: 前缀。
func isLegacyHashedAPIKey(v string) bool {
	return v != "" && !strings.HasPrefix(v, "sk-") && !crypto.IsEncrypted(v) && len(v) == 64
}
