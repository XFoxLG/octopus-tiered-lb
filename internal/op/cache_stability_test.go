package op

import (
	"context"
	"errors"
	"testing"
)

func TestCacheStageDoesNotRetryPermanentErrors(t *testing.T) {
	attempts := 0
	err := runCacheInitStage(context.Background(), 0, func(context.Context) error { attempts++; return errors.New("column not found") })
	if err == nil || attempts != 1 {
		t.Fatalf("permanent error retried: attempts=%d err=%v", attempts, err)
	}
}

func TestCacheStageRespectsCancelledBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runCacheInitStage(ctx, 0, func(context.Context) error { t.Fatal("started outside budget"); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
