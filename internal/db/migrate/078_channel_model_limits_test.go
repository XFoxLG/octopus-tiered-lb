package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

// TestChannelModelLimitsMigrationIsIdempotentAndUnique 验证迁移 078：
//   - 首次执行建表并补建唯一索引；
//   - 重复执行不报错、不覆盖已有数据；
//   - (channel_id, model_name) 唯一键阻止重复行（学习写入的去重依据）。
func TestChannelModelLimitsMigrationIsIdempotentAndUnique(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}

	if err := createChannelModelLimits(database); err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	if !database.Migrator().HasTable(&model.ChannelModelLimit{}) {
		t.Fatal("channel_model_limits table was not created")
	}
	if !database.Migrator().HasIndex(&model.ChannelModelLimit{}, "uidx_channel_model_limit") {
		t.Fatal("unique index uidx_channel_model_limit was not created")
	}

	for i := 0; i < 2; i++ {
		if err := createChannelModelLimits(database); err != nil {
			t.Fatalf("idempotent run %d failed: %v", i, err)
		}
	}

	row := model.ChannelModelLimit{
		ChannelID:        7,
		ModelName:        "glm-4.6",
		MaxContextTokens: 128000,
		Source:           model.ChannelModelLimitSourceManual,
		EvidenceCount:    3,
	}
	if err := database.Create(&row).Error; err != nil {
		t.Fatalf("seed row failed: %v", err)
	}
	if err := createChannelModelLimits(database); err != nil {
		t.Fatalf("rerun after seed failed: %v", err)
	}

	var back model.ChannelModelLimit
	if err := database.Where("channel_id = ? AND model_name = ?", 7, "glm-4.6").Take(&back).Error; err != nil {
		t.Fatal(err)
	}
	if back.MaxContextTokens != 128000 || back.Source != model.ChannelModelLimitSourceManual || back.EvidenceCount != 3 {
		t.Fatalf("legacy row changed by migration rerun: %+v", back)
	}

	dup := model.ChannelModelLimit{
		ChannelID:        7,
		ModelName:        "glm-4.6",
		MaxContextTokens: 1,
		Source:           model.ChannelModelLimitSourceLearned,
	}
	if err := database.Create(&dup).Error; err == nil {
		t.Fatal("duplicate (channel_id, model_name) must be rejected by the unique index")
	}
}
