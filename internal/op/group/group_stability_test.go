package group

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestGroupUpdateStabilitySQLite(t *testing.T) {
	ctx := initGroupEndpointNameTestDB(t)
	testGroupUpdateStability(t, ctx)
}

func TestGroupUpdateStabilityPostgres(t *testing.T) {
	dsn := os.Getenv("OCTOPUS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL test database not configured")
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" {
		t.Fatal("test PostgreSQL must be local, never a production endpoint")
	}
	connection, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := connection.DB()
	defer sqlDB.Close()
	schema := fmt.Sprintf("stability_%d", time.Now().UnixNano())
	if err := connection.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer connection.Exec("DROP SCHEMA " + schema + " CASCADE")
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	if err := db.InitDB("postgres", parsed.String(), false); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := RefreshAllCache(ctx); err != nil {
		t.Fatal(err)
	}
	testGroupUpdateStability(t, ctx)
}

func testGroupUpdateStability(t *testing.T, ctx context.Context) {
	t.Helper()
	connection := db.GetDB()
	sqlDB, _ := connection.DB()
	sqlDB.SetMaxOpenConns(1)
	group := &model.Group{Name: "stability", EndpointType: model.EndpointTypeChat, Mode: model.GroupModeRoundRobin}
	if err := GroupCreate(group, ctx); err != nil {
		t.Fatal(err)
	}
	items := []model.GroupItem{{GroupID: group.ID, ChannelID: 1, ModelName: "model-a", Priority: 1, Weight: 1}, {GroupID: group.ID, ChannelID: 2, ModelName: "model-b", Priority: 2, Weight: 1}}
	if err := connection.Create(&items).Error; err != nil {
		t.Fatal(err)
	}
	if err := RefreshCacheByID(group.ID, ctx); err != nil {
		t.Fatal(err)
	}
	zero, two := 0, 2
	for _, values := range [][]*int{{nil, nil}, {&zero, &two}, {nil, &zero}} {
		request := &model.GroupUpdateRequest{ID: group.ID, ItemsToUpdate: []model.GroupItemUpdateRequest{{ID: items[0].ID, Priority: 2, Weight: 1, RelayRetryCountOverride: values[0]}, {ID: items[1].ID, Priority: 1, Weight: 1, RelayRetryCountOverride: values[1]}}}
		if _, err := GroupUpdate(request, ctx); err != nil {
			t.Fatal(err)
		}
		var loaded []model.GroupItem
		if err := connection.Where("group_id = ?", group.ID).Order("id").Find(&loaded).Error; err != nil {
			t.Fatal(err)
		}
		for index, expected := range values {
			actual := loaded[index].RelayRetryCountOverride
			if (expected == nil) != (actual == nil) || expected != nil && *expected != *actual {
				t.Fatalf("NULL/zero inheritance corrupted at %d", index)
			}
		}
	}
	if err := connection.Callback().Create().Before("gorm:create").Register("stability_fail", func(transaction *gorm.DB) {
		if transaction.Statement.Schema != nil && transaction.Statement.Schema.Table == "group_items" {
			transaction.AddError(errors.New("injected insert failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer connection.Callback().Create().Remove("stability_fail")
	newName := "must-rollback"
	_, err := GroupUpdate(&model.GroupUpdateRequest{ID: group.ID, Name: &newName, ItemsToDelete: []int{items[0].ID}, ItemsToAdd: []model.GroupItemAddRequest{{ChannelID: 3, ModelName: "model-c", Weight: 1, Priority: 1}}}, ctx)
	if err == nil {
		t.Fatal("injected failure accepted")
	}
	var loaded model.Group
	if err := connection.First(&loaded, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "stability" {
		t.Fatal("transaction did not roll back group")
	}
	var count int64
	connection.Model(&model.GroupItem{}).Where("group_id = ?", group.ID).Count(&count)
	if count != 2 || sqlDB.Stats().InUse != 0 {
		t.Fatalf("lost item or leaked connection: count=%d stats=%+v", count, sqlDB.Stats())
	}
}
