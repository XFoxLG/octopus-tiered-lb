package stats

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func TestRefreshFailurePreservesQuotaSnapshot(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "stats.db"), false); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := RefreshCache(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { totalCache = model.StatsTotal{}; apiKeyCache.Clear() }()
	totalCache = model.StatsTotal{ID: 1, StatsMetrics: model.StatsMetrics{InputToken: 100}}
	apiKeyCache.Set(10, model.StatsAPIKey{APIKeyID: 10, StatsMetrics: model.StatsMetrics{InputCost: 20}})
	connection := db.GetDB()
	if err := connection.Create(&model.StatsTotal{ID: 1, StatsMetrics: model.StatsMetrics{InputToken: 999}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := connection.Callback().Query().Before("gorm:query").Register("fail_quota", func(transaction *gorm.DB) {
		if transaction.Statement.Schema != nil && transaction.Statement.Schema.Table == "stats_api_keys" {
			transaction.AddError(context.DeadlineExceeded)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer connection.Callback().Query().Remove("fail_quota")
	if err := RefreshCache(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected quota read failure, got %v", err)
	}
	quota, exists := apiKeyCache.Get(10)
	if totalCache.InputToken != 100 || !exists || quota.InputCost != 20 {
		t.Fatal("partial/zero statistics published after failed load")
	}
}
