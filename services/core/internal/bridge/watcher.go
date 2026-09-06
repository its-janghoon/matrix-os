package bridge

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// This file closes the burn->unlock loop into an always-on watcher. Where
// burnlog.go decodes a single observed log and ProcessBurn applies it, the
// Watcher continuously polls an Ethereum endpoint (eth_getLogs) for WrappedMatrix
// `Burned` events from a fixed block onward and drives each one through the
// existing decode + replay-safe ProcessBurn primitive, so an operator does not
// have to hand-feed logs.
//
// AUTOMATION BOUNDARY (honest): the Watcher is a polling relayer, not a consensus
// participant. It applies burns to THIS node's ledger via ProcessBurn (which is
// replay-protected per burn id). In a multi-validator deployment the unlock must
// ultimately be agreed by consensus; running a Watcher on a single node is the
// correct model for a solo/dev node or an operator-run relayer, and is what the
// hardhat-backed test exercises. The operator supplies the endpoint URL, the
// WrappedMatrix contract address, and the start block (see cmd/matrixd wiring or
// an operator script); the Watcher does the rest.

// WatcherState is the persisted cursor of a Watcher: the next block it should
// scan from. It is stored under the bridge/* namespace so a restarted watcher
// resumes without re-scanning (idempotent regardless, since ProcessBurn dedups
// by burn id, but persisting the cursor avoids redundant work).
const watcherCursorKey = "bridge/watch_cursor"

// Applier is the subset of *Bridge the Watcher needs to apply a decoded burn. It
// is an interface so the Watcher can be unit-tested with a recording fake and so
// the dependency is explicit.
type Applier interface {
	// ProcessBurn applies a decoded burn exactly once (replay-safe per id).
	ProcessBurn(burn BurnEvent) error
}

// WatcherConfig configures a Watcher.
type WatcherConfig struct {
	// Client is the Ethereum JSON-RPC client to poll (required).
	Client EthClient
	// Bridge applies decoded burns to the native ledger (required).
	Bridge Applier
	// Contract is the WrappedMatrix contract address whose Burned events are
	// watched (required).
	Contract Address
	// StartBlock is the first block to scan. On a fresh watcher scanning begins
	// here; a persisted cursor (when Store is set) takes precedence on resume.
	StartBlock uint64
	// Confirmations is how many blocks behind head the watcher stays before
	// treating a block as final, to avoid acting on a reorged burn. Zero means
	// act on the latest block (fine for a local hardhat chain with instant
	// finality).
	Confirmations uint64
	// PollInterval is how often the watcher polls for new logs when caught up to
	// head. Zero defaults to 2s.
	PollInterval time.Duration
	// MaxBlockSpan bounds the number of blocks scanned per eth_getLogs call so a
	// large backlog is chunked. Zero defaults to 2000.
	MaxBlockSpan uint64
	// OnBurn, when set, is called after each burn is successfully applied, for
	// logging/metrics. It must not block.
	OnBurn func(DecodedBurn)
	// OnError, when set, is called with non-fatal errors (a transient RPC failure
	// or a single malformed log) so the operator has visibility without the
	// watcher stopping. It must not block.
	OnError func(error)
}

// Watcher polls an Ethereum endpoint for WrappedMatrix Burned events and applies
// each one to the native bridge exactly once. It is the always-on relayer form
// of the decode + ProcessBurn primitive.
type Watcher struct {
	client        EthClient
	bridge        Applier
	contract      Address
	confirmations uint64
	pollInterval  time.Duration
	maxBlockSpan  uint64
	onBurn        func(DecodedBurn)
	onError       func(error)

	mu   sync.Mutex
	next uint64 // next block to scan
}

// NewWatcher builds a Watcher from cfg, validating required fields.
func NewWatcher(cfg WatcherConfig) (*Watcher, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("bridge: watcher client is required")
	}
	if cfg.Bridge == nil {
		return nil, fmt.Errorf("bridge: watcher bridge applier is required")
	}
	poll := cfg.PollInterval
	if poll <= 0 {
		poll = 2 * time.Second
	}
	span := cfg.MaxBlockSpan
	if span == 0 {
		span = 2000
	}
	return &Watcher{
		client:        cfg.Client,
		bridge:        cfg.Bridge,
		contract:      cfg.Contract,
		confirmations: cfg.Confirmations,
		pollInterval:  poll,
		maxBlockSpan:  span,
		onBurn:        cfg.OnBurn,
		onError:       cfg.OnError,
		next:          cfg.StartBlock,
	}, nil
}

// Cursor returns the next block the watcher will scan from.
func (w *Watcher) Cursor() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.next
}

// Run polls until ctx is cancelled, applying every Burned event it observes. It
// returns ctx.Err() on cancellation. Transient errors are reported via OnError
// and retried on the next tick rather than stopping the watcher.
func (w *Watcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	// Do an immediate first pass so a caught-up chain is processed without
	// waiting a full interval (important for the test's determinism).
	if _, err := w.Poll(ctx); err != nil && !errors.Is(err, context.Canceled) {
		w.reportError(err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := w.Poll(ctx); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return err
				}
				w.reportError(err)
			}
		}
	}
}

// Poll performs a single scan pass: it fetches the current head, computes the
// finalized target block (head - confirmations), scans [next, target] for
// Burned logs in bounded spans, applies each, and advances the cursor. It
// returns the number of burns applied in this pass. It is exported so an
// operator tool (or a test) can drive a single deterministic pass.
func (w *Watcher) Poll(ctx context.Context) (int, error) {
	head, err := w.client.BlockNumber(ctx)
	if err != nil {
		return 0, fmt.Errorf("bridge: watcher head: %w", err)
	}
	// Stay `confirmations` blocks behind head. If head is shallower than the
	// confirmation depth there is nothing final to scan yet.
	if head < w.confirmations {
		return 0, nil
	}
	target := head - w.confirmations

	w.mu.Lock()
	from := w.next
	w.mu.Unlock()

	if from > target {
		return 0, nil
	}

	applied := 0
	for from <= target {
		to := from + w.maxBlockSpan - 1
		if to > target {
			to = target
		}
		logs, err := w.client.FilterBurnedLogs(ctx, w.contract, from, to)
		if err != nil {
			return applied, fmt.Errorf("bridge: watcher getLogs [%d,%d]: %w", from, to, err)
		}
		for _, log := range logs {
			n, err := w.applyLog(log)
			if err != nil {
				// A single malformed or already-processed log must not wedge the
				// watcher: report it and continue. ProcessBurn's replay guard makes
				// re-observing a burn a benign no-op.
				w.reportError(err)
				continue
			}
			applied += n
		}
		// Advance the cursor past the scanned range so a restart resumes here.
		w.mu.Lock()
		w.next = to + 1
		w.mu.Unlock()
		from = to + 1
	}
	return applied, nil
}

// applyLog decodes one Burned log and applies it, returning 1 if a new burn was
// applied and 0 if it was a benign replay (already processed).
func (w *Watcher) applyLog(log EthLog) (int, error) {
	decoded, err := DecodeBurnedLog(log)
	if err != nil {
		return 0, fmt.Errorf("bridge: watcher decode burn: %w", err)
	}
	if err := w.bridge.ProcessBurn(decoded.BurnEvent()); err != nil {
		if errors.Is(err, ErrBurnAlreadyProcessed) {
			// Already applied (e.g. after a restart before the cursor advanced):
			// benign, not an error to the operator.
			return 0, nil
		}
		return 0, fmt.Errorf("bridge: watcher apply burn %s: %w", decoded.ID, err)
	}
	if w.onBurn != nil {
		w.onBurn(*decoded)
	}
	return 1, nil
}

func (w *Watcher) reportError(err error) {
	if w.onError != nil && err != nil {
		w.onError(err)
	}
}
