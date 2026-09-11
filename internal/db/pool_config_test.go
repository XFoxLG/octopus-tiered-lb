package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/conf"
)

func TestConnectionPoolsAreBoundedWithoutLivePostgres(t *testing.T) {
	original := conf.AppConfig.Database
	t.Cleanup(func() { conf.AppConfig.Database = original })
	conf.AppConfig.Database.Pool = conf.DefaultDatabasePoolConfig()
	connection, err := OpenStandalone("sqlite", "file:pool-budget?mode=memory&cache=shared", false)
	if err != nil {
		t.Fatal(err)
	}
	sqlDatabase, err := connection.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDatabase.Close() })
	if sqlDatabase.Stats().MaxOpenConnections != 1 {
		t.Fatal("SQLite must retain its single-connection transaction policy")
	}

	// Exercise database/sql pool admission locally, without claiming PostgreSQL load validation.
	configureConnectionPool(sqlDatabase, "postgres")
	if sqlDatabase.Stats().MaxOpenConnections != 5 {
		t.Fatal("main pool should reserve headroom on small managed databases")
	}
	connections := make([]*sql.Conn, 0, 5)
	for connectionIndex := 0; connectionIndex < 5; connectionIndex++ {
		pooledConnection, err := sqlDatabase.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, pooledConnection)
	}
	t.Cleanup(func() {
		for _, pooledConnection := range connections {
			_ = pooledConnection.Close()
		}
	})
	waitContext, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if extraConnection, err := sqlDatabase.Conn(waitContext); err == nil {
		_ = extraConnection.Close()
		t.Fatal("pool opened a sixth connection despite its configured limit")
	}
	for _, pooledConnection := range connections {
		_ = pooledConnection.Close()
	}
	connections = nil
	configureLogConnectionPool(connection, "postgres")
	if sqlDatabase.Stats().MaxOpenConnections != 2 {
		t.Fatal("separate log pool must have its own smaller budget")
	}
	conf.AppConfig.Database.Pool.MaxOpenConns = 3
	configureConnectionPool(sqlDatabase, "postgres")
	if sqlDatabase.Stats().MaxOpenConnections != 3 {
		t.Fatal("deployment override was not applied to the pool")
	}
}
