package relay

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestRelayBudgetUsesArrivalAndShortestDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		arrival := time.Now()
		parent, stop := context.WithTimeout(context.Background(), 180*time.Second)
		defer stop()
		time.Sleep(20 * time.Second)
		ctx, cancel, source := withRelayRequestBudget(parent, arrival, 90, 120)
		defer cancel()
		deadline, _ := ctx.Deadline()
		if source != "group" || !deadline.Equal(arrival.Add(90*time.Second)) {
			t.Fatalf("deadline reset: %s %v", source, deadline)
		}
		time.Sleep(70 * time.Second)
		synctest.Wait()
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("budget did not expire: %v", ctx.Err())
		}
		retry, end, _ := withRelayRequestBudget(ctx, arrival, 900, 0)
		defer end()
		if retry.Err() == nil {
			t.Fatal("retry extended expired request")
		}
	})
}

func TestExplicitReplayRetainsDeadlineButDetachesClientCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, stop := context.WithTimeout(context.Background(), 90*time.Second)
		replay, cancel, deadlineCancel := newRelayReplayOperationContext(parent)
		defer cancel(nil)
		defer deadlineCancel()
		stop()
		synctest.Wait()
		if replay.Err() != nil {
			t.Fatal("replay inherited client disconnect")
		}
		time.Sleep(90 * time.Second)
		synctest.Wait()
		if !errors.Is(replay.Err(), context.DeadlineExceeded) {
			t.Fatal("replay lost original deadline")
		}
	})
}

func TestReplayLastSubscriberGraceAndFinish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req, _ := newStreamTestAttempt(t, context.Background())
		session := attachStreamSession(t, req)
		ctx, cancel := context.WithCancelCause(context.Background())
		session.bindOperation(cancel)
		session.MarkClientGone()
		time.Sleep(29 * time.Second)
		if ctx.Err() != nil {
			t.Fatal("cancelled within reconnect grace")
		}
		_, leave := session.Subscribe()
		time.Sleep(10 * time.Second)
		if ctx.Err() != nil {
			t.Fatal("cancelled with subscriber present")
		}
		leave()
		time.Sleep(30 * time.Second)
		synctest.Wait()
		if !errors.Is(context.Cause(ctx), errRelayStreamClientGone) || !session.IsDone() {
			t.Fatalf("grace did not cancel operation: %v", context.Cause(ctx))
		}
		if len(session.AddPayload([]byte("data: late\n\n"))) != 0 {
			t.Fatal("finished replay accepted late payload")
		}
	})
}
