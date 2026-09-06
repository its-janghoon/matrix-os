package bridge

import (
	"context"
	"encoding/binary"
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
// WrappedMatrix contract address, and the start block (via the matrixd
// bridge.watch config section, which constructs and runs this Watcher against
// the node's own ledger, or via cmd/bridge-watch out of process); the Watcher
// does the rest.

// RESUME BEHAVIOR: where the watcher resumes depends on whether a cursor Store
// is supplied.
//
//	Store == nil: the cursor lives in memory only. On start the watcher scans
//	from StartBlock and a restart re-scans that whole range. Harmless but
//	wasteful, and unbounded for a contract deployed far behind head.
//
//	Store != nil: the cursor is persisted (CursorKey, default
//	bridge/watch_cursor) after each scanned span, so a restart resumes at the
//	next unscanned block instead of re-scanning. The persisted cursor never
//	rewinds the watcher behind StartBlock, so raising StartBlock in config can
//	skip ahead but a stale cursor can never pull it back.
//
// Either way correctness does not depend on the cursor: it is written AFTER the
// span's burns are applied, so a crash between apply and write re-observes those
// burns, and ProcessBurn's per-id replay guard makes a re-observed burn return
// ErrBurnAlreadyProcessed, which applyLog treats as a benign no-op. The cursor
// is a redundant-work optimization, never the thing that prevents a double
// unlock.

// watcherCursorKey is the default KV key the Watcher persists its scan cursor
// under when a Store is supplied. It lives in the same bridge/* namespace as the
// rest of the bridge's bookkeeping (see bridge.go).
const watcherCursorKey = "bridge/watch_cursor"

// CursorStore is the minimal key/value surface the Watcher needs to persist its
// scan cursor across restarts. *kv.Store satisfies it; keeping it an interface
// means the bridge does not import internal/kv for this and the persistence path
// is unit-testable with an in-memory map.
type CursorStore interface {
	// Get returns the value for key, or nil if the key is absent.
	Get(key []byte) ([]byte, error)
	// Put stores value under key.
	Put(key, value []byte) error
}

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
	// StartBlock is the earliest block to scan. Scanning begins here unless a
	// Store holds a persisted cursor further ahead, in which case it resumes
	// there (the cursor never rewinds the watcher behind StartBlock).
	StartBlock uint64
	// Store, when non-nil, persists the scan cursor so a restart resumes at the
	// next unscanned block instead of re-scanning from StartBlock. It is a
	// redundant-work optimization, never a correctness requirement: ProcessBurn
	// dedups by burn id, so a re-scan is a benign no-op. When nil the cursor is
	// in-memory only. See RESUME BEHAVIOR above.
	Store CursorStore
	// CursorKey overrides the KV key the cursor is persisted under. Empty uses
	// watcherCursorKey. Set it when one store hosts watchers for more than one
	// contract, so their cursors do not overwrite each other.
	CursorKey string
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
	store         CursorStore
	cursorKey     string

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
	cursorKey := cfg.CursorKey
	if cursorKey == "" {
		cursorKey = watcherCursorKey
	}
	// Resume from the persisted cursor when one is available and ahead of
	// StartBlock. A cursor behind StartBlock is ignored rather than honoured, so
	// an operator who raises StartBlock in config always skips forward and a
	// stale cursor can never drag the scan back to a range they excluded.
	next := cfg.StartBlock
	if cfg.Store != nil {
		saved, err := loadWatcherCursor(cfg.Store, cursorKey)
		if err != nil {
			return nil, err
		}
		if saved > next {
			next = saved
		}
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
		store:         cfg.Store,
		cursorKey:     cursorKey,
		next:          next,
	}, nil
}

// loadWatcherCursor reads the persisted next-block-to-scan cursor, treating an
// absent key as zero (no cursor recorded yet).
func loadWatcherCursor(store CursorStore, key string) (uint64, error) {
	data, err := store.Get([]byte(key))
	if err != nil {
		return 0, fmt.Errorf("bridge: read watcher cursor %q: %w", key, err)
	}
	if data == nil {
		return 0, nil
	}
	if len(data) != 8 {
		return 0, fmt.Errorf("bridge: corrupt watcher cursor %q: expected 8 bytes, got %d", key, len(data))
	}
	return binary.BigEndian.Uint64(data), nil
}

// saveCursor persists the next block to scan. It is a no-op when no Store was
// configured.
func (w *Watcher) saveCursor(next uint64) error {
	if w.store == nil {
		return nil
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, next)
	if err := w.store.Put([]byte(w.cursorKey), buf); err != nil {
		return fmt.Errorf("bridge: persist watcher cursor %q: %w", w.cursorKey, err)
	}
	return nil
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
		// Advance the cursor past the scanned range so the next poll continues
		// forward, then persist it if a Store was configured. Persisting AFTER the
		// span's burns are applied keeps the failure mode at-least-once (a crash
		// here re-scans the span, which ProcessBurn dedups) rather than
		// at-most-once (which could skip an unapplied burn). A failed cursor write
		// is reported but never stops the relay: the in-memory cursor has already
		// advanced and the worst outcome is a re-scan after restart.
		w.mu.Lock()
		w.next = to + 1
		w.mu.Unlock()
		if err := w.saveCursor(to + 1); err != nil {
			w.reportError(err)
		}
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
