package relay

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestFirstVisibleDeadlineIsRequestWide(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req := &relayRequest{}

		first := req.withFirstVisibleDeadline(context.Background(), 2)
		defer first.Close()
		firstDeadline := req.firstVisibleDeadline.deadline

		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		retry := req.withFirstVisibleDeadline(context.Background(), 2)
		defer retry.Close()

		if got := req.firstVisibleDeadline.deadline; !got.Equal(firstDeadline) {
			t.Fatalf("retry reset deadline: first=%s retry=%s", firstDeadline, got)
		}
		if remaining, ok := req.remainingFirstVisibleDeadline(); !ok || remaining < 1490*time.Millisecond || remaining > 1500*time.Millisecond {
			t.Fatalf("remaining deadline = %s, ok=%v", remaining, ok)
		}
	})
}

func TestFirstVisibleDeadlineDoesNotOverrideEarlierBudget(t *testing.T) {
	req := &relayRequest{}
	budgetCtx, cancelBudget := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelBudget()
	guard := req.withFirstVisibleDeadline(budgetCtx, 60)
	defer guard.Close()
	<-guard.Context().Done()
	if guard.TimedOut() {
		t.Fatal("earlier request budget was misclassified as first-visible timeout")
	}
	if req.firstVisibleDeadlineApplies(guard.Context()) {
		t.Fatal("earlier request budget was misclassified as first-visible timeout")
	}
}

func TestFirstVisibleDispatchTimerStopsAfterHeaders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req := &relayRequest{}
		guard := req.withFirstVisibleDeadline(context.Background(), 1)
		defer guard.Close()

		time.Sleep(500 * time.Millisecond)
		guard.ResponseHeadersReceived()
		time.Sleep(time.Second)
		synctest.Wait()

		if guard.TimedOut() {
			t.Fatal("first-visible timer fired after response headers")
		}
		if err := guard.Context().Err(); err != nil {
			t.Fatalf("dispatch context was cut after headers: %v", err)
		}
	})
}

func TestFirstVisibleDispatchTimerCancelsHeaderWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req := &relayRequest{}
		guard := req.withFirstVisibleDeadline(context.Background(), 1)
		defer guard.Close()

		time.Sleep(time.Second)
		synctest.Wait()
		if !guard.TimedOut() {
			t.Fatal("first-visible timer did not expire")
		}
		if err := guard.Context().Err(); err == nil {
			t.Fatal("dispatch context remained live after first-visible timeout")
		}
	})
}

func TestRelayRequestCopySharesFirstVisibleStatePointer(t *testing.T) {
	original := &relayRequest{}
	original.ensureFirstVisibleDeadlineState()
	cloned := *original

	if cloned.firstVisibleDeadline != original.firstVisibleDeadline {
		t.Fatal("relayRequest copy detached first-visible state")
	}
}
