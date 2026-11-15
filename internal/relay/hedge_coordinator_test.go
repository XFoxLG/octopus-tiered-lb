package relay

import (
	"context"
	"errors"
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
	}, func(_ int, frame relayHedgeFrame) error {
		mu.Lock()
		writes = append(writes, append([]byte(nil), frame.Payload...))
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
	outcome, err := coordinateRelayHedge(ctx, 5*time.Millisecond, lane("slow", 30*time.Millisecond), func() relayHedgeLane { return lane("backup", 1*time.Millisecond) }, func(_ int, frame relayHedgeFrame) error {
		writes = append(writes, append([]byte(nil), frame.Payload...))
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
	}, func(int, relayHedgeFrame) error { writes++; return errors.New("client disconnected") })
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
	result, err := coordinateRelayHedge(context.Background(), time.Hour, primary, func() relayHedgeLane { starts++; return nil }, func(_ int, frame relayHedgeFrame) error { output += string(frame.Payload); return nil })
	if err != nil || result.Winner != 0 || starts != 0 || output != "role:tool:terminal" {
		t.Fatalf("result=%+v err=%v starts=%d output=%q", result, err, starts, output)
	}
}
func TestCoordinateRelayHedgeBothFailWithoutClientOutput(t *testing.T) {
	first, second := errors.New("primary failed"), errors.New("backup failed")
	lane := func(failure error) relayHedgeLane {
		return func(context.Context, func(relayHedgeFrame) error) error { return failure }
	}
	result, err := coordinateRelayHedge(context.Background(), time.Hour, lane(first), func() relayHedgeLane { return lane(second) }, func(int, relayHedgeFrame) error { t.Error("failed lanes wrote client output"); return nil })
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
	result, err := coordinateRelayHedge(context.Background(), 0, lane, func() relayHedgeLane { close(release); return nil }, func(int, relayHedgeFrame) error { return nil })
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
		}, func(int, relayHedgeFrame) error {
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
	result, err := coordinateRelayHedge(ctx, 0, primary, func() relayHedgeLane { return backup }, func(_ int, frame relayHedgeFrame) error {
		writes = append(writes, string(frame.Payload))
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
		func(_ int, frame relayHedgeFrame) error {
			if string(frame.Payload) != "policy terminal" {
				t.Fatalf("payload=%q", frame.Payload)
			}
			return nil
		})
	if err != nil || result.Winner != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCoordinateRelayHedgeTransportsControlWithoutSelectingWinner(t *testing.T) {
	var writes []relayHedgeFrame
	primary := func(ctx context.Context, emit func(relayHedgeFrame) error) error {
		if err := emit(relayHedgeFrame{Payload: []byte(": control\n\n"), Control: true}); err != nil {
			return err
		}
		return emit(relayHedgeFrame{Payload: []byte("answer"), Visible: true})
	}
	result, err := coordinateRelayHedge(context.Background(), time.Hour, primary,
		func() relayHedgeLane {
			t.Fatal("control frame must not start the backup")
			return nil
		},
		func(_ int, frame relayHedgeFrame) error {
			writes = append(writes, frame)
			return nil
		},
	)
	if err != nil || result.Winner != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(writes) != 2 || !writes[0].Control || writes[0].Visible || writes[0].Terminal ||
		writes[1].Control || !writes[1].Visible || string(writes[1].Payload) != "answer" {
		t.Fatalf("control/winner frames = %#v", writes)
	}
}

func TestCoordinateRelayHedgeControlWriteFailureAbortsLanes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	backupStarted := make(chan struct{})
	backupCancelled := make(chan struct{})
	primary := func(ctx context.Context, emit func(relayHedgeFrame) error) error {
		return emit(relayHedgeFrame{Payload: []byte(": control\n\n"), Control: true})
	}
	backup := func(ctx context.Context, emit func(relayHedgeFrame) error) error {
		close(backupStarted)
		<-ctx.Done()
		close(backupCancelled)
		return context.Cause(ctx)
	}
	writeErr := errors.New("control write failed")
	result, err := coordinateRelayHedge(ctx, 0, primary,
		func() relayHedgeLane { return backup },
		func(_ int, frame relayHedgeFrame) error {
			if !frame.Control {
				t.Fatalf("unexpected non-control write: %#v", frame)
			}
			return writeErr
		},
	)
	if !errors.Is(err, writeErr) || result.Winner >= 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	select {
	case <-backupStarted:
	default:
		t.Fatal("backup did not start")
	}
	select {
	case <-backupCancelled:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("control write failure did not cancel the backup lane")
	}
}
