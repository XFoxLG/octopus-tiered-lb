package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/ratelimitstore"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/server/resp"
)

const loginRateLimitCleanupInterval = 10 * time.Minute

func getLoginRateLimitWindow() time.Duration {
	if v, err := setting.GetInt(model.SettingKeyLoginRateLimitWindow); err == nil && v > 0 {
		return time.Duration(v) * time.Minute
	}
	return 10 * time.Minute
}

func getLoginRateLimitMaxFailed() int {
	if v, err := setting.GetInt(model.SettingKeyLoginRateLimitMaxFailed); err == nil && v > 0 {
		return v
	}
	return 5
}

type loginAttempt struct {
	FailedCount  int
	BlockedUntil time.Time
	LastFailedAt time.Time
}

var loginAttemptCache = struct {
	sync.Mutex
	items map[string]*loginAttempt
}{
	items: make(map[string]*loginAttempt),
}

func LoginRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.ClientIP()
		if key == "" {
			key = c.RemoteIP()
		}
		if isLoginBlocked(key, time.Now()) {
			resp.Error(c, http.StatusTooManyRequests, resp.ErrTooManyRequests)
			c.Abort()
			return
		}
		c.Set("login_rate_limit_key", key)
		c.Next()
	}
}

func RecordLoginFailure(key string, now time.Time) {
	if key == "" {
		return
	}

	loginAttemptCache.Lock()
	defer loginAttemptCache.Unlock()

	attempt, ok := loginAttemptCache.items[key]
	if !ok || now.Sub(attempt.LastFailedAt) > getLoginRateLimitWindow() {
		attempt = &loginAttempt{}
		loginAttemptCache.items[key] = attempt
	}

	attempt.FailedCount++
	attempt.LastFailedAt = now
	if attempt.FailedCount >= getLoginRateLimitMaxFailed() {
		attempt.BlockedUntil = now.Add(getLoginRateLimitWindow())
	}
}

func ClearLoginFailures(key string) {
	if key == "" {
		return
	}
	loginAttemptCache.Lock()
	delete(loginAttemptCache.items, key)
	loginAttemptCache.Unlock()
}

func isLoginBlocked(key string, now time.Time) bool {
	if key == "" {
		return false
	}

	loginAttemptCache.Lock()
	defer loginAttemptCache.Unlock()

	attempt, ok := loginAttemptCache.items[key]
	if !ok {
		return false
	}
	if !attempt.BlockedUntil.IsZero() && now.Before(attempt.BlockedUntil) {
		return true
	}
	if now.Sub(attempt.LastFailedAt) > getLoginRateLimitWindow() {
		delete(loginAttemptCache.items, key)
		return false
	}
	return false
}

// startLoginRateLimitCleanup periodically purges expired entries from the login attempt cache.
func startLoginRateLimitCleanup() {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// cleanup goroutine is best-effort; re-panics are not propagated
			}
		}()
		ticker := time.NewTicker(loginRateLimitCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			loginAttemptCache.Lock()
			for key, attempt := range loginAttemptCache.items {
				if now.Sub(attempt.LastFailedAt) > getLoginRateLimitWindow() {
					delete(loginAttemptCache.items, key)
				}
			}
			loginAttemptCache.Unlock()
		}
	}()
}

// GlobalRelayRateLimit 是作用于 4 条转发路由(chat/responses/messages/embeddings)
// 的全站下游限速:请求频率(每分钟)+ 同时在途并发。两个设置默认 0 = 关闭,
// 升级后行为不变。管理 API、登录、日志不经过这里,避免把自己锁在外面。
//
// 并发名额在 c.Next() 前占用、之后释放,与 Gin 的同步 handler 生命周期严格配对
// (relay.Handler 是同步阻塞的),不会泄漏名额。
func GlobalRelayRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if rpm := getGlobalRateLimitRPM(); rpm > 0 {
			if allowed, retryAfter := ratelimitstore.CheckGlobalRPM(rpm); !allowed {
				c.Header("Retry-After", strconv.Itoa(retryAfter))
				resp.Error(c, http.StatusTooManyRequests, "global rate limit exceeded")
				c.Abort()
				return
			}
		}

		acquired := false
		if maxConcurrency := getGlobalMaxConcurrency(); maxConcurrency > 0 {
			if !ratelimitstore.AcquireGlobalSlot(maxConcurrency) {
				c.Header("Retry-After", "1")
				resp.Error(c, http.StatusTooManyRequests, "global concurrency limit exceeded")
				c.Abort()
				return
			}
			acquired = true
		}
		defer func() {
			if acquired {
				ratelimitstore.ReleaseGlobalSlot()
			}
		}()
		c.Next()
	}
}

func getGlobalRateLimitRPM() int {
	if v, err := setting.GetInt(model.SettingKeyGlobalRateLimitRPM); err == nil && v > 0 {
		return v
	}
	return 0
}

func getGlobalMaxConcurrency() int {
	if v, err := setting.GetInt(model.SettingKeyGlobalMaxConcurrency); err == nil && v > 0 {
		return v
	}
	return 0
}

func init() {
	startLoginRateLimitCleanup()
}
