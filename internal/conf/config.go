package conf

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lingyuins/octopus/internal/utils/log"
	"github.com/spf13/viper"
)

type Server struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
	// TrustedProxies 控制信任的反向代理 CIDR/IP 列表（逗号分隔），用于解析
	// X-Forwarded-For / X-Real-IP 取得真实客户端 IP。空值（默认）表示不信任
	// 任何代理，c.ClientIP() 只返回 TCP 直连地址（安全默认，防 XFF 伪造）。
	// 反代/Docker 部署应配置实际代理网段，例如 "172.17.0.0/16"。
	// "*" 表示信任所有来源（等价于 Gin 旧行为，仅开发用，有安全风险）。
	TrustedProxies string `mapstructure:"trusted_proxies"`
	// ExternalURL 是本服务对外的可访问基础 URL（含 scheme://host[:port]），
	// 用于 OAuth 回调地址拼接。为空时回退到 http://host:port。重启生效（engine 级配置）。
	ExternalURL string `mapstructure:"external_url"`
}

type Log struct {
	Level string `mapstructure:"level"`
}

type Database struct {
	Type string `mapstructure:"type"`
	Path string `mapstructure:"path"`
	// LogType / LogPath 为可选的独立「日志数据库」配置（仅承载 relay_logs）。
	// 二者任一为空时，日志沿用主库连接，行为与旧版完全一致（向后兼容）。
	// 配置后，relay_logs 落到独立库，可通过直接删库/断连实现秒级清理与卸载。
	LogType string             `mapstructure:"log_type"`
	LogPath string             `mapstructure:"log_path"`
	Pool    DatabasePoolConfig `mapstructure:"pool"`
	LogPool DatabasePoolConfig `mapstructure:"log_pool"`
	// SQLite 为主库且 type=sqlite 时生效的 per-connection PRAGMA 调优（见 issue #97）。
	// 日志库为 SQLite 时复用同一组值。
	SQLite SQLiteConfig `mapstructure:"sqlite"`
}

type DatabasePoolConfig struct {
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
	ConnMaxIdleTime time.Duration `mapstructure:"conn_max_idle_time"`
}

func DefaultDatabasePoolConfig() DatabasePoolConfig {
	return DatabasePoolConfig{
		MaxOpenConns: 5, MaxIdleConns: 1,
		ConnMaxLifetime: time.Hour, ConnMaxIdleTime: time.Minute,
	}
}

func DefaultLogDatabasePoolConfig() DatabasePoolConfig {
	configuration := DefaultDatabasePoolConfig()
	configuration.MaxOpenConns = 2
	return configuration
}

// SQLiteConfig 暴露 SQLite 运行时可调的 PRAGMA。这些值通过 glebarez/go-sqlite
// 驱动 DSN 的 _pragma 参数下发（驱动只认 _pragma/_txlock/_time_format/vfs 四种
// query 参数）。配置项默认面向低内存安全：禁用 mmap、cache 约 20MB。
type SQLiteConfig struct {
	// CacheSize 对应 PRAGMA cache_size。负值按 KB 计（如 -20000≈20MB），
	// 正值按页计（每页 4KB）。0 表示使用 internal/db.DefaultSQLiteCacheSize。
	CacheSize int `mapstructure:"cache_size"`
	// MMapSize 对应 PRAGMA mmap_size。0 表示禁用 mmap（低内存环境安全默认值，
	// 直接规避 mmap 缺页导致的磁盘 IO），正值按字节计。
	MMapSize int64 `mapstructure:"mmap_size"`
}

type Auth struct {
	JWTSecret string `mapstructure:"jwt_secret"`
}

type Relay struct {
	MaxJSONBodyBytes      int64 `mapstructure:"max_json_body_bytes"`
	MaxMultipartBodyBytes int64 `mapstructure:"max_multipart_body_bytes"`
}

type External struct {
	LLMPriceURL  string `mapstructure:"llm_price_url"`
	UpdateURL    string `mapstructure:"update_url"`
	UpdateAPIURL string `mapstructure:"update_api_url"`
}

type Security struct {
	EncryptionKey string `mapstructure:"encryption_key"`
}

// Cache selects optional Redis runtime state. SQL remains authoritative for
// durable configuration and statistics; Redis is not an accounting journal.
type Cache struct {
	Type  string      `mapstructure:"type"` // "" | "redis"（空=内存，向后兼容）
	Redis RedisConfig `mapstructure:"redis"`
}

// RedisConfig 描述 Redis 单机连接参数。哨兵/集群模式留待后续迭代。
type RedisConfig struct {
	Addr        string        `mapstructure:"addr"`         // "127.0.0.1:6379"
	Password    string        `mapstructure:"password"`     // 可选
	Username    string        `mapstructure:"username"`     // ACL 用户名（可选）
	DB          int           `mapstructure:"db"`           // 0-15
	PoolSize    int           `mapstructure:"pool_size"`    // 0 selects the application default of 5.
	DialTimeout time.Duration `mapstructure:"dial_timeout"` // 0 selects the application default of 3s.
	ReadTimeout time.Duration `mapstructure:"read_timeout"` // 0 selects the application default of 3s.
	TLS         bool          `mapstructure:"tls"`
	CAFile      string        `mapstructure:"ca_file"`
}

type Config struct {
	Server   Server   `mapstructure:"server"`
	Log      Log      `mapstructure:"log"`
	Database Database `mapstructure:"database"`
	Auth     Auth     `mapstructure:"auth"`
	Relay    Relay    `mapstructure:"relay"`
	External External `mapstructure:"external"`
	Security Security `mapstructure:"security"`
	Cache    Cache    `mapstructure:"cache"`
}

var AppConfig Config
var cacheConfigurationLock sync.RWMutex

func GetCacheConfig() Cache {
	cacheConfigurationLock.RLock()
	defer cacheConfigurationLock.RUnlock()
	return AppConfig.Cache
}

// ephemeralJWTSecret marks whether the JWT secret was generated ephemerally
// during Load() (because it was empty or a known placeholder). When true the
// secret is NOT safe to derive an AES encryption key from — using it would
// cause encrypted data to become unrecoverable after the next restart.
var ephemeralJWTSecret bool

// IsEphemeralJWTSecret reports whether the current JWT secret was generated
// ephemerally this process (i.e. not persisted to config). Callers that need a
// durable key (such as crypto.Init) should refuse to use it.
func IsEphemeralJWTSecret() bool { return ephemeralJWTSecret }

func Load(path string) error {
	configFile := path
	if path != "" {
		viper.SetConfigFile(path)
	} else {
		viper.SetConfigName("config")
		viper.SetConfigType("json")
		viper.AddConfigPath(defaultDataDir())
		configFile = defaultConfigPath()
	}

	viper.AutomaticEnv()
	viper.SetEnvPrefix(APP_NAME)
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	setDefaults()

	if err := viper.ReadInConfig(); err == nil {
		log.Infof("Using config file: %s", viper.ConfigFileUsed())
	} else {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			log.Infof("Config file not found, creating default config")
			if err := os.MkdirAll(filepath.Dir(configFile), 0755); err != nil {
				return wrapConfigPathError("failed to create config directory", filepath.Dir(configFile), err)
			}
			// Never serialize AutomaticEnv into a newly created file: platform
			// secrets belong in the deployment environment, not a local copy.
			configurationFile, err := os.OpenFile(configFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return wrapConfigPathError("failed to create default config", configFile, err)
			}
			_, writeError := configurationFile.WriteString("{}\n")
			closeError := configurationFile.Close()
			if writeError != nil {
				return writeError
			}
			if closeError != nil {
				return closeError
			}
			viper.SetConfigFile(configFile)
			if err := viper.ReadInConfig(); err != nil {
				return fmt.Errorf("read created config: %w", err)
			}
		} else {
			return fmt.Errorf("error reading config file: %w", err)
		}
	}

	if err := viper.Unmarshal(&AppConfig); err != nil {
		return fmt.Errorf("unable to decode config into struct: %w", err)
	}
	if err := validateDatabasePool("database.pool", AppConfig.Database.Pool); err != nil {
		return err
	}
	if err := validateDatabasePool("database.log_pool", AppConfig.Database.LogPool); err != nil {
		return err
	}
	if AppConfig.Cache.Type != "" && AppConfig.Cache.Type != "redis" {
		return fmt.Errorf("cache.type must be empty or redis")
	}
	if AppConfig.Cache.Type == "redis" {
		if strings.TrimSpace(AppConfig.Cache.Redis.Addr) == "" {
			return fmt.Errorf("cache.redis.addr is required when cache.type is redis")
		}
		if err := ValidateRedisConfig(AppConfig.Cache.Redis); err != nil {
			return err
		}
	}
	if AppConfig.Auth.JWTSecret == "" {
		secret, err := generateJWTSecret()
		if err != nil {
			return fmt.Errorf("failed to generate JWT secret: %w", err)
		}
		AppConfig.Auth.JWTSecret = secret
		ephemeralJWTSecret = true
		log.Warnf("auth.jwt_secret is empty, generated an ephemeral secret for this process; configure OCTOPUS_AUTH_JWT_SECRET or auth.jwt_secret to keep tokens valid across restarts")
	} else if isKnownPlaceholderJWTSecret(AppConfig.Auth.JWTSecret) {
		secret, err := generateJWTSecret()
		if err != nil {
			return fmt.Errorf("failed to generate JWT secret: %w", err)
		}
		AppConfig.Auth.JWTSecret = secret
		ephemeralJWTSecret = true
		log.Warnf("auth.jwt_secret is a known placeholder value; generated an ephemeral secret instead. Set a unique value to keep tokens valid across restarts")
	}
	return nil
}

// SaveCacheConfig persists local configuration without replacing the active store.
// Deployment-managed values must be changed at their source; restart applies changes.
func SaveCacheConfig(cacheType string, redis RedisConfig) error {
	cacheConfigurationLock.Lock()
	defer cacheConfigurationLock.Unlock()
	cacheType = strings.TrimSpace(cacheType)
	if CacheConfigSource() != "file" {
		return fmt.Errorf("cache configuration is managed by deployment environment; update it there instead of saving a temporary local file")
	}
	if cacheType != "" && cacheType != "redis" {
		return fmt.Errorf("cache type must be empty or redis")
	}
	if err := ValidateRedisConfig(redis); err != nil {
		return err
	}
	if cacheType == "redis" && strings.TrimSpace(redis.Addr) == "" {
		return fmt.Errorf("redis address is required")
	}
	fileConfiguration := viper.New()
	fileConfiguration.SetConfigFile(viper.ConfigFileUsed())
	if err := fileConfiguration.ReadInConfig(); err != nil {
		return fmt.Errorf("read config before save: %w", err)
	}
	fileConfiguration.Set("cache", map[string]any{
		"type": cacheType,
		"redis": map[string]any{
			"addr": redis.Addr, "password": redis.Password, "username": redis.Username,
			"db": redis.DB, "pool_size": redis.PoolSize, "tls": redis.TLS, "ca_file": redis.CAFile,
			"dial_timeout": redis.DialTimeout.String(), "read_timeout": redis.ReadTimeout.String(),
		},
	})
	fileConfiguration.SetConfigPermissions(0600)
	if err := fileConfiguration.WriteConfig(); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	AppConfig.Cache.Type = cacheType
	AppConfig.Cache.Redis = redis
	return nil
}

func setDefaults() {
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.external_url", "")
	viper.SetDefault("database.type", "sqlite")
	viper.SetDefault("database.path", defaultDatabasePath())
	// 日志库默认留空：留空表示与主库共用连接（向后兼容）。
	viper.SetDefault("database.log_type", "")
	viper.SetDefault("database.log_path", "")
	setDatabasePoolDefaults("database.pool", DefaultDatabasePoolConfig())
	setDatabasePoolDefaults("database.log_pool", DefaultLogDatabasePoolConfig())
	// SQLite per-connection PRAGMA 调优（见 issue #97：低内存环境持续高磁盘 IO）。
	// cache_size 默认 -20000KB（≈20MB），与 internal/db.DefaultSQLiteCacheSize 对齐；
	// mmap_size 默认 0（禁用 mmap，避免物理内存 < 库大小时空洞缺页导致的持续读盘）。
	viper.SetDefault("database.sqlite.cache_size", -20000)
	viper.SetDefault("database.sqlite.mmap_size", int64(0))
	viper.SetDefault("log.level", "info")
	viper.SetDefault("auth.jwt_secret", "")
	viper.SetDefault("relay.max_json_body_bytes", int64(64<<20))
	viper.SetDefault("relay.max_multipart_body_bytes", int64(64<<20))
	viper.SetDefault("external.llm_price_url", "https://models.dev/api.json")
	viper.SetDefault("external.update_url", "https://github.com/lingyuins/octopus/releases/latest/download")
	viper.SetDefault("external.update_api_url", "https://api.github.com/repos/lingyuins/octopus/releases/latest")
	viper.SetDefault("security.encryption_key", "")
	// 缓存/状态后端默认留空：留空表示沿用内存 + 数据库策略（向后兼容）。
	// 配置 "redis" 时启用 Redis 后端（见 issue #123）。
	viper.SetDefault("cache.type", "")
	// Unmarshal only visits known keys; AutomaticEnv alone misses fresh-container credentials.
	viper.SetDefault("cache.redis.addr", "")
	viper.SetDefault("cache.redis.password", "")
	viper.SetDefault("cache.redis.username", "")
	viper.SetDefault("cache.redis.db", 0)
	viper.SetDefault("cache.redis.pool_size", 5)
	viper.SetDefault("cache.redis.tls", false)
	viper.SetDefault("cache.redis.ca_file", "")
	// Redis 连接超时默认 3s（issue #135）：比 go-redis 默认 DialTimeout 5s 更激进，
	// 避免远程 Redis 重启期间 TCP hang 导致启动期长时间无日志阻塞。
	viper.SetDefault("cache.redis.dial_timeout", "3s")
	viper.SetDefault("cache.redis.read_timeout", "3s")
}

func setDatabasePoolDefaults(prefix string, configuration DatabasePoolConfig) {
	viper.SetDefault(prefix+".max_open_conns", configuration.MaxOpenConns)
	viper.SetDefault(prefix+".max_idle_conns", configuration.MaxIdleConns)
	viper.SetDefault(prefix+".conn_max_lifetime", configuration.ConnMaxLifetime.String())
	viper.SetDefault(prefix+".conn_max_idle_time", configuration.ConnMaxIdleTime.String())
}

func validateDatabasePool(name string, configuration DatabasePoolConfig) error {
	if configuration.MaxOpenConns < 1 || configuration.MaxIdleConns < 0 || configuration.MaxIdleConns > configuration.MaxOpenConns {
		return fmt.Errorf("%s requires max_open_conns >= 1 and 0 <= max_idle_conns <= max_open_conns", name)
	}
	if configuration.ConnMaxLifetime < 0 || configuration.ConnMaxIdleTime < 0 {
		return fmt.Errorf("%s connection timeouts cannot be negative", name)
	}
	return nil
}

func ValidateRedisConfig(configuration RedisConfig) error {
	if configuration.DB < 0 || configuration.PoolSize < 0 {
		return fmt.Errorf("redis database index and pool size cannot be negative")
	}
	if configuration.DialTimeout < 0 || configuration.ReadTimeout < 0 {
		return fmt.Errorf("redis timeouts cannot be negative")
	}
	return nil
}

// CacheConfigSource distinguishes durable deployment configuration from a local editable file.
func CacheConfigSource() string {
	for _, variable := range os.Environ() {
		name, value, _ := strings.Cut(variable, "=")
		if strings.HasPrefix(strings.ToUpper(name), "OCTOPUS_CACHE_") && value != "" {
			return "environment"
		}
	}
	if os.Getenv("RENDER") == "true" || os.Getenv("RENDER_SERVICE_ID") != "" {
		return "deployment"
	}
	return "file"
}

func defaultDataDir() string {
	if path := strings.TrimSpace(os.Getenv(strings.ToUpper(APP_NAME) + "_DATA_DIR")); path != "" {
		return filepath.Clean(path)
	}
	return "data"
}

// DataDir returns the resolved data directory (from OCTOPUS_DATA_DIR env or
// "data" fallback). Exported so other packages can constrain file operations
// (e.g. SQLite migration paths) to within the data directory.
func DataDir() string {
	return defaultDataDir()
}

func defaultConfigPath() string {
	return filepath.Join(defaultDataDir(), "config.json")
}

func defaultDatabasePath() string {
	return filepath.Join(defaultDataDir(), "data.db")
}

func wrapConfigPathError(action, path string, err error) error {
	if err == nil {
		return nil
	}
	if os.IsPermission(err) {
		return fmt.Errorf("%s %q: %w; make sure the target directory is writable by the current process (the official Docker image runs as UID/GID 1000 and needs write access to /app/data)", action, path, err)
	}
	return fmt.Errorf("%s %q: %w", action, path, err)
}

var knownPlaceholderSecrets = []string{
	"change-this-to-a-long-random-secret",
}

func isKnownPlaceholderJWTSecret(secret string) bool {
	for _, p := range knownPlaceholderSecrets {
		if secret == p {
			return true
		}
	}
	return false
}

func generateJWTSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
