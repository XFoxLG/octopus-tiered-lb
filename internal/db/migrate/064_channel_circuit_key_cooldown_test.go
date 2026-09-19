package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func TestAddChannelCircuitAndKeyCooldownOverrides(t *testing.T) {
	dbPath := t.TempDir() + "/circuit-cooldown-064.db"
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	// 从旧 schema 开始（channels 不含新列），验证迁移真正加列而非 no-op。
	if err := db.Exec("CREATE TABLE channels (id INTEGER PRIMARY KEY, name TEXT)").Error; err != nil {
		t.Fatalf("create legacy channels table: %v", err)
	}
	if err := db.Exec("INSERT INTO channels (id, name) VALUES (1, 'legacy')").Error; err != nil {
		t.Fatalf("insert legacy channel: %v", err)
	}

	if err := addChannelCircuitAndKeyCooldownOverrides(db); err != nil {
		t.Fatalf("addChannelCircuitAndKeyCooldownOverrides: %v", err)
	}
	// 幂等：重复执行不报错。
	if err := addChannelCircuitAndKeyCooldownOverrides(db); err != nil {
		t.Fatalf("addChannelCircuitAndKeyCooldownOverrides idempotent: %v", err)
	}

	for _, col := range []string{
		"circuit_breaker_threshold",
		"circuit_breaker_cooldown",
		"circuit_breaker_max_cooldown",
		"key_cooldown_ratelimit",
		"key_cooldown_auth_error",
		"key_cooldown_server_error",
	} {
		if !db.Migrator().HasColumn(&model.Channel{}, col) {
			t.Fatalf("channels missing column %s", col)
		}
	}

	// 既有数据存活；新列默认 0（= 跟随全局设置，行为不变）。
	var stored struct {
		ID                        int
		Name                      string
		CircuitBreakerThreshold   int
		CircuitBreakerCooldown    int
		CircuitBreakerMaxCooldown int
		KeyCooldownRatelimit      int
		KeyCooldownAuthError      int
		KeyCooldownServerError    int
	}
	if err := db.Table("channels").Select("id", "name",
		"circuit_breaker_threshold", "circuit_breaker_cooldown", "circuit_breaker_max_cooldown",
		"key_cooldown_ratelimit", "key_cooldown_auth_error", "key_cooldown_server_error",
	).First(&stored, 1).Error; err != nil {
		t.Fatalf("query channel: %v", err)
	}
	if stored.Name != "legacy" {
		t.Fatalf("unexpected legacy channel name: %+v", stored)
	}
	if stored.CircuitBreakerThreshold != 0 || stored.CircuitBreakerCooldown != 0 || stored.CircuitBreakerMaxCooldown != 0 ||
		stored.KeyCooldownRatelimit != 0 || stored.KeyCooldownAuthError != 0 || stored.KeyCooldownServerError != 0 {
		t.Fatalf("new columns should default to 0 (follow global): %+v", stored)
	}
}
