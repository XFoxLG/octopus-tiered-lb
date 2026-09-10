package store

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/lingyuins/octopus/internal/conf"
)

func TestBuildRedisOptionsPreservesURIAndVerifiesTLS(t *testing.T) {
	options, err := BuildRedisOptions(conf.RedisConfig{
		Addr: "rediss://uri-user:synthetic-password@valkey.example.test:12345/3",
		DB:   1, Username: "field-user", PoolSize: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.DB != 3 || options.Username != "uri-user" || options.Password != "synthetic-password" {
		t.Fatal("URI credentials and database must take precedence over separate fields")
	}
	if options.TLSConfig == nil || options.TLSConfig.InsecureSkipVerify || options.TLSConfig.ServerName != "valkey.example.test" || options.TLSConfig.MinVersion < tls.VersionTLS12 {
		t.Fatal("TLS must verify the intended service identity")
	}
	if options.PoolSize != 4 || options.MaxActiveConns != 4 || !options.ContextTimeoutEnabled {
		t.Fatal("Redis connections and operations must have bounded budgets")
	}
}

func TestBuildRedisOptionsRejectsUnsafeOrMalformedURI(t *testing.T) {
	for _, address := range []string{
		"https://user:secret@host:6379", "rediss://user:secret@host:6379?skip_verify=true",
		"rediss://user:secret@host:6379?unknown=secret", "redis://host:6379/-1", "user:secret@host:6379",
	} {
		_, err := BuildRedisOptions(conf.RedisConfig{Addr: address})
		if err == nil {
			t.Errorf("expected unsafe address to be rejected: %s", SafeRedisAddress(address))
		} else if strings.Contains(err.Error(), "secret") {
			t.Error("validation leaked URI credentials")
		}
		if strings.Contains(SafeRedisAddress(address), "secret") {
			t.Error("safe address leaked credentials")
		}
	}
}

func TestRedisURIPreservesExplicitEmptyAuthenticationFields(t *testing.T) {
	configuration := conf.RedisConfig{Addr: "rediss://:new-password@host:6379", Username: "stale-user", Password: "stale-password"}
	options, err := BuildRedisOptions(configuration)
	if err != nil || options.Username != "" || options.Password != "new-password" {
		t.Fatal("password-only URI must not inherit a stale ACL username")
	}
	configuration.Addr = "rediss://new-user:@host:6379"
	options, err = BuildRedisOptions(configuration)
	if err != nil || options.Username != "new-user" || options.Password != "" {
		t.Fatal("explicitly empty URI password must not inherit a stale password")
	}
}

func TestPendingReconnectReportsConfigurationChanges(t *testing.T) {
	resetState(t)
	t.Cleanup(func() { resetState(t) })
	configuration := conf.RedisConfig{Addr: "127.0.0.1:1"}
	if err := StartReconnect(configuration, nil); err != nil {
		t.Fatal(err)
	}
	unchanged := InspectBackend(context.Background(), conf.Cache{Type: "redis", Redis: configuration})
	if !unchanged.Reconnecting || unchanged.RestartNeeded {
		t.Fatal("unchanged pending target should be reconnecting without a configuration change")
	}
	if !InspectBackend(context.Background(), conf.Cache{}).RestartNeeded {
		t.Fatal("saving memory mode must not pretend to cancel a pending Redis connection")
	}
	configuration.Addr = "127.0.0.1:2"
	if !InspectBackend(context.Background(), conf.Cache{Type: "redis", Redis: configuration}).RestartNeeded {
		t.Fatal("saved different target requires restart while old target is still reconnecting")
	}
	if err := Close(); err != nil {
		t.Fatal(err)
	}
	if InspectBackend(context.Background(), conf.Cache{}).Reconnecting {
		t.Fatal("shutdown must cancel background connection work")
	}
}

func TestRedisTLSAuthenticatesUsingTrustedCA(t *testing.T) {
	certificateServer := httptest.NewTLSServer(http.NotFoundHandler())
	serverTLS := certificateServer.TLS.Clone()
	certificate := certificateServer.Certificate()
	certificateServer.Close()
	redisServer, err := miniredis.RunTLS(serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(redisServer.Close)
	redisServer.RequireUserAuth("service-user", "synthetic-password")
	certificatePath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	configuration := conf.RedisConfig{
		Addr: redisServer.Addr(), TLS: true, CAFile: certificatePath,
		Username: "service-user", Password: "synthetic-password", DialTimeout: time.Second,
	}
	if err := TestConnection(configuration); err != nil {
		t.Fatalf("trusted TLS connection and ACL authentication failed: %v", err)
	}
	configuration.CAFile = ""
	if err := TestConnection(configuration); err == nil {
		t.Fatal("an untrusted certificate must not be accepted")
	}
}

func TestBackendStatusDistinguishesConfiguredAndRunning(t *testing.T) {
	resetState(t)
	t.Cleanup(func() { resetState(t) })
	_, redisServer := newTestRedis(t)
	configuration := conf.RedisConfig{Addr: redisServer.Addr()}
	before := InspectBackend(context.Background(), conf.Cache{Type: "redis", Redis: configuration})
	if before.Backend != "memory" || !before.RestartNeeded {
		t.Fatal("saved config is not evidence of an active Redis backend")
	}
	if err := Init(configuration); err != nil {
		t.Fatal(err)
	}
	after := InspectBackend(context.Background(), conf.Cache{Type: "redis", Redis: configuration})
	if after.Backend != "redis" || !after.Healthy || after.RestartNeeded {
		t.Fatal("active matching Redis config should report healthy and applied")
	}
	changed := configuration
	changed.DB = 1
	if !InspectBackend(context.Background(), conf.Cache{Type: "redis", Redis: changed}).RestartNeeded {
		t.Fatal("changing saved config must not pretend to hot-swap the running store")
	}
}
