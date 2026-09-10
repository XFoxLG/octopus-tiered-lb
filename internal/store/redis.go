// Package store provides optional Redis runtime state with a memory baseline.
// Entity mirrors remain local and durable statistics use SQL snapshots.
// Selecting Redis is not a guarantee of health or cross-instance accounting.
package store

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/utils/log"
	"github.com/redis/go-redis/v9"
)

// keyPrefix 统一所有 Redis key 前缀，避免与其他服务冲突。
const keyPrefix = "octopus:"

var (
	client              *redis.Client
	enabled             bool
	mu                  sync.RWMutex
	reconnectCancel     context.CancelFunc
	reconnecting        bool
	activeConfiguration conf.RedisConfig
)

// Init 初始化 Redis 连接并 ping 验证。cfg.Addr 为空或连接失败时返回错误。
// 成功后 Enabled() 返回 true，Get() 返回可用 client。
//
// 失败时（Redis 不可达）不阻塞调用方降级决策：返回错误后调用方可选择降级到
// 内存后端并启动 StartReconnect 后台重连（issue #135）。
func Init(cfg conf.RedisConfig) error {
	c, err := newClient(cfg)
	if err != nil {
		return err
	}
	// 启动期先打印「正在连接」，避免 Ping 超时窗口内日志空白（issue #135）。
	log.Infof("redis backend connecting: %s db=%d (dial_timeout=%s, read_timeout=%s)",
		SafeRedisAddress(cfg.Addr), cfg.DB, durationStr(cfg.DialTimeout), durationStr(cfg.ReadTimeout))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		log.Warnf("redis init failed, falling back to memory backend: %v", err)
		return fmt.Errorf("redis ping %s: %w", SafeRedisAddress(cfg.Addr), err)
	}

	switchToRedis(c)
	mu.Lock()
	activeConfiguration = cfg
	mu.Unlock()
	log.Infof("redis backend connected: %s db=%d", SafeRedisAddress(cfg.Addr), cfg.DB)
	return nil
}

// TestConnection 验证给定 Redis 配置的连通性，不改变全局状态（不调用 Init、
// 不设置 client）。供设置页「测试连接」按钮调用——用户可在保存前先验证地址/
// 密码是否正确。成功后立即关闭临时连接。
func TestConnection(cfg conf.RedisConfig) error {
	if cfg.Addr == "" {
		return fmt.Errorf("redis addr is empty")
	}
	c, err := newClient(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping %s: %w", SafeRedisAddress(cfg.Addr), err)
	}
	return nil
}

// Get 返回 Redis client；未启用时返回 nil。调用方需自行判空。
func Get() *redis.Client {
	mu.RLock()
	defer mu.RUnlock()
	return client
}

// Enabled 报告 Redis 后端是否已启用。
func Enabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return enabled
}

// Close 关闭 Redis 连接。
func Close() error {
	mu.Lock()
	defer mu.Unlock()
	if reconnectCancel != nil {
		reconnectCancel()
		reconnectCancel = nil
	}
	reconnecting = false
	if client == nil {
		return nil
	}
	err := client.Close()
	client = nil
	enabled = false
	return err
}

// reconnectBackoff 计算第 attempt 次（从 1 开始）重试前的等待时长：初始 2s，
// 指数增长（×2），上限 30s。
func reconnectBackoff(attempt int) time.Duration {
	d := 2 * time.Second
	for i := 1; i < attempt && d < 30*time.Second; i++ {
		d *= 2
	}
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// StartReconnect 启动后台 goroutine 退避重试连接 Redis。供 cmd/start.go 在
// Init 失败后调用：服务以内存后端继续启动并监听端口，后台重连成功后热切换到
// Redis 后端（issue #135）。
//
// 成功后自动调用 switchToRedis 切换后端，并执行 onConnect 回调（如注册
// shutdown.Close）。回调在重连 goroutine 中同步执行，可为 nil。
//
// 已启用 Redis（Enabled()==true）时返回 ErrAlreadyConnected，不重复启动。
func StartReconnect(cfg conf.RedisConfig, onConnect func()) error {
	if _, err := BuildRedisOptions(cfg); err != nil {
		return err
	}
	mu.Lock()
	if enabled || reconnecting {
		mu.Unlock()
		return fmt.Errorf("redis backend already connected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	reconnectCancel = cancel
	reconnecting = true
	activeConfiguration = cfg
	mu.Unlock()
	log.Warnf("redis will retry in background (initial backoff=2s, max=30s); service continues with memory backend")
	go reconnectLoop(ctx, cfg, onConnect)
	return nil
}

// reconnectLoop 是 StartReconnect 的后台循环。每次重试用独立 client + 3s ping
// 超时；失败则按指数退避等待后重试，成功则切换后端并退出循环。
func reconnectLoop(parentContext context.Context, cfg conf.RedisConfig, onConnect func()) {
	const pingTimeout = 3 * time.Second
	for attempt := 1; ; attempt++ {
		// 先等待退避再重试（首次也等待，给 Redis 一点恢复时间）。
		backoff := reconnectBackoff(attempt)
		timer := time.NewTimer(backoff)
		select {
		case <-parentContext.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		c, err := newClient(cfg)
		if err != nil {
			mu.Lock()
			reconnecting = false
			mu.Unlock()
			return
		}
		ctx, cancel := context.WithTimeout(parentContext, pingTimeout)
		err = c.Ping(ctx).Err()
		cancel()
		if err != nil {
			_ = c.Close()
			next := reconnectBackoff(attempt + 1)
			log.Warnf("redis reconnect attempt %d failed: %v (next in %s)", attempt, err, next)
			continue
		}
		mu.Lock()
		if parentContext.Err() != nil {
			mu.Unlock()
			_ = c.Close()
			return
		}

		switchToRedisLocked(c)
		activeConfiguration = cfg
		reconnecting = false
		mu.Unlock()
		log.Infof("redis backend reconnected after %d attempts: %s db=%d", attempt, SafeRedisAddress(cfg.Addr), cfg.DB)
		log.Infof("runtime state backend switched to redis; statistics continue using SQL persistence")
		if onConnect != nil {
			onConnect()
		}
		return
	}
}

// durationStr 返回 Duration 的可读字符串，0 时返回 "default"。
func durationStr(d time.Duration) string {
	if d <= 0 {
		return "default"
	}
	return d.String()
}

// withPrefix 给 key 加上统一前缀。
func withPrefix(key string) string {
	return keyPrefix + key
}
