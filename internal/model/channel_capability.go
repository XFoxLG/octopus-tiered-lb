package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const legacyCapabilityScopePrefix = "legacy-channel-"

// CapabilityScopeUID returns the stable channel identity stored with capability
// evidence. A channel created before migration 085 is backfilled before this is
// called. If an orphan row still has no channel, the explicit legacy prefix
// keeps it distinguishable from a real UID instead of fabricating one.
func CapabilityScopeUID(channelUID string, channelID int) string {
	if uid := strings.TrimSpace(channelUID); uid != "" {
		return uid
	}
	return fmt.Sprintf("%s%d", legacyCapabilityScopePrefix, channelID)
}

// IsLegacyCapabilityScopeUID reports whether a scope was derived from a
// channel_id because no stable UID was available.
func IsLegacyCapabilityScopeUID(uid string) bool {
	return strings.HasPrefix(strings.TrimSpace(uid), legacyCapabilityScopePrefix)
}

type baseURLFingerprint struct {
	URL        string `json:"url,omitempty"`
	SuffixMode string `json:"suffix_mode,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
}

// CapabilityConfigurationFingerprint hashes only the settings that can change
// what an upstream capability probe observes. Display names, group membership,
// timeouts, prices, and routing weights are deliberately excluded: changing
// them does not invalidate probe evidence.
func CapabilityConfigurationFingerprint(channel *Channel, endpointID, protocol string) string {
	if channel == nil {
		return ""
	}

	type endpointFingerprint struct {
		ID            string         `json:"id,omitempty"`
		Protocol      string         `json:"protocol,omitempty"`
		URL           string         `json:"url,omitempty"`
		URLMode       string         `json:"url_mode,omitempty"`
		Auth          string         `json:"auth,omitempty"`
		Compatibility string         `json:"compatibility,omitempty"`
		ForwardMode   string         `json:"forward_mode,omitempty"`
		Transport     string         `json:"transport,omitempty"`
		TaskAPIURL    string         `json:"task_api_url,omitempty"`
		Headers       []CustomHeader `json:"headers,omitempty"`
	}
	input := struct {
		ChannelType            string                `json:"channel_type,omitempty"`
		OutboundFormatOverride string                `json:"outbound_format_override,omitempty"`
		UpstreamProtocols      []string              `json:"upstream_protocols,omitempty"`
		CustomHeaders          []CustomHeader        `json:"custom_headers,omitempty"`
		BaseURLs               []baseURLFingerprint  `json:"base_urls,omitempty"`
		ProxyMode              string                `json:"proxy_mode,omitempty"`
		ProxyConfigID          *int                  `json:"proxy_config_id,omitempty"`
		ChannelProxy           *string               `json:"channel_proxy,omitempty"`
		RequestRewrite         *RequestRewriteConfig `json:"request_rewrite,omitempty"`
		ParamOverride          *string               `json:"param_override,omitempty"`
		Endpoint               *endpointFingerprint  `json:"endpoint,omitempty"`
	}{
		ChannelType:            channel.Type.String(),
		OutboundFormatOverride: channel.OutboundFormatOverride,
		UpstreamProtocols:      sortedStrings(channel.UpstreamProtocols),
		CustomHeaders:          sortedHeaders(channel.CustomHeader),
		BaseURLs:               capabilityBaseURLs(channel.BaseUrls),
		ProxyMode:              string(channel.ProxyMode),
		ProxyConfigID:          channel.ProxyConfigID,
		ChannelProxy:           channel.ChannelProxy,
		RequestRewrite:         channel.RequestRewrite,
		ParamOverride:          channel.ParamOverride,
	}
	if channel.ConnectionConfig != nil {
		// A configured connection has no trustworthy legacy URL scope.
		input.BaseURLs = nil
		for index := range channel.ConnectionConfig.Endpoints {
			configured := channel.ConnectionConfig.Endpoints[index]
			if configured.ID != endpointID {
				continue
			}
			input.Endpoint = &endpointFingerprint{
				ID:            configured.ID,
				Protocol:      configured.Protocol,
				URL:           configured.URL,
				URLMode:       configured.URLMode,
				Auth:          configured.Auth,
				Compatibility: configured.Compatibility,
				ForwardMode:   configured.ForwardMode,
				Transport:     configured.EffectiveTransport(),
				TaskAPIURL:    configured.TaskAPIURL,
				Headers:       sortedHeaders(configured.Headers),
			}
			break
		}
	}
	if protocol != "" && input.Endpoint == nil {
		input.UpstreamProtocols = append(input.UpstreamProtocols, "probe-protocol:"+protocol)
		sort.Strings(input.UpstreamProtocols)
	}

	encoded, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func capabilityBaseURLs(values []BaseUrl) []baseURLFingerprint {
	if len(values) == 0 {
		return nil
	}
	copied := make([]baseURLFingerprint, 0, len(values))
	for _, value := range values {
		copied = append(copied, baseURLFingerprint{
			URL:        value.URL,
			SuffixMode: value.SuffixMode,
			Protocol:   value.Protocol,
		})
	}
	return copied
}

func sortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	copied := append([]string(nil), values...)
	sort.Strings(copied)
	return copied
}

func sortedHeaders(headers []CustomHeader) []CustomHeader {
	if len(headers) == 0 {
		return nil
	}
	copied := append([]CustomHeader(nil), headers...)
	sort.Slice(copied, func(i, j int) bool {
		if copied[i].HeaderKey == copied[j].HeaderKey {
			return copied[i].HeaderValue < copied[j].HeaderValue
		}
		return copied[i].HeaderKey < copied[j].HeaderKey
	})
	return copied
}
