package node

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
)

// The threshold is real and small rather than a faked clock. An injected
// time.Now is what made the first version of this wrong: comparing an injected
// wall clock against a wall-clock stamp discards Go's monotonic reading, so a
// forward clock step reported a stall that never happened. Lock ages now come
// from a monotonic counter, and the honest way to test that is to actually hold
// the lock for longer than a small threshold.
const testStallThreshold = 40 * time.Millisecond

type stallHarness struct {
	ledger   *market.Ledger
	watch    *ledgerStallWatch
	out      *bytes.Buffer
	statuses *[]healthpb.HealthCheckResponse_ServingStatus
}

func newStallHarness(t *testing.T) *stallHarness {
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
	w.threshold = testStallThreshold
	w.stacks = func() []byte { return []byte("goroutine 1 [running]: (stack elided in test)") }
	w.setServing = func(s healthpb.HealthCheckResponse_ServingStatus) {
		statuses = append(statuses, s)
	}
	return &stallHarness{ledger: ledger, watch: w, out: out, statuses: &statuses}
}

// hold parks a goroutine inside a ledger section and returns a release func.
func (h *stallHarness) hold(t *testing.T, section func(fn func(market.LedgerTx) error) error) func() {
	t.Helper()
	in := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = section(func(market.LedgerTx) error {
			close(in)
			<-release
			return nil
		})
	}()
	<-in
	var once sync.Once
	return func() { once.Do(func() { close(release); wg.Wait() }) }
}

// A WRITER that never returns. This is the failure that shipped.
func TestAStuckWriterIsReported(t *testing.T) {
	h := newStallHarness(t)
	release := h.hold(t, h.ledger.Atomically)
	defer release()

	// An ordinary hold must stay silent, or the log fills with every block the
	// node applies. Asserted with a threshold that CANNOT have elapsed rather
	// than by holding briefly: under -race everything is ten times slower, and a
	// "this was quick" assertion measured against a 40ms threshold is a flake
	// waiting for a loaded CI box.
	h.watch.threshold = time.Hour
	h.watch.sample()
	if h.out.Len() != 0 {
		t.Fatalf("a hold far below the threshold was reported: %q", h.out.String())
	}

	// Now a threshold the hold has certainly passed.
	h.watch.threshold = testStallThreshold
	time.Sleep(2 * testStallThreshold)
	h.watch.sample()
	got := h.out.String()
	// Everything a reader needs without a SIGQUIT dump and prior knowledge of
	// this codebase: that it is stuck rather than slow, the reentrancy cause,
	// the frame to search for, the other explanation, and the dump.
	for _, want := range []string{
		"STALLED", "wedged rather than busy", "not reentrant",
		"Atomically", "fsyncs once per transfer", "stack elided in test",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the report does not mention %q:\n%s", want, got)
		}
	}
	if len(*h.statuses) != 1 || (*h.statuses)[0] != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("health = %v, want one NOT_SERVING", *h.statuses)
	}

	before := h.out.Len()
	h.watch.sample()
	h.watch.sample()
	if h.out.Len() != before {
		t.Fatalf("the same stall was reported again: %q", h.out.String()[before:])
	}
}

// A READER that never returns wedges the ledger just as thoroughly, and the
// first version of this watchdog reported it as RECOVERY: no writer holds the
// lock, so a holder check reads "not locked". Go's RWMutex is
// writer-preferring, so one parked RLock holder blocks every writer behind it
// and then every later reader.
func TestAStuckReaderIsReportedAndNotMistakenForHealth(t *testing.T) {
	h := newStallHarness(t)
	release := h.hold(t, h.ledger.ReadOnly)
	defer release()

	// A reader alone, with nothing waiting, is not a stall at ANY age: a
	// snapshot that blocks nobody is not wedging anything.
	time.Sleep(2 * testStallThreshold)
	h.watch.sample()
	if h.out.Len() != 0 {
		t.Fatalf("an idle read section with no writers queued was reported: %q", h.out.String())
	}

	// Now queue a writer behind it. Nothing can proceed.
	writerDone := make(chan struct{})
	go func() { defer close(writerDone); _ = h.ledger.Credit("a", 1) }()
	waitForWaitingWriter(t, h.ledger)

	h.watch.sample() // establishes the progress baseline
	time.Sleep(2 * testStallThreshold)
	h.watch.sample()

	got := h.out.String()
	for _, want := range []string{"STALLED", "queued", "READER", "ReadOnly", "stack elided in test"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the report does not identify a stuck reader (%q missing):\n%s", want, got)
		}
	}
	if strings.Contains(got, "serving again") {
		t.Fatal("the watchdog announced recovery on a wedged ledger, which is the exact " +
			"blind spot this test exists for")
	}
	if len(*h.statuses) != 1 || (*h.statuses)[0] != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("health = %v, want one NOT_SERVING", *h.statuses)
	}

	release()
	<-writerDone
}

// Sustained write load keeps writers queued continuously. That is busy, not
// wedged, and reporting it would train an operator to ignore the alarm.
func TestSustainedWriteLoadIsNotAStall(t *testing.T) {
	h := newStallHarness(t)
	// This test distinguishes queued writers from a wedged holder; it is not a
	// test of the 40ms synthetic threshold. A Pebble fsync can honestly exceed
	// 40ms on a loaded CI disk, so use a threshold that still makes any single
	// acquisition ordinary while the loop samples thousands of acquisitions.
	h.watch.threshold = time.Second
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = h.ledger.Credit("a", 1)
				}
			}
		}()
	}
	defer func() { close(stop); wg.Wait() }()

	// Sample across enough acquisitions that the watchdog has repeatedly seen
	// writers queued. Driven by observed PROGRESS rather than wall time, so a
	// slow or descheduled run makes the test longer, never wrong.
	start := h.ledger.LockStatus().Acquisitions
	deadline := time.Now().Add(30 * time.Second)
	for h.ledger.LockStatus().Acquisitions-start < 2000 {
		if time.Now().After(deadline) {
			t.Skip("the ledger did not turn over enough times to exercise sustained load")
		}
		h.watch.sample()
		time.Sleep(time.Millisecond)
	}
	if h.out.Len() != 0 {
		t.Fatalf("a busy ledger was reported as stalled, which trains an operator to ignore "+
			"the alarm:\n%s", h.out.String())
	}
}

func TestAnIdleLedgerIsNeverReported(t *testing.T) {
	h := newStallHarness(t)
	time.Sleep(2 * testStallThreshold)
	h.watch.sample()
	if h.out.Len() != 0 {
		t.Fatalf("an idle ledger was reported as stalled: %q", h.out.String())
	}
	if len(*h.statuses) != 0 {
		t.Fatalf("health was touched for an idle ledger: %v", *h.statuses)
	}
}

// A hold that ends was slow, not stuck. Leaving the node NOT_SERVING after it
// recovers would make the alarm for the first outage into a second one.
func TestRecoveryPutsTheNodeBackInService(t *testing.T) {
	h := newStallHarness(t)
	release := h.hold(t, h.ledger.Atomically)
	time.Sleep(2 * testStallThreshold)
	h.watch.sample()
	release()

	h.watch.sample()
	if !strings.Contains(h.out.String(), "serving again") {
		t.Fatalf("recovery was not reported:\n%s", h.out.String())
	}
	if len(*h.statuses) != 2 || (*h.statuses)[1] != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health = %v, want NOT_SERVING then SERVING", *h.statuses)
	}
}

// The entry point the incident came through must be visible. That every other
// write-lock site is too is pinned structurally in internal/market.
func TestAWriterIsVisibleToTheWatchdogWhileItRuns(t *testing.T) {
	h := newStallHarness(t)
	var heldInside bool
	if err := h.ledger.Atomically(func(market.LedgerTx) error {
		heldInside = h.ledger.LockStatus().Held
		return nil
	}); err != nil {
		t.Fatalf("Atomically: %v", err)
	}
	if !heldInside {
		t.Fatal("the ledger did not report its write lock as held from inside a critical " +
			"section, so a stall there would be invisible")
	}
	if h.ledger.LockStatus().Held {
		t.Fatal("the ledger still reports its write lock as held after the section ended, " +
			"which would report every idle node as stalled")
	}
}

func waitForWaitingWriter(t *testing.T, l *market.Ledger) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if l.LockStatus().WritersWaiting > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no writer ever showed as queued, so the reader-stall signal cannot be exercised")
}

// Shutdown releases the lock too, and it is not recovery. Announcing "serving
// again" as the node stops would undo the NOT_SERVING that Server.Stop sets and
// put a dying node back into a load balancer's rotation.
func TestShutdownIsNotReportedAsRecovery(t *testing.T) {
	h := newStallHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.watch.ctx = ctx

	release := h.hold(t, h.ledger.Atomically)
	time.Sleep(2 * testStallThreshold)
	h.watch.sample()
	if len(*h.statuses) != 1 {
		t.Fatalf("expected the stall to be reported first, got %v", *h.statuses)
	}

	cancel()
	release()
	h.watch.sample()

	if strings.Contains(h.out.String(), "serving again") {
		t.Fatalf("shutdown was announced as recovery:\n%s", h.out.String())
	}
	if len(*h.statuses) != 1 {
		t.Fatalf("health = %v, want the NOT_SERVING to stand through shutdown", *h.statuses)
	}
}

// An IDLE node followed by one brief queue must not report a stall.
//
// The reader-stall branch times "how long since a write lock was last taken",
// and on a node doing no ledger writes that clock runs from the first sample.
// So a node quiet for an hour, then one Reconcile poll overlapping one write,
// reported a wedge instantly: writers-waiting became non-zero and the
// since-last-acquisition age was the whole hour of quiet. Nothing was wrong.
func TestAnIdleNodeThenOneBriefQueueIsNotAStall(t *testing.T) {
	h := newStallHarness(t)

	// Quiet. No writes at all, sampled repeatedly - this is a node nobody is
	// using, which is most nodes most of the time.
	h.watch.sample()
	time.Sleep(3 * testStallThreshold)
	h.watch.sample()

	// Now one snapshot opens and one writer queues behind it, briefly.
	release := h.hold(t, h.ledger.ReadOnly)
	wrote := make(chan struct{})
	go func() { defer close(wrote); _ = h.ledger.Credit("a", 1) }()
	waitForQueuedWriter(t, h.ledger)
	h.watch.sample()
	release()
	<-wrote

	if h.out.Len() != 0 {
		t.Fatalf("a quiet node with one briefly-queued writer was reported as stalled. The "+
			"reader-stall clock is measuring idleness, not blocking:\n%s", h.out.String())
	}
}

func waitForQueuedWriter(t *testing.T, l *market.Ledger) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if l.LockStatus().WritersWaiting > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no writer ever queued")
}
