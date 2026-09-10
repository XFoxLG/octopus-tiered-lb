package store

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/lingyuins/octopus/internal/conf"
	"github.com/redis/go-redis/v9"
)

// BuildRedisOptions is shared by startup, connection tests and reconnection.
// URI credentials/database take precedence over separate fields when present.
func BuildRedisOptions(configuration conf.RedisConfig) (*redis.Options, error) {
	if err := conf.ValidateRedisConfig(configuration); err != nil {
		return nil, err
	}
	address := strings.TrimSpace(configuration.Addr)
	if address == "" {
		return nil, fmt.Errorf("redis address is required")
	}
	options := &redis.Options{Addr: address, DB: configuration.DB}
	uriHasUsername, uriHasPassword := false, false
	if strings.Contains(address, "://") {
		parsedURL, err := url.Parse(address)
		if err != nil || (parsedURL.Scheme != "redis" && parsedURL.Scheme != "rediss") {
			return nil, fmt.Errorf("redis address must be host:port or a redis:// or rediss:// URI")
		}
		options, err = redis.ParseURL(address)
		if err != nil {
			return nil, fmt.Errorf("invalid redis URI options")
		}
		if parsedURL.User != nil {
			uriHasUsername = true
			_, uriHasPassword = parsedURL.User.Password()
		}
		if parsedURL.Path == "" && !parsedURL.Query().Has("db") {
			options.DB = configuration.DB
		}
	}
	if !uriHasUsername {
		options.Username = configuration.Username
	}
	if !uriHasPassword {
		options.Password = configuration.Password
	}
	host, _, err := net.SplitHostPort(options.Addr)
	if err != nil || host == "" {
		return nil, fmt.Errorf("redis address must include a valid host and port")
	}
	if options.DB < 0 {
		return nil, fmt.Errorf("redis database index cannot be negative")
	}
	if options.TLSConfig != nil && options.TLSConfig.InsecureSkipVerify {
		return nil, fmt.Errorf("redis TLS certificate verification cannot be disabled")
	}
	if configuration.TLS || options.TLSConfig != nil {
		options.TLSConfig = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	if configuration.CAFile != "" {
		if options.TLSConfig == nil {
			return nil, fmt.Errorf("redis CA certificate requires TLS")
		}
		certificatePEM, err := os.ReadFile(configuration.CAFile)
		if err != nil {
			return nil, fmt.Errorf("unable to read redis CA certificate")
		}
		certificatePool, err := x509.SystemCertPool()
		if err != nil {
			certificatePool = x509.NewCertPool()
		}
		if !certificatePool.AppendCertsFromPEM(certificatePEM) {
			return nil, fmt.Errorf("redis CA certificate contains no valid PEM certificate")
		}
		options.TLSConfig.RootCAs = certificatePool
	}
	if configuration.PoolSize > 0 {
		options.PoolSize = configuration.PoolSize
	}
	if options.PoolSize <= 0 {
		options.PoolSize = 5
	}
	// PoolSize alone is not a hard limit in go-redis. Bound burst connections too.
	options.MaxActiveConns = options.PoolSize
	options.MinIdleConns = 0
	options.MaxIdleConns = options.PoolSize
	options.ConnMaxIdleTime = time.Minute
	if configuration.DialTimeout > 0 {
		options.DialTimeout = configuration.DialTimeout
	}
	if configuration.ReadTimeout > 0 {
		options.ReadTimeout = configuration.ReadTimeout
	}
	if options.DialTimeout <= 0 {
		options.DialTimeout = 3 * time.Second
	}
	if options.ReadTimeout <= 0 {
		options.ReadTimeout = 3 * time.Second
	}
	options.WriteTimeout = options.ReadTimeout
	options.PoolTimeout = options.ReadTimeout
	options.ContextTimeoutEnabled = true
	return options, nil
}

func SafeRedisAddress(address string) string {
	address = strings.TrimSpace(address)
	if strings.Contains(address, "://") {
		parsedURL, err := url.Parse(address)
		if err != nil || parsedURL.Hostname() == "" {
			return "[invalid redis address]"
		}
		return parsedURL.Host
	}
	if strings.ContainsAny(address, "@/?#") {
		return "[invalid redis address]"
	}
	return address
}

func newClient(configuration conf.RedisConfig) (*redis.Client, error) {
	options, err := BuildRedisOptions(configuration)
	if err != nil {
		return nil, err
	}
	return redis.NewClient(options), nil
}
