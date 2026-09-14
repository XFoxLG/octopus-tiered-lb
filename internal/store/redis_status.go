package store

import (
	"context"
	"time"

	"github.com/lingyuins/octopus/internal/conf"
)

type BackendStatus struct {
	Backend       string
	Healthy       bool
	TLS           bool
	RestartNeeded bool
	Reconnecting  bool
}

func InspectBackend(ctx context.Context, configured conf.Cache) BackendStatus {
	mu.RLock()
	currentClient := client
	usingRedis := enabled
	connectionPending := reconnecting
	restartNeeded := requiresRestartLocked(configured)
	mu.RUnlock()

	status := BackendStatus{Backend: "memory", Healthy: true, Reconnecting: connectionPending, RestartNeeded: restartNeeded}
	if !usingRedis || currentClient == nil {
		return status
	}
	status.Backend = "redis"
	status.TLS = currentClient.Options().TLSConfig != nil
	probeContext, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	status.Healthy = currentClient.Ping(probeContext).Err() == nil
	return status
}

// RequiresRestart compares desired and active configuration without performing
// network I/O. Saving configuration must not depend on Redis being available.
func RequiresRestart(configured conf.Cache) bool {
	mu.RLock()
	defer mu.RUnlock()
	return requiresRestartLocked(configured)
}

func requiresRestartLocked(configured conf.Cache) bool {
	if reconnecting || (enabled && client != nil) {
		return configured.Type != "redis" || activeConfiguration != configured.Redis
	}
	return configured.Type == "redis"
}
