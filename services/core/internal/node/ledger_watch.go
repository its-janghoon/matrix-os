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
// WHY THE THRESHOLD IS MINUTES AND NOT MILLISECONDS. The tempting number is
// wrong twice over.
//
// First, the honest section is not cheap. The test suite makes it look
// sub-millisecond, which is an artefact of the suite never building a full
// block: market.(*Ledger).transferLocked commits its own fsynced pebble batch
// PER TRANSFER rather than batching across the block, and commitAndApply holds
// this lock across every one of them. A full block is up to
// DefaultMaxBlockTxs (512) transactions, each costing two transfers when a fee
// applies, plus a fee split over up to MaxValidators (128) and an emission pass
// over the credited accounts: roughly 1665 fsyncs in ONE hold, with nothing
// wrong.
//
// Second, how long that takes is not a number, it is a distribution. Measured
// in this repository on local NVMe, the same 1665-transfer hold came out at
// 370ms on an idle machine and at 2.2s on a loaded one, and reached 8.6s when a
// concurrent list-style scan and block-store writes contended for the kv
// store's own mutex underneath it. Network-attached storage at ~5ms per fsync
// is another order of magnitude on top of whichever of those you start from.
//
// So the threshold is set against the bad tail rather than the median, and
// generously: TWO MINUTES. The reasoning for the margin is asymmetric and worth
// stating, because the instinct is to tune this down. A false positive takes a
// healthy node out of rotation and teaches its operator to distrust the alarm.
// A late true positive costs nothing at all: the condition it detects does not
// resolve on its own, the node is already producing no blocks, and finding out
// at two minutes instead of thirty seconds changes no outcome except that
// somebody finds out.
const (
	// defaultLedgerStallThreshold is how long one holder may keep the ledger
	// write lock before the node treats itself as stalled.
	defaultLedgerStallThreshold = 2 * time.Minute
	// defaultLedgerStallInterval is how often the holder is sampled. It bounds
	// how late the report can be, and costs one atomic load per tick.
	defaultLedgerStallInterval = 10 * time.Second
	// minDumpInterval is the floor between goroutine dumps. A dump stops the
	// world and emits megabytes; taking one per threshold crossing would make a
	// node that is merely slow into one that is stopped.
	minDumpInterval = 5 * time.Minute
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
	// out is where the report goes.
	out io.Writer
	// setServing flips the node's gRPC health and its HTTP probe. Nil disables
	// that half, which is what a test that only wants the log does.
	setServing func(healthpb.HealthCheckResponse_ServingStatus)
	// stacks dumps every goroutine's stack. Nil uses the runtime.
	stacks func() []byte
	// ctx is the node's lifetime, so recovery can be told from shutdown.
	ctx context.Context

	// reported is whether the CURRENT stall has already been reported. A stall
	// does not resolve on its own, so without this the log fills with the same
	// paragraph every interval and the stack dump is written over and over.
	reported bool
	// lastAcquisitions and progressAt track write-lock PROGRESS between samples,
	// which is what makes a stuck reader visible. A waiter count on its own is
	// not enough: under sustained write load somebody is always queued, and
	// their age grows without anything being wrong. Writers queued AND not one
	// acquisition completing across the whole window is a wedge at any load.
	lastAcquisitions uint64
	progressAt       time.Time
	// started is when the watch began, so the first window is measured from a
	// real instant rather than the zero time.
	started bool
	// lastDump is when a goroutine dump was last taken, so repeated crossings
	// under storage backpressure cannot stop the world once per crossing.
	lastDump time.Time
}

// There is deliberately NO injectable clock here any more.
//
// There was one, and it is what made the first version wrong: a wall-clock
// stamp compared against an injectable time.Now discards Go's monotonic
// reading, so an NTP correction or a VM resume that steps the clock forward
// past the threshold reported a stall that never happened and took a healthy
// node out of rotation. Lock ages now come from the ledger's own monotonic
// counter, which no adjustment can move, and tests drive a small real threshold
// against a real hold instead of inventing a time.

func newLedgerStallWatch(ledger *market.Ledger, out io.Writer) *ledgerStallWatch {
	return &ledgerStallWatch{
		ledger:    ledger,
		threshold: defaultLedgerStallThreshold,
		interval:  defaultLedgerStallInterval,
		out:       out,
	}
}

// run samples until the context is cancelled.
//
// The context is kept so recovered() can tell "the stall cleared" from "the
// node is shutting down". Both look like a free lock, and only one of them
// means the node is serving again: asserting SERVING during shutdown would
// undo the NOT_SERVING that Server.Stop just set and put a dying node back into
// a load balancer's rotation.
func (w *ledgerStallWatch) run(ctx context.Context) {
	w.ctx = ctx
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

// shuttingDown reports whether the node is on its way out. A watch with no
// context (a test calling sample directly) is not.
func (w *ledgerStallWatch) shuttingDown() bool {
	return w.ctx != nil && w.ctx.Err() != nil
}

// sample is one pass, separated from the ticker so a test can call it directly
// rather than sleep out a whole interval.
//
// It looks for TWO wedges, because there are two and only one of them was
// obvious. A writer that holds the lock and never returns is the one that
// happened. A READER that never returns is the one that would have been
// reported as recovery: no writer holds the lock, so a holder check reads "not
// locked", and the watchdog would have announced the node was serving again
// while every writer sat queued behind a parked RLock.
func (w *ledgerStallWatch) sample() {
	st := w.ledger.LockStatus()
	now := time.Now()
	// progressAt is the start of the window in which nothing has got in. It
	// restarts on an acquisition, and ALSO whenever nothing is queued - because
	// with no writer blocked there is nothing being denied, and the clock would
	// otherwise be measuring how long the node has been quiet.
	//
	// That omission was a reproducible false alarm: a node doing no ledger
	// writes for an hour, then one snapshot overlapping one write, reported
	// "1 writer(s) have been queued for 0s" and took itself out of rotation. The
	// age it printed was the hour of idleness.
	if !w.started || st.Acquisitions != w.lastAcquisitions || st.WritersWaiting == 0 {
		w.started = true
		w.lastAcquisitions = st.Acquisitions
		w.progressAt = now
	}

	switch {
	case st.Held && st.HeldFor >= w.threshold:
		w.raise(heldTooLong(st.HeldFor))
	case st.WritersWaiting > 0 && now.Sub(w.progressAt) >= w.threshold:
		w.raise(noProgress(st.WritersWaiting, now.Sub(w.progressAt)))
	default:
		w.recovered()
	}
}

// raise reports a stall once. A second report of the same stall adds a
// paragraph and a full goroutine dump and no information.
func (w *ledgerStallWatch) raise(cause string) {
	if w.reported {
		return
	}
	w.reported = true
	w.report(cause)
}

// heldTooLong describes a stuck WRITER: someone has the lock and is not giving
// it back.
func heldTooLong(held time.Duration) string {
	return fmt.Sprintf(
		"One holder has kept the ledger write lock for %s. The slowest honest hold is a "+
			"full block, which is seconds even on slow storage under load, so this node is "+
			"wedged rather than busy. The usual cause is code "+
			"called from INSIDE the ledger critical section that opens its own - the mutex "+
			"is not reentrant, so a goroutine deadlocks against itself and parks forever "+
			"without logging anything. Look in the dump below for a goroutine blocked in "+
			"sync.(*RWMutex).Lock underneath a market.(*Ledger).Atomically frame: that one "+
			"is waiting for a lock it already holds. If instead it is inside a pebble "+
			"commit, this is storage and not a deadlock: the section fsyncs once per "+
			"transfer.", held.Round(time.Second))
}

// noProgress describes a stuck READER: nobody holds the write lock, writers are
// queued, and not one of them has got in.
func noProgress(waiting int, quiet time.Duration) string {
	return fmt.Sprintf(
		"%d writer(s) have been queued for the ledger for %s and not one write lock has "+
			"been taken in that time. Nobody holds the write lock, so this is a READER "+
			"that never returned: Go's RWMutex is writer-preferring, so one parked RLock "+
			"holder blocks every writer behind it and then every later reader. Look in the "+
			"dump below for a goroutine inside market.(*Ledger).ReadOnly or "+
			"market.(*Ledger).Balance that is not moving.", waiting, quiet.Round(time.Second))
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
	if w.shuttingDown() {
		// The lock came free because the node is stopping, not because it
		// recovered. Saying "serving again" here would contradict the
		// NOT_SERVING that shutdown sets.
		return
	}
	fmt.Fprintf(w.out, "Ledger: the write lock came free; the node is serving again.\n")
	if w.setServing != nil {
		w.setServing(healthpb.HealthCheckResponse_SERVING)
	}
}

// report says what happened, in terms that do not require a stack dump to act
// on, and then provides the stack dump anyway.
func (w *ledgerStallWatch) report(cause string) {
	// HEALTH FIRST, THEN THE LOG. A blocked write to stdout is one of the causes
	// listed in the report itself, and if that is what is wrong then writing the
	// report is exactly what will not finish. The health flip is a memory store
	// and cannot block, so it goes first and lands either way.
	if w.setServing != nil {
		w.setServing(healthpb.HealthCheckResponse_NOT_SERVING)
	}
	fmt.Fprintf(w.out, "Ledger: STALLED. %s\n", cause)
	if w.setServing != nil {
		fmt.Fprintf(w.out, "Ledger: reporting NOT_SERVING on the health endpoint.\n")
	}
	// The dump is the whole point. Getting it in the earlier incident required
	// SIGQUIT, which kills the process and so can only be done once, after the
	// damage.
	//
	// RATE LIMITED ACROSS STALLS, not just within one. runtime.Stack stops the
	// world for a time proportional to the goroutine count and emits megabytes.
	// Under storage backpressure a hold can cross the threshold, clear, and
	// cross again repeatedly, and a dump per crossing turns a slow node into a
	// stopped one.
	if w.lastDump.IsZero() || time.Since(w.lastDump) >= minDumpInterval {
		w.lastDump = time.Now()
		fmt.Fprintf(w.out, "%s\n", w.goroutineDump())
	} else {
		fmt.Fprintf(w.out, "Ledger: goroutine dump suppressed; one was taken %s ago.\n",
			time.Since(w.lastDump).Round(time.Second))
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
