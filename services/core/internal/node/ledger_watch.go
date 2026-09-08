package node

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"time"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/ecirlabs/matrix-core/internal/market"
)

// A node whose ledger write lock is never released is a node that has stopped,
// and until now it did not say so.
//
// THE INCIDENT. A bridge lock committed and the consensus apply path re-entered
// the ledger mutex from inside its own critical section. The driver goroutine
// parked and never came back. What the operator saw: gRPC health answering
// SERVING, `tx list` answering normally from the block store, and every balance
// read hanging until the client's deadline. No blocks. Not one line in the log,
// because a parked goroutine writes nothing - the last entry was an ordinary
// pubsub validation from six minutes earlier. It took SIGQUIT, which kills the
// process, to see the stack that named it in one frame.
//
// WHY A HELD WRITE LOCK IS THE RIGHT THING TO WATCH. It is the single
// chokepoint: a writer that does not return blocks every other writer and,
// because Go's RWMutex does not starve a pending writer, every subsequent
// reader too. Watching balances or RPC latency would find the same stall later
// and describe it worse.
//
// WHY THE THRESHOLD IS SECONDS AND NOT MILLISECONDS. The tempting number is
// wrong. A typical critical section really is sub-millisecond, but the section
// is not bounded by the typical case: block apply holds this lock across every
// transfer in the block, and market.(*Ledger).transferLocked commits its own
// fsynced pebble batch PER TRANSFER rather than batching across the block. A
// full block is up to 512 transactions, each costing two transfers when a fee
// applies, plus a fee distribution over up to 128 validators and an emission
// pass over the credited accounts: roughly 1665 fsyncs in ONE hold, with no
// bug present.
//
// Measured on local NVMe in this repository: 512 transfers in one hold took
// 139ms, 1030 took 254ms, and the 1665-transfer worst case took 965ms. On
// network-attached storage at ~5ms per fsync the same block projects to ~8s.
// So a one-second threshold would fire on an honest busy block, and a
// five-second one would fire on slow storage.
//
// Thirty seconds is chosen against the SLOW-STORAGE worst case, not the fast
// one: ~3.7x the projected 8s NAS block and ~31x the measured local block.
// That is wide enough that a firing means something is stuck rather than slow,
// which is the only way the report below can name a cause with confidence. The
// cost of the margin is bounded and small - a deadlocked node is already
// producing no blocks, so waiting thirty seconds to say so changes nothing
// except that the operator now finds out at all.
const (
	// defaultLedgerStallThreshold is how long one holder may keep the ledger
	// write lock before the node treats itself as stalled.
	defaultLedgerStallThreshold = 30 * time.Second
	// defaultLedgerStallInterval is how often the holder is sampled. It bounds
	// how late the report can be, and costs one atomic load per tick.
	defaultLedgerStallInterval = 5 * time.Second
)

// ledgerStallWatch samples the ledger's current write-lock holder and reports a
// hold that has outlasted any honest one.
//
// Everything is a field so a test can drive a stall it invents instead of
// waiting out a real threshold, and so the reporting can be captured rather
// than read off a terminal.
type ledgerStallWatch struct {
	ledger    *market.Ledger
	threshold time.Duration
	interval  time.Duration
	// now is the clock, injectable for the same reason.
	now func() time.Time
	// out is where the report goes.
	out io.Writer
	// setServing flips the node's gRPC health. Nil disables that half, which is
	// what a test that only wants the log does.
	setServing func(healthpb.HealthCheckResponse_ServingStatus)
	// stacks dumps every goroutine's stack. Nil uses the runtime.
	stacks func() []byte

	// reported is whether the CURRENT stall has already been reported. A stall
	// does not resolve on its own, so without this the log fills with the same
	// paragraph every interval and the stack dump is written over and over.
	reported bool
}

func newLedgerStallWatch(ledger *market.Ledger, out io.Writer) *ledgerStallWatch {
	return &ledgerStallWatch{
		ledger:    ledger,
		threshold: defaultLedgerStallThreshold,
		interval:  defaultLedgerStallInterval,
		now:       time.Now,
		out:       out,
	}
}

// run samples until the context is cancelled.
func (w *ledgerStallWatch) run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sample()
		}
	}
}

// sample is one pass, separated from the ticker so a test can call it directly
// rather than sleep.
func (w *ledgerStallWatch) sample() {
	held, locked := w.ledger.WriteLockHeldFor(w.now())
	if !locked || held < w.threshold {
		w.recovered()
		return
	}
	if w.reported {
		return
	}
	w.reported = true
	w.report(held)
}

// recovered handles the lock coming free after a report. It is not the expected
// outcome - a deadlock never resolves - but a hold caused by something slow
// rather than something stuck does, and a node that stayed NOT_SERVING after
// recovering would be a second outage caused by the alarm for the first.
func (w *ledgerStallWatch) recovered() {
	if !w.reported {
		return
	}
	w.reported = false
	fmt.Fprintf(w.out, "Ledger: the write lock came free; the node is serving again.\n")
	if w.setServing != nil {
		w.setServing(healthpb.HealthCheckResponse_SERVING)
	}
}

// report says what happened, in terms that do not require a stack dump to act
// on, and then provides the stack dump anyway.
func (w *ledgerStallWatch) report(held time.Duration) {
	fmt.Fprintf(w.out,
		"Ledger: STALLED. One holder has kept the ledger write lock for %s. The "+
			"slowest honest hold is a full block, which is under a second on local "+
			"disk and a few seconds on network storage, so this node is wedged rather "+
			"than busy: no block will be applied and every balance read will hang. The "+
			"usual cause is code called from INSIDE the ledger critical section that "+
			"opens its own - the mutex is not reentrant, so a goroutine deadlocks "+
			"against itself and parks forever without logging anything. Look in the "+
			"dump below for a goroutine blocked in sync.(*RWMutex).Lock underneath a "+
			"market.(*Ledger).Atomically frame: that one is waiting for a lock it is "+
			"already holding. If instead every goroutine is blocked waiting and none "+
			"holds it, the holder is stuck in I/O - check for a pebble write stall or "+
			"a blocked write to stdout.\n",
		held.Round(time.Second))
	// The dump is the whole point. Getting it in the earlier incident required
	// SIGQUIT, which kills the process and so can only be done once, after the
	// damage. runtime.Stack costs a stop-the-world pause proportional to the
	// goroutine count and is written exactly once per stall.
	fmt.Fprintf(w.out, "%s\n", w.goroutineDump())
	// A node that cannot read a balance is not serving, whatever it has been
	// answering. Saying so is what lets a supervisor or a load balancer act
	// without a human reading the log first.
	if w.setServing != nil {
		w.setServing(healthpb.HealthCheckResponse_NOT_SERVING)
		fmt.Fprintf(w.out, "Ledger: reporting NOT_SERVING on the health endpoint.\n")
	}
}

func (w *ledgerStallWatch) goroutineDump() []byte {
	if w.stacks != nil {
		return w.stacks()
	}
	// Grown rather than guessed: a truncated dump can omit the very goroutine
	// worth seeing, and this runs once.
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return buf[:n]
		}
		buf = make([]byte, 2*len(buf))
	}
}
