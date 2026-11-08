package store

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisKV 是 KVStore 的 Redis 实现。
// 失败提示缓存 / key 冷却 / 分组探测进度都是 string key -> 小结构体 + TTL，
// 天然映射到 Redis SET ... EX / GET / DEL。惰性过期由 Redis TTL 保证，无需主动 purge。
type redisKV struct {
	c *redis.Client
}

func newRedisKV(c *redis.Client) *redisKV { return &redisKV{c: c} }

var compareKVScript = redis.NewScript(`
if KEYS[2] ~= '' and redis.call('EXISTS', KEYS[2]) == 1 then return 0 end
local current = redis.call('GET', KEYS[1])
if ARGV[1] == 'absent' then
  if current then return 0 end
elseif not current or current ~= ARGV[2] then return 0 end
if ARGV[3] == 'delete' then redis.call('DEL', KEYS[1])
elseif tonumber(ARGV[5]) > 0 then redis.call('SET', KEYS[1], ARGV[4], 'PX', ARGV[5])
else redis.call('SET', KEYS[1], ARGV[4]) end
return 1
`)

func (r *redisKV) CompareAndSwap(ctx context.Context, key string, old, next []byte, ttl time.Duration, absentKey string) (bool, error) {
	match, action := "value", "set"
	if old == nil {
		match = "absent"
	}
	if next == nil {
		action = "delete"
	}
	if absentKey != "" {
		absentKey = withPrefix(absentKey)
	}
	n, err := compareKVScript.Run(ctx, r.c, []string{withPrefix(key), absentKey}, match, string(old), action, string(next), ttl.Milliseconds()).Int()
	return n == 1, err
}

func (r *redisKV) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return r.c.Set(ctx, withPrefix(key), val, 0).Err()
	}
	return r.c.Set(ctx, withPrefix(key), val, ttl).Err()
}

func (r *redisKV) Get(ctx context.Context, key string) ([]byte, bool, error) {
	v, err := r.c.Get(ctx, withPrefix(key)).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return v, true, nil
}

func (r *redisKV) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	full := make([]string, len(keys))
	for i, k := range keys {
		full[i] = withPrefix(k)
	}
	return r.c.Del(ctx, full...).Err()
}

// DelByPrefix 用 SCAN 遍历并删除所有匹配前缀的 key。
// 不用 KEYS（会阻塞 Redis），分批删除避免大 DEL 卡顿。
func (r *redisKV) DelByPrefix(ctx context.Context, prefix string) error {
	full := withPrefix(prefix)
	var cursor uint64
	for {
		// COUNT 500 在扫描完整性与 Redis 负载间取平衡。
		keys, next, err := r.c.Scan(ctx, cursor, full+"*", 500).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := r.c.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		if next == 0 {
			return nil
		}
		cursor = next
	}
}

// DelBySubstring 用 SCAN 遍历指定前缀下的 key，删除所有值含 substr 的 key。
// 用于 key 冷却的 RemoveKeyCooldowns：key 形如 "cooldown:{channelID}:{keyID}:{model}"，
// 按 ":keyID:" 子串定位（无法用前缀表达）。prefix 限定扫描范围避免全库扫描。
func (r *redisKV) DelBySubstring(ctx context.Context, prefix, substr string) error {
	full := withPrefix(prefix)
	var cursor uint64
	for {
		keys, next, err := r.c.Scan(ctx, cursor, full+"*", 500).Result()
		if err != nil {
			return err
		}
		var toDelete []string
		for _, k := range keys {
			if strings.Contains(k, substr) {
				toDelete = append(toDelete, k)
			}
		}
		if len(toDelete) > 0 {
			if err := r.c.Del(ctx, toDelete...).Err(); err != nil {
				return err
			}
		}
		if next == 0 {
			return nil
		}
		cursor = next
	}
}

// --- RateLimitStore Redis 实现 ---

// tokenBucketScript 实现单 key 的 token bucket：refill + 比较 + 扣减，原子执行。
// KEYS[1] = bucket key
// ARGV[1] = rate（tokens/秒，= rpm/60）
// ARGV[2] = burst（桶容量）
// ARGV[3] = n（本次请求 token 数）
// ARGV[4] = now（秒，unix 时间戳，float）
// ARGV[5] = ttl（秒，bucket key 的过期时间，用于防无界增长）
//
// 返回: {allowed(0/1), remaining(int), reset_at(unix秒)}
// Redis 内 token 以浮点存储（HGET/HSET），last_update 记上次 refill 时间。
var tokenBucketScript = redis.NewScript(`
local key = KEYS[1]
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local n = tonumber(ARGV[3])
local now = tonumber(ARGV[4])
local ttl = tonumber(ARGV[5])

local tokens = tonumber(redis.call('HGET', key, 't')) or burst
local last = tonumber(redis.call('HGET', key, 'l')) or now

-- refill
local elapsed = now - last
if elapsed > 0 then
  tokens = tokens + elapsed * rate
  if tokens > burst then tokens = burst end
end

local allowed = 0
if tokens >= n then
  tokens = tokens - n
  allowed = 1
end

redis.call('HMSET', key, 't', tostring(tokens), 'l', tostring(now))
redis.call('EXPIRE', key, ttl)

local remaining = math.floor(tokens)
local reset_at = now + (burst - tokens) / rate
return {allowed, remaining, tostring(reset_at)}
`)

// tokenBucketTTL bucket key 的过期时间，与现有 balancerIdleThreshold(1h) 对齐。
// bucket 长时间不活动后自动清理，替代内存模式的 PurgeStaleBuckets。
const tokenBucketTTL = 3600

// redisRateLimit 是 RateLimitStore 的 Redis 实现。
type redisRateLimit struct {
	c *redis.Client
}

func newRedisRateLimit(c *redis.Client) *redisRateLimit { return &redisRateLimit{c: c} }

func (r *redisRateLimit) CheckAndConsume(ctx context.Context, key string, rate, burst, n int) (bool, int, time.Time, error) {
	if rate <= 0 {
		return true, burst, time.Now(), nil
	}
	if burst <= 0 {
		burst = rate
	}
	if n <= 0 {
		n = 1
	}
	ratePerSec := float64(rate) / 60.0
	now := float64(time.Now().UnixNano()) / 1e9
	res, err := tokenBucketScript.Run(ctx, r.c,
		[]string{withPrefix(key)},
		ratePerSec, burst, n, now, tokenBucketTTL,
	).Result()
	if err != nil {
		return false, 0, time.Now(), err
	}
	slice, ok := res.([]interface{})
	if !ok || len(slice) < 3 {
		return false, 0, time.Now(), nil
	}
	allowed := slice[0].(int64) == 1
	remaining := int(slice[1].(int64))
	resetAtF, _ := strconv.ParseFloat(slice[2].(string), 64)
	return allowed, remaining, time.Unix(0, int64(resetAtF*1e9)), nil
}

func (r *redisRateLimit) ConsumeTokens(ctx context.Context, key string, rate, burst, n int) error {
	if rate <= 0 || n <= 0 {
		return nil
	}
	if burst <= 0 {
		burst = rate
	}
	ratePerSec := float64(rate) / 60.0
	now := float64(time.Now().UnixNano()) / 1e9
	// 复用同一脚本：n 个 token 直接扣减（脚本内部会 refill，若不足则 allowed=0 但仍扣减）。
	// TPM 回扣语义：请求已成功，token 数已确定，直接扣减即可（与内存 AllowN 一致）。
	_, err := tokenBucketScript.Run(ctx, r.c,
		[]string{withPrefix(key)},
		ratePerSec, burst, n, now, tokenBucketTTL,
	).Result()
	return err
}

func (r *redisRateLimit) RemoveByAPIKey(ctx context.Context, apiKeyID int) error {
	// 限流 key 形如 "rl:req:{apiKeyID}:{modelName}" / "rl:tok:{apiKeyID}:{modelName}"。
	// 按 apiKeyID 前缀删除两个命名空间。
	prefix1 := "rl:req:" + itoa(apiKeyID) + ":"
	prefix2 := "rl:tok:" + itoa(apiKeyID) + ":"
	if err := r.delByPrefix(ctx, prefix1); err != nil {
		return err
	}
	return r.delByPrefix(ctx, prefix2)
}

func (r *redisRateLimit) delByPrefix(ctx context.Context, prefix string) error {
	full := withPrefix(prefix)
	var cursor uint64
	for {
		keys, next, err := r.c.Scan(ctx, cursor, full+"*", 500).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := r.c.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		if next == 0 {
			return nil
		}
		cursor = next
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
