// Package cacheconfig persists optional, service-owned Redis configuration.
// Configuration is loaded at startup and saved explicitly, never per relay.
package cacheconfig

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/store"
	"github.com/lingyuins/octopus/internal/utils/crypto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var (
	ErrEnvironmentManaged  = errors.New("cache configuration is managed by environment variables; remove the cache overrides to save it here")
	ErrServiceIdentity     = errors.New("a stable Render service ID is required to save cache configuration in the database")
	configurationWriteLock sync.Mutex
)

type storedConfiguration struct {
	ServiceID string      `json:"service_id"`
	Cache     *conf.Cache `json:"cache"`
}

// Load runs after SQL and encryption initialization, before store initialization.
// A corrupt record is an explicit error, never a reason to overwrite it.
func Load(ctx context.Context) error {
	if conf.CacheConfigSource() != "database" {
		return nil
	}
	configuration, found, err := loadConfiguration(ctx, db.GetDB(), conf.CacheServiceID())
	if err != nil {
		return err
	}
	if found {
		conf.SetCacheConfig(configuration)
		return nil
	}
	// Without a service record the file/default cache section is the
	// effective fallback; validate it here where the outcome is known
	// instead of blocking startup before the database was consulted.
	return conf.ValidateCacheConfiguration(conf.GetCacheConfig())
}

// Save writes the desired configuration only. It does not close, initialize or
// switch a runtime client, including a client currently reconnecting.
func Save(ctx context.Context, request model.CacheConfigRequest) (conf.Cache, error) {
	configurationWriteLock.Lock()
	defer configurationWriteLock.Unlock()
	source := conf.CacheConfigSource()
	if source == "environment" {
		return conf.Cache{}, ErrEnvironmentManaged
	}
	if source == "deployment" {
		return conf.Cache{}, ErrServiceIdentity
	}
	configuration, err := Prepare(request, conf.GetCacheConfig())
	if err != nil {
		return conf.Cache{}, err
	}
	if source == "file" {
		if err := conf.SaveCacheConfig(configuration.Type, configuration.Redis); err != nil {
			return conf.Cache{}, errors.New("unable to save cache configuration file")
		}
		return configuration, nil
	}
	if err := saveConfiguration(ctx, db.GetDB(), conf.CacheServiceID(), configuration); err != nil {
		return conf.Cache{}, err
	}
	conf.SetCacheConfig(configuration)
	return configuration, nil
}

// Prepare is shared by preview, PING and save. A replacement is complete: a new
// endpoint can never inherit the saved password or transport identity.
func Prepare(request model.CacheConfigRequest, current conf.Cache) (conf.Cache, error) {
	configuration := conf.Cache{Type: request.Type, Redis: current.Redis}
	if request.Redis != nil {
		replacement := request.Redis
		configuration.Redis = conf.RedisConfig{
			Addr: strings.TrimSpace(replacement.Addr), Username: replacement.Username,
			Password: replacement.Password, DB: replacement.DB, TLS: replacement.TLS,
			CAFile: strings.TrimSpace(replacement.CAFile), PoolSize: replacement.PoolSize,
		}
		if strings.Contains(configuration.Redis.Addr, "://") {
			// Web URL mode is self-contained. Legacy env/file configurations still
			// retain their separate-field fallback semantics in BuildRedisOptions.
			configuration.Redis.Username = ""
			configuration.Redis.Password = ""
			configuration.Redis.DB = 0
			configuration.Redis.TLS = false
		}
		if err := applyTuning(&configuration.Redis, model.CacheRedisTuning{
			PoolSize: replacement.PoolSize, DialTimeout: replacement.DialTimeout,
			ReadTimeout: replacement.ReadTimeout,
		}); err != nil {
			return conf.Cache{}, err
		}
	}
	if request.Tuning != nil {
		if err := applyTuning(&configuration.Redis, *request.Tuning); err != nil {
			return conf.Cache{}, err
		}
	}
	if err := validateConfiguration(configuration); err != nil {
		return conf.Cache{}, err
	}
	return configuration, nil
}

func applyTuning(configuration *conf.RedisConfig, tuning model.CacheRedisTuning) error {
	dialTimeout, err := parseTimeout(tuning.DialTimeout)
	if err != nil {
		return err
	}
	readTimeout, err := parseTimeout(tuning.ReadTimeout)
	if err != nil {
		return err
	}
	configuration.PoolSize = tuning.PoolSize
	configuration.DialTimeout = dialTimeout
	configuration.ReadTimeout = readTimeout
	return conf.ValidateRedisConfig(*configuration)
}

func parseTimeout(value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || duration < 0 {
		return 0, errors.New("redis timeout must be empty or a non-negative duration such as 3s")
	}
	return duration, nil
}

func validateConfiguration(configuration conf.Cache) error {
	if configuration.Type != "" && configuration.Type != "redis" {
		return errors.New("cache type must be empty or redis")
	}
	if err := conf.ValidateRedisConfig(configuration.Redis); err != nil {
		return err
	}
	if configuration.Type == "redis" {
		_, err := store.BuildRedisOptions(configuration.Redis)
		return err
	}
	return nil
}

// Describe returns parser-normalized display metadata, never URI userinfo,
// query parameters, a saved password, or encryption ciphertext.
func Describe(configuration conf.RedisConfig) (model.CacheRedisSummary, bool, error) {
	if strings.TrimSpace(configuration.Addr) == "" {
		return model.CacheRedisSummary{PoolSize: 5, DialTimeout: "3s", ReadTimeout: "3s"}, false, nil
	}
	options, err := store.BuildRedisOptions(configuration)
	if err != nil {
		return model.CacheRedisSummary{}, false, err
	}
	return model.CacheRedisSummary{
		Addr: options.Addr, DB: options.DB, TLS: options.TLSConfig != nil,
		PoolSize: options.PoolSize, DialTimeout: options.DialTimeout.String(),
		ReadTimeout: options.ReadTimeout.String(),
	}, options.Password != "", nil
}

func loadConfiguration(ctx context.Context, database *gorm.DB, serviceID string) (conf.Cache, bool, error) {
	if serviceID == "" {
		return conf.Cache{}, false, ErrServiceIdentity
	}
	if database == nil {
		return conf.Cache{}, false, errors.New("cache configuration database is unavailable")
	}
	var record model.ServiceCacheConfig
	result := database.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).
		WithContext(ctx).Where("service_id = ?", serviceID).Take(&record)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return conf.Cache{}, false, nil
	}
	if result.Error != nil {
		return conf.Cache{}, false, errors.New("unable to read service cache configuration")
	}
	if !crypto.IsEncrypted(record.EncryptedConfig) {
		return conf.Cache{}, false, errors.New("saved service cache configuration is not encrypted")
	}
	plaintext, err := crypto.Decrypt(record.EncryptedConfig)
	if err != nil {
		return conf.Cache{}, false, errors.New("unable to decrypt service cache configuration; check the original encryption key")
	}
	var stored storedConfiguration
	if err := json.Unmarshal([]byte(plaintext), &stored); err != nil || stored.Cache == nil || stored.ServiceID != serviceID {
		return conf.Cache{}, false, errors.New("invalid saved service cache configuration")
	}
	if err := validateConfiguration(*stored.Cache); err != nil {
		return conf.Cache{}, false, err
	}
	return *stored.Cache, true, nil
}

func saveConfiguration(ctx context.Context, database *gorm.DB, serviceID string, configuration conf.Cache) error {
	if serviceID == "" {
		return ErrServiceIdentity
	}
	if database == nil {
		return errors.New("cache configuration database is unavailable")
	}
	if err := validateConfiguration(configuration); err != nil {
		return err
	}
	plaintext, err := json.Marshal(storedConfiguration{ServiceID: serviceID, Cache: &configuration})
	if err != nil {
		return errors.New("unable to encode service cache configuration")
	}
	ciphertext, err := crypto.Encrypt(string(plaintext))
	if err != nil || !crypto.IsEncrypted(ciphertext) {
		return errors.New("unable to encrypt service cache configuration")
	}
	record := model.ServiceCacheConfig{ServiceID: serviceID, EncryptedConfig: ciphertext}
	result := database.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).
		WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "service_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"encrypted_config", "updated_at"}),
	}).Create(&record)
	if result.Error != nil {
		return errors.New("unable to save service cache configuration")
	}
	return nil
}
