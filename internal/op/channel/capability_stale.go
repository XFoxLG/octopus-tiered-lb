package channel

import (
	"reflect"

	"github.com/lingyuins/octopus/internal/model"
)

// capabilityStaleScopeForUpdate derives which evidence a channel edit
// invalidates. Only settings that change what reaches the upstream count:
// URL modes and addresses, authentication, headers, protocols, rewrites and
// proxy egress. Renames, group moves, timeouts, and routing weights keep
// evidence valid.
func capabilityStaleScopeForUpdate(current *model.Channel, req *model.ChannelUpdateRequest) CapabilityStaleScope {
	if current == nil || req == nil {
		return CapabilityStaleScope{}
	}
	scope := CapabilityStaleScope{Reason: "configuration_changed"}

	if req.ConnectionConfig != nil {
		if current.ConnectionConfig == nil {
			// Switching from the legacy BaseUrls generation to configured
			// endpoints changes the meaning of every existing scope.
			scope.All = true
		} else {
			scope.EndpointIDs = changedEndpointIDs(current.ConnectionConfig.Endpoints, req.ConnectionConfig.Endpoints)
		}
	} else if current.ConnectionConfig == nil &&
		(req.BaseUrls != nil || req.UpstreamProtocols != nil || req.OutboundFormatOverride != nil) {
		// Without a ConnectionConfig these fields describe the legacy
		// channel-wide scope only. They do not invalidate a configured
		// endpoint scope that no longer belongs to this generation.
		scope.Legacy = true
	}
	if req.CustomHeader != nil && !reflect.DeepEqual(*req.CustomHeader, current.CustomHeader) {
		// Channel headers are applied to every endpoint. Re-sending the
		// unchanged list on an ordinary save must not invalidate evidence.
		scope.All = true
	}
	if req.Type != nil && *req.Type != current.Type {
		scope.All = true
	}
	if req.ProxyMode != nil || req.ProxyConfigID != nil || req.Proxy != nil || req.ChannelProxy != nil {
		// Proxy routing changes the egress used by every endpoint.
		scope.All = true
	}
	if req.RequestRewrite != nil && !reflect.DeepEqual(req.RequestRewrite, current.RequestRewrite) {
		// The rewritten body is shared by every endpoint.
		scope.All = true
	}
	if req.ParamOverride != nil && !stringPointersEqual(req.ParamOverride, current.ParamOverride) {
		// Parameter overrides change the outbound body seen by every endpoint.
		scope.All = true
	}

	for _, key := range req.KeysToUpdate {
		currentKey, ok := findChannelKey(current.Keys, key.ID)
		if !ok {
			continue
		}
		changed := false
		if key.ChannelKey != nil && *key.ChannelKey != currentKey.ChannelKey {
			changed = true
		}
		if key.Enabled != nil && *key.Enabled != currentKey.Enabled {
			changed = true
		}
		if key.SupportedModels != nil && *key.SupportedModels != currentKey.SupportedModels {
			changed = true
		}
		if changed {
			scope.KeyIDs = append(scope.KeyIDs, key.ID)
		}
	}
	if len(req.KeysToDelete) > 0 {
		scope.KeyIDs = append(scope.KeyIDs, req.KeysToDelete...)
	}
	return scope
}

func changedEndpointIDs(previous, next []model.ChannelEndpoint) []string {
	nextByID := make(map[string]model.ChannelEndpoint, len(next))
	for _, endpoint := range next {
		nextByID[endpoint.ID] = endpoint
	}

	changed := make([]string, 0)
	for _, oldEndpoint := range previous {
		newEndpoint, exists := nextByID[oldEndpoint.ID]
		if !exists || !reflect.DeepEqual(oldEndpoint, newEndpoint) {
			changed = append(changed, oldEndpoint.ID)
		}
		delete(nextByID, oldEndpoint.ID)
	}
	// Added endpoints have no prior evidence, so they require no invalidation.
	return changed
}

func findChannelKey(keys []model.ChannelKey, keyID int) (model.ChannelKey, bool) {
	for _, key := range keys {
		if key.ID == keyID {
			return key, true
		}
	}
	return model.ChannelKey{}, false
}

func stringPointersEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
