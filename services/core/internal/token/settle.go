package token

import (
	"errors"
	"fmt"
	"sync"

	"github.com/ecirlabs/matrix-core/internal/market"
)

// ErrUnaffordable is returned when a signed transfer cannot be settled because
// the sender's ledger balance is below the transfer amount. It wraps
// market.ErrInsufficientFunds so callers may match on either.
var ErrUnaffordable = fmt.Errorf("token: %w", market.ErrInsufficientFunds)

// ErrSelfTransfer is returned when a signed transfer names the same account as
// both sender and recipient. Such a transfer moves zero credits (the ledger
// no-ops a self-transfer) but would still consume a nonce and append an
// economically-empty record, so Settle rejects it before touching the chain. It
// wraps market.ErrSelfDealing so callers may match on either.
var ErrSelfTransfer = fmt.Errorf("token: %w", market.ErrSelfDealing)

// ErrEmptyRecipient is returned when a signed transfer has an empty recipient
// (tx.To == ""). It wraps market.ErrInvalidTransaction-adjacent semantics via
// ErrInvalidTransaction so callers get a structural-validation error rather than
// silently crediting the empty account key.
var ErrEmptyRecipient = fmt.Errorf("%w: recipient must not be empty", ErrInvalidTransaction)

// SettledLedger is the signed-settlement entrypoint the marketplace, gRPC and
// P2P layers use to move compute credits. Credits move ONLY through this path:
// a caller submits a signed Transaction, the settlement verifies and appends it
// to the hash-chained log, and only then moves credits on the wrapped
// market.Ledger.
//
// Invariant and ordering: SettledLedger appends the transaction to the chain
// FIRST and performs the ledger transfer SECOND. Append is the authoritative,
// gatekeeping step (signature + nonce + hash-link checks); if it fails, no
// credits move and no record is written. To avoid a chain record without a
// matching balance change, Settle runs the affordability check, the append, and
// the transfer as one critical section under the market ledger's write lock (via
// Ledger.Atomically). Because every other ledger writer, including the direct
// market.Ledger.Transfer that market.CompleteJob uses, takes that same lock, no
// concurrent writer can drain the sender between the check and the transfer, so
// an appended settlement always has a matching balance movement. The
// market.Ledger.Transfer remains available for internal callers; SettledLedger
// is the additional signed entrypoint layered on top of it.
type SettledLedger struct {
	ledger *market.Ledger
	chain  *Chain
	mu     sync.Mutex
}

// NewSettledLedger builds a SettledLedger over an existing market.Ledger and
// token Chain, both backed by the shared kv.Store.
func NewSettledLedger(ledger *market.Ledger, chain *Chain) *SettledLedger {
	return &SettledLedger{ledger: ledger, chain: chain}
}

// Ledger exposes the wrapped market.Ledger for balance reads and internal,
// unsigned operations (e.g. minting initial credits in tests or bootstrap).
func (s *SettledLedger) Ledger() *market.Ledger {
	return s.ledger
}

// Chain exposes the wrapped transaction chain for inspection and validation.
func (s *SettledLedger) Chain() *Chain {
	return s.chain
}

// Settle verifies and records a signed transfer, then moves credits from the
// sender (tx.From's AccountID) to the recipient (tx.To). It returns the appended
// chain Record on success.
//
// On any failure NO credits move and NO transaction is appended:
//   - an empty recipient or a self-transfer is rejected before the chain is
//     touched;
//   - an invalid signature, wrong nonce, or bad prev-hash is rejected by
//     Chain.Append before any ledger mutation;
//   - an unaffordable transfer is rejected by the affordability check that runs
//     under the held ledger lock before Append is attempted, so the chain is not
//     advanced.
//
// This keeps the chain and the ledger consistent: every appended settlement has
// a corresponding successful balance movement, even under concurrent writers.
func (s *SettledLedger) Settle(tx *Transaction) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Fail fast on signature validity so an unsigned/forged tx never reaches the
	// affordability check or the chain.
	if err := tx.Verify(); err != nil {
		return nil, err
	}

	sender := tx.SenderID()

	// Reject structurally-empty and economically-empty transfers before touching
	// the chain: an empty recipient would credit the bare balance key, and a
	// self-transfer moves nothing yet would still consume a nonce and append a
	// record. Rejecting both here keeps the chain free of records with no real
	// balance effect (the market layer already rejects self-dealing at submit).
	if tx.To == "" {
		return nil, ErrEmptyRecipient
	}
	if tx.To == sender {
		return nil, fmt.Errorf("settle from %s to itself: %w", sender, ErrSelfTransfer)
	}

	// The append and the transfer must be atomic with respect to every other
	// ledger writer, or a concurrent completion (market.CompleteJob transfers on
	// the same ledger) could drain the sender between the affordability check and
	// the transfer, leaving a persisted chain record whose transfer then fails.
	// Running the whole critical section inside ledger.Atomically holds the
	// ledger write lock across the affordability check, the append, and the
	// transfer, so no other Credit/Debit/Transfer can interleave and the
	// invariant "an appended settlement always has a matching balance movement"
	// holds even under concurrent completions.
	var rec *Record
	err := s.ledger.Atomically(func(ltx market.LedgerTx) error {
		balance, err := ltx.Balance(sender)
		if err != nil {
			return err
		}
		if balance < tx.Amount {
			return fmt.Errorf("settle %d from %s: %w", tx.Amount, sender, ErrUnaffordable)
		}

		// Append is the authoritative gate: it re-verifies the signature, enforces
		// the sender's monotonic nonce, and checks the hash link. Nothing is
		// written on failure.
		appended, err := s.chain.Append(tx)
		if err != nil {
			return err
		}

		// Move credits. The affordability check ran under this same held ledger
		// lock, so an insufficient-funds failure here is now unreachable; we still
		// surface any unexpected transfer error rather than swallow it.
		if err := ltx.Transfer(sender, tx.To, tx.Amount); err != nil {
			return fmt.Errorf("token: chain appended at height %d but ledger transfer failed (balances unchanged): %w", appended.Height, err)
		}
		rec = appended
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// IsUnaffordable reports whether err indicates an unaffordable settlement.
func IsUnaffordable(err error) bool {
	return errors.Is(err, market.ErrInsufficientFunds)
}

// IsSelfTransfer reports whether err indicates a rejected self-transfer.
func IsSelfTransfer(err error) bool {
	return errors.Is(err, market.ErrSelfDealing)
}
