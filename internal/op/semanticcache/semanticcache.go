// Package semanticcache applies persisted semantic cache settings to the
// global cache used by the relay hot path. The hot path itself re-derives the
// config from the setting generation (see relay/semantic_cache.go); this
// package forces an immediate refresh when settings are saved or data is
// imported.
package semanticcache

import (
	"strings"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/utils/semantic_cache"
)

const (
	defaultTTLSeconds = 3600
	defaultThreshold  = 98
	defaultMaxEntries = 1000
	defaultTimeoutSec = 10
)

// RefreshSemanticCacheRuntime rebuilds the global semantic cache from current
// settings. When the cache is disabled or the embedding configuration is
// incomplete, the global cache is reset so no stale entries are served.
func RefreshSemanticCacheRuntime() error {
	cfg, ok, err := buildRuntimeConfigFromSettings()
	if err != nil {
		return err
	}
	if !ok {
		semantic_cache.Reset()
		return nil
	}
	semantic_cache.ApplyRuntimeConfig(cfg)
	return nil
}

func buildRuntimeConfigFromSettings() (semantic_cache.RuntimeConfig, bool, error) {
	enabled, err := setting.GetBool(model.SettingKeySemanticCacheEnabled)
	if err != nil {
		return semantic_cache.RuntimeConfig{}, false, err
	}
	if !enabled {
		return semantic_cache.RuntimeConfig{}, false, nil
	}

	ttlSeconds, err := setting.GetInt(model.SettingKeySemanticCacheTTL)
	if err != nil || ttlSeconds <= 0 {
		ttlSeconds = defaultTTLSeconds
	}

	thresholdRaw, err := setting.GetInt(model.SettingKeySemanticCacheThreshold)
	if err != nil || thresholdRaw < 0 || thresholdRaw > 100 {
		thresholdRaw = defaultThreshold
	}

	maxEntries, err := setting.GetInt(model.SettingKeySemanticCacheMaxEntries)
	if err != nil || maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}

	baseURL, err := setting.GetString(model.SettingKeySemanticCacheEmbeddingBaseURL)
	if err != nil {
		return semantic_cache.RuntimeConfig{}, false, err
	}
	modelName, err := setting.GetString(model.SettingKeySemanticCacheEmbeddingModel)
	if err != nil {
		return semantic_cache.RuntimeConfig{}, false, err
	}
	baseURL = strings.TrimSpace(baseURL)
	modelName = strings.TrimSpace(modelName)
	if baseURL == "" || modelName == "" {
		return semantic_cache.RuntimeConfig{}, false, nil
	}

	apiKey, err := setting.GetString(model.SettingKeySemanticCacheEmbeddingAPIKey)
	if err != nil {
		return semantic_cache.RuntimeConfig{}, false, err
	}

	timeoutSeconds, err := setting.GetInt(model.SettingKeySemanticCacheEmbeddingTimeoutSeconds)
	if err != nil || timeoutSeconds <= 0 {
		timeoutSeconds = defaultTimeoutSec
	}

	return semantic_cache.RuntimeConfig{
		Enabled:          true,
		MaxEntries:       maxEntries,
		Threshold:        float64(thresholdRaw) / 100.0,
		TTL:              time.Duration(ttlSeconds) * time.Second,
		EmbeddingBaseURL: baseURL,
		EmbeddingAPIKey:  strings.TrimSpace(apiKey),
		EmbeddingModel:   modelName,
		EmbeddingTimeout: time.Duration(timeoutSeconds) * time.Second,
	}, true, nil
}
