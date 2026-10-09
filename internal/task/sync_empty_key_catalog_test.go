package task

import (
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"testing"
)

func TestReviewEmptySuccessfulKeyCatalogPreservesModels(t *testing.T) {
	setupPerKeySyncDB(t)
	srv := perKeyUpstream(t, map[string][]string{"sk-empty": {}, "sk-good": {"model-b"}})
	ch := &model.Channel{Name: "review-empty-catalog", GroupID: 1, Type: outbound.OutboundTypeOpenAIChat,
		Enabled: true, AutoSync: true, AutoSyncKeyModels: true, Model: "model-a,model-b",
		BaseUrls: []model.BaseUrl{{URL: srv.URL}},
		Keys: []model.ChannelKey{
			{Enabled: true, ChannelKey: "sk-empty", Remark: "empty", SupportedModels: "model-a"},
			{Enabled: true, ChannelKey: "sk-good", Remark: "good", SupportedModels: "model-b"},
		},
	}
	seedSyncChannel(t, ch)
	group := &model.Group{Name: "review-model-a"}
	if err := db.GetDB().Create(group).Error; err != nil {
		t.Fatal(err)
	}
	item := &model.GroupItem{GroupID: group.ID, ChannelID: ch.ID, ModelName: "model-a"}
	if err := db.GetDB().Create(item).Error; err != nil {
		t.Fatal(err)
	}
	SyncModelsTask()
	got, keys := loadSyncChannel(t, ch.ID)
	var count int64
	if err := db.GetDB().Model(&model.GroupItem{}).Where("id = ?", item.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	t.Logf("channel models=%q; key A models=%q; group item count=%d", got.Model, keys[0].SupportedModels, count)
	if !equalStringSlices(modelsCSV(got.Model), []string{"model-a", "model-b"}) {
		t.Errorf("empty response dropped known channel model: %q", got.Model)
	}
	if count != 1 {
		t.Errorf("empty response deleted the model-a group item")
	}
}
