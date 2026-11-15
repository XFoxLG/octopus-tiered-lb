package relay

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/model"
)

func TestSelectedAttemptNetworkRetainsHedgeIdentityAndClearsOldValues(t *testing.T) {
	trace := &relayRequestTrace{id: "fixture"}
	first := &model.RelayNetworkDiagnostics{PreparationAt: 10, DispatchAt: 12, FirstEffectiveOutputAt: 20}
	second := &model.RelayNetworkDiagnostics{PreparationAt: 11, DispatchAt: 14, FirstEffectiveOutputAt: 16}
	trace.recordAttemptNetwork(1, 40, "chat", first, "primary-endpoint", "full-primary-model", 60, "primary")
	trace.recordAttemptNetwork(2, 41, "messages", second, "backup-endpoint", "full-backup-model", 61, "backup")
	trace.selectDiagnosticAttempt(2)
	if trace.diagnostics.Network != second || trace.diagnostics.Attempts[1].ChannelID != 41 || trace.diagnostics.Attempts[1].KeyID != 61 || trace.diagnostics.Attempts[1].Protocol != "messages" || trace.diagnostics.Attempts[1].HedgeLane != "backup" {
		t.Fatalf("wrong selected identity: %+v", trace.diagnostics)
	}
	trace.selectDiagnosticAttempt(3)
	if trace.diagnostics.Network != nil || trace.diagnostics.OutboundBudgetState != "unknown" {
		t.Fatal("unknown selected attempt kept previous milestones")
	}
}

func TestDispatchFirstOutputDeadlineIncludesSafeDNS(t *testing.T) {
	// Go's DNS lookup can outlive cancellation. Keep the resolver fixture in a
	// separate process so restoring shared resolver fields cannot race with its
	// A/AAAA lookup goroutines or affect other relay tests.
	const fixtureEnv = "OCTOPUS_TEST_SLOW_SAFE_DNS"
	if os.Getenv(fixtureEnv) != "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, executable, "-test.run=^TestDispatchFirstOutputDeadlineIncludesSafeDNS$", "-test.v")
		command.Env = append(os.Environ(), fixtureEnv+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated safe DNS fixture: %v\n%s", err, output)
		}
		return
	}

	// xurl's lookup is bound to this resolver at initialization. Configure the
	// same object once before the child performs any lookup; do not restore it.
	resolver := net.DefaultResolver
	var dials atomic.Int32
	resolver.PreferGo = true
	resolver.Dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		dials.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	for _, earlierBudget := range []bool{false, true} {
		t.Run(map[bool]string{false: "first_output", true: "earlier_request_budget"}[earlierBudget], func(t *testing.T) {
			ctx := context.Background()
			if earlierBudget {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 500*time.Millisecond)
				defer cancel()
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			attempt := &relayAttempt{relayRequest: &relayRequest{c: c}, firstTokenTimeOutSec: 1}
			request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://slow-safe-dns.example/v1/chat/completions", http.NoBody)
			beforeDials := dials.Load()
			started := time.Now()
			response, guard, err := attempt.sendRequest(request)
			defer guard.Close()
			if response != nil || err == nil || guard.TimedOut() == earlierBudget {
				t.Fatalf("DNS deadline attribution: response=%v timedOut=%t err=%v", response, guard.TimedOut(), err)
			}
			if earlierBudget && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.Fatal("earlier request deadline was lost")
			}
			if time.Since(started) > 3*time.Second {
				t.Fatal("safe DNS escaped first-output deadline")
			}
			if dials.Load() == beforeDials {
				t.Fatal("safe DNS bypassed the isolated resolver")
			}
			observed := attempt.networkObserver.Snapshot()
			if observed.DNSStartAt == 0 || observed.DNSEndAt == 0 || observed.DispatchAt != 0 || observed.ResponseHeadersAt != 0 {
				t.Fatalf("DNS wait fabricated HTTP dispatch: %+v", observed)
			}
		})
	}
}
