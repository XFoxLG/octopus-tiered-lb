package relaylog

import (
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
	"gorm.io/gorm"
)

func initializeRelayLogContentTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "relay-log-content.db")
	if err := db.InitDB("sqlite", databasePath, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	if err := db.InitLogDB("", "", false); err != nil {
		t.Fatalf("InitLogDB(shared) failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := setting.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache failed: %v", err)
	}
	if err := setting.SetString(model.SettingKeyRelayLogKeepEnabled, "true"); err != nil {
		t.Fatalf("enable relay logs: %v", err)
	}
	if err := setting.SetString(model.SettingKeyRelayLogContentEnabled, "true"); err != nil {
		t.Fatalf("enable relay log content: %v", err)
	}
	t.Cleanup(SetCacheForTest(nil))
	return db.GetLogDB()
}

func capturedText(boundary string, attemptNumber int, payload string) model.RelayLogCapturedContent {
	return model.RelayLogCapturedContent{
		RelayLogContentRef: model.RelayLogContentRef{
			AttemptNum:    attemptNumber,
			Boundary:      boundary,
			Kind:          "body",
			ContentType:   "application/json",
			State:         model.RelayLogContentStateReady,
			Complete:      true,
			CapturedBytes: int64(len(payload)),
			CreatedAt:     100,
		},
		Data: []byte(payload),
	}
}

func TestRelayLogFlushPersistsParentAttemptsAndBoundariesTogether(t *testing.T) {
	connection := initializeRelayLogContentTestDatabase(t)
	relayLog := model.RelayLog{
		Time:             100,
		TraceID:          "trace-four-boundaries",
		RequestModelName: "client-model",
		ActualModelName:  "upstream-model",
		Attempts: []model.ChannelAttempt{
			{ChannelID: 11, ChannelName: "first", ModelName: "upstream-model", AttemptNum: 1, Status: model.AttemptFailed},
			{ChannelID: 22, ChannelName: "second", ModelName: "upstream-model", AttemptNum: 2, Status: model.AttemptSuccess},
		},
		TotalAttempts: 2,
		Contents: []model.RelayLogCapturedContent{
			capturedText(model.RelayLogBoundaryClientIngress, 0, `{"model":"client-model"}`),
			capturedText(model.RelayLogBoundaryUpstreamRequest, 2, `{"model":"upstream-model"}`),
			capturedText(model.RelayLogBoundaryUpstreamResponse, 2, `{"choices":[{"text":"ok"}]}`),
			capturedText(model.RelayLogBoundaryClientEgress, 0, `{"choices":[{"message":{"content":"ok"}}]}`),
		},
	}

	relayLogID, err := RelayLogAdd(context.Background(), relayLog)
	if err != nil {
		t.Fatalf("RelayLogAdd failed: %v", err)
	}
	if relayLogID == 0 {
		t.Fatal("RelayLogAdd returned zero ID")
	}
	if err := relayLogFlushToDB(context.Background()); err != nil {
		t.Fatalf("relayLogFlushToDB failed: %v", err)
	}

	var persisted model.RelayLog
	if err := connection.First(&persisted, "id = ?", relayLogID).Error; err != nil {
		t.Fatalf("query persisted relay log: %v", err)
	}
	if persisted.TraceID != "trace-four-boundaries" {
		t.Fatalf("TraceID = %q, want trace-four-boundaries", persisted.TraceID)
	}
	if persisted.ContentState != model.RelayLogContentStateReady {
		t.Fatalf("ContentState = %q, want ready", persisted.ContentState)
	}

	var attemptCount int64
	if err := connection.Model(&model.RelayLogAttempt{}).Where("relay_log_id = ?", relayLogID).Count(&attemptCount).Error; err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if attemptCount != 2 {
		t.Fatalf("attempt count = %d, want 2", attemptCount)
	}

	var contentRefCount int64
	if err := connection.Model(&model.RelayLogContentRef{}).Where("relay_log_id = ?", relayLogID).Count(&contentRefCount).Error; err != nil {
		t.Fatalf("count content refs: %v", err)
	}
	if contentRefCount != 4 {
		t.Fatalf("content ref count = %d, want 4", contentRefCount)
	}

	detail, err := RelayLogGetByID(context.Background(), relayLogID)
	if err != nil {
		t.Fatalf("RelayLogGetByID failed: %v", err)
	}
	if detail == nil || len(detail.Contents) != 4 {
		t.Fatalf("detail contents = %#v, want four boundaries", detail)
	}
	for _, content := range detail.Contents {
		if content.Text == "" {
			t.Fatalf("text boundary %s attempt %d was not hydrated", content.Boundary, content.AttemptNum)
		}
	}
}

func TestRelayLogContentBlobsDeduplicateIdenticalPayloads(t *testing.T) {
	connection := initializeRelayLogContentTestDatabase(t)
	sharedPayload := `{"same":true}`
	relayLog := model.RelayLog{
		Time:             200,
		RequestModelName: "model",
		Contents: []model.RelayLogCapturedContent{
			capturedText(model.RelayLogBoundaryClientIngress, 0, sharedPayload),
			capturedText(model.RelayLogBoundaryUpstreamRequest, 1, sharedPayload),
		},
	}
	if _, err := RelayLogAdd(context.Background(), relayLog); err != nil {
		t.Fatalf("RelayLogAdd failed: %v", err)
	}
	if err := relayLogFlushToDB(context.Background()); err != nil {
		t.Fatalf("relayLogFlushToDB failed: %v", err)
	}

	var blobCount int64
	if err := connection.Model(&model.RelayLogContentBlob{}).Count(&blobCount).Error; err != nil {
		t.Fatalf("count blobs: %v", err)
	}
	if blobCount != 1 {
		t.Fatalf("blob count = %d, want 1 deduplicated blob", blobCount)
	}
}

func TestRelayLogFlushRollsBackParentWhenBoundaryPersistenceFails(t *testing.T) {
	connection := initializeRelayLogContentTestDatabase(t)
	callbackName := "test:fail_relay_log_content_ref_create"
	if err := connection.Callback().Create().Before("gorm:create").Register(callbackName, func(transaction *gorm.DB) {
		if transaction.Statement.Table == "relay_log_content_refs" {
			transaction.AddError(errors.New("injected content ref failure"))
		}
	}); err != nil {
		t.Fatalf("register callback: %v", err)
	}
	t.Cleanup(func() { _ = connection.Callback().Create().Remove(callbackName) })

	relayLogID, err := RelayLogAdd(context.Background(), model.RelayLog{
		Time:             300,
		RequestModelName: "model",
		Attempts: []model.ChannelAttempt{
			{ChannelID: 9, ChannelName: "channel", AttemptNum: 1, Status: model.AttemptFailed},
		},
		Contents: []model.RelayLogCapturedContent{
			capturedText(model.RelayLogBoundaryClientIngress, 0, `{"model":"model"}`),
		},
	})
	if err != nil {
		t.Fatalf("RelayLogAdd failed: %v", err)
	}
	if err := relayLogFlushToDB(context.Background()); err == nil {
		t.Fatal("relayLogFlushToDB succeeded, want injected failure")
	}

	var parentCount int64
	if err := connection.Model(&model.RelayLog{}).Where("id = ?", relayLogID).Count(&parentCount).Error; err != nil {
		t.Fatalf("count parents: %v", err)
	}
	if parentCount != 0 {
		t.Fatalf("parent count = %d, want transaction rollback", parentCount)
	}
	var attemptCount int64
	if err := connection.Model(&model.RelayLogAttempt{}).Where("relay_log_id = ?", relayLogID).Count(&attemptCount).Error; err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if attemptCount != 0 {
		t.Fatalf("attempt count = %d, want transaction rollback", attemptCount)
	}
}

func TestRelayLogAgeRetentionPreservesSharedBlobUntilLastReferenceExpires(t *testing.T) {
	connection := initializeRelayLogContentTestDatabase(t)
	if err := setting.SetString(model.SettingKeyRelayLogContentKeepPeriod, "7"); err != nil {
		t.Fatalf("set content age retention: %v", err)
	}
	if err := setting.SetString(model.SettingKeyRelayLogContentKeepSizeMB, "0"); err != nil {
		t.Fatalf("disable content size retention: %v", err)
	}

	sharedPayload := `{"shared":"attachment"}`
	oldLogID, err := RelayLogAdd(context.Background(), model.RelayLog{
		Time: time.Now().Add(-10 * 24 * time.Hour).Unix(),
		Contents: []model.RelayLogCapturedContent{
			capturedText(model.RelayLogBoundaryClientIngress, 0, sharedPayload),
		},
	})
	if err != nil {
		t.Fatalf("add old relay log: %v", err)
	}
	newLogID, err := RelayLogAdd(context.Background(), model.RelayLog{
		Time: time.Now().Unix(),
		Contents: []model.RelayLogCapturedContent{
			capturedText(model.RelayLogBoundaryClientIngress, 0, sharedPayload),
		},
	})
	if err != nil {
		t.Fatalf("add new relay log: %v", err)
	}
	if err := relayLogFlushToDB(context.Background()); err != nil {
		t.Fatalf("flush relay logs: %v", err)
	}

	if err := expireRelayLogContentByAge(context.Background(), connection); err != nil {
		t.Fatalf("expire old content: %v", err)
	}
	assertRelayLogContentState(t, connection, oldLogID, model.RelayLogContentStateExpired)
	assertRelayLogContentState(t, connection, newLogID, model.RelayLogContentStateReady)
	assertRelayLogContentRowCounts(t, connection, oldLogID, 0, 1)
	assertRelayLogContentRowCounts(t, connection, newLogID, 1, 1)

	if err := connection.Model(&model.RelayLog{}).
		Where("id = ?", newLogID).
		Update("time", time.Now().Add(-10*24*time.Hour).Unix()).Error; err != nil {
		t.Fatalf("age second relay log: %v", err)
	}
	if err := expireRelayLogContentByAge(context.Background(), connection); err != nil {
		t.Fatalf("expire final shared reference: %v", err)
	}
	assertRelayLogContentRowCounts(t, connection, newLogID, 0, 0)
}

func TestRelayLogSizeRetentionExpiresOldestWholeBundle(t *testing.T) {
	connection := initializeRelayLogContentTestDatabase(t)
	if err := setting.SetString(model.SettingKeyRelayLogContentKeepPeriod, "0"); err != nil {
		t.Fatalf("disable content age retention: %v", err)
	}
	if err := setting.SetString(model.SettingKeyRelayLogContentKeepSizeMB, "1"); err != nil {
		t.Fatalf("set content size retention: %v", err)
	}

	oldPayload := make([]byte, 700*1024)
	newPayload := make([]byte, 700*1024)
	if _, err := rand.Read(oldPayload); err != nil {
		t.Fatalf("build old payload: %v", err)
	}
	if _, err := rand.Read(newPayload); err != nil {
		t.Fatalf("build new payload: %v", err)
	}
	buildBinaryContent := func(payload []byte) model.RelayLogCapturedContent {
		return model.RelayLogCapturedContent{
			RelayLogContentRef: model.RelayLogContentRef{
				Boundary:      model.RelayLogBoundaryClientIngress,
				Kind:          "attachment",
				ContentType:   "application/octet-stream",
				State:         model.RelayLogContentStateReady,
				Complete:      true,
				CapturedBytes: int64(len(payload)),
				CreatedAt:     time.Now().Unix(),
			},
			Data: payload,
		}
	}

	oldLogID, err := RelayLogAdd(context.Background(), model.RelayLog{
		Time:     time.Now().Add(-time.Hour).Unix(),
		Contents: []model.RelayLogCapturedContent{buildBinaryContent(oldPayload)},
	})
	if err != nil {
		t.Fatalf("add old bundle: %v", err)
	}
	newLogID, err := RelayLogAdd(context.Background(), model.RelayLog{
		Time:     time.Now().Unix(),
		Contents: []model.RelayLogCapturedContent{buildBinaryContent(newPayload)},
	})
	if err != nil {
		t.Fatalf("add new bundle: %v", err)
	}
	if err := relayLogFlushToDB(context.Background()); err != nil {
		t.Fatalf("flush bundles: %v", err)
	}
	if err := expireRelayLogContentBySize(context.Background(), connection); err != nil {
		t.Fatalf("expire content by size: %v", err)
	}

	assertRelayLogContentState(t, connection, oldLogID, model.RelayLogContentStateExpired)
	assertRelayLogContentState(t, connection, newLogID, model.RelayLogContentStateReady)
	assertRelayLogContentRowCounts(t, connection, oldLogID, 0, 1)
	assertRelayLogContentRowCounts(t, connection, newLogID, 1, 1)
}

func TestRelayLogMaintenanceDiscardsOnlyPreRestorePendingLogs(t *testing.T) {
	initializeRelayLogContentTestDatabase(t)
	oldLogID, err := RelayLogAdd(context.Background(), model.RelayLog{Time: 1, RequestModelName: "old"})
	if err != nil {
		t.Fatalf("add pre-maintenance log: %v", err)
	}

	finishMaintenance := BeginMaintenance(true)
	newLogID, err := RelayLogAdd(context.Background(), model.RelayLog{Time: 2, RequestModelName: "new"})
	if err != nil {
		finishMaintenance(false)
		t.Fatalf("add log during maintenance: %v", err)
	}
	finishMaintenance(true)

	cachedLogs, cacheLock := GetCacheAndLock()
	cacheLock.Lock()
	defer cacheLock.Unlock()
	if len(cachedLogs) != 1 || cachedLogs[0].ID != newLogID {
		t.Fatalf("pending logs after maintenance = %#v, want only %d (discarded %d)", cachedLogs, newLogID, oldLogID)
	}
}

func TestRelayLogContentDecodeRejectsCorruptBlob(t *testing.T) {
	blob, err := buildRelayLogContentBlob([]byte("forensic payload"), time.Now().Unix())
	if err != nil {
		t.Fatalf("build content blob: %v", err)
	}
	blob.Payload[0] ^= 0xff
	if _, err := decodeRelayLogContentBlob(blob); err == nil {
		t.Fatal("corrupt content blob decoded without an integrity error")
	}
}

func assertRelayLogContentState(t *testing.T, connection *gorm.DB, relayLogID int64, expectedState string) {
	t.Helper()
	var relayLog model.RelayLog
	if err := connection.Select("id", "content_state").First(&relayLog, "id = ?", relayLogID).Error; err != nil {
		t.Fatalf("query relay log %d: %v", relayLogID, err)
	}
	if relayLog.ContentState != expectedState {
		t.Fatalf("relay log %d content state = %q, want %q", relayLogID, relayLog.ContentState, expectedState)
	}
}

func assertRelayLogContentRowCounts(t *testing.T, connection *gorm.DB, relayLogID int64, expectedRefs int64, expectedBlobs int64) {
	t.Helper()
	var referenceCount int64
	if err := connection.Model(&model.RelayLogContentRef{}).Where("relay_log_id = ?", relayLogID).Count(&referenceCount).Error; err != nil {
		t.Fatalf("count refs for relay log %d: %v", relayLogID, err)
	}
	if referenceCount != expectedRefs {
		t.Fatalf("relay log %d ref count = %d, want %d", relayLogID, referenceCount, expectedRefs)
	}
	var blobCount int64
	if err := connection.Model(&model.RelayLogContentBlob{}).Count(&blobCount).Error; err != nil {
		t.Fatalf("count blobs: %v", err)
	}
	if blobCount != expectedBlobs {
		t.Fatalf("blob count = %d, want %d", blobCount, expectedBlobs)
	}
}
