package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func TestAddChannelErrorPolicy(t *testing.T) {
	dbPath := t.TempDir() + "/channel-error-policy-067.db"
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	// 从旧 schema 开始(channels 不含新列),验证迁移真正加列而非 no-op。
	if err := db.Exec("CREATE TABLE channels (id INTEGER PRIMARY KEY, name TEXT)").Error; err != nil {
		t.Fatalf("create legacy channels table: %v", err)
	}
	if err := db.Exec("INSERT INTO channels (id, name) VALUES (1, 'legacy')").Error; err != nil {
		t.Fatalf("insert legacy channel: %v", err)
	}

	if err := addChannelErrorPolicy(db); err != nil {
		t.Fatalf("addChannelErrorPolicy: %v", err)
	}
	// 幂等:重复执行不报错。
	if err := addChannelErrorPolicy(db); err != nil {
		t.Fatalf("addChannelErrorPolicy idempotent: %v", err)
	}

	for _, col := range []string{
		"retryable_status_codes",
		"retryable_keywords",
		"non_retryable_status_codes",
		"error_message_template",
	} {
		if !db.Migrator().HasColumn(&model.Channel{}, col) {
			t.Fatalf("channels missing column %s", col)
		}
	}

	// 既有数据存活;新列默认为空串(= 完全沿用现有行为,行为不变)。
	var stored struct {
		ID                      int
		Name                    string
		RetryableStatusCodes    string
		RetryableKeywords       string
		NonRetryableStatusCodes string
		ErrorMessageTemplate    string
	}
	if err := db.Table("channels").Select("id", "name",
		"retryable_status_codes", "retryable_keywords", "non_retryable_status_codes", "error_message_template",
	).First(&stored, 1).Error; err != nil {
		t.Fatalf("query channel: %v", err)
	}
	if stored.Name != "legacy" {
		t.Fatalf("unexpected legacy channel name: %+v", stored)
	}
	if stored.RetryableStatusCodes != "" || stored.RetryableKeywords != "" || stored.NonRetryableStatusCodes != "" || stored.ErrorMessageTemplate != "" {
		t.Fatalf("new columns should default to empty (preserve behavior): %+v", stored)
	}
}
