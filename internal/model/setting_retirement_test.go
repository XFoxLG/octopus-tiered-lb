package model

import "testing"

func TestSemanticCacheSettingsAreRetiredNotDefaulted(test *testing.T) {
	expectedKeys := []SettingKey{
		"semantic_cache_enabled",
		"semantic_cache_ttl",
		"semantic_cache_threshold",
		"semantic_cache_max_entries",
		"semantic_cache_embedding_base_url",
		"semantic_cache_embedding_api_key",
		"semantic_cache_embedding_model",
		"semantic_cache_embedding_timeout_seconds",
	}
	retiredKeys := RetiredSemanticCacheSettingKeys()
	if len(retiredKeys) != len(expectedKeys) {
		test.Fatalf("retired keys = %v, want %v", retiredKeys, expectedKeys)
	}
	for keyIndex, expectedKey := range expectedKeys {
		if retiredKeys[keyIndex] != expectedKey || !IsRetiredSemanticCacheSetting(expectedKey) {
			test.Fatalf("retired key %d = %q, want %q", keyIndex, retiredKeys[keyIndex], expectedKey)
		}
		for _, legacyValue := range []string{"true", "false", "", "legacy value"} {
			legacySetting := Setting{Key: expectedKey, Value: legacyValue}
			if err := legacySetting.Validate(); err == nil {
				test.Errorf("Validate(%q, %q) accepted a retired setting", expectedKey, legacyValue)
			}
		}
	}
	for _, defaultSetting := range DefaultSettings() {
		if IsRetiredSemanticCacheSetting(defaultSetting.Key) {
			test.Errorf("DefaultSettings still creates %q", defaultSetting.Key)
		}
	}
	for _, activeKey := range []SettingKey{SettingKeyAIRouteModel, SettingKeyFailureHintTTLNetwork, SettingKeyRelayLogContentEnabled, "semantic_cache_unknown"} {
		if IsRetiredSemanticCacheSetting(activeKey) {
			test.Errorf("unrelated setting %q was retired", activeKey)
		}
	}

	retiredKeys[0] = SettingKeyAIRouteModel
	if !IsRetiredSemanticCacheSetting(SettingKeySemanticCacheEnabled) || RetiredSemanticCacheSettingKeys()[0] != SettingKeySemanticCacheEnabled {
		test.Fatal("caller mutation changed the retired-key registry")
	}
}
