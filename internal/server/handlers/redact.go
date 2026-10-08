package handlers

import (
	"net/url"
	"strings"

	"github.com/lingyuins/octopus/internal/model"
)

const viewerMaskedDomain = "***"

// Copy before redaction: cached endpoint objects are shared with the relay.
func maskConnectionConfig(config *model.ConnectionConfig) *model.ConnectionConfig {
	if config == nil {
		return nil
	}
	masked := *config
	masked.Endpoints = append([]model.ChannelEndpoint(nil), config.Endpoints...)
	for i := range masked.Endpoints {
		masked.Endpoints[i].URL = viewerMaskedDomain
		masked.Endpoints[i].Headers = append([]model.CustomHeader(nil), masked.Endpoints[i].Headers...)
		for j := range masked.Endpoints[i].Headers {
			masked.Endpoints[i].Headers[j].HeaderValue = viewerMaskedDomain
		}
	}
	if masked.Catalog.URL != "" {
		masked.Catalog.URL = viewerMaskedDomain
	}
	return &masked
}

func isViewerRole(role string) bool {
	return strings.TrimSpace(role) == model.UserRoleViewer
}

func maskURLDomainForViewer(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return raw
	}

	parsed, err := url.Parse(trimmed)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.User = nil
		parsed.Host = viewerMaskedDomain
		return parsed.String()
	}

	return viewerMaskedDomain
}

func redactChannelBaseURLsForViewer(channels []model.Channel) {
	for channelIndex := range channels {
		for baseURLIndex := range channels[channelIndex].BaseUrls {
			channels[channelIndex].BaseUrls[baseURLIndex].URL = maskURLDomainForViewer(channels[channelIndex].BaseUrls[baseURLIndex].URL)
		}
		// Mask custom proxy addresses (may contain credentials: socks5://user:pass@host)
		if channels[channelIndex].ChannelProxy != nil {
			masked := maskURLDomainForViewer(*channels[channelIndex].ChannelProxy)
			channels[channelIndex].ChannelProxy = &masked
		}
	}
}

func redactCredentialBaseURLsForViewer(profiles []model.APICredentialProfile) {
	for profileIndex := range profiles {
		profiles[profileIndex].BaseURL = maskURLDomainForViewer(profiles[profileIndex].BaseURL)
		profiles[profileIndex].APIKey = viewerMaskedDomain
	}
}

func redactSettingsURLsForViewer(settings []model.Setting) {
	for settingIndex := range settings {
		switch settings[settingIndex].Key {
		case model.SettingKeyProxyURL,
			model.SettingKeyPublicAPIBaseURL,
			model.SettingKeySemanticCacheEmbeddingBaseURL,
			model.SettingKeyAIRouteBaseURL:
			settings[settingIndex].Value = maskURLDomainForViewer(settings[settingIndex].Value)
		case model.SettingKeyWebDAVConfig,
			model.SettingKeySemanticCacheEmbeddingAPIKey,
			model.SettingKeyAIRouteAPIKey,
			model.SettingKeyAIRouteServices:
			// 密钥类设置（WebDAV 密码、embedding/路由 API Key、服务池 JSON）对
			// viewer 整体遮蔽，避免明文凭据经设置列表泄露。
			settings[settingIndex].Value = viewerMaskedDomain
		}
	}
}
