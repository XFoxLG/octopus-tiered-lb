package op

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/lingyuins/octopus/internal/db"
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
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if len(cacheInitFuncs) == 0 {
		return nil
	}

	// Stage 1: setting cache (gates log level and is read by later caches).
	if err := runCacheInitStage(ctx, 0, cacheInitFuncs[0]); err != nil {
		return err
	}

	// Stage 2: remaining independent caches in parallel.
	rest := cacheInitFuncs[1:]
	if len(rest) == 0 {
		return nil
	}
	g, gctx := errgroup.WithContext(ctx)
	parallelism := 2
	if conn := db.GetDB(); conn != nil {
		if sqlDB, err := conn.DB(); err == nil {
			if limit := sqlDB.Stats().MaxOpenConnections; limit > 0 && limit < parallelism {
				parallelism = limit
			}
		}
	}
	g.SetLimit(parallelism)
	for index, fn := range rest {
		fn := fn
		index := index
		g.Go(func() error {
			return runCacheInitStage(gctx, index+1, fn)
		})
	}
	return g.Wait()
}

func runCacheInitStage(ctx context.Context, stage int, fn CacheInitFunc) error {
	for attempt := 1; attempt <= 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		stageCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		started := time.Now()
		before := cacheConnectionStats()
		err := fn(stageCtx)
		cancel()
		after := cacheConnectionStats()
		classification := "success"
		if err != nil {
			classification = "permanent"
			if cacheInitRetryable(err) {
				classification = "transient"
			}
			if ctx.Err() != nil {
				classification = "budget_exhausted"
			}
		}
		log.Infof("cache init stage=%d attempt=%d duration=%s result=%s connection_waits=%d connection_wait=%s", stage, attempt, time.Since(started), classification, after.WaitCount-before.WaitCount, after.WaitDuration-before.WaitDuration)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt == 3 || !cacheInitRetryable(err) {
			return err
		}
		log.Warnf("cache init retry: stage=%d attempt=%d duration=%s error=%v", stage, attempt, time.Since(started), err)
		timer := time.NewTimer(time.Duration(attempt) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func cacheConnectionStats() sql.DBStats {
	if connection := db.GetDB(); connection != nil {
		if pool, err := connection.DB(); err == nil {
			return pool.Stats()
		}
	}
	return sql.DBStats{}
}

func cacheInitRetryable(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return true
	}
	var sqlError interface{ SQLState() string }
	if errors.As(err, &sqlError) {
		code := sqlError.SQLState()
		return len(code) >= 2 && code[:2] == "08" || code == "53300" || code == "57P03"
	}
	return false
}

// SaveCache persists all registered sub-package caches.
func SaveCache() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var errs []error
	for _, fn := range cacheSaveFuncs {
		if err := fn(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// init registers cache init and save functions in explicit dependency order.
// This avoids init() file-order non-determinism by centralizing all registrations.
func init() {
	// ── Cache init order: setting → channelGroup → channel → group → apikey → llm → stats ──
	RegisterCacheInit(func(ctx context.Context) error {
		if err := settingRefreshCache(ctx); err != nil {
			return fmt.Errorf("setting refresh cache error: %w", err)
		}
		// 设置加载后应用日志级别
		if level, err := setting.GetString(model.SettingKeyLogLevel); err == nil {
			log.SetLevel(level)
		}
		return nil
	})
	RegisterCacheInit(func(ctx context.Context) error {
		if err := channelGroupRefreshCache(ctx); err != nil {
			return fmt.Errorf("channel group refresh cache error: %w", err)
		}
		return nil
	})
	RegisterCacheInit(func(ctx context.Context) error {
		if err := channelRefreshCache(ctx); err != nil {
			return fmt.Errorf("channel refresh cache error: %w", err)
		}
		return nil
	})
	RegisterCacheInit(func(ctx context.Context) error {
		if err := groupRefreshCache(ctx); err != nil {
			return fmt.Errorf("group refresh cache error: %w", err)
		}
		return nil
	})
	RegisterCacheInit(func(ctx context.Context) error {
		if err := apiKeyRefreshCache(ctx); err != nil {
			return fmt.Errorf("api key refresh cache error: %w", err)
		}
		return nil
	})
	RegisterCacheInit(func(ctx context.Context) error {
		if err := llmRefreshCache(ctx); err != nil {
			return fmt.Errorf("llm refresh cache error: %w", err)
		}
		return nil
	})
	RegisterCacheInit(func(ctx context.Context) error {
		if err := statsRefreshCache(ctx); err != nil {
			return fmt.Errorf("stats refresh cache error: %w", err)
		}
		return nil
	})
	RegisterCacheInit(func(ctx context.Context) error {
		if err := modelmapping.InitCache(ctx); err != nil {
			return fmt.Errorf("model mapping init cache error: %w", err)
		}
		return nil
	})
	RegisterCacheInit(func(ctx context.Context) error {
		if err := proxyConfigurationRefreshCache(ctx); err != nil {
			return fmt.Errorf("proxy configuration refresh cache error: %w", err)
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
