package stats

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/store"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	isolationChannelID = 42
	isolationAPIKeyID  = 7
	isolationModelID   = 101
	isolationModelName = "test-model"
)

// These tests execute the existing SQL persistence path against an isolated
// in-memory database. They do not connect to a live PostgreSQL or Redis service.
func initializeStatsPersistenceTest(t *testing.T, needsDatabase bool) *gorm.DB {
	t.Helper()
	t.Cleanup(SetTimeNowForTest(func() time.Time {
		return time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	}))
	setting.GetCache().Clear()
	setting.GetCache().Set(model.SettingKeyStatsTimezone, "UTC")
	ClearAllCachesForTest()
	pendingDailyOverride.Store(nil)
	ResetCachesForTest(model.StatsTotal{ID: 1}, model.StatsDaily{Date: today()}, 0, 0, 0)
	t.Cleanup(func() {
		pendingDailyOverride.Store(nil)
		ClearAllCachesForTest()
		setting.GetCache().Clear()
	})
	if !needsDatabase {
		return nil
	}
	testName := strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(t.Name())
	if err := db.InitDB("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared", testName), false); err != nil {
		t.Fatalf("initialize isolated database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.GetDB().Create(&model.Channel{ID: isolationChannelID, Name: "stats-isolation-channel"}).Error; err != nil {
		t.Fatalf("seed channel for statistics foreign key: %v", err)
	}
	return db.GetDB()
}

// usePostgresDialectForTest exercises PostgreSQL-generated snapshot SQL using
// the isolated SQLite connection. This is not a live PostgreSQL integration
// test: server-specific transaction behavior and migrations are not covered.
func usePostgresDialectForTest(t *testing.T, databaseConnection *gorm.DB) {
	t.Helper()
	postgresDatabase, err := gorm.Open(postgres.New(postgres.Config{
		Conn: databaseConnection.ConnPool,
	}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("initialize PostgreSQL dialect without a PostgreSQL connection: %v", err)
	}
	originalDatabase := *databaseConnection
	*databaseConnection = *postgresDatabase
	t.Cleanup(func() { *databaseConnection = originalDatabase })
}

func recordSnapshotMetricsForTest(t *testing.T, metrics model.StatsMetrics) {
	t.Helper()
	if err := updateSnapshotMetricsForTest(metrics); err != nil {
		t.Fatal(err)
	}
}

func updateSnapshotMetricsForTest(metrics model.StatsMetrics) error {
	updates := []struct {
		name   string
		update func() error
	}{
		{"total", func() error { return TotalUpdate(metrics) }},
		{"daily", func() error { return DailyUpdate(context.Background(), metrics) }},
		{"hourly", func() error { return HourlyUpdate(metrics) }},
		{"channel", func() error { return ChannelUpdate(isolationChannelID, metrics) }},
		{"model", func() error {
			return ModelUpdate(model.StatsModel{
				ID: isolationModelID, ChannelID: isolationChannelID, Name: isolationModelName, StatsMetrics: metrics,
			})
		}},
		{"apikey", func() error { return APIKeyUpdate(isolationAPIKeyID, metrics) }},
	}
	for _, update := range updates {
		if err := update.update(); err != nil {
			return fmt.Errorf("update %s: %w", update.name, err)
		}
	}
	return nil
}

func assertSnapshotMetricsForTest(t *testing.T, expected model.StatsMetrics) {
	t.Helper()
	modelStats, found := modelCache.Get(isolationModelID)
	if !found || modelStats.Name != isolationModelName || modelStats.ChannelID != isolationChannelID {
		t.Fatalf("model identity was not preserved: %+v (found=%v)", modelStats, found)
	}
	metricsByDimension := map[string]model.StatsMetrics{
		"total":   TotalGet().StatsMetrics,
		"daily":   TodayGet().StatsMetrics,
		"hourly":  HourlyGet()[now().Hour()].StatsMetrics,
		"channel": ChannelGet(isolationChannelID).StatsMetrics,
		"model":   modelStats.StatsMetrics,
		"apikey":  APIKeyGet(isolationAPIKeyID).StatsMetrics,
	}
	for dimension, actual := range metricsByDimension {
		if actual != expected {
			t.Errorf("%s metrics = %+v, want %+v", dimension, actual, expected)
		}
	}
}

func seedLegacyRedisDeltasForTest(t *testing.T) map[string]string {
	t.Helper()
	identifiers := map[string]string{
		statsScopeTotal:             statsIDTotal,
		statsScopeDaily:             today(),
		statsScopeHourly:            fmt.Sprintf("%s:%d", today(), now().Hour()),
		statsScopeChannel:           strconv.Itoa(isolationChannelID),
		statsScopeModel:             strconv.Itoa(isolationModelID),
		statsScopeAPIKey:            strconv.Itoa(isolationAPIKeyID),
		statsScopeDailyChannel:      today() + ":42",
		statsScopeDailyModel:        today() + ":" + isolationModelName,
		statsScopeDailyAPIKey:       today() + ":7",
		statsScopeDailyChannelModel: today() + ":42:" + isolationModelName,
	}
	for scope, identifier := range identifiers {
		if err := store.GetStats().IncrMetrics(context.Background(), scope, identifier, legacyRedisMetricsForTest()); err != nil {
			t.Fatalf("seed legacy Redis %s: %v", scope, err)
		}
	}
	return identifiers
}

func legacyRedisMetricsForTest() model.StatsMetrics {
	return model.StatsMetrics{InputToken: 999, InputCost: 99.5, RequestSuccess: 99, LatencyP99: 9999}
}

func assertLegacyRedisUnchangedForTest(t *testing.T, identifiers map[string]string) {
	t.Helper()
	for scope, identifier := range identifiers {
		actual, err := store.GetStats().GetMetrics(context.Background(), scope, identifier)
		if err != nil {
			t.Fatalf("read legacy Redis %s: %v", scope, err)
		}
		if actual != legacyRedisMetricsForTest() {
			t.Errorf("legacy Redis %s changed: %+v", scope, actual)
		}
	}
}

func TestRefreshCacheIgnoresLegacyRedisDeltas(t *testing.T) {
	databaseConnection := initializeStatsPersistenceTest(t, true)
	usePostgresDialectForTest(t, databaseConnection)
	ctx := context.Background()
	expected := model.StatsMetrics{InputToken: 100, OutputToken: 30, InputCost: 1.5, OutputCost: 0.25, RequestSuccess: 2}
	recordSnapshotMetricsForTest(t, expected)
	if err := SaveDB(ctx); err != nil {
		t.Fatalf("save baseline database snapshot: %v", err)
	}
	redisServer := newStatsTestRedis(t)
	legacyIdentifiers := seedLegacyRedisDeltasForTest(t)
	commandsBefore := redisServer.CommandCount()
	for cycle := 0; cycle < 2; cycle++ {
		ClearAllCachesForTest()
		if err := RefreshCache(ctx); err != nil {
			t.Fatalf("refresh cycle %d: %v", cycle, err)
		}
		assertSnapshotMetricsForTest(t, expected)
		if len(GetChannelDirtyIDs()) != 0 || len(GetModelDirtyIDs()) != 0 || len(GetAPIKeyDirtyIDs()) != 0 {
			t.Fatal("refresh marked persisted statistics dirty from legacy Redis data")
		}
		if err := SaveDB(ctx); err != nil {
			t.Fatalf("save after refresh cycle %d: %v", cycle, err)
		}
	}
	if commandsAfter := redisServer.CommandCount(); commandsAfter != commandsBefore {
		t.Errorf("refresh/save sent %d Redis commands, want none", commandsAfter-commandsBefore)
	}
	assertLegacyRedisUnchangedForTest(t, legacyIdentifiers)
}

func TestStatsSaveReloadIndependentOfRedis(t *testing.T) {
	for _, redisEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("redis=%v", redisEnabled), func(t *testing.T) {
			databaseConnection := initializeStatsPersistenceTest(t, true)
			usePostgresDialectForTest(t, databaseConnection)
			var redisServer *miniredis.Miniredis
			if redisEnabled {
				redisServer = newStatsTestRedis(t)
			} else if store.Enabled() {
				t.Fatal("expected isolated in-memory store")
			}
			ctx := context.Background()
			first := model.StatsMetrics{
				InputToken: 100, OutputToken: 50, InputCost: 1.5, OutputCost: 0.25,
				WaitTime: 200, RequestSuccess: 3, RequestFailed: 1,
			}
			second := model.StatsMetrics{
				InputToken: 70, OutputToken: 20, InputCost: 0.5, OutputCost: 0.125,
				WaitTime: 150, RequestSuccess: 2, RequestFailed: 2,
			}
			expected := model.StatsMetrics{
				InputToken: 170, OutputToken: 70, InputCost: 2, OutputCost: 0.375,
				WaitTime: 350, RequestSuccess: 5, RequestFailed: 3,
			}
			recordSnapshotMetricsForTest(t, first)
			if err := SaveDB(ctx); err != nil {
				t.Fatalf("save first snapshot: %v", err)
			}
			recordSnapshotMetricsForTest(t, second)
			for cycle := 0; cycle < 2; cycle++ {
				if err := SaveDB(ctx); err != nil {
					t.Fatalf("save cumulative snapshot: %v", err)
				}
				ClearAllCachesForTest()
				if err := RefreshCache(ctx); err != nil {
					t.Fatalf("reload snapshot: %v", err)
				}
				assertSnapshotMetricsForTest(t, expected)
			}
			if redisServer != nil && redisServer.CommandCount() != 0 {
				t.Fatalf("snapshot persistence sent %d Redis commands", redisServer.CommandCount())
			}
		})
	}
}

func TestStatsDeletionLeavesLegacyRedisDeltasUntouched(t *testing.T) {
	databaseConnection := initializeStatsPersistenceTest(t, true)
	recordSnapshotMetricsForTest(t, model.StatsMetrics{RequestSuccess: 1})
	if err := SaveDB(context.Background()); err != nil {
		t.Fatalf("save initial snapshot: %v", err)
	}
	redisServer := newStatsTestRedis(t)
	legacyIdentifiers := seedLegacyRedisDeltasForTest(t)
	commandsBefore := redisServer.CommandCount()
	if err := ChannelDel(isolationChannelID); err != nil {
		t.Fatalf("delete channel stats: %v", err)
	}
	if err := APIKeyDel(isolationAPIKeyID); err != nil {
		t.Fatalf("delete apikey stats: %v", err)
	}
	for _, row := range []any{&model.StatsChannel{}, &model.StatsAPIKey{}} {
		var count int64
		if err := databaseConnection.Model(row).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("existing SQL deletion changed for %T: count=%d, error=%v", row, count, err)
		}
	}
	_ = ChannelUpdate(isolationChannelID, model.StatsMetrics{RequestSuccess: 1})
	_ = APIKeyUpdate(isolationAPIKeyID, model.StatsMetrics{RequestSuccess: 1})
	OnChannelDeleted(isolationChannelID)
	OnAPIKeyDeleted(isolationAPIKeyID)
	if len(ChannelList()) != 0 || len(APIKeyList()) != 0 || len(GetChannelDirtyIDs()) != 0 || len(GetAPIKeyDirtyIDs()) != 0 {
		t.Fatal("deletion callbacks must still clear local entries and dirty identifiers")
	}
	if commandsAfter := redisServer.CommandCount(); commandsAfter != commandsBefore {
		t.Errorf("stats deletion sent %d Redis commands, want none", commandsAfter-commandsBefore)
	}
	assertLegacyRedisUnchangedForTest(t, legacyIdentifiers)
}

func TestDailyDimensionsRemainIndependentOfRedis(t *testing.T) {
	for _, redisEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("redis=%v", redisEnabled), func(t *testing.T) {
			databaseConnection := initializeStatsPersistenceTest(t, true)
			var redisServer *miniredis.Miniredis
			if redisEnabled {
				redisServer = newStatsTestRedis(t)
			}
			ctx := context.Background()
			first := model.StatsMetrics{
				InputToken: 100, OutputToken: 50, InputCost: 1.5, OutputCost: 0.5,
				WaitTime: 200, RequestSuccess: 1, RequestFailed: 1,
				LatencyP50: 100, LatencyP95: 200, LatencyP99: 300,
				FtutAvg: 40, FtutP50: 30, FtutP95: 50, FtutP99: 70,
				HistogramLt100: 1, Histogram100to500: 2, Histogram500to1k: 3, Histogram1kto5k: 4, HistogramGt5k: 5,
			}
			second := model.StatsMetrics{
				InputToken: 50, OutputToken: 20, InputCost: 0.5, OutputCost: 0.25,
				WaitTime: 100, RequestSuccess: 2,
				LatencyP50: 50, LatencyP95: 250, LatencyP99: 250,
				FtutAvg: 50, FtutP50: 20, FtutP95: 60, FtutP99: 60,
				HistogramLt100: 2, Histogram100to500: 3, Histogram500to1k: 4, Histogram1kto5k: 5, HistogramGt5k: 6,
			}
			expected := model.StatsMetrics{
				InputToken: 150, OutputToken: 70, InputCost: 2, OutputCost: 0.75,
				WaitTime: 300, RequestSuccess: 3, RequestFailed: 1,
				LatencyP50: 100, LatencyP95: 250, LatencyP99: 300,
				FtutAvg: 50, FtutP50: 30, FtutP95: 60, FtutP99: 70,
				HistogramLt100: 3, Histogram100to500: 5, Histogram500to1k: 7, Histogram1kto5k: 9, HistogramGt5k: 11,
			}
			for _, metrics := range []model.StatsMetrics{first, second} {
				for _, err := range []error{
					DailyDimensionChannelUpdate(ctx, isolationChannelID, " channel ", metrics),
					DailyDimensionModelUpdate(ctx, " "+isolationModelName+" ", metrics),
					DailyDimensionAPIKeyUpdate(ctx, isolationAPIKeyID, " key ", metrics),
					DailyDimensionChannelModelUpdate(ctx, isolationChannelID, " channel ", " "+isolationModelName+" ", metrics),
				} {
					if err != nil {
						t.Fatalf("persist daily dimension: %v", err)
					}
				}
			}
			rows := []struct {
				actual   any
				expected any
			}{
				{&model.StatsDailyChannel{}, &model.StatsDailyChannel{
					Date: today(), ChannelID: isolationChannelID, ChannelName: "channel", StatsMetrics: expected,
				}},
				{&model.StatsDailyModel{}, &model.StatsDailyModel{
					Date: today(), ModelName: isolationModelName, StatsMetrics: expected,
				}},
				{&model.StatsDailyAPIKey{}, &model.StatsDailyAPIKey{
					Date: today(), APIKeyID: isolationAPIKeyID, Name: "key", StatsMetrics: expected,
				}},
				{&model.StatsDailyChannelModel{}, &model.StatsDailyChannelModel{
					Date: today(), ChannelID: isolationChannelID, ChannelName: "channel", ModelName: isolationModelName, StatsMetrics: expected,
				}},
			}
			// Daily dimensions must exist before SaveDB and remain unchanged after it.
			for _, afterSave := range []bool{false, true} {
				if afterSave {
					if err := SaveDB(ctx); err != nil {
						t.Fatalf("save snapshots: %v", err)
					}
				}
				for _, row := range rows {
					if err := databaseConnection.First(row.actual).Error; err != nil {
						t.Fatalf("read daily dimension %T: %v", row.actual, err)
					}
					if !reflect.DeepEqual(row.actual, row.expected) {
						t.Errorf("daily dimension afterSave=%v: got %+v, want %+v", afterSave, row.actual, row.expected)
					}
				}
			}
			if redisServer != nil && redisServer.CommandCount() != 0 {
				t.Fatalf("daily dimensions sent %d Redis commands", redisServer.CommandCount())
			}
		})
	}
}
