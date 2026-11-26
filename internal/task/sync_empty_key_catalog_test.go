package task

import (
	"context"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/llm"
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
			{Enabled: true, ChannelKey: "sk-empty", Remark: "empty"},
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

func TestSyncModelsTaskKeepsConfiguredGroupItemWhenCatalogOmitsModel(t *testing.T) {
	setupPerKeySyncDB(t)
	srv := perKeyUpstream(t, map[string][]string{"sk-sync": {"model-b"}})
	ch := &model.Channel{
		Name:     "sync-omitted-model",
		GroupID:  1,
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		AutoSync: true,
		Model:    "model-a,model-b",
		BaseUrls: []model.BaseUrl{{URL: srv.URL}},
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "sk-sync"}},
	}
	seedSyncChannel(t, ch)
	g := &model.Group{Name: "stable-model-alias"}
	if err := db.GetDB().Create(g).Error; err != nil {
		t.Fatal(err)
	}
	item := &model.GroupItem{GroupID: g.ID, ChannelID: ch.ID, ModelName: "model-a"}
	if err := db.GetDB().Create(item).Error; err != nil {
		t.Fatal(err)
	}

	SyncModelsTask()

	got, _ := loadSyncChannel(t, ch.ID)
	if got.Model != "model-b" {
		t.Fatalf("channel catalog = %q, want refreshed catalog %q", got.Model, "model-b")
	}
	var count int64
	if err := db.GetDB().Model(&model.GroupItem{}).Where("id = ?", item.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("configured group item was deleted after catalog omission")
	}
}

func TestSyncModelsTaskKeepsSnapshotWhenSingleKeyCatalogIsEmpty(t *testing.T) {
	setupPerKeySyncDB(t)
	srv := perKeyUpstream(t, map[string][]string{"sk-sync": {}})
	ch := &model.Channel{
		Name:     "sync-empty-single-key",
		GroupID:  1,
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		AutoSync: true,
		Model:    "model-a",
		BaseUrls: []model.BaseUrl{{URL: srv.URL}},
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "sk-sync"}},
	}
	seedSyncChannel(t, ch)
	g := &model.Group{Name: "stable-empty-alias"}
	if err := db.GetDB().Create(g).Error; err != nil {
		t.Fatal(err)
	}
	item := &model.GroupItem{GroupID: g.ID, ChannelID: ch.ID, ModelName: "model-a"}
	if err := db.GetDB().Create(item).Error; err != nil {
		t.Fatal(err)
	}

	SyncModelsTask()

	got, _ := loadSyncChannel(t, ch.ID)
	if got.Model != "model-a" {
		t.Fatalf("empty catalog cleared last known snapshot: %q", got.Model)
	}
	var count int64
	if err := db.GetDB().Model(&model.GroupItem{}).Where("id = ?", item.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("empty catalog deleted configured group item")
	}
}

func TestSyncModelsTaskKeepsPriceCatalogWhenUpstreamFetchFails(t *testing.T) {
	setupPerKeySyncDB(t)
	if err := db.GetDB().Create(&model.LLMInfo{Name: "model-a"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := llm.RefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}

	srv := perKeyUpstream(t, map[string][]string{"sk-sync": nil})
	ch := &model.Channel{
		Name:     "sync-failed-price-snapshot",
		GroupID:  1,
		Type:     outbound.OutboundTypeOpenAIChat,
		Enabled:  true,
		AutoSync: true,
		Model:    "model-a",
		BaseUrls: []model.BaseUrl{{URL: srv.URL}},
		Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: "sk-sync"}},
	}
	seedSyncChannel(t, ch)

	SyncModelsTask()

	var count int64
	if err := db.GetDB().Model(&model.LLMInfo{}).Where("name = ?", "model-a").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("temporary upstream failure deleted the last-known zero-price model entry")
	}
}
