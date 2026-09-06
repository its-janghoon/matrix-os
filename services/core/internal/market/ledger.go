// Package market implements the economic core of the Matrix OS compute
// marketplace: a compute-credits ledger and a compute-job marketplace.
//
// Providers register idle-compute capacity, buyers submit paid compute jobs,
// and credits transfer from buyer to provider when a job completes. All state
// is persisted through the existing Pebble-backed kv.Store.
//
// This is a self-contained internal package. It is not a proto/gRPC service,
// not wired into the Node, and not a real blockchain or token; those remain
// future roadmap items.
package market

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/cockroachdb/pebble"
	"github.com/ecirlabs/matrix-core/internal/kv"
)

// Exported sentinel errors so callers and tests can errors.Is against them.
var (
	// ErrInsufficientFunds is returned when an account balance is too low to
	// satisfy a debit or transfer.
	ErrInsufficientFunds = errors.New("market: insufficient funds")
	// ErrProviderNotFound is returned when a referenced provider does not exist.
	ErrProviderNotFound = errors.New("market: provider not found")
	// ErrInsufficientCapacity is returned when a provider cannot satisfy the
	// requested compute units.
	ErrInsufficientCapacity = errors.New("market: insufficient capacity")
	// ErrJobNotFound is returned when a referenced job does not exist.
	ErrJobNotFound = errors.New("market: job not found")
	// ErrInvalidProvider is returned when a provider fails validation.
	ErrInvalidProvider = errors.New("market: invalid provider")
	// ErrInvalidJobState is returned when a job cannot transition to the
	// requested state.
	ErrInvalidJobState = errors.New("market: invalid job state")
	// ErrSelfDealing is returned when a buyer submits a job against their own
	// provider account, which would settle credits from an account to itself.
	ErrSelfDealing = errors.New("market: buyer and provider must differ")
)

// balanceKeyPrefix namespaces compute-credit balances in the KV store.
const balanceKeyPrefix = "market/balance/"

// balanceKey returns the KV key used to persist an account balance.
func balanceKey(account string) []byte {
	return []byte(balanceKeyPrefix + account)
}

// Ledger is a compute-credits ledger backed by a Pebble kv.Store. Balances are
// stored as big-endian uint64 values under the `market/balance/<account>` key
// prefix. All mutations are guarded by a sync.RWMutex so concurrent callers
// observe consistent balances.
type Ledger struct {
	store *kv.Store
	mu    sync.RWMutex
}

// NewLedger creates a new Ledger backed by the given store.
func NewLedger(store *kv.Store) *Ledger {
	return &Ledger{store: store}
}

// LedgerTx is the unlocked view of the ledger handed to an Atomically
// callback. Its methods assume the caller already holds the ledger write lock
// (Atomically guarantees this), so they perform no locking of their own. This
// lets a compound operation (for example: read a balance, decide, then
// transfer) run as a single critical section that excludes every other ledger
// writer, including the direct Transfer path used by market.CompleteJob.
type LedgerTx interface {
	// Balance returns the current balance for account.
	Balance(account string) (uint64, error)
	// Transfer moves amount from one account to another with the same semantics
	// as Ledger.Transfer, but without taking the write lock (the enclosing
	// Atomically call already holds it).
	Transfer(from, to string, amount uint64) error
}

// lockedLedger is the LedgerTx implementation backed by a Ledger whose write
// lock is already held by the enclosing Atomically call.
type lockedLedger struct{ l *Ledger }

func (t lockedLedger) Balance(account string) (uint64, error) {
	return t.l.readBalance(account)
}

func (t lockedLedger) Transfer(from, to string, amount uint64) error {
	return t.l.transferLocked(from, to, amount)
}

// Atomically runs fn as a single critical section under the ledger write lock,
// passing a LedgerTx view whose Balance/Transfer operate without re-locking.
// Because every mutating Ledger method (Credit, Debit, Transfer) takes the same
// write lock, an Atomically block is mutually exclusive with all of them: a
// caller can safely read a balance, make a decision, and transfer without a
// concurrent writer draining the account in between. fn's error is returned
// unchanged.
func (l *Ledger) Atomically(fn func(tx LedgerTx) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return fn(lockedLedger{l})
}

// readBalance loads an account balance from the store. A missing key means a
// zero balance. Callers must hold at least a read lock.
func (l *Ledger) readBalance(account string) (uint64, error) {
	data, err := l.store.Get(balanceKey(account))
	if err != nil {
		return 0, fmt.Errorf("failed to read balance for %q: %w", account, err)
	}
	if data == nil {
		return 0, nil
	}
	if len(data) != 8 {
		return 0, fmt.Errorf("corrupt balance for %q: expected 8 bytes, got %d", account, len(data))
	}
	return binary.BigEndian.Uint64(data), nil
}

// encodeBalance encodes a uint64 balance as an 8-byte big-endian value.
func encodeBalance(amount uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, amount)
	return buf
}

// Balance returns the current compute-credit balance for an account. Unknown
// accounts have a zero balance.
func (l *Ledger) Balance(account string) (uint64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.readBalance(account)
}

// Credit adds amount to an account balance.
func (l *Ledger) Credit(account string, amount uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	current, err := l.readBalance(account)
	if err != nil {
		return err
	}
	if err := l.store.Put(balanceKey(account), encodeBalance(current+amount)); err != nil {
		return fmt.Errorf("failed to credit %q: %w", account, err)
	}
	return nil
}

// Debit subtracts amount from an account balance. It returns ErrInsufficientFunds
// (and leaves the balance unchanged) when the balance is less than amount.
func (l *Ledger) Debit(account string, amount uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	current, err := l.readBalance(account)
	if err != nil {
		return err
	}
	if current < amount {
		return fmt.Errorf("debit %q of %d: %w", account, amount, ErrInsufficientFunds)
	}
	if err := l.store.Put(balanceKey(account), encodeBalance(current-amount)); err != nil {
		return fmt.Errorf("failed to debit %q: %w", account, err)
	}
	return nil
}

// Transfer atomically moves amount from one account to another. It is atomic:
// if the source has insufficient funds, or the underlying commit fails, neither
// balance changes. The two balance writes are applied through a single kv batch
// so a partial application is impossible.
func (l *Ledger) Transfer(from, to string, amount uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.transferLocked(from, to, amount)
}

// transferLocked is the unlocked body of Transfer. Callers must hold the write
// lock (either via Transfer or via Atomically). It has the same atomic
// batch-commit and self-transfer semantics as Transfer.
func (l *Ledger) transferLocked(from, to string, amount uint64) error {
	fromBalance, err := l.readBalance(from)
	if err != nil {
		return err
	}
	if fromBalance < amount {
		return fmt.Errorf("transfer %d from %q to %q: %w", amount, from, to, ErrInsufficientFunds)
	}
	// A self-transfer is a no-op once affordability is confirmed: debiting and
	// crediting the same account nets to zero. Short-circuit here because both
	// balanceKey(from) and balanceKey(to) would be identical, so the two batched
	// writes below would collapse to a single write and the credit would win,
	// inflating the balance to balance+amount. The insufficient-funds check
	// above still runs first so an unaffordable self-transfer is rejected.
	if from == to {
		return nil
	}
	toBalance, err := l.readBalance(to)
	if err != nil {
		return err
	}

	batch := l.store.NewBatch()
	defer batch.Close()

	if err := batch.Set(balanceKey(from), encodeBalance(fromBalance-amount), nil); err != nil {
		return fmt.Errorf("failed to stage debit for %q: %w", from, err)
	}
	if err := batch.Set(balanceKey(to), encodeBalance(toBalance+amount), nil); err != nil {
		return fmt.Errorf("failed to stage credit for %q: %w", to, err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("failed to commit transfer: %w", err)
	}
	return nil
}
