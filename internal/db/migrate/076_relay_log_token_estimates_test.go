package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestRelayLogTokenEstimatesMigrationIsAdditiveAndIdempotent 验证迁移 076：
//   - 存量行保持原值，新列回填默认 0（未估算）；
//   - 重复执行不报错、不覆盖已有数据；
//   - relay_logs 表不存在时安全跳过（全新库由 AutoMigrate 建表）。
func TestRelayLogTokenEstimatesMigrationIsAdditiveAndIdempotent(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}

	// 表不存在时安全跳过。
	if err := addRelayLogTokenEstimates(database); err != nil {
		t.Fatalf("missing table should be a no-op: %v", err)
	}

	if err := database.Exec(`CREATE TABLE relay_logs (id INTEGER PRIMARY KEY, input_tokens INTEGER, output_tokens INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO relay_logs (id, input_tokens, output_tokens) VALUES (1, 1200, 0)`).Error; err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if err := addRelayLogTokenEstimates(database); err != nil {
			t.Fatalf("run %d failed: %v", i, err)
		}
	}

	var row struct {
		InputTokens     int
		OutputTokens    int
		InputTokensEst  int
		OutputTokensEst int
	}
	if err := database.Table("relay_logs").Where("id = 1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.InputTokens != 1200 || row.OutputTokens != 0 || row.InputTokensEst != 0 || row.OutputTokensEst != 0 {
		t.Fatalf("legacy record changed: %+v", row)
	}

	// 写入的估算值在迁移重跑后必须保持不变。
	if err := database.Exec(`UPDATE relay_logs SET input_tokens_est = 88, output_tokens_est = 22 WHERE id = 1`).Error; err != nil {
		t.Fatal(err)
	}
	if err := addRelayLogTokenEstimates(database); err != nil {
		t.Fatal(err)
	}
	if err := database.Table("relay_logs").Where("id = 1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.InputTokensEst != 88 || row.OutputTokensEst != 22 {
		t.Fatalf("rerun changed estimates: %+v", row)
	}
}
