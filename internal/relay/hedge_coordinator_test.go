package relay

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestCoordinateRelayHedgePrimaryWinsAndCancelsBackup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var mu sync.Mutex
	var writes [][]byte
	backupCancelled := make(chan struct{})
	backupStarted := make(chan struct{})
	lane := func(text string, delay time.Duration, visible bool, cancelled chan struct{}) relayHedgeLane {
		return func(ctx context.Context, emit func(relayHedgeFrame) error) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				if cancelled != nil {
					close(cancelled)
				}
				return context.Cause(ctx)
			}
			return emit(relayHedgeFrame{Payload: []byte(text), Visible: visible})
		}
	}
	primary := func(ctx context.Context, emit func(relayHedgeFrame) error) error {
		select {
		case <-backupStarted:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
		return emit(relayHedgeFrame{Payload: []byte("primary"), Visible: true})
	}
	outcome, err := coordinateRelayHedge(ctx, 0, primary, func() relayHedgeLane {
		close(backupStarted)
		return lane("backup", 200*time.Millisecond, true, backupCancelled)
	}, func(_ int, data []byte, _ http.Header) error {
		mu.Lock()
		writes = append(writes, append([]byte(nil), data...))
		mu.Unlock()
		return nil
	})
	if err != nil && !errors.Is(err, errHedgeLost) {
		t.Fatal(err)
	}
	if outcome.Winner != 0 || !outcome.BackupStarted {
		t.Fatalf("outcome=%+v", outcome)
	}
	select {
	case <-backupCancelled:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("backup was not cancelled")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(writes) != 1 || string(writes[0]) != "primary" {
		t.Fatalf("writes=%q", writes)
	}
}

func TestCoordinateRelayHedgeBackupWinsAfterDelay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var writes [][]byte
	lane := func(text string, delay time.Duration) relayHedgeLane {
		return func(ctx context.Context, emit func(relayHedgeFrame) error) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
			return emit(relayHedgeFrame{Payload: []byte(text), Visible: true})
		}
	}
	outcome, err := coordinateRelayHedge(ctx, 5*time.Millisecond, lane("slow", 30*time.Millisecond), func() relayHedgeLane { return lane("backup", 1*time.Millisecond) }, func(_ int, data []byte, _ http.Header) error {
		writes = append(writes, append([]byte(nil), data...))
		return nil
	})
	if err != nil && !errors.Is(err, errHedgeLost) {
		t.Fatal(err)
	}
	if outcome.Winner != 1 || string(writes[0]) != "backup" {
		t.Fatalf("outcome=%+v writes=%q", outcome, writes)
	}
}

func TestCoordinateRelayHedgeWriteFailureNeverSwitches(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	writes := 0
	outcome, err := coordinateRelayHedge(ctx, 0, func(ctx context.Context, emit func(relayHedgeFrame) error) error {
		return emit(relayHedgeFrame{Payload: []byte("primary"), Visible: true})
	}, func() relayHedgeLane {
		return func(ctx context.Context, emit func(relayHedgeFrame) error) error {
			return emit(relayHedgeFrame{Payload: []byte("backup"), Visible: true})
		}
	}, func(int, []byte, http.Header) error { writes++; return errors.New("client disconnected") })
	if err == nil || writes != 1 || outcome.Winner >= 0 {
		t.Fatalf("outcome=%+v err=%v writes=%d", outcome, err, writes)
	}
}

func TestCoordinateRelayHedgeBuffersProtocolPrefixAndDoesNotStartLateBackup(t *testing.T) {
	var output string
	starts := 0
	primary := func(ctx context.Context, emit func(relayHedgeFrame) error) error {
		for _, frame := range []relayHedgeFrame{
			{Payload: []byte("role:")},
			{Payload: []byte("tool"), Visible: true},
			{Payload: []byte(":terminal")},
		} {
			if err := emit(frame); err != nil {
				return err
			}
		}
		return nil
	}
	result, err := coordinateRelayHedge(context.Background(), time.Hour, primary, func() relayHedgeLane { starts++; return nil }, func(_ int, payload []byte, _ http.Header) error { output += string(payload); return nil })
	if err != nil || result.Winner != 0 || starts != 0 || output != "role:tool:terminal" {
		t.Fatalf("result=%+v err=%v starts=%d output=%q", result, err, starts, output)
	}
}
func TestCoordinateRelayHedgeBothFailWithoutClientOutput(t *testing.T) {
	first, second := errors.New("primary failed"), errors.New("backup failed")
	lane := func(failure error) relayHedgeLane {
		return func(context.Context, func(relayHedgeFrame) error) error { return failure }
	}
	result, err := coordinateRelayHedge(context.Background(), time.Hour, lane(first), func() relayHedgeLane { return lane(second) }, func(int, []byte, http.Header) error { t.Error("failed lanes wrote client output"); return nil })
	if result.Winner != -1 || !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
func TestCoordinateRelayHedgeInsufficientCapacityStaysSerial(t *testing.T) {
	release := make(chan struct{})
	lane := func(ctx context.Context, emit func(relayHedgeFrame) error) error {
		<-release
		return emit(relayHedgeFrame{Payload: []byte("primary"), Visible: true})
	}
	result, err := coordinateRelayHedge(context.Background(), 0, lane, func() relayHedgeLane { close(release); return nil }, func(int, []byte, http.Header) error { return nil })
	if err != nil || result.BackupStarted || result.Winner != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCoordinateRelayHedgeClientCancellationWaitsForBothLanes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	var startOnce sync.Once
	lane := func(ctx context.Context, emit func(relayHedgeFrame) error) error {
		startOnce.Do(func() { close(started) })
		<-ctx.Done()
		return context.Cause(ctx)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = coordinateRelayHedge(ctx, time.Hour, lane, func() relayHedgeLane {
			t.Error("client cancellation must prevent backup startup")
			return nil
		}, func(int, []byte, http.Header) error {
			t.Error("cancelled lanes must not write client output")
			return nil
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("primary lane did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("coordinator did not return after client cancellation")
	}
}

func TestCoordinateRelayHedgeReasoningOnlyDoesNotWin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var writes []string
	primary := func(ctx context.Context, emit func(relayHedgeFrame) error) error {
		if err := emit(relayHedgeFrame{Payload: []byte("reasoning"), Visible: false}); err != nil {
			return err
		}
		return nil
	}
	backup := func(ctx context.Context, emit func(relayHedgeFrame) error) error {
		return emit(relayHedgeFrame{Payload: []byte("answer"), Visible: true})
	}
	result, err := coordinateRelayHedge(ctx, 0, primary, func() relayHedgeLane { return backup }, func(index int, payload []byte, _ http.Header) error {
		writes = append(writes, string(payload))
		return nil
	})
	if err != nil || result.Winner != 1 || len(writes) != 1 || writes[0] != "answer" {
		t.Fatalf("result=%+v err=%v writes=%q", result, err, writes)
	}
}

func TestCoordinateRelayHedgeTerminalOnlyCanWin(t *testing.T) {
	result, err := coordinateRelayHedge(context.Background(), time.Hour,
		func(ctx context.Context, emit func(relayHedgeFrame) error) error {
			return emit(relayHedgeFrame{Payload: []byte("policy terminal"), Terminal: true})
		},
		func() relayHedgeLane { t.Fatal("terminal lane should win before hedge delay"); return nil },
		func(_ int, payload []byte, _ http.Header) error {
			if string(payload) != "policy terminal" {
				t.Fatalf("payload=%q", payload)
			}
			return nil
		})
	if err != nil || result.Winner != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
