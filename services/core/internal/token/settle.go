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
// matching balance change, Settle pre-checks affordability under the same lock
// before appending, so a transfer that would fail for insufficient funds is
// rejected before anything is persisted. The market.Ledger.Transfer remains
// available for internal callers; SettledLedger is the additional signed
// entrypoint layered on top of it.
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
//   - an invalid signature, wrong nonce, or bad prev-hash is rejected by
//     Chain.Append before any ledger mutation;
//   - an unaffordable transfer is rejected by the affordability pre-check before
//     Append is attempted, so the chain is not advanced.
//
// This keeps the chain and the ledger consistent: every appended settlement has
// a corresponding successful balance movement.
func (s *SettledLedger) Settle(tx *Transaction) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Fail fast on signature validity so an unsigned/forged tx never reaches the
	// affordability check or the chain.
	if err := tx.Verify(); err != nil {
		return nil, err
	}

	sender := tx.SenderID()

	// Affordability pre-check under the same lock. This runs before Append so we
	// never persist a chain record that we then cannot back with a transfer.
	balance, err := s.ledger.Balance(sender)
	if err != nil {
		return nil, err
	}
	if balance < tx.Amount {
		return nil, fmt.Errorf("settle %d from %s: %w", tx.Amount, sender, ErrUnaffordable)
	}

	// Append is the authoritative gate: it re-verifies the signature, enforces
	// the sender's monotonic nonce, and checks the hash link. Nothing is written
	// on failure.
	rec, err := s.chain.Append(tx)
	if err != nil {
		return nil, err
	}

	// Move credits. The affordability pre-check above makes an insufficient-funds
	// failure here practically unreachable while holding s.mu, but we still
	// surface any transfer error rather than swallow it.
	if err := s.ledger.Transfer(sender, tx.To, tx.Amount); err != nil {
		return nil, fmt.Errorf("token: chain appended at height %d but ledger transfer failed (balances unchanged): %w", rec.Height, err)
	}

	return rec, nil
}

// IsUnaffordable reports whether err indicates an unaffordable settlement.
func IsUnaffordable(err error) bool {
	return errors.Is(err, market.ErrInsufficientFunds)
}
