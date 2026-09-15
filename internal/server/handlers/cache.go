package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/cacheconfig"
	"github.com/lingyuins/octopus/internal/server/resp"
	"github.com/lingyuins/octopus/internal/store"
)

func getCacheConfig(context *gin.Context) {
	configuration := conf.GetCacheConfig()
	summary, hasPassword, err := cacheconfig.Describe(configuration.Redis)
	if err != nil {
		resp.Error(context, http.StatusBadRequest, err.Error())
		return
	}
	status := store.InspectBackend(context.Request.Context(), configuration)
	resp.Success(context, model.CacheConfig{
		Type: configuration.Type, Redis: summary,
		HasSavedConnection: strings.TrimSpace(configuration.Redis.Addr) != "",
		HasPassword:        hasPassword, ConfigSource: conf.CacheConfigSource(),
		RuntimeBackend: status.Backend, RuntimeHealthy: status.Healthy,
		RuntimeTLS: status.TLS, RestartNeeded: status.RestartNeeded,
		Reconnecting: status.Reconnecting,
	})
}

// previewCacheConnection validates and redacts a candidate without dialing it.
// Parsing, testing, saving and startup all use the same Redis options builder.
func previewCacheConnection(context *gin.Context) {
	configuration, valid := prepareCacheConnection(context)
	if !valid {
		return
	}
	summary, _, err := cacheconfig.Describe(configuration)
	if err != nil {
		resp.Error(context, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(context, summary)
}

func testCacheConnection(context *gin.Context) {
	configuration, valid := prepareCacheConnection(context)
	if !valid {
		return
	}
	if err := store.TestConnection(configuration); err != nil {
		resp.Error(context, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(context, true)
}

func saveCacheConfig(context *gin.Context) {
	request, valid := readCacheConfigRequest(context)
	if !valid {
		return
	}
	configuration, err := cacheconfig.Save(context.Request.Context(), request)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, cacheconfig.ErrEnvironmentManaged) || errors.Is(err, cacheconfig.ErrServiceIdentity) {
			status = http.StatusConflict
		}
		resp.Error(context, status, err.Error())
		return
	}
	resp.Success(context, model.CacheConfigResult{
		Type: configuration.Type, RestartNeeded: store.RequiresRestart(configuration),
	})
}

func prepareCacheConnection(context *gin.Context) (conf.RedisConfig, bool) {
	request, valid := readCacheConfigRequest(context)
	if !valid {
		return conf.RedisConfig{}, false
	}
	if request.Type != "redis" {
		resp.Error(context, http.StatusBadRequest, "cache type is not redis")
		return conf.RedisConfig{}, false
	}
	configuration, err := cacheconfig.Prepare(request, conf.GetCacheConfig())
	if err != nil {
		resp.Error(context, http.StatusBadRequest, err.Error())
		return conf.RedisConfig{}, false
	}
	return configuration.Redis, true
}

func readCacheConfigRequest(context *gin.Context) (model.CacheConfigRequest, bool) {
	var request model.CacheConfigRequest
	context.Request.Body = http.MaxBytesReader(context.Writer, context.Request.Body, 16<<10)
	if err := context.ShouldBindJSON(&request); err != nil {
		resp.Error(context, http.StatusBadRequest, resp.ErrInvalidJSON)
		return request, false
	}
	return request, true
}
