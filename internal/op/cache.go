package op

import (
	"context"
	"fmt"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/modelmapping"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/utils/log"
	"golang.org/x/sync/errgroup"
)

// CacheInitFunc is a function that initializes a sub-package's in-memory cache.
type CacheInitFunc func(context.Context) error

// CacheSaveFunc is a function that persists a sub-package's in-memory cache.
type CacheSaveFunc func(context.Context) error

var cacheInitFuncs []CacheInitFunc
var cacheSaveFuncs []CacheSaveFunc

// RegisterCacheInit registers a cache initialization function.
// Functions are called in registration order during InitCache().
func RegisterCacheInit(fn CacheInitFunc) {
	cacheInitFuncs = append(cacheInitFuncs, fn)
}

// RegisterCacheSave registers a cache save function.
// Functions are called in registration order during SaveCache().
func RegisterCacheSave(fn CacheSaveFunc) {
	cacheSaveFuncs = append(cacheSaveFuncs, fn)
}

// InitCache initializes all registered sub-package caches.
//
// The first registered function (settingRefreshCache, see init() below) is run
// first and on its own: other caches read the setting cache during load (stats
// reads the timezone offset) and the log level is applied from settings right
// after. The remaining caches only read their own DB table during refresh and
// are mutually independent, so they are loaded concurrently. This keeps startup
// wall-clock time close to the slowest single cache rather than the sum of all
// of them, and avoids one large table blocking the shared init timeout.
func InitCache() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if len(cacheInitFuncs) == 0 {
		return nil
	}

	// Stage 1: setting cache (gates log level and is read by later caches).
	if err := cacheInitFuncs[0](ctx); err != nil {
		return err
	}

	// Stage 2: remaining independent caches in parallel.
	rest := cacheInitFuncs[1:]
	if len(rest) == 0 {
		return nil
	}
	g, gctx := errgroup.WithContext(ctx)
	for _, fn := range rest {
		fn := fn
		g.Go(func() error {
			return fn(gctx)
		})
	}
	return g.Wait()
}

// SaveCache persists all registered sub-package caches.
func SaveCache() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, fn := range cacheSaveFuncs {
		if err := fn(ctx); err != nil {
			return err
		}
	}
	return nil
}

// init registers cache init and save functions in explicit dependency order.
// This avoids init() file-order non-determinism by centralizing all registrations.
func init() {
	// ── Cache init order: setting → channelGroup → channel → group → apikey → llm → stats ──
	RegisterCacheInit(func(ctx context.Context) error {
		if err := settingRefreshCache(ctx); err != nil {
			return fmt.Errorf("setting refresh cache error: %v", err)
		}
		// 设置加载后应用日志级别
		if level, err := setting.GetString(model.SettingKeyLogLevel); err == nil {
			log.SetLevel(level)
		}
		return nil
	})
	RegisterCacheInit(func(ctx context.Context) error {
		if err := channelGroupRefreshCache(ctx); err != nil {
			return fmt.Errorf("channel group refresh cache error: %v", err)
		}
		return nil
	})
	RegisterCacheInit(func(ctx context.Context) error {
		if err := channelRefreshCache(ctx); err != nil {
			return fmt.Errorf("channel refresh cache error: %v", err)
		}
		return nil
	})
	RegisterCacheInit(func(ctx context.Context) error {
		if err := groupRefreshCache(ctx); err != nil {
			return fmt.Errorf("group refresh cache error: %v", err)
		}
		return nil
	})
	RegisterCacheInit(func(ctx context.Context) error {
		if err := apiKeyRefreshCache(ctx); err != nil {
			return fmt.Errorf("api key refresh cache error: %v", err)
		}
		return nil
	})
	RegisterCacheInit(func(ctx context.Context) error {
		if err := llmRefreshCache(ctx); err != nil {
			return fmt.Errorf("llm refresh cache error: %v", err)
		}
		return nil
	})
	RegisterCacheInit(func(ctx context.Context) error {
		if err := statsRefreshCache(ctx); err != nil {
			return fmt.Errorf("stats refresh cache error: %v", err)
		}
		return nil
	})
	RegisterCacheInit(func(ctx context.Context) error {
		if err := modelmapping.InitCache(ctx); err != nil {
			return fmt.Errorf("model mapping init cache error: %v", err)
		}
		return nil
	})
	RegisterCacheInit(func(ctx context.Context) error {
		if err := proxyConfigurationRefreshCache(ctx); err != nil {
			return fmt.Errorf("proxy configuration refresh cache error: %v", err)
		}
		return nil
	})

	// ── Cache save order ──
	RegisterCacheSave(func(ctx context.Context) error {
		if err := StatsSaveDB(ctx); err != nil {
			return err
		}
		return nil
	})
	RegisterCacheSave(func(ctx context.Context) error {
		if err := ChannelKeySaveDB(ctx); err != nil {
			return err
		}
		return nil
	})
	RegisterCacheSave(func(ctx context.Context) error {
		if err := RelayLogSaveDBTask(ctx); err != nil {
			return err
		}
		return nil
	})
}
