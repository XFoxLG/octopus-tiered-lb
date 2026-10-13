package relaylog

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func withRelayLogPendingBudgetForTest(t *testing.T, budgetBytes int64, drainTimeout time.Duration) {
	t.Helper()
	previousBudget := relayLogPendingContentMaxBytes
	previousTimeout := relayLogPendingContentDrainTimeout
	relayLogPendingContentMaxBytes = budgetBytes
	relayLogPendingContentDrainTimeout = drainTimeout
	t.Cleanup(func() {
		relayLogPendingContentMaxBytes = previousBudget
		relayLogPendingContentDrainTimeout = previousTimeout
	})
}

// startRelayLogFlushPumpForTest drives relayLogFlushToDB in the background,
// standing in for the production flush worker. Tests do not start the
// process-wide serial writer, so the pump calls the flush entry point directly
// (the same function the worker invokes).
func startRelayLogFlushPumpForTest(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Failure-injection tests expect errors here; the queue keeps
				// the batch for a later retry, which is exactly what the drain
				// timeout path is supposed to observe.
				_ = relayLogFlushToDB(context.Background())
			}
		}
	}()
	t.Cleanup(cancel)
}

func cachedRelayLogByID(t *testing.T, relayLogID int64) (model.RelayLog, bool) {
	t.Helper()
	cachedLogs, cacheLock := GetCacheAndLock()
	cacheLock.Lock()
	defer cacheLock.Unlock()
	for _, cached := range cachedLogs {
		if cached.ID == relayLogID {
			return cached, true
		}
	}
	return model.RelayLog{}, false
}

func contentPayloadForQueueTest(size int, fill string) model.RelayLogCapturedContent {
	return capturedText(model.RelayLogBoundaryClientIngress, 0, strings.Repeat(fill, size))
}

// The queue must flush before degrading: filling the soft budget with one
// bundle and adding a second must persist both, not mark the second
// unavailable.
func TestRelayLogQueueFlushesBeforeDegradingContent(t *testing.T) {
	connection := initializeRelayLogContentTestDatabase(t)
	withRelayLogPendingBudgetForTest(t, 1024, 5*time.Second)
	startRelayLogFlushPumpForTest(t)

	unavailableBefore := relayLogUnavailableContentBundles.Load()

	firstPayload := strings.Repeat("a", 800)
	firstID, err := RelayLogAdd(context.Background(), model.RelayLog{
		Time:             1,
		RequestModelName: "first",
		Contents:         []model.RelayLogCapturedContent{contentPayloadForQueueTest(800, "a")},
	})
	if err != nil {
		t.Fatalf("RelayLogAdd(first) failed: %v", err)
	}

	secondPayload := strings.Repeat("b", 800)
	secondID, err := RelayLogAdd(context.Background(), model.RelayLog{
		Time:             2,
		RequestModelName: "second",
		Contents:         []model.RelayLogCapturedContent{contentPayloadForQueueTest(800, "b")},
	})
	if err != nil {
		t.Fatalf("RelayLogAdd(second) failed: %v", err)
	}

	if relayLogUnavailableContentBundles.Load() != unavailableBefore {
		cached, _ := cachedRelayLogByID(t, secondID)
		t.Fatalf("content bundles marked unavailable = %d, want unchanged %d (drain should prevent degradation); reason=%q lastPersistenceError=%v",
			relayLogUnavailableContentBundles.Load(), unavailableBefore, cached.ContentUnavailableError, relayLogLastPersistenceError.Load())
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := relayLogFlushToDB(context.Background()); err != nil {
			t.Fatalf("relayLogFlushToDB failed: %v", err)
		}
		var remaining model.RelayLog
		if err := connection.First(&remaining, "id = ?", secondID).Error; err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("relay logs were not persisted within the deadline")
		}
		time.Sleep(50 * time.Millisecond)
	}

	for _, expectation := range []struct {
		id      int64
		payload string
	}{
		{firstID, firstPayload},
		{secondID, secondPayload},
	} {
		detail, err := RelayLogGetByID(context.Background(), expectation.id)
		if err != nil {
			t.Fatalf("RelayLogGetByID(%d) failed: %v", expectation.id, err)
		}
		if detail == nil || detail.ContentState != model.RelayLogContentStateReady {
			t.Fatalf("relay log %d content state = %#v, want ready", expectation.id, detail)
		}
		if len(detail.Contents) != 1 || detail.Contents[0].Text != expectation.payload {
			t.Fatalf("relay log %d content was not persisted completely", expectation.id)
		}
	}

	relayLogCacheLock.Lock()
	pendingAfterFlush := relayLogPendingContentBytes
	relayLogCacheLock.Unlock()
	if pendingAfterFlush != 0 {
		t.Fatalf("pending content bytes after flush = %d, want 0 (regression: released batch payloads zeroed the cache accounting)", pendingAfterFlush)
	}
}

// When persistence keeps failing, the bounded wait must time out and the
// content must be degraded with the real cause (the database error), never a
// generic "budget exceeded" string.
func TestRelayLogQueueDegradesWithDatabaseFailureReason(t *testing.T) {
	connection := initializeRelayLogContentTestDatabase(t)
	callbackName := "test:fail_relay_log_create_for_queue_drain"
	if err := connection.Callback().Create().Before("gorm:create").Register(callbackName, func(transaction *gorm.DB) {
		if transaction.Statement.Table == "relay_logs" {
			transaction.AddError(errors.New("injected relay log persistence failure"))
		}
	}); err != nil {
		t.Fatalf("register callback: %v", err)
	}
	t.Cleanup(func() { _ = connection.Callback().Create().Remove(callbackName) })

	withRelayLogPendingBudgetForTest(t, 1024, 600*time.Millisecond)
	startRelayLogFlushPumpForTest(t)

	if _, err := RelayLogAdd(context.Background(), model.RelayLog{
		Time:             1,
		RequestModelName: "first",
		Contents:         []model.RelayLogCapturedContent{contentPayloadForQueueTest(800, "a")},
	}); err != nil {
		t.Fatalf("RelayLogAdd(first) failed: %v", err)
	}

	secondID, err := RelayLogAdd(context.Background(), model.RelayLog{
		Time:             2,
		RequestModelName: "second",
		Contents:         []model.RelayLogCapturedContent{contentPayloadForQueueTest(800, "b")},
	})
	if err != nil {
		t.Fatalf("RelayLogAdd(second) failed: %v", err)
	}

	cached, ok := cachedRelayLogByID(t, secondID)
	if !ok {
		t.Fatalf("relay log %d not found in cache", secondID)
	}
	if cached.ContentState != model.RelayLogContentStateUnavailable {
		t.Fatalf("content state = %q, want %q", cached.ContentState, model.RelayLogContentStateUnavailable)
	}
	if !strings.Contains(cached.ContentUnavailableError, "did not drain") {
		t.Fatalf("content unavailable error = %q, want a drain-timeout reason", cached.ContentUnavailableError)
	}
	if !strings.Contains(cached.ContentUnavailableError, "injected relay log persistence failure") {
		t.Fatalf("content unavailable error = %q, want the underlying database failure", cached.ContentUnavailableError)
	}
	if strings.Contains(cached.ContentUnavailableError, "budget exceeded") {
		t.Fatalf("content unavailable error = %q, must not fall back to the old budget-exceeded text", cached.ContentUnavailableError)
	}
	if cached.RequestContent != "" || cached.ResponseContent != "" {
		t.Fatalf("degraded bundle still holds inline payload bytes: %#v", cached)
	}
	for _, content := range cached.Contents {
		if content.State != model.RelayLogContentStateUnavailable {
			t.Fatalf("degraded content state = %q, want %q", content.State, model.RelayLogContentStateUnavailable)
		}
		if content.Data != nil || content.Text != "" {
			t.Fatalf("degraded content still holds payload bytes: %#v", content)
		}
	}
}

// A bundle larger than the hard safety cap is the only byte-based degradation
// path: it must count the event and write one error-log entry.
func TestRelayLogQueueHardCapDegradesAndCounts(t *testing.T) {
	connection := initializeRelayLogContentTestDatabase(t)
	withRelayLogPendingBudgetForTest(t, 1024, 200*time.Millisecond)

	degradeCountBefore := relayLogContentHardCapDegrades.Load()

	relayLogID, err := RelayLogAdd(context.Background(), model.RelayLog{
		Time:             3,
		RequestModelName: "huge",
		Contents:         []model.RelayLogCapturedContent{contentPayloadForQueueTest(4096, "c")},
	})
	if err != nil {
		t.Fatalf("RelayLogAdd(huge) failed: %v", err)
	}
	if got := relayLogContentHardCapDegrades.Load(); got != degradeCountBefore+1 {
		t.Fatalf("hard cap degrade counter = %d, want %d", got, degradeCountBefore+1)
	}

	if err := relayLogFlushToDB(context.Background()); err != nil {
		t.Fatalf("relayLogFlushToDB failed: %v", err)
	}
	var persisted model.RelayLog
	if err := connection.First(&persisted, "id = ?", relayLogID).Error; err != nil {
		t.Fatalf("load persisted relay log: %v", err)
	}
	if persisted.ContentState != model.RelayLogContentStateUnavailable {
		t.Fatalf("content state = %q, want %q", persisted.ContentState, model.RelayLogContentStateUnavailable)
	}
	if !strings.Contains(persisted.ContentUnavailableError, "hard memory safety cap") {
		t.Fatalf("content unavailable error = %q, want the hard-cap reason", persisted.ContentUnavailableError)
	}

	var errorLogCount int64
	if err := connection.Model(&model.ErrorLog{}).
		Where("message LIKE ?", "%hard_cap_degrade_total%").
		Count(&errorLogCount).Error; err != nil {
		t.Fatalf("count hard-cap error logs: %v", err)
	}
	if errorLogCount != 1 {
		t.Fatalf("hard-cap error logs = %d, want 1", errorLogCount)
	}
}
