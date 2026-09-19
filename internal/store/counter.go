package store

import (
	"context"
	"time"
)

// counter.go 提供 KVStore 的原子计数器原语（Redis INCR/DECR）。
//
// 渠道并发信号量（internal/relay/balancer）需要「check-and-increment」原子语义：
// 先 INCR 拿到新计数，再在客户端比较是否超限——INCR 本身原子，比较在其返回值上
// 进行，多个并发占用者不会互相覆盖（对比 GET+SET 竞态：两个请求都读到 4、都写 5）。
// TTL 刷新与 INCR 放在同一 TxPipeline（MULTI/EXEC）内原子执行：每次占用都续期，
// 持续流量下计数器永不过期；占用停止（部署重启/崩溃/请求挂死）后 ttl 内自愈。

func (r *redisKV) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	full := withPrefix(key)
	if ttl <= 0 {
		return r.c.Incr(ctx, full).Result()
	}
	// TxPipeline = MULTI/EXEC：INCR 与 EXPIRE 原子提交，进程在两条命令之间崩溃
	// 也不会留下无 TTL 的计数器（那样泄漏的名额永远不会自愈）。
	pipe := r.c.TxPipeline()
	incrCmd := pipe.Incr(ctx, full)
	pipe.Expire(ctx, full, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	// incrCmd.Val() 在 Exec 后生效：原子读到的正是本次 INCR 的新计数，
	// 不需要（也不能）再 GET 一次——那会读到并发请求刚写入的值。
	return incrCmd.Val(), nil
}

func (r *redisKV) Decr(ctx context.Context, key string) (int64, error) {
	return r.c.Decr(ctx, withPrefix(key)).Result()
}
