package channel

import (
	"context"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"reflect"
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

func TestTaskConnectionUpdateAndCacheReload(t *testing.T) {
	setupBatchGroupTest(t)
	seedChannel(t, 1, 1)
	config := &model.ConnectionConfig{Version: 2, Selection: "configured", Catalog: model.ModelCatalog{Format: "manual"}, Endpoints: []model.ChannelEndpoint{{ID: "task-chat", Protocol: "chat", URL: "https://example.com/v1", URLMode: "base", Auth: "bearer", ForwardMode: "convert", Transport: "catie_tasks", TaskAPIURL: "https://example.com/v1/openai/tasks"}}}
	updated, err := Update(&model.ChannelUpdateRequest{ID: 1, ConnectionConfig: config}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated.ConnectionConfig, config) {
		t.Fatalf("update lost task fields: %+v", updated.ConnectionConfig)
	}
	if err := RefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	back, err := Get(1, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.ConnectionConfig, config) {
		t.Fatalf("reload lost task fields: %+v", back.ConnectionConfig)
	}
	direct := *config
	direct.Version = 1
	direct.Endpoints = append([]model.ChannelEndpoint(nil), config.Endpoints...)
	direct.Endpoints[0].Transport, direct.Endpoints[0].TaskAPIURL = "", ""
	if _, err := Update(&model.ChannelUpdateRequest{ID: 1, ConnectionConfig: &direct}, context.Background()); err != nil {
		t.Fatal(err)
	}
	var stored model.Channel
	if err := db.GetDB().First(&stored, 1).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ConnectionConfig.Version != 1 || stored.ConnectionConfig.Endpoints[0].TaskAPIURL != "" {
		t.Fatalf("disable did not restore v1: %+v", stored.ConnectionConfig)
	}
}
