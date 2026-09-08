package node

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
)

func stallHarness(t *testing.T) (*market.Ledger, *ledgerStallWatch, *bytes.Buffer, *[]healthpb.HealthCheckResponse_ServingStatus) {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ledger := market.NewLedger(store)

	out := &bytes.Buffer{}
	var statuses []healthpb.HealthCheckResponse_ServingStatus
	w := newLedgerStallWatch(ledger, out)
	w.threshold = time.Second
	w.stacks = func() []byte { return []byte("goroutine 1 [running]: (stack elided in test)") }
	w.setServing = func(s healthpb.HealthCheckResponse_ServingStatus) {
		statuses = append(statuses, s)
	}
	return ledger, w, out, &statuses
}

// The failure this exists for: a holder that never returns. Held open on
// another goroutine, because the test goroutine taking the lock and then
// sampling would prove nothing about the case where the holder is stuck.
func TestAHeldLedgerLockIsReported(t *testing.T) {
	ledger, w, out, statuses := stallHarness(t)

	release := make(chan struct{})
	held := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = ledger.Atomically(func(market.LedgerTx) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	defer func() { close(release); wg.Wait() }()

	// A hold shorter than the threshold is ordinary and must be silent, or the
	// log fills with every block the node applies.
	w.now = func() time.Time { return time.Now().Add(500 * time.Millisecond) }
	w.sample()
	if out.Len() != 0 {
		t.Fatalf("a half-threshold hold was reported: %q", out.String())
	}

	// Past the threshold it must speak, and name the cause.
	w.now = func() time.Time { return time.Now().Add(90 * time.Second) }
	w.sample()
	got := out.String()
	// The report has to stand on its own. Each of these is a thing the operator
	// would otherwise have had to get from a SIGQUIT dump and prior knowledge of
	// this codebase: that it is stuck rather than merely slow, the reentrancy
	// cause, the frame to search the dump for, the other cause if that frame is
	// absent, and the dump itself.
	for _, want := range []string{
		"STALLED",
		"wedged rather than busy",
		"not reentrant",
		"Atomically",
		"stuck in I/O",
		"stack elided in test",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the stall report does not mention %q, so a reader still needs a stack dump:\n%s", want, got)
		}
	}
	if len(*statuses) != 1 || (*statuses)[0] != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("health statuses = %v, want one NOT_SERVING: a node that cannot read a "+
			"balance must stop claiming to serve", *statuses)
	}

	// Reported once, not once per tick: a stall does not resolve, and repeating
	// the paragraph plus a full goroutine dump every interval is its own outage.
	before := out.Len()
	w.sample()
	w.sample()
	if out.Len() != before {
		t.Fatalf("the same stall was reported again: %q", out.String()[before:])
	}
}

// A free lock is the normal state and must never be reported, whatever the
// clock says.
func TestAnIdleLedgerIsNeverReported(t *testing.T) {
	_, w, out, statuses := stallHarness(t)
	w.now = func() time.Time { return time.Now().Add(10 * time.Hour) }
	w.sample()
	if out.Len() != 0 {
		t.Fatalf("an idle ledger was reported as stalled: %q", out.String())
	}
	if len(*statuses) != 0 {
		t.Fatalf("health was touched for an idle ledger: %v", *statuses)
	}
}

// A hold that ends was slow, not stuck. Leaving the node NOT_SERVING after it
// recovers would make the alarm for the first outage into a second one.
func TestRecoveryPutsTheNodeBackInService(t *testing.T) {
	ledger, w, out, statuses := stallHarness(t)

	release := make(chan struct{})
	held := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = ledger.Atomically(func(market.LedgerTx) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	w.now = func() time.Time { return time.Now().Add(90 * time.Second) }
	w.sample()
	close(release)
	<-done

	w.now = time.Now
	w.sample()
	if !strings.Contains(out.String(), "serving again") {
		t.Fatalf("recovery was not reported:\n%s", out.String())
	}
	if len(*statuses) != 2 || (*statuses)[1] != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health statuses = %v, want NOT_SERVING then SERVING", *statuses)
	}
}

// The entry point the incident came through must be visible to the watchdog.
// That every OTHER write-lock site is too is pinned structurally in
// internal/market (TestNoWriteLockSiteBypassesTheWatchdog), because a
// behavioural test for Credit or Transfer would have to race a microsecond-long
// critical section and would pass by luck.
func TestAWriterIsVisibleToTheWatchdogWhileItRuns(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ledger := market.NewLedger(store)

	var lockedInside bool
	if err := ledger.Atomically(func(market.LedgerTx) error {
		_, lockedInside = ledger.WriteLockHeldFor(time.Now())
		return nil
	}); err != nil {
		t.Fatalf("Atomically: %v", err)
	}
	if !lockedInside {
		t.Fatal("the ledger did not report its write lock as held from inside a critical " +
			"section, so a stall there would be invisible to the watchdog")
	}
	if _, locked := ledger.WriteLockHeldFor(time.Now()); locked {
		t.Fatal("the ledger still reports its write lock as held after the section ended, " +
			"which would report every idle node as stalled")
	}
}
