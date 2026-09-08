package bridge

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

func orderHarness(t *testing.T, consensusOrdered bool) (*Bridge, *market.Ledger) {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ledger := market.NewLedger(store)
	params := AttestationParams{ChainID: big.NewInt(31337)}
	if consensusOrdered {
		return NewConsensusOrdered(ledger, store, params), ledger
	}
	return New(ledger, store, params), ledger
}

const orderTestAccount = "dfb781c10b66f1eb88372bbb038a03d2b0b507e8d772aacaaa39acc70ca5070f"

// TestConsensusOrderedBridgeRefusesDirectProcessBurn is the guard for the fork.
// A per-node relayer releases escrow on its own ledger and nowhere else, so on a
// validator set the nodes' escrow balances and their 1:1 backing invariant
// diverge - the same divergence that made FundAccount unsafe, except this one
// moves real collateral. Wiring the bridge itself into a watcher on such a
// network must fail loudly rather than silently fork.
func TestConsensusOrderedBridgeRefusesDirectProcessBurn(t *testing.T) {
	b, ledger := orderHarness(t, true)
	if err := ledger.Credit(EscrowAccount, 1000); err != nil {
		t.Fatalf("credit escrow: %v", err)
	}

	err := b.ProcessBurn(BurnEvent{
		ID:          "0xdeadbeef:0",
		ToAccount:   orderTestAccount,
		ERC20Amount: token.NativeToERC20(500),
	})
	if !errors.Is(err, ErrConsensusOrdered) {
		t.Fatalf("error = %v, want ErrConsensusOrdered", err)
	}
	bal, err := ledger.Balance(EscrowAccount)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if bal != 1000 {
		t.Fatalf("escrow = %d, want 1000 unchanged after a refused direct burn", bal)
	}
}

// TestDirectBridgeRefusesAttestedUnlock is the other half of the exclusivity.
// ProcessBurn takes b.mu then the ledger; ApplyAttestedUnlock is called with the
// ledger already held. Two orders over the same two locks is a deadlock waiting
// for the run where both paths are live, so the modes exclude each other rather
// than relying on the caller.
func TestDirectBridgeRefusesAttestedUnlock(t *testing.T) {
	b, ledger := orderHarness(t, false)
	if err := ledger.Credit(EscrowAccount, 1000); err != nil {
		t.Fatalf("credit escrow: %v", err)
	}
	err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return b.ApplyAttestedUnlock(ltx, BurnIDHash("0xdeadbeef:0"), orderTestAccount, 500)
	})
	if !errors.Is(err, ErrNotConsensusOrdered) {
		t.Fatalf("error = %v, want ErrNotConsensusOrdered", err)
	}
}

// TestAttestedUnlockReleasesEscrowExactlyOnce covers the working path and the
// idempotence the consensus interface contract requires: a node that applies the
// same committed block twice has to reach the same escrow balance.
func TestAttestedUnlockReleasesEscrowExactlyOnce(t *testing.T) {
	b, ledger := orderHarness(t, true)
	if err := ledger.Credit(EscrowAccount, 1000); err != nil {
		t.Fatalf("credit escrow: %v", err)
	}
	hash := BurnIDHash("0xdeadbeef:0")

	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return b.ApplyAttestedUnlock(ltx, hash, orderTestAccount, 500)
	}); err != nil {
		t.Fatalf("first unlock: %v", err)
	}

	err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return b.ApplyAttestedUnlock(ltx, hash, orderTestAccount, 500)
	})
	if !errors.Is(err, ErrBurnAlreadyProcessed) {
		t.Fatalf("second unlock error = %v, want ErrBurnAlreadyProcessed", err)
	}

	escrow, err := ledger.Balance(EscrowAccount)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if escrow != 500 {
		t.Fatalf("escrow = %d, want 500: the burn was released twice", escrow)
	}
	paid, err := ledger.Balance(orderTestAccount)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if paid != 500 {
		t.Fatalf("recipient = %d, want 500", paid)
	}
}

// TestAttestedUnlockAndProcessBurnShareTheSameReplayMarker: the two paths must
// track the same burn as the same burn, or a network that switched modes could
// release one burn twice.
func TestAttestedUnlockAndProcessBurnShareTheSameReplayMarker(t *testing.T) {
	burnID := "0xdeadbeef:7"

	direct, ledger := orderHarness(t, false)
	if err := ledger.Credit(EscrowAccount, 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := direct.ProcessBurn(BurnEvent{
		ID:          burnID,
		ToAccount:   orderTestAccount,
		ERC20Amount: token.NativeToERC20(500),
	}); err != nil {
		t.Fatalf("ProcessBurn: %v", err)
	}

	// Same store, now read by a consensus-ordered bridge: the marker written by
	// the direct path must already block the attested path.
	direct.consensusOrdered = true
	err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return direct.ApplyAttestedUnlock(ltx, BurnIDHash(burnID), orderTestAccount, 500)
	})
	if !errors.Is(err, ErrBurnAlreadyProcessed) {
		t.Fatalf("error = %v, want ErrBurnAlreadyProcessed: the two paths do not share a replay marker", err)
	}
}

// TestAttestedUnlockRejectsNonsense pins the input checks, since consensus hands
// these values straight from a recipient string.
func TestAttestedUnlockRejectsNonsense(t *testing.T) {
	b, ledger := orderHarness(t, true)
	if err := ledger.Credit(EscrowAccount, 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	cases := []struct {
		name    string
		hash    string
		account string
		amount  uint64
	}{
		{"empty burn id", "", orderTestAccount, 500},
		{"empty recipient", BurnIDHash("a"), "", 500},
		{"zero amount", BurnIDHash("a"), orderTestAccount, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ledger.Atomically(func(ltx market.LedgerTx) error {
				return b.ApplyAttestedUnlock(ltx, tc.hash, tc.account, tc.amount)
			})
			if err == nil {
				t.Fatal("want an error")
			}
		})
	}
	bal, err := ledger.Balance(EscrowAccount)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if bal != 1000 {
		t.Fatalf("escrow = %d, want 1000 unchanged", bal)
	}
}

// TestADirectBridgeStillRecordsAConsensusLock pins the ASYMMETRY between the two
// halves, which is easy to get backwards. The unlock half has a mode - a solo
// node may release escrow itself, a set may not - so a direct bridge refuses
// ApplyAttestedUnlock, as the test above requires. The lock half has no mode:
// locking is a transaction, so every lock on every node arrives through the
// engine, and a solo node runs a direct bridge. Gating RecordLock the same way
// would leave exactly that node escrowing value it never recorded - the lock
// invisible to attestation, and Reconcile failing on the gap it left.
func TestADirectBridgeStillRecordsAConsensusLock(t *testing.T) {
	b, ledger := orderHarness(t, false)
	if err := ledger.Credit(orderTestAccount, 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	id := [LockIDLen]byte{0x01}
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		if err := ltx.Transfer(orderTestAccount, EscrowAccount, 500); err != nil {
			return err
		}
		return b.RecordLock(ltx, id, orderTestAccount, Address{0xaa}, 500)
	}); err != nil {
		t.Fatalf("a solo node could not record a committed lock: %v", err)
	}
	if _, err := b.GetLock(id); err != nil {
		t.Fatalf("GetLock after recording: %v", err)
	}
	rec, err := b.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rec.OutstandingNative != 500 || rec.EscrowBalance != 500 {
		t.Fatalf("outstanding %d escrow %d, want 500/500", rec.OutstandingNative, rec.EscrowBalance)
	}
}

// TestRecordLockRunsInsideTheCallersSection pins the contract that the engine
// depends on: RecordLock is callable with the ledger write lock already held, it
// records the lock, and it is idempotent per id so a replayed block does not
// double count. Calling it twice inside ONE section is the strongest form of the
// check - a second lock acquisition anywhere in it would never return.
func TestRecordLockRunsInsideTheCallersSection(t *testing.T) {
	b, ledger := orderHarness(t, true)
	if err := ledger.Credit(orderTestAccount, 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	id := [LockIDLen]byte{0x07}

	done := make(chan error, 1)
	go func() {
		done <- ledger.Atomically(func(ltx market.LedgerTx) error {
			if err := ltx.Transfer(orderTestAccount, EscrowAccount, 500); err != nil {
				return err
			}
			if err := b.RecordLock(ltx, id, orderTestAccount, Address{0xaa}, 500); err != nil {
				return err
			}
			// Twice, in the same section: this is what block replay looks like.
			return b.RecordLock(ltx, id, orderTestAccount, Address{0xaa}, 500)
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("record inside the caller's section: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RecordLock did not return inside the caller's ledger section: it is taking a " +
			"lock the caller already holds, which deadlocks a node's consensus driver")
	}

	ev, err := b.GetLock(id)
	if err != nil {
		t.Fatalf("GetLock: %v", err)
	}
	if ev.NativeAmount != 500 {
		t.Fatalf("recorded %d native, want 500", ev.NativeAmount)
	}
	rec, err := b.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rec.OutstandingNative != 500 {
		t.Fatalf("outstanding = %d, want 500: recording the same lock twice double counted it",
			rec.OutstandingNative)
	}
}

// Reconcile must not take the ledger WRITE lock.
//
// It is a read-only backing snapshot, and it is reachable from an
// UNAUTHENTICATED GetBridgeReconciliation. When it opened a write section,
// anyone who could poll that read serialized every block the node was applying:
// a monitoring loop pointed at the health of the bridge would have throttled
// the chain.
//
// Held deterministic by running it while a read section is open. A read section
// admits other readers and excludes writers, so a Reconcile that finished
// proves it wanted the read lock, and one that blocks proves it wanted the
// write lock.
func TestReconcileDoesNotTakeTheWriteLock(t *testing.T) {
	b, ledger := orderHarness(t, true)

	inRead := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = ledger.ReadOnly(func(market.LedgerTx) error {
			close(inRead)
			<-release
			return nil
		})
	}()
	<-inRead
	defer close(release)

	done := make(chan error, 1)
	go func() {
		_, err := b.Reconcile()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Reconcile blocked behind an open read section, so it is taking the ledger " +
			"WRITE lock for a read-only snapshot: an unauthenticated poll of " +
			"GetBridgeReconciliation can stall block application")
	}
}
