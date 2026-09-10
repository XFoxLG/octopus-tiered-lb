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
	currentConfiguration := activeConfiguration
	mu.RUnlock()

	status := BackendStatus{Backend: "memory", Healthy: true, Reconnecting: connectionPending}
	if !usingRedis || currentClient == nil {
		status.RestartNeeded = configured.Type == "redis" && !connectionPending
		if connectionPending {
			status.RestartNeeded = configured.Type != "redis" || currentConfiguration != configured.Redis
		}
		return status
	}
	status.Backend = "redis"
	status.TLS = currentClient.Options().TLSConfig != nil
	status.RestartNeeded = configured.Type != "redis" || currentConfiguration != configured.Redis
	probeContext, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	status.Healthy = currentClient.Ping(probeContext).Err() == nil
	return status
}
