package semanticcache

import (
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/utils/semantic_cache"
)

func seedSettingsForTest(t *testing.T, overrides map[model.SettingKey]string) {
	t.Helper()
	cache := setting.GetCache()
	for _, s := range model.DefaultSettings() {
		value := s.Value
		if override, ok := overrides[s.Key]; ok {
			value = override
		}
		cache.Set(s.Key, value)
	}
	for key, value := range overrides {
		if _, exists := cache.Get(key); exists {
			continue
		}
		cache.Set(key, value)
	}
}

func TestRefreshSemanticCacheRuntime_ResetsDisabledOrIncompleteConfig(t *testing.T) {
	semantic_cache.Reset()
	semantic_cache.ResetRuntimeStats()
	defer semantic_cache.Reset()
	defer semantic_cache.ResetRuntimeStats()

	semantic_cache.ApplyRuntimeConfig(semantic_cache.RuntimeConfig{
		Enabled:          true,
		MaxEntries:       8,
		Threshold:        0.98,
		TTL:              time.Hour,
		EmbeddingBaseURL: "https://example.com",
		EmbeddingModel:   "text-embedding-3-small",
	})
	if !semantic_cache.Enabled() {
		t.Fatal("expected seeded runtime cache to be enabled")
	}

	seedSettingsForTest(t, map[model.SettingKey]string{
		model.SettingKeySemanticCacheEnabled: "false",
	})
	if err := RefreshSemanticCacheRuntime(); err != nil {
		t.Fatalf("RefreshSemanticCacheRuntime() disabled config error = %v", err)
	}
	if semantic_cache.RuntimeEnabled() {
		t.Fatal("expected disabled setting to clear semantic cache runtime")
	}

	semantic_cache.ApplyRuntimeConfig(semantic_cache.RuntimeConfig{
		Enabled:          true,
		MaxEntries:       8,
		Threshold:        0.98,
		TTL:              time.Hour,
		EmbeddingBaseURL: "https://example.com",
		EmbeddingModel:   "text-embedding-3-small",
	})
	seedSettingsForTest(t, map[model.SettingKey]string{
		model.SettingKeySemanticCacheEnabled:          "true",
		model.SettingKeySemanticCacheEmbeddingBaseURL: "",
		model.SettingKeySemanticCacheEmbeddingModel:   "text-embedding-3-small",
	})
	if err := RefreshSemanticCacheRuntime(); err != nil {
		t.Fatalf("RefreshSemanticCacheRuntime() incomplete config error = %v", err)
	}
	if semantic_cache.RuntimeEnabled() {
		t.Fatal("expected incomplete config to clear semantic cache runtime")
	}
}
