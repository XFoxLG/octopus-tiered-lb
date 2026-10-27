package backup

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm/clause"
)

// TestBatchInsertLargeDataset 验证分批插入能正确处理大数据集（>5000行）
func TestBatchInsertLargeDataset(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "batch_test.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if conn := db.GetDB(); conn != nil {
			sqlDB, _ := conn.DB()
			if sqlDB != nil {
				_ = sqlDB.Close()
			}
		}
	}()

	// 准备 12000 条 channel 数据（超过 batchInsertSize=5000，会分3批）
	totalRows := 12000
	channels := make([]model.Channel, totalRows)
	for i := range totalRows {
		channels[i] = model.Channel{
			Name:          fmt.Sprintf("test-channel-%d", i),
			Type:          1,
			BaseUrls:      []model.BaseUrl{{URL: fmt.Sprintf("https://api%d.example.com", i)}},
			Enabled:       true,
			AutoSync:      false,
			SkipModelTest: false,
		}
	}

	// 导入
	cfg := &importConfig{
		conn:    db.GetDB().WithContext(context.Background()),
		res:     &model.DBImportResult{RowsAffected: map[string]int64{}},
		isFull:  false,
		version: 1,
	}

	err := cfg.batchInsert("channels", &channels, len(channels), clause.OnConflict{DoNothing: true}, "insert")
	if err != nil {
		t.Fatalf("batchInsert failed: %v", err)
	}

	// 验证所有行都已插入
	var count int64
	if err := db.GetDB().Model(&model.Channel{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != int64(totalRows) {
		t.Errorf("expected %d channels, got %d", totalRows, count)
	}

	// 验证结果报告（appendStep 写 Progress，RowsAffected map 由 ImportWithModeToDB 汇总）
	if len(cfg.res.Progress) == 0 {
		t.Fatal("expected at least one progress step")
	}
	var totalReported int64
	for _, step := range cfg.res.Progress {
		totalReported += step.RowsAffected
	}
	if totalReported != int64(totalRows) {
		t.Errorf("expected total RowsAffected=%d, got %d", totalRows, totalReported)
	}
}

// TestBatchInsertPartialConflict 验证分批插入遇到冲突时的 DoNothing 行为
func TestBatchInsertPartialConflict(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "conflict_test.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if conn := db.GetDB(); conn != nil {
			sqlDB, _ := conn.DB()
			if sqlDB != nil {
				_ = sqlDB.Close()
			}
		}
	}()

	// 先插入 5 条
	existing := []model.Channel{
		{ID: 1, Name: "existing-1", Type: 1, BaseUrls: []model.BaseUrl{{URL: "https://api1.example.com"}}, Enabled: true},
		{ID: 2, Name: "existing-2", Type: 1, BaseUrls: []model.BaseUrl{{URL: "https://api2.example.com"}}, Enabled: true},
		{ID: 3, Name: "existing-3", Type: 1, BaseUrls: []model.BaseUrl{{URL: "https://api3.example.com"}}, Enabled: true},
		{ID: 4, Name: "existing-4", Type: 1, BaseUrls: []model.BaseUrl{{URL: "https://api4.example.com"}}, Enabled: true},
		{ID: 5, Name: "existing-5", Type: 1, BaseUrls: []model.BaseUrl{{URL: "https://api5.example.com"}}, Enabled: true},
	}
	if err := db.GetDB().Create(&existing).Error; err != nil {
		t.Fatal(err)
	}

	// 准备 10 条，其中前 5 条 ID 冲突
	importData := make([]model.Channel, 10)
	for i := range 10 {
		importData[i] = model.Channel{
			ID:       i + 1, // ID 1-10，前 5 个冲突
			Name:     fmt.Sprintf("import-%d", i+1),
			Type:     1,
			BaseUrls: []model.BaseUrl{{URL: fmt.Sprintf("https://import%d.example.com", i+1)}},
			Enabled:  true,
		}
	}

	cfg := &importConfig{
		conn:    db.GetDB().WithContext(context.Background()),
		res:     &model.DBImportResult{RowsAffected: map[string]int64{}},
		isFull:  false,
		version: 1,
	}

	// DoNothing：冲突跳过
	err := cfg.batchInsert("channels", &importData, len(importData), clause.OnConflict{DoNothing: true}, "insert")
	if err != nil {
		t.Fatalf("batchInsert failed: %v", err)
	}

	// 验证总数：5 个已存在 + 5 个新插入 = 10
	var count int64
	if err := db.GetDB().Model(&model.Channel{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 10 {
		t.Errorf("expected 10 channels, got %d", count)
	}

	// 验证前 5 个保持原名称（未被覆盖）
	var ch model.Channel
	if err := db.GetDB().First(&ch, 1).Error; err != nil {
		t.Fatal(err)
	}
	if ch.Name != "existing-1" {
		t.Errorf("expected name 'existing-1', got '%s' (DoNothing should not overwrite)", ch.Name)
	}
}

// TestBatchInsertExactValuesUpsertWithStringPrimaryKey covers the map insertion
// path used when a model has default-valued columns. PostgreSQL needs the model
// retained on the statement to render the LLMInfo string primary key in an
// ON CONFLICT UPDATE; Table(...) plus a map alone produced
// "model value required".
func TestBatchInsertExactValuesUpsertWithStringPrimaryKey(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "pointer_path_test.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if conn := db.GetDB(); conn != nil {
			sqlDB, _ := conn.DB()
			if sqlDB != nil {
				_ = sqlDB.Close()
			}
		}
	})

	rows := make([]model.LLMInfo, 1200)
	for i := range rows {
		rows[i] = model.LLMInfo{Name: fmt.Sprintf("model-%04d", i)}
	}
	cfg := &importConfig{
		conn:    db.GetDB().WithContext(context.Background()),
		res:     &model.DBImportResult{RowsAffected: map[string]int64{}},
		version: dbDumpVersion,
	}
	if err := cfg.batchInsert(
		"llm_infos",
		&rows,
		len(rows),
		clause.OnConflict{
			Columns:   []clause.Column{{Name: "name"}},
			UpdateAll: true,
		},
		"upsert",
	); err != nil {
		t.Fatalf("batchInsert pointer path failed: %v", err)
	}

	var count int64
	if err := db.GetDB().Model(&model.LLMInfo{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != int64(len(rows)) {
		t.Fatalf("llm_infos count = %d, want %d", count, len(rows))
	}
}

// TestBatchInsertEmptySlice 验证空切片不会导致错误
func TestBatchInsertEmptySlice(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "empty_test.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if conn := db.GetDB(); conn != nil {
			sqlDB, _ := conn.DB()
			if sqlDB != nil {
				_ = sqlDB.Close()
			}
		}
	}()

	empty := []model.Channel{}
	cfg := &importConfig{
		conn:    db.GetDB().WithContext(context.Background()),
		res:     &model.DBImportResult{RowsAffected: map[string]int64{}},
		isFull:  false,
		version: 1,
	}

	err := cfg.batchInsert("channels", &empty, 0, clause.OnConflict{DoNothing: true}, "insert")
	if err != nil {
		t.Errorf("empty slice should not cause error: %v", err)
	}
}
