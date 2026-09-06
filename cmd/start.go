package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/op"
	"github.com/lingyuins/octopus/internal/relay/balancer"
	"github.com/lingyuins/octopus/internal/server"
	"github.com/lingyuins/octopus/internal/store"
	"github.com/lingyuins/octopus/internal/task"
	"github.com/lingyuins/octopus/internal/utils/crypto"
	"github.com/lingyuins/octopus/internal/utils/log"
	"github.com/lingyuins/octopus/internal/utils/shutdown"
	"github.com/lingyuins/octopus/internal/utils/telemetry"
	"github.com/spf13/cobra"
)

var cfgFile string

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start " + conf.APP_NAME,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		conf.PrintBanner()
		if err := conf.Load(cfgFile); err != nil {
			return err
		}
		log.SetLevel(conf.AppConfig.Log.Level)
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStart()
	},
}

func runStart() error {
	shutdown.Init(log.Logger)

	if key := conf.AppConfig.Security.EncryptionKey; key != "" {
		crypto.Init(key)
	} else if secret := conf.AppConfig.Auth.JWTSecret; secret != "" && !conf.IsEphemeralJWTSecret() {
		crypto.Init(secret)
	} else {
		// Refuse to derive an AES key from an ephemeral JWT secret: the key
		// would change every restart, making previously encrypted fields
		// (access tokens, passwords, channel keys) permanently unrecoverable.
		log.Errorf("security.encryption_key (or a persistent auth.jwt_secret) is required; refusing to start to prevent data loss on restart")
		return fmt.Errorf("encryption key not configured: set OCTOPUS_SECURITY_ENCRYPTION_KEY or security.encryption_key")
	}

	// SQLite per-connection PRAGMA（cache_size / mmap_size）从 config.json 注入。
	// 默认禁用 mmap、cache 约 20MB，面向低内存环境（见 issue #97）。非 SQLite 类型时被忽略。
	sqliteOpts := db.SQLiteOptions{
		CacheSize: conf.AppConfig.Database.SQLite.CacheSize,
		MMapSize:  conf.AppConfig.Database.SQLite.MMapSize,
	}
	if err := db.InitDBWithOptions(conf.AppConfig.Database.Type, conf.AppConfig.Database.Path, conf.IsDebug(), sqliteOpts); err != nil {
		return fmt.Errorf("database init error: %w", err)
	}
	// 独立日志库（仅承载 relay_logs）。log_type/log_path 留空时回落到主库，
	// 行为与旧版一致。必须在主库 InitDB 之后调用。日志库为 SQLite 时复用同一组 PRAGMA。
	if err := db.InitLogDBWithOptions(conf.AppConfig.Database.LogType, conf.AppConfig.Database.LogPath, conf.IsDebug(), sqliteOpts); err != nil {
		return fmt.Errorf("log database init error: %w", err)
	}
	shutdown.Register(db.Close)

	// 可选 Redis 缓存与状态存储后端（issue #123）。
	// cache.type="redis" 且 redis.addr 非空时启用，将统计/运行时状态/限流冷却/
	// 失败提示/频道延迟等卸载到 Redis，支持多实例共享与低内存主机。
	// 未配置时所有 store 后端保持内存实现，行为与旧版完全一致（零破坏性）。
	// 必须在 op.InitCache 之前初始化--RefreshCache/LoadRuntimeState 会从 Redis 叠加增量。
	//
	// 降级策略（issue #135）：Redis 启动期不可达时不再硬失败退出，而是降级到内存
	// 后端继续启动（服务立即监听端口），并启动后台退避重连。重连成功后热切换到
	// Redis 后端；重连期间累积的内存统计不会回填（符合 issue #123 降级语义）。
	if conf.AppConfig.Cache.Type == "redis" && conf.AppConfig.Cache.Redis.Addr != "" {
		if err := store.Init(conf.AppConfig.Cache.Redis); err != nil {
			log.Warnf("redis unavailable, starting with memory backend: %v", err)
			if rerr := store.StartReconnect(conf.AppConfig.Cache.Redis, func() {
				shutdown.Register(store.Close)
			}); rerr != nil {
				log.Warnf("redis background reconnect not started: %v", rerr)
			}
		} else {
			shutdown.Register(store.Close)
			log.Infof("redis cache backend enabled: %s", conf.AppConfig.Cache.Redis.Addr)
		}
	}

	startupTaskCtx, startupTaskCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if interruptedCount, err := op.AIRouteTaskMarkActiveInterrupted(startupTaskCtx, op.DefaultAIRouteTaskInterruptedMessage); err != nil {
		log.Warnf("ai route task recovery failed: %v", err)
	} else if interruptedCount > 0 {
		log.Warnf("marked %d stale ai route task(s) as interrupted on startup", interruptedCount)
	}
	startupTaskCancel()

	if err := op.InitCache(); err != nil {
		shutdown.Shutdown()
		return fmt.Errorf("cache init error: %w", err)
	}

	telemetry.Global().StartBackground()

	restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := balancer.LoadRuntimeState(restoreCtx); err != nil {
		log.Warnf("balancer runtime state load error: %v", err)
	}
	restoreCancel()

	if err := op.UserInit(); err != nil {
		shutdown.Shutdown()
		return fmt.Errorf("user init error: %w", err)
	}
	if err := op.EnsureDevBootstrapData(context.Background()); err != nil {
		shutdown.Shutdown()
		return fmt.Errorf("dev bootstrap init error: %w", err)
	}

	if err := server.Start(); err != nil {
		shutdown.Shutdown()
		return fmt.Errorf("server start error: %w", err)
	}

	loc := time.Now().Location()
	log.Infof("server timezone: %s (UTC offset: %s)", loc.String(), time.Now().Format("-07:00"))
	log.Infof("server local time: %s", time.Now().Format(time.RFC3339))
	log.Infof("server utc time:   %s", time.Now().UTC().Format(time.RFC3339))

	shutdown.Register(server.Close)
	shutdown.Register(func() error {
		telemetry.Global().StopBackground()
		return nil
	})
	shutdown.Register(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return balancer.SaveRuntimeState(ctx)
	})
	shutdown.Register(func() error {
		task.Shutdown()
		db.StopSerialWriter()
		return nil
	})
	shutdown.Register(op.SaveCache)
	shutdown.Register(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		interruptedCount, err := op.AIRouteTaskMarkActiveInterrupted(ctx, op.DefaultAIRouteTaskInterruptedMessage)
		if err != nil {
			return err
		}
		if interruptedCount > 0 {
			log.Warnf("marked %d active ai route task(s) as interrupted during shutdown", interruptedCount)
		}
		return nil
	})

	task.Init()
	go task.RUN()
	shutdown.Listen()
	return nil
}

func init() {
	startCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ./data/config.json)")
	rootCmd.AddCommand(startCmd)
}
