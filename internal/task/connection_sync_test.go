package task

import (
	"github.com/lingyuins/octopus/internal/model"
	"testing"
)

func TestManualConnectionCatalogDoesNotRunAutomaticSync(t *testing.T) {
	setupPerKeySyncDB(t)
	ch := &model.Channel{Name: "manual-connection", GroupID: 1, Enabled: true, AutoSync: true, AutoSyncKeyModels: true, Model: "kept-model", Keys: []model.ChannelKey{{Enabled: true, ChannelKey: "local-key", SupportedModels: "kept-model"}}, ConnectionConfig: &model.ConnectionConfig{Version: 1, Selection: "same_protocol", Endpoints: []model.ChannelEndpoint{{ID: "chat", Protocol: "chat", URL: "http://192.0.2.1/v1", URLMode: "base", Auth: "default"}}, Catalog: model.ModelCatalog{Format: "manual"}}}
	seedSyncChannel(t, ch)
	SyncModelsTask()
	got, keys := loadSyncChannel(t, ch.ID)
	if got.Model != "kept-model" || len(keys) != 1 || keys[0].SupportedModels != "kept-model" {
		t.Fatal("manual catalog changed configured models")
	}
	if syncFailureTracker.ShouldSkip(ch.ID) {
		t.Fatal("manual catalog incorrectly entered failure cooldown")
	}
}
