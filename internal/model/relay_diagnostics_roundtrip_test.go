package model

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestRelayStreamDiagnosticsPersistAndReadBack(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "relay-diagnostics.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
	})
	if err := database.AutoMigrate(&RelayLog{}); err != nil {
		t.Fatal(err)
	}

	entry := RelayLog{
		ID: 1,
		Diagnostics: &RelayDiagnostics{
			Version: RelayDiagnosticsVersion,
			Attempts: []RelayAttemptDiagnostics{{
				Attempt: 1,
				Stream: &RelayStreamDiagnostics{
					FailureStage:         "transform_event",
					FailureKind:          "invalid_event",
					FailureEventIndex:    12,
					FailureEventBytes:    384,
					IncompleteEventIndex: 12,
					IncompleteEventBytes: 384,
				},
			}},
		},
	}
	if err := database.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}

	var stored RelayLog
	if err := database.First(&stored, entry.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Diagnostics == nil || len(stored.Diagnostics.Attempts) != 1 {
		t.Fatalf("diagnostics did not round-trip: %+v", stored.Diagnostics)
	}
	stream := stored.Diagnostics.Attempts[0].Stream
	if stream == nil {
		t.Fatal("stream diagnostics did not round-trip")
	}
	if stream.FailureStage != "transform_event" ||
		stream.FailureKind != "invalid_event" ||
		stream.FailureEventIndex != 12 ||
		stream.FailureEventBytes != 384 ||
		stream.IncompleteEventIndex != 12 ||
		stream.IncompleteEventBytes != 384 {
		t.Fatalf("stream diagnostics changed during persistence: %+v", stream)
	}
}
