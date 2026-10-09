package apikey

import (
	"context"
	"fmt"
	"strings"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/utils/apikeyhash"
	"github.com/lingyuins/octopus/internal/utils/cache"
	"github.com/lingyuins/octopus/internal/utils/crypto"
)

var keyCache = cache.New[int, model.APIKey](16)

// keyIDMap 以**明文** API Key 为键映射到 ID（仅内存），用于热路径查找。
// 数据库存 AES-GCM 加密密文（enc: 前缀，可逆），重启后由 GetByKey
// 按确定性哈希列 api_key_hash 回查 DB 并解密重建映射。
var keyIDMap = cache.New[string, int](16)

// GetCache returns the internal API key cache (for backward compatibility).
func GetCache() cache.Cache[int, model.APIKey] { return keyCache }

// GetIDMap returns the internal key ID map (for backward compatibility).
func GetIDMap() cache.Cache[string, int] { return keyIDMap }

// HashAPIKey 计算 API Key 明文的 SHA-256 哈希（hex），用于确定性查找列 api_key_hash。
// 加密密文因 AES-GCM nonce 随机而不可直接用于 WHERE 查询，故另存确定性哈希列定位。
// 实现位于 utils/apikeyhash，供 backup 导入侧与 db/migrate 回填复用，避免哈希逻辑多份漂移。
func HashAPIKey(raw string) string {
	return apikeyhash.Sum(raw)
}

// isLegacyHashedAPIKey 判断列值是否为哈希化时期（commit e6cbcde15）写入的存量哈希。
// 这些值是 64 位 hex（不带 sk- 前缀、不带 enc: 前缀），明文已不可逆，
// 认证仍可用（客户端持明文→sha256 匹配），但显示/复制无法恢复明文。
func isLegacyHashedAPIKey(v string) bool {
	return v != "" && !strings.HasPrefix(v, "sk-") && !crypto.IsEncrypted(v) && len(v) == 64
}

func Create(key *model.APIKey, ctx context.Context) error {
	raw := strings.TrimSpace(key.APIKey)
	if raw == "" {
		return fmt.Errorf("api key value is empty")
	}
	encrypted, err := crypto.Encrypt(raw)
	if err != nil {
		return fmt.Errorf("failed to encrypt API key: %w", err)
	}
	// 落库存加密密文 + 确定性哈希；成功后恢复明文供响应与内存缓存使用。
	key.APIKey = encrypted
	key.APIKeyHash = HashAPIKey(raw)
	if err := db.GetDB().WithContext(ctx).Create(key).Error; err != nil {
		key.APIKey = raw
		key.APIKeyHash = ""
		return fmt.Errorf("failed to create API key: %w", err)
	}
	key.APIKey = raw
	keyCache.Set(key.ID, *key)
	keyIDMap.Set(raw, key.ID)
	return nil
}

func Update(key *model.APIKey, ctx context.Context) error {
	existing, ok := keyCache.Get(key.ID)
	if !ok {
		return fmt.Errorf("API key not found")
	}

	// Determine whether the key value itself is being changed.
	newKeyValue := strings.TrimSpace(key.APIKey)
	keyValueChanged := newKeyValue != "" && newKeyValue != existing.APIKey

	if keyValueChanged {
		encrypted, err := crypto.Encrypt(newKeyValue)
		if err != nil {
			return fmt.Errorf("failed to encrypt API key: %w", err)
		}
		// 加密后落库；恢复明文供缓存使用。
		key.APIKey = encrypted
		key.APIKeyHash = HashAPIKey(newKeyValue)
		if err := db.GetDB().WithContext(ctx).Save(key).Error; err != nil {
			key.APIKey = newKeyValue
			key.APIKeyHash = ""
			return fmt.Errorf("failed to update API key: %w", err)
		}
		key.APIKey = newKeyValue
		// 清理该 ID 的旧映射（重启后明文键未知，扫描 keyIDMap 按 ID 删除）。
		for k, v := range keyIDMap.GetAll() {
			if v == key.ID {
				keyIDMap.Del(k)
			}
		}
		keyIDMap.Set(newKeyValue, key.ID)
		keyCache.Set(key.ID, *key)
	} else {
		// Key value unchanged; omit key columns from the save to avoid accidental overwrite.
		key.APIKey = existing.APIKey
		key.APIKeyHash = existing.APIKeyHash
		if err := db.GetDB().WithContext(ctx).Omit("api_key", "api_key_hash").Save(key).Error; err != nil {
			return fmt.Errorf("failed to update API key: %w", err)
		}
		keyCache.Set(key.ID, *key)
	}
	return nil
}

func List(ctx context.Context) ([]model.APIKey, error) {
	keys := make([]model.APIKey, 0, keyCache.Len())
	for _, apiKey := range keyCache.GetAll() {
		keys = append(keys, apiKey)
	}
	return keys, nil
}

func Get(id int, ctx context.Context) (model.APIKey, error) {
	apiKey, ok := keyCache.Get(id)
	if !ok {
		return model.APIKey{}, fmt.Errorf("API key not found")
	}
	return apiKey, nil
}

func GetByKey(apiKey string, ctx context.Context) (model.APIKey, error) {
	id, ok := keyIDMap.Get(apiKey)
	if !ok {
		// 明文映射未命中（如重启后）：按确定性哈希列回查 DB 并解密重建映射。
		var key model.APIKey
		hash := HashAPIKey(apiKey)
		if err := db.GetDB().WithContext(ctx).Where("api_key_hash = ?", hash).First(&key).Error; err != nil {
			return model.APIKey{}, fmt.Errorf("API key not found")
		}
		// 解密恢复明文。存量哈希化 key（明文不可逆）的 api_key 列存的是 64 hex 而非 enc:，
		// crypto.Decrypt 会原样返回该哈希值——认证不受影响（客户端持明文，hash 列已匹配），
		// 但显示/复制时该哈希值会被前端标记为「需重生」。
		plaintext, err := crypto.Decrypt(key.APIKey)
		if err != nil {
			return model.APIKey{}, fmt.Errorf("API key not found")
		}
		key.APIKey = plaintext
		keyCache.Set(key.ID, key)
		keyIDMap.Set(plaintext, key.ID)
		return key, nil
	}
	return Get(id, ctx)
}

// DeleteStatsFunc is a callback to delete stats associated with an API key.
// Set by the op package to handle cross-package stats cache references.
var DeleteStatsFunc func(id int)

// DeleteSessionFunc is a callback to delete sticky session entries for an API key.
// Set by the relay package to handle cross-package balancer session cleanup.
var DeleteSessionFunc func(id int)

func Delete(id int, ctx context.Context) error {
	if _, err := Get(id, ctx); err != nil {
		return err
	}
	tx := db.GetDB().WithContext(ctx).Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	result := tx.Delete(&model.APIKey{ID: id})
	if result.Error != nil {
		tx.Rollback()
		return fmt.Errorf("failed to delete API key: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		tx.Rollback()
		return fmt.Errorf("API key not found")
	}
	if err := tx.Where("api_key_id = ?", id).Delete(&model.StatsAPIKey{}).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to delete stats API key: %w", err)
	}
	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed to commit API key deletion: %w", err)
	}

	if DeleteStatsFunc != nil {
		DeleteStatsFunc(id)
	}

	if DeleteSessionFunc != nil {
		DeleteSessionFunc(id)
	}

	keyCache.Del(id)
	// keyIDMap 以明文为键（重启后明文未知），按 ID 扫描清理。
	for k, v := range keyIDMap.GetAll() {
		if v == id {
			keyIDMap.Del(k)
		}
	}
	return nil
}

func RefreshCache(ctx context.Context) error {
	apiKeys := []model.APIKey{}
	if err := db.GetDB().WithContext(ctx).Find(&apiKeys).Error; err != nil {
		return err
	}
	keyCache.Clear()
	keyIDMap.Clear()
	for i := range apiKeys {
		raw := apiKeys[i].APIKey
		hash := apiKeys[i].APIKeyHash

		if crypto.IsEncrypted(raw) {
			// 加密密文：解密恢复明文，重建明文→ID 映射。
			plaintext, err := crypto.Decrypt(raw)
			if err == nil {
				apiKeys[i].APIKey = plaintext
				keyIDMap.Set(plaintext, apiKeys[i].ID)
			}
			// 解密失败（密钥变更等）：保留密文，认证仍可用 hash 列；显示需重生。
		} else if isLegacyHashedAPIKey(raw) {
			// 哈希化时期存量：明文不可逆。api_key 列保留哈希不动，
			// 认可用 hash 列匹配（客户端持明文）；显示/复制标记需重生。
			// 若 hash 列为空（存量哈希迁移未补 hash 列），用哈希值本身补齐。
			if hash == "" {
				hash = raw
				_ = db.GetDB().WithContext(ctx).Model(&model.APIKey{}).
					Where("id = ?", apiKeys[i].ID).
					Update("api_key_hash", hash).Error
				apiKeys[i].APIKeyHash = hash
			}
		} else if strings.HasPrefix(raw, "sk-") {
			// 哈希化前的存量明文：加密化落库 + 补 hash 列。
			encrypted, err := crypto.Encrypt(raw)
			if err == nil {
				if hash == "" {
					hash = HashAPIKey(raw)
				}
				_ = db.GetDB().WithContext(ctx).Model(&model.APIKey{}).
					Where("id = ?", apiKeys[i].ID).
					Updates(map[string]any{
						"api_key":      encrypted,
						"api_key_hash": hash,
					}).Error
				apiKeys[i].APIKey = raw
				apiKeys[i].APIKeyHash = hash
				keyIDMap.Set(raw, apiKeys[i].ID)
			}
		}

		keyCache.Set(apiKeys[i].ID, apiKeys[i])
	}
	return nil
}
