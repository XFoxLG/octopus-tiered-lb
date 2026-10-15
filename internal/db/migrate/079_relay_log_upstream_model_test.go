package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestRelayLogUpstreamModelMigrationIsAdditiveAndIdempotent 验证迁移 079：
//   - relay_logs 表不存在时安全跳过（全新库由 AutoMigrate 建表）；
//   - 重复执行不报错、不覆盖已有数据（新列默认空串 = 未回报）。
func TestRelayLogUpstreamModelMigrationIsAdditiveAndIdempotent(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}

	// 表不存在时安全跳过。
	if err := addRelayLogUpstreamModel(database); err != nil {
		t.Fatalf("missing table should be a no-op: %v", err)
	}

	if err := database.Exec(`CREATE TABLE relay_logs (id INTEGER PRIMARY KEY, input_tokens INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO relay_logs (id, input_tokens) VALUES (1, 1200)`).Error; err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if err := addRelayLogUpstreamModel(database); err != nil {
			t.Fatalf("run %d failed: %v", i, err)
		}
	}

	var row struct {
		InputTokens   int
		UpstreamModel string
	}
	if err := database.Table("relay_logs").Where("id = 1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.InputTokens != 1200 || row.UpstreamModel != "" {
		t.Fatalf("legacy record changed: %+v", row)
	}

	// 已写入的自报模型在迁移重跑后必须保持不变。
	if err := database.Exec(`UPDATE relay_logs SET upstream_model = 'gpt-5-nano' WHERE id = 1`).Error; err != nil {
		t.Fatal(err)
	}
	if err := addRelayLogUpstreamModel(database); err != nil {
		t.Fatal(err)
	}
	if err := database.Table("relay_logs").Where("id = 1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.UpstreamModel != "gpt-5-nano" || row.InputTokens != 1200 {
		t.Fatalf("rerun changed data: %+v", row)
	}
}
