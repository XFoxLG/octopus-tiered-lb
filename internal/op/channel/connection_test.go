package channel

import (
	"context"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"testing"
)

func TestConnectionUpdatePersistsAndRejectsLegacyWrites(t *testing.T) {
	setupBatchGroupTest(t)
	seedChannel(t, 1, 1)
	cfg := &model.ConnectionConfig{Version: 1, Selection: "same_protocol", Endpoints: []model.ChannelEndpoint{{ID: "chat-1", Protocol: "chat", URL: "https://example.com/v1", URLMode: "base", Auth: "default"}}, Catalog: model.ModelCatalog{Format: "manual"}}
	updated, err := Update(&model.ChannelUpdateRequest{ID: 1, ConnectionConfig: cfg}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if updated.ConnectionConfig == nil || updated.ConnectionConfig.Endpoints[0].ID != "chat-1" {
		t.Fatal("cache missing new configuration")
	}
	var got model.Channel
	if err := db.GetDB().First(&got, 1).Error; err != nil {
		t.Fatal(err)
	}
	if got.ConnectionConfig == nil {
		t.Fatal("serializer failed")
	}
	urls := []model.BaseUrl{{URL: "https://wrong.test"}}
	if _, err := Update(&model.ChannelUpdateRequest{ID: 1, BaseUrls: &urls}, context.Background()); err == nil {
		t.Fatal("legacy update accepted")
	}
	if _, err := ApplyProbeProtocols(context.Background(), 1, []string{"chat_only"}); err == nil {
		t.Fatal("probe rewrote legacy protocols")
	}
	name := "renamed"
	if _, err := Update(&model.ChannelUpdateRequest{ID: 1, Name: &name}, context.Background()); err != nil {
		t.Fatal(err)
	}
}
