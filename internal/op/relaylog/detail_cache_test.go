package relaylog

import (
	"context"
	"errors"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
	"testing"
)

func TestPendingLogDetailDoesNotDependOnDatabase(t *testing.T) {
	connection := initializeRelayLogContentTestDatabase(t)
	const logID int64 = 117392002743437825
	restore := SetCacheForTest([]model.RelayLog{{ID: logID, RequestModelName: "pending-model"}})
	defer restore()
	if err := connection.Callback().Query().Before("gorm:query").Register("unavailable_log_db", func(transaction *gorm.DB) { transaction.AddError(context.DeadlineExceeded) }); err != nil {
		t.Fatal(err)
	}
	defer connection.Callback().Query().Remove("unavailable_log_db")
	detail, err := RelayLogGetByID(context.Background(), logID)
	if err != nil || detail == nil || detail.ID != logID {
		t.Fatalf("cached detail unavailable: %v %v", detail, err)
	}
	_, err = RelayLogGetByID(context.Background(), logID+1)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DB timeout incorrectly became not found: %v", err)
	}
}
