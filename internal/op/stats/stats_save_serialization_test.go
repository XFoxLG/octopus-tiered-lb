package stats

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func TestSnapshotSavesSerializeWithoutBlockingUpdates(t *testing.T) {
	for _, firstUsesOverride := range []bool{false, true} {
		name := "normal_then_override"
		if firstUsesOverride {
			name = "override_then_normal"
		}
		t.Run(name, func(t *testing.T) {
			databaseConnection := initializeStatsPersistenceTest(t, true)
			usePostgresDialectForTest(t, databaseConnection)
			sqlConnection, err := databaseConnection.DB()
			if err != nil {
				t.Fatalf("get isolated connection pool: %v", err)
			}
			// Let the second transaction start if the save mutex is missing, rather
			// than accidentally relying on a single-connection pool for ordering.
			sqlConnection.SetMaxOpenConns(4)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			dailyOverride := model.StatsDaily{Date: "20260908", StatsMetrics: model.StatsMetrics{RequestSuccess: 9}}
			firstSave := func() error { return SaveDB(ctx) }
			secondSave := func() error { return saveDBWithDailyOverride(ctx, dailyOverride) }
			if firstUsesOverride {
				firstSave, secondSave = secondSave, firstSave
			}
			recordSnapshotMetricsForTest(t, model.StatsMetrics{InputToken: 100, RequestSuccess: 1})

			firstSnapshotReached := make(chan struct{})
			secondSnapshotReached := make(chan struct{}, 1)
			releaseFirstSnapshot := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseFirstSnapshot) }) }
			var totalSnapshots atomic.Int32
			if err := databaseConnection.Callback().Update().Before("gorm:update").Register("test:block_first_stats_snapshot", func(transaction *gorm.DB) {
				if _, isTotal := transaction.Statement.Dest.(*model.StatsTotal); !isTotal {
					return
				}
				if totalSnapshots.Add(1) == 1 {
					close(firstSnapshotReached)
					select {
					case <-releaseFirstSnapshot:
					case <-ctx.Done():
						transaction.AddError(ctx.Err())
					}
					return
				}
				select {
				case secondSnapshotReached <- struct{}{}:
				default:
				}
			}); err != nil {
				t.Fatalf("register snapshot barrier: %v", err)
			}

			var workers sync.WaitGroup
			defer workers.Wait()
			// Release before waiting, including on fatal assertions.
			defer release()
			firstResult := make(chan error, 1)
			workers.Add(1)
			go func() {
				defer workers.Done()
				firstResult <- firstSave()
			}()
			select {
			case <-firstSnapshotReached:
			case <-ctx.Done():
				t.Fatal("first save did not reach its snapshot write")
			}

			// The save mutex must not extend cache locks across SQL I/O.
			updatesFinished := make(chan error, 1)
			workers.Add(1)
			go func() {
				defer workers.Done()
				updatesFinished <- updateSnapshotMetricsForTest(model.StatsMetrics{InputToken: 50, RequestSuccess: 1})
			}()
			select {
			case err := <-updatesFinished:
				if err != nil {
					t.Fatalf("update metrics during snapshot write: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("in-memory updates blocked behind snapshot database I/O")
			}

			secondResult := make(chan error, 1)
			workers.Add(1)
			go func() {
				defer workers.Done()
				secondResult <- secondSave()
			}()
			select {
			case <-secondSnapshotReached:
				t.Error("second snapshot reached SQL before the first save completed")
			case <-time.After(100 * time.Millisecond):
			}
			release()
			for _, result := range []<-chan error{firstResult, secondResult} {
				select {
				case err := <-result:
					if err != nil {
						t.Fatalf("save snapshot: %v", err)
					}
				case <-ctx.Done():
					t.Fatal("serialized saves did not complete")
				}
			}
			var persistedTotal model.StatsTotal
			if err := databaseConnection.First(&persistedTotal).Error; err != nil {
				t.Fatalf("read persisted total: %v", err)
			}
			if persistedTotal.InputToken != 150 || persistedTotal.RequestSuccess != 2 {
				t.Fatalf("older snapshot overwrote newer statistics: %+v", persistedTotal)
			}
			var persistedPreviousDay model.StatsDaily
			if err := databaseConnection.First(&persistedPreviousDay, "date = ?", dailyOverride.Date).Error; err != nil {
				t.Fatalf("read persisted previous day: %v", err)
			}
			if persistedPreviousDay != dailyOverride {
				t.Fatalf("previous-day override changed: %+v, want %+v", persistedPreviousDay, dailyOverride)
			}
		})
	}
}

func TestSaveDBFlushesPendingDailyOverrideWithoutRecursiveLock(t *testing.T) {
	databaseConnection := initializeStatsPersistenceTest(t, true)
	usePostgresDialectForTest(t, databaseConnection)
	previousDay := model.StatsDaily{Date: "20260908", StatsMetrics: model.StatsMetrics{RequestSuccess: 9}}
	pendingDailyOverride.Store(&previousDay)
	recordSnapshotMetricsForTest(t, model.StatsMetrics{InputToken: 100, RequestSuccess: 2})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- SaveDB(ctx) }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("save pending daily override: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("SaveDB deadlocked while flushing a pending day boundary")
	}
	var persistedDays []model.StatsDaily
	if err := databaseConnection.Order("date").Find(&persistedDays).Error; err != nil {
		t.Fatalf("read persisted daily rows: %v", err)
	}
	if len(persistedDays) != 2 || persistedDays[0] != previousDay || persistedDays[1] != TodayGet() {
		t.Fatalf("pending override and today's metrics were not both persisted: %+v", persistedDays)
	}
	if pendingDailyOverride.Load() != nil {
		t.Fatal("successfully saved pending daily override was not consumed")
	}
}
