package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestRelayLogImageCountMigrationIsAdditiveAndIdempotent 验证迁移 077：
//   - 存量行保持原值，新列回填默认 0（无图/未统计）；
//   - 重复执行不报错、不覆盖已有数据；
//   - relay_logs 表不存在时安全跳过（全新库由 AutoMigrate 建表）。
func TestRelayLogImageCountMigrationIsAdditiveAndIdempotent(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}

	// 表不存在时安全跳过。
	if err := addRelayLogImageCount(database); err != nil {
		t.Fatalf("missing table should be a no-op: %v", err)
	}

	if err := database.Exec(`CREATE TABLE relay_logs (id INTEGER PRIMARY KEY, input_tokens INTEGER, output_tokens INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO relay_logs (id, input_tokens, output_tokens) VALUES (1, 1200, 0)`).Error; err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if err := addRelayLogImageCount(database); err != nil {
			t.Fatalf("run %d failed: %v", i, err)
		}
	}

	var row struct {
		InputTokens int
		ImageCount  int
	}
	if err := database.Table("relay_logs").Where("id = 1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.InputTokens != 1200 || row.ImageCount != 0 {
		t.Fatalf("legacy record changed: %+v", row)
	}

	// 写入的图片计数在迁移重跑后必须保持不变。
	if err := database.Exec(`UPDATE relay_logs SET image_count = 3 WHERE id = 1`).Error; err != nil {
		t.Fatal(err)
	}
	if err := addRelayLogImageCount(database); err != nil {
		t.Fatal(err)
	}
	if err := database.Table("relay_logs").Where("id = 1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.ImageCount != 3 {
		t.Fatalf("rerun changed image count: %+v", row)
	}
}
