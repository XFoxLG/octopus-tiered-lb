package db

import (
	"context"
	"fmt"
	"time"

	"github.com/lingyuins/octopus/internal/utils/log"
	"gorm.io/gorm"
)

// migrationAdvisoryLockKey 是 schema 迁移的全局互斥键。任意取值都可以，只要所有
// 实例一致；这里用一个固定字面量而不是运行时哈希，避免不同版本算出不同的键从而
// 互相看不见对方的锁。
const migrationAdvisoryLockKey int64 = 7213705478792345

const (
	migrationLockPollInterval = 500 * time.Millisecond
	// 超时后放行而非失败：迁移本身是幂等的（AutoMigrate 与各 Migration 都做存在性
	// 检查），把启动卡死比偶发并发 DDL 更糟。
	migrationLockMaxWait = 2 * time.Minute
)

// acquirePostgresMigrationLock 在 Postgres 上取得迁移互斥锁，返回释放函数。
//
// 背景：多个实例共用一个 Postgres 时（两个 Render 服务同库部署），同时启动会各自
// 执行 AutoMigrate 与全部注册迁移。并发 DDL 在 Postgres 上会报 "column already
// exists" / "relation already exists"，或在 ALTER TABLE 上互相等锁。
//
// 实现要点：advisory lock 是会话级的，而 GORM 用连接池——若在池上加锁，释放时可能
// 拿到另一条连接，锁会一直留到会话结束。这里显式借一条专用连接持锁，迁移 DDL 仍走
// 连接池的其它连接，锁在同一会话上加与解。
//
// 非 Postgres 返回空操作：SQLite 单连接串行，MySQL 走 ALTER TABLE 且本项目不共库。
func acquirePostgresMigrationLock(conn *gorm.DB) (func(), error) {
	if conn == nil || conn.Dialector == nil || conn.Dialector.Name() != "postgres" {
		return func() {}, nil
	}

	sqlDatabase, err := conn.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql database for migration lock: %w", err)
	}

	ctx := context.Background()
	lockConnection, err := sqlDatabase.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("reserve connection for migration lock: %w", err)
	}

	releaseConnection := func() {
		if closeErr := lockConnection.Close(); closeErr != nil {
			log.Warnf("failed to return migration lock connection to the pool: %v", closeErr)
		}
	}

	deadline := time.Now().Add(migrationLockMaxWait)
	for {
		var acquired bool
		if err := lockConnection.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", migrationAdvisoryLockKey).Scan(&acquired); err != nil {
			releaseConnection()
			return nil, fmt.Errorf("acquire migration advisory lock: %w", err)
		}
		if acquired {
			break
		}
		if time.Now().After(deadline) {
			// 走到这里说明另一个实例迁移超时未完成。继续执行而不是退出：迁移幂等，
			// 且拒绝启动会让服务彻底不可用。
			log.Warnf("migration advisory lock still held after %v; proceeding without it", migrationLockMaxWait)
			releaseConnection()
			return func() {}, nil
		}
		log.Infof("another instance is migrating the shared database; waiting for the advisory lock")
		time.Sleep(migrationLockPollInterval)
	}

	return func() {
		if _, err := lockConnection.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", migrationAdvisoryLockKey); err != nil {
			log.Warnf("failed to release migration advisory lock: %v", err)
		}
		releaseConnection()
	}, nil
}
