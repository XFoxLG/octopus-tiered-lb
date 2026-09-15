package conf

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

var cacheDefaults = map[string]any{
	"cache.type":               "",
	"cache.redis.addr":         "",
	"cache.redis.password":     "",
	"cache.redis.username":     "",
	"cache.redis.db":           0,
	"cache.redis.pool_size":    5,
	"cache.redis.tls":          false,
	"cache.redis.ca_file":      "",
	"cache.redis.dial_timeout": "3s",
	"cache.redis.read_timeout": "3s",
}

func setCacheDefaults(configuration *viper.Viper) {
	// Unmarshal visits known keys only, including on a fresh env-only container.
	for key, value := range cacheDefaults {
		configuration.SetDefault(key, value)
	}
}

// HasCacheEnvironment includes explicit empty values: setting CACHE_TYPE to an
// empty string must disable Redis, not revive a saved database configuration.
func HasCacheEnvironment() bool {
	for key := range cacheDefaults {
		variable := strings.ToUpper(APP_NAME + "_" + strings.ReplaceAll(key, ".", "_"))
		if _, exists := os.LookupEnv(variable); exists {
			return true
		}
	}
	return false
}

func loadCacheEnvironment() (Cache, error) {
	environment := viper.New()
	environment.SetEnvPrefix(APP_NAME)
	environment.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	environment.AutomaticEnv()
	environment.AllowEmptyEnv(true)
	setCacheDefaults(environment)
	var configuration struct {
		Cache Cache `mapstructure:"cache"`
	}
	if err := environment.Unmarshal(&configuration); err != nil {
		return Cache{}, fmt.Errorf("unable to decode cache environment configuration")
	}
	return configuration.Cache, nil
}

// CacheServiceID is stable across Render restarts. Host headers, browser input
// and RENDER_INSTANCE_ID must never select the owner of saved credentials.
func CacheServiceID() string {
	serviceID := os.Getenv("RENDER_SERVICE_ID")
	if !strings.HasPrefix(serviceID, "srv-") || len(serviceID) <= 4 || len(serviceID) > 128 {
		return ""
	}
	for _, character := range serviceID {
		if character != '-' && character != '_' &&
			(character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') {
			return ""
		}
	}
	return serviceID
}

// CacheConfigSource identifies the authoritative source and persistence owner.
// No default database owner is used on deployments without a valid service ID.
func CacheConfigSource() string {
	if HasCacheEnvironment() {
		return "environment"
	}
	if CacheServiceID() != "" {
		return "database"
	}
	if os.Getenv("RENDER") == "true" || os.Getenv("RENDER_SERVICE_ID") != "" {
		return "deployment"
	}
	return "file"
}
