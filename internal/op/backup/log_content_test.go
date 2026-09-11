package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	internaldb "github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func initializeBackupLogContentDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "backup-log-content.db")
	if err := internaldb.InitDB("sqlite", databasePath, false); err != nil {
		t.Fatalf("init database: %v", err)
	}
	if err := internaldb.InitLogDB("", "", false); err != nil {
		t.Fatalf("init shared log database: %v", err)
	}
	t.Cleanup(func() { _ = internaldb.Close() })
	return internaldb.GetDB()
}

func buildBackupTestBlob(payload []byte, createdAt int64) model.RelayLogContentBlob {
	digestBytes := sha256.Sum256(payload)
	digest := hex.EncodeToString(digestBytes[:])
	return model.RelayLogContentBlob{
		Digest:       digest,
		Encoding:     "identity",
		OriginalSize: int64(len(payload)),
		StoredSize:   int64(len(payload)),
		Payload:      append([]byte(nil), payload...),
		CreatedAt:    createdAt,
	}
}

func seedBackupTestLogFamily(t *testing.T, connection *gorm.DB, relayLogID int64, payload []byte) model.RelayLogContentBlob {
	t.Helper()
	createdAt := time.Now().Unix()
	blob := buildBackupTestBlob(payload, createdAt)
	parent := model.RelayLog{
		ID:               relayLogID,
		TraceID:          "backup-trace",
		Time:             createdAt,
		RequestModelName: "client-model",
		ActualModelName:  "provider-model",
		ContentState:     model.RelayLogContentStateReady,
		PersistenceState: model.RelayLogPersistenceReady,
	}
	attempt := model.RelayLogAttempt{
		RelayLogID:       relayLogID,
		AttemptNum:       1,
		ChannelID:        9,
		ChannelName:      "provider",
		ModelName:        "provider-model",
		AdapterType:      "chat",
		Status:           string(model.AttemptSuccess),
		HTTPStatus:       200,
		RequestPrepared:  true,
		SendStarted:      true,
		RequestBytes:     23,
		RequestComplete:  true,
		ResponseReceived: true,
		ResponseBytes:    int64(len(payload)),
		ResponseComplete: true,
		Time:             createdAt,
	}
	contentRef := model.RelayLogContentRef{
		RelayLogID:    relayLogID,
		AttemptNum:    1,
		Boundary:      model.RelayLogBoundaryUpstreamResponse,
		Slot:          0,
		Kind:          "body",
		Protocol:      "chat",
		ContentType:   "application/json",
		HTTPStatus:    200,
		State:         model.RelayLogContentStateReady,
		Complete:      true,
		CapturedBytes: int64(len(payload)),
		BlobDigest:    blob.Digest,
		CreatedAt:     createdAt,
	}
	for _, row := range []any{&parent, &attempt, &blob, &contentRef} {
		if err := connection.Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}
	return blob
}

func TestStreamedRelayLogFamilyBackupRoundTrip(t *testing.T) {
	connection := initializeBackupLogContentDatabase(t)
	payload := []byte(`{"message":"完整 Unicode 内容 🐙","unknown_extension":{"kept":true}}`)
	expectedBlob := seedBackupTestLogFamily(t, connection, 101, payload)

	exportFile, exportSize, err := CreateJSONExportFile(context.Background(), true, false, true)
	if err != nil {
		t.Fatalf("create streamed export: %v", err)
	}
	t.Cleanup(func() {
		_ = exportFile.Close()
		_ = os.Remove(exportFile.Name())
	})
	exportedJSON, err := io.ReadAll(exportFile)
	if err != nil {
		t.Fatalf("read streamed export: %v", err)
	}
	if int64(len(exportedJSON)) != exportSize || !json.Valid(exportedJSON) {
		t.Fatalf("streamed export size=%d actual=%d valid=%t", exportSize, len(exportedJSON), json.Valid(exportedJSON))
	}

	var dump model.DBDump
	if err := json.Unmarshal(exportedJSON, &dump); err != nil {
		t.Fatalf("decode streamed export: %v", err)
	}
	if dump.Version != dbDumpVersion || len(dump.RelayLogs) != 1 || len(dump.RelayLogAttempts) != 1 || len(dump.RelayLogContentRefs) != 1 || len(dump.RelayLogContentBlobs) != 1 {
		t.Fatalf("streamed log family is incomplete: version=%d parents=%d attempts=%d refs=%d blobs=%d", dump.Version, len(dump.RelayLogs), len(dump.RelayLogAttempts), len(dump.RelayLogContentRefs), len(dump.RelayLogContentBlobs))
	}

	if err := connection.Where("1 = 1").Delete(&model.RelayLogContentRef{}).Error; err != nil {
		t.Fatalf("clear refs: %v", err)
	}
	if err := connection.Where("1 = 1").Delete(&model.RelayLogAttempt{}).Error; err != nil {
		t.Fatalf("clear attempts: %v", err)
	}
	if err := connection.Where("1 = 1").Delete(&model.RelayLogContentBlob{}).Error; err != nil {
		t.Fatalf("clear blobs: %v", err)
	}
	if err := connection.Where("1 = 1").Delete(&model.RelayLog{}).Error; err != nil {
		t.Fatalf("clear parents: %v", err)
	}

	if _, err := ImportWithMode(context.Background(), &dump, model.ImportModeFull); err != nil {
		t.Fatalf("restore streamed export: %v", err)
	}
	assertBackupLogFamilyCounts(t, connection, 1, 1, 1, 1)

	var restoredBlob model.RelayLogContentBlob
	if err := connection.First(&restoredBlob, "digest = ?", expectedBlob.Digest).Error; err != nil {
		t.Fatalf("query restored blob: %v", err)
	}
	if string(restoredBlob.Payload) != string(payload) || restoredBlob.OriginalSize != int64(len(payload)) {
		t.Fatalf("restored blob changed: %#v", restoredBlob)
	}
}

func TestRelayLogBackupRejectsCorruptBlobWithoutPartialImport(t *testing.T) {
	connection := initializeBackupLogContentDatabase(t)
	existing := model.RelayLog{ID: 1, Time: 1, RequestModelName: "existing"}
	if err := connection.Create(&existing).Error; err != nil {
		t.Fatalf("seed existing parent: %v", err)
	}

	payload := []byte("corrupt")
	blob := buildBackupTestBlob(payload, 2)
	blob.Digest = "0000000000000000000000000000000000000000000000000000000000000000"
	dump := model.DBDump{
		Version:     dbDumpVersion,
		IncludeLogs: true,
		RelayLogs: []model.RelayLog{{
			ID:               2,
			Time:             2,
			ContentState:     model.RelayLogContentStateReady,
			PersistenceState: model.RelayLogPersistenceReady,
		}},
		RelayLogContentRefs: []model.RelayLogContentRef{{
			RelayLogID: 2,
			Boundary:   model.RelayLogBoundaryClientIngress,
			Kind:       "body",
			State:      model.RelayLogContentStateReady,
			BlobDigest: blob.Digest,
		}},
		RelayLogContentBlobs: []model.RelayLogContentBlobDump{{
			Digest:       blob.Digest,
			Encoding:     blob.Encoding,
			OriginalSize: blob.OriginalSize,
			StoredSize:   blob.StoredSize,
			Payload:      blob.Payload,
			CreatedAt:    blob.CreatedAt,
		}},
	}
	if _, err := ImportWithMode(context.Background(), &dump, model.ImportModeIncremental); err == nil {
		t.Fatal("corrupt relay log blob import succeeded")
	}
	assertRelayLogParentPresence(t, connection, 1, true)
	assertRelayLogParentPresence(t, connection, 2, false)
	assertBackupLogFamilyCounts(t, connection, 1, 0, 0, 0)
}

func TestRelayLogBackupRejectsReadyReferenceWithoutBlob(t *testing.T) {
	connection := initializeBackupLogContentDatabase(t)
	dump := model.DBDump{
		Version:     dbDumpVersion,
		IncludeLogs: true,
		RelayLogs: []model.RelayLog{{
			ID:           3,
			Time:         3,
			ContentState: model.RelayLogContentStateReady,
		}},
		RelayLogContentRefs: []model.RelayLogContentRef{{
			RelayLogID: 3,
			Boundary:   model.RelayLogBoundaryClientIngress,
			Kind:       "body",
			State:      model.RelayLogContentStateReady,
			BlobDigest: "missing",
		}},
	}
	if _, err := ImportWithMode(context.Background(), &dump, model.ImportModeIncremental); err == nil {
		t.Fatal("ready content reference without a blob was restored")
	}
	assertRelayLogParentPresence(t, connection, 3, false)
}

func TestVersionOneInlineRelayLogBackupRemainsReadable(t *testing.T) {
	connection := initializeBackupLogContentDatabase(t)
	dump := model.DBDump{
		Version:     1,
		IncludeLogs: true,
		RelayLogs: []model.RelayLog{{
			ID:              7,
			Time:            7,
			RequestContent:  `{"model":"legacy"}`,
			ResponseContent: `{"ok":true}`,
		}},
	}
	if _, err := ImportWithMode(context.Background(), &dump, model.ImportModeFull); err != nil {
		t.Fatalf("restore version-one inline log: %v", err)
	}
	var restored model.RelayLog
	if err := connection.First(&restored, "id = ?", 7).Error; err != nil {
		t.Fatalf("query restored inline log: %v", err)
	}
	if restored.RequestContent != dump.RelayLogs[0].RequestContent || restored.ResponseContent != dump.RelayLogs[0].ResponseContent {
		t.Fatalf("legacy inline content changed: %#v", restored)
	}
}

func TestFullRestoreWithoutLogsClearsExistingLogFamily(t *testing.T) {
	connection := initializeBackupLogContentDatabase(t)
	seedBackupTestLogFamily(t, connection, 11, []byte(`{"old":true}`))
	if _, err := ImportWithMode(context.Background(), &model.DBDump{Version: dbDumpVersion, IncludeLogs: false}, model.ImportModeFull); err != nil {
		t.Fatalf("full restore without logs: %v", err)
	}
	assertBackupLogFamilyCounts(t, connection, 0, 0, 0, 0)
}

func assertRelayLogParentPresence(t *testing.T, connection *gorm.DB, relayLogID int64, expected bool) {
	t.Helper()
	var count int64
	if err := connection.Model(&model.RelayLog{}).Where("id = ?", relayLogID).Count(&count).Error; err != nil {
		t.Fatalf("count relay log %d: %v", relayLogID, err)
	}
	if (count == 1) != expected {
		t.Fatalf("relay log %d presence = %t, want %t", relayLogID, count == 1, expected)
	}
}

func assertBackupLogFamilyCounts(t *testing.T, connection *gorm.DB, expectedParents int64, expectedAttempts int64, expectedRefs int64, expectedBlobs int64) {
	t.Helper()
	checks := []struct {
		modelValue any
		expected   int64
		name       string
	}{
		{modelValue: &model.RelayLog{}, expected: expectedParents, name: "parents"},
		{modelValue: &model.RelayLogAttempt{}, expected: expectedAttempts, name: "attempts"},
		{modelValue: &model.RelayLogContentRef{}, expected: expectedRefs, name: "refs"},
		{modelValue: &model.RelayLogContentBlob{}, expected: expectedBlobs, name: "blobs"},
	}
	for _, check := range checks {
		var count int64
		if err := connection.Model(check.modelValue).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count != check.expected {
			t.Fatalf("%s count = %d, want %d", check.name, count, check.expected)
		}
	}
}
