// Package market implements the economic core of the Matrix OS compute
// marketplace: the native MATRIX balance ledger and a compute-job marketplace.
//
// Providers register idle-compute capacity, buyers submit paid compute jobs,
// and native MATRIX transfers from buyer to provider when a job completes. All
// state is persisted through the existing Pebble-backed kv.Store.
//
// Balances are the canonical native MATRIX balances of the consensus L1: this
// ledger is the single source of truth for who holds how much of the coin.
// Every balance is a uint64 count of native base units (9 native decimals; see
// token.NativeUnit) stored under the market/balance/<account> key prefix.
// Issuance is not open-ended: native MATRIX enters circulation only through the
// genesis allocation and the supply-capped issuance/reward-pool path defined in
// internal/token (see token.Treasury), and thereafter moves between accounts via
// signed, consensus-ordered transfers. The low-level Credit/Debit/Transfer
// primitives here are the mechanism those higher layers build on.
package market

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

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
	// provider account, which would settle native MATRIX from an account to
	// itself.
	ErrSelfDealing = errors.New("market: buyer and provider must differ")
)

// balanceKeyPrefix namespaces native MATRIX balances in the KV store.
const balanceKeyPrefix = "market/balance/"

// balanceKey returns the KV key used to persist an account balance.
func balanceKey(account string) []byte {
	return []byte(balanceKeyPrefix + account)
}

// BalanceKey returns the KV key under which the given account's native MATRIX
// balance is persisted. It is exported so the issuance layer in internal/token
// (token.Treasury) can stage genesis and issuance credits in the same balance
// keyspace through an atomic kv batch, using the identical encoding this ledger
// uses. The on-disk layout (the market/balance/<account> prefix and 8-byte
// big-endian value) is unchanged.
func BalanceKey(account string) []byte {
	return balanceKey(account)
}

// Ledger is the native MATRIX balance ledger backed by a Pebble kv.Store.
// Balances are stored as big-endian uint64 values (native base units) under the
// `market/balance/<account>` key prefix. All mutations are guarded by a
// sync.RWMutex so concurrent callers observe consistent balances.
type Ledger struct {
	store *kv.Store
	mu    sync.RWMutex

	// heldSinceNS is the MONOTONIC nanosecond at which the CURRENT holder took
	// the write lock, or zero when nobody holds it.
	//
	// Monotonic and not wall-clock, deliberately. A wall-clock stamp makes the
	// watchdog fire on a clock STEP: an NTP correction or a VM resume that jumps
	// the clock forward past the threshold turns a healthy holder into a
	// reported stall and takes a working node out of rotation. lockedNanos
	// counts from process start and no adjustment can move it.
	//
	// It exists because a stalled ledger is otherwise silent. A goroutine that
	// takes this lock and never gives it back stops every writer AND every
	// reader (a pending writer blocks new RLocks), so the node keeps answering
	// gRPC health with SERVING while every balance read hangs and no block is
	// produced. That is not a hypothetical: it shipped, in a bridge lock that
	// re-entered this mutex from inside its own critical section, and finding it
	// took a SIGQUIT stack dump because nothing in any log said a word.
	//
	// A timestamp rather than a "locked" flag, because the question worth
	// asking is not whether the lock is held - it is held constantly, that is
	// its job - but whether THIS holder has had it for longer than any honest
	// holder ever needs. See LockStatus.
	heldSinceNS atomic.Int64

	// writeWaiters is how many goroutines are blocked trying to take the write
	// lock, and writeAcquires counts how many have ever succeeded.
	//
	// THESE ARE HOW A STUCK READER IS CAUGHT, and without them the watchdog has
	// a blind spot that reports the opposite of the truth. A parked RLock holder
	// wedges the ledger exactly as thoroughly as a parked writer - Go's RWMutex
	// is writer-preferring, so a queued writer stops all later readers too - but
	// heldSinceNS is ZERO the whole time, because no writer ever got in. The
	// watchdog would have read "not locked", concluded the stall was over, and
	// announced the node was serving again while nothing could proceed.
	//
	// A waiter count alone is not enough either: under sustained write load
	// there is always someone waiting, and their age grows without anything
	// being wrong. Progress is the honest signal, so the acquisition counter
	// goes alongside it: writers queued AND not one acquisition completing is a
	// wedge, at any load.
	writeWaiters  atomic.Int64
	writeAcquires atomic.Uint64
}

// ledgerStart anchors the monotonic clock used for lock ages. time.Since on a
// time.Time captured here reads its monotonic component, so the durations below
// are immune to wall-clock adjustment.
var ledgerStart = time.Now()

func monoNanos() int64 { return int64(time.Since(ledgerStart)) }

// LockStatus is a snapshot of what the ledger write lock is doing, for a
// watchdog that has to tell a wedged node from a busy one.
type LockStatus struct {
	// Held is whether a writer holds the lock right now, and HeldFor is how
	// long it has. A long hold is a stuck WRITER.
	Held    bool
	HeldFor time.Duration
	// WritersWaiting is how many writers are blocked. Together with Acquisitions
	// not advancing, it is a stuck READER: nobody holds the write lock, and
	// nobody can get it.
	WritersWaiting int
	// Acquisitions is the total number of write locks ever taken. A watchdog
	// compares it across samples; unchanged means no progress at all.
	Acquisitions uint64
}

// LockStatus reads the current state of the write lock. It takes no lock - it
// could not, since the thing it reports on is the lock being unavailable - and
// is safe to call from any goroutine at any time.
func (l *Ledger) LockStatus() LockStatus {
	st := LockStatus{
		WritersWaiting: int(l.writeWaiters.Load()),
		Acquisitions:   l.writeAcquires.Load(),
	}
	if since := l.heldSinceNS.Load(); since != 0 {
		st.Held = true
		if held := monoNanos() - since; held > 0 {
			st.HeldFor = time.Duration(held)
		}
	}
	return st
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

// lockWrite takes the write lock and records when, so a holder that never
// returns can be named by WriteLockHeldFor instead of being inferred from a
// hung node. EVERY write-lock acquisition goes through this pair: a site that
// took l.mu directly would be a stall the watchdog cannot see, which is the
// same silence this is here to end.
func (l *Ledger) lockWrite() {
	l.writeWaiters.Add(1)
	l.mu.Lock()
	l.writeWaiters.Add(-1)
	l.writeAcquires.Add(1)
	l.heldSinceNS.Store(monoNanos())
}

// unlockWrite clears the holder timestamp before releasing, so the window in
// which the lock is free but still looks held is empty rather than merely
// short.
func (l *Ledger) unlockWrite() {
	l.heldSinceNS.Store(0)
	l.mu.Unlock()
}

// ReadOnly runs fn as a single critical section under the ledger READ lock,
// passing the same LedgerTx view.
//
// It exists because a consistent SNAPSHOT does not need to exclude other
// readers, only writers, and taking the write lock for one is a self-inflicted
// wound: Bridge.Reconcile did exactly that, and Reconcile is reachable from an
// unauthenticated GetBridgeReconciliation, so polling a read serialized every
// block the node was trying to apply.
//
// The consistency it offers is the one a snapshot needs: no writer can be
// midway through a block while fn runs, so two values read inside it belong to
// the same committed state. What it does NOT offer is a read-modify-write - the
// LedgerTx handed over will fail any Transfer, because the caller holds no
// write lock. Anything that decides and then writes must use Atomically.
//
// A section here still wedges the ledger if it never returns: a queued writer
// blocks behind it and, since Go's RWMutex is writer-preferring, so does every
// later reader. That is why the watchdog watches queued writers and not only
// the write holder. See LockStatus.
func (l *Ledger) ReadOnly(fn func(tx LedgerTx) error) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return fn(readOnlyLedger{l})
}

// readOnlyLedger is the LedgerTx handed to ReadOnly. Balance reads through the
// same unlocked helper a write section uses; Transfer refuses rather than
// corrupting the store under a read lock, which is what calling
// transferLocked here would do - concurrently, since RLock admits many holders.
type readOnlyLedger struct{ l *Ledger }

func (t readOnlyLedger) Balance(account string) (uint64, error) {
	return t.l.readBalance(account)
}

func (t readOnlyLedger) Transfer(from, to string, amount uint64) error {
	return fmt.Errorf("market: Transfer inside a ReadOnly section: this holds the read "+
		"lock, which admits other readers, so writing here would race them; use "+
		"Atomically (from %q to %q, %d)", from, to, amount)
}

// Atomically runs fn as a single critical section under the ledger write lock,
// passing a LedgerTx view whose Balance/Transfer operate without re-locking.
// Because every mutating Ledger method (Credit, Debit, Transfer) takes the same
// write lock, an Atomically block is mutually exclusive with all of them: a
// caller can safely read a balance, make a decision, and transfer without a
// concurrent writer draining the account in between. fn's error is returned
// unchanged.
func (l *Ledger) Atomically(fn func(tx LedgerTx) error) error {
	l.lockWrite()
	defer l.unlockWrite()
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

// Balance returns the current native MATRIX balance (in native base units) for
// an account. Unknown accounts have a zero balance.
func (l *Ledger) Balance(account string) (uint64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.readBalance(account)
}

// Credit adds amount (native base units) to an account balance. It is the
// low-level, unguarded balance-increase primitive: it does NOT track cumulative
// issued supply or enforce the native supply cap, so honest minting of new
// native MATRIX must go through the supply-tracked path in internal/token
// (token.Treasury.ApplyGenesis / Issue) rather than calling Credit directly.
// Credit remains for internal, non-issuing balance adjustments and tests.
func (l *Ledger) Credit(account string, amount uint64) error {
	l.lockWrite()
	defer l.unlockWrite()

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
	l.lockWrite()
	defer l.unlockWrite()

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
	l.lockWrite()
	defer l.unlockWrite()
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
