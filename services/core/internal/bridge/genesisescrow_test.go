package bridge

import (
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
)

// A relaunched chain is handed its escrow instead of locking for it.
//
// The wrapped supply on Base does not know the native chain restarted, so the
// escrow has to carry over as a genesis allocation. But locked/unlocked only
// move on a lock, so the new chain opened holding collateral it had no record
// of receiving: Reconcile computes outstanding as locked minus unlocked, got
// zero against a non-empty escrow, and refused to report at all. The line above
// it - unlocked must never exceed locked - then made every future unlock
// impossible. The escrow was present, correct, and could neither move nor prove
// itself.

func escrowBridge(t *testing.T, escrow uint64) (*Bridge, *market.Ledger) {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ledger := market.NewLedger(store)
	if escrow > 0 {
		if err := ledger.Credit(EscrowAccount, escrow); err != nil {
			t.Fatalf("credit escrow: %v", err)
		}
	}
	return NewConsensusOrdered(ledger, store, AttestationParams{}), ledger
}

func TestAChainHandedItsEscrowCanReconcile(t *testing.T) {
	const carried = 50_100_000_000_000_000
	b, _ := escrowBridge(t, carried)

	if _, err := b.Reconcile(); err == nil {
		t.Fatal("precondition: a chain that has not adopted its escrow should not reconcile")
	}

	if err := b.AdoptGenesisEscrow(); err != nil {
		t.Fatalf("AdoptGenesisEscrow: %v", err)
	}

	rec, err := b.Reconcile()
	if err != nil {
		t.Fatalf("after adopting the genesis escrow, reconciliation must close: %v", err)
	}
	if rec.EscrowBalance != carried || rec.OutstandingNative != carried {
		t.Fatalf("escrow %d / outstanding %d, want both %d",
			rec.EscrowBalance, rec.OutstandingNative, carried)
	}
	if rec.UnlockedNative != 0 {
		t.Fatalf("nothing has been unlocked yet; got %d", rec.UnlockedNative)
	}
}

// Running twice must not double the opening total, because the second call sees
// a chain that has locked.
func TestAdoptingTheGenesisEscrowIsIdempotent(t *testing.T) {
	const carried = 1_234_000_000
	b, _ := escrowBridge(t, carried)
	for i := 0; i < 3; i++ {
		if err := b.AdoptGenesisEscrow(); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	rec, err := b.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rec.OutstandingNative != carried {
		t.Fatalf("outstanding %d after three calls, want %d", rec.OutstandingNative, carried)
	}
}

// A chain with no bridge escrow gets nothing, and still reconciles at zero.
func TestAChainWithNoEscrowAdoptsNothing(t *testing.T) {
	b, _ := escrowBridge(t, 0)
	if err := b.AdoptGenesisEscrow(); err != nil {
		t.Fatalf("AdoptGenesisEscrow: %v", err)
	}
	rec, err := b.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rec.OutstandingNative != 0 || rec.EscrowBalance != 0 {
		t.Fatalf("expected an empty bridge; got outstanding %d escrow %d",
			rec.OutstandingNative, rec.EscrowBalance)
	}
}

// The dangerous case. A chain with its own locking history must be left alone:
// seeding locked from the escrow balance there would overwrite a real running
// total with the remainder after unlocks, and the bridge would then believe less
// had ever been locked than actually was.
func TestAChainWithItsOwnHistoryIsNotTouched(t *testing.T) {
	const (
		everLocked   = 100_000
		everUnlocked = 40_000
		escrow       = everLocked - everUnlocked
	)
	b, _ := escrowBridge(t, escrow)

	batch := b.store.NewBatch()
	if err := batch.Set([]byte(lockedTTLKey), encodeU64(everLocked), nil); err != nil {
		t.Fatal(err)
	}
	if err := batch.Set([]byte(unlockedTTLKey), encodeU64(everUnlocked), nil); err != nil {
		t.Fatal(err)
	}
	if err := batch.Commit(nil); err != nil {
		t.Fatal(err)
	}
	_ = batch.Close()

	if err := b.AdoptGenesisEscrow(); err != nil {
		t.Fatalf("AdoptGenesisEscrow: %v", err)
	}

	got, err := b.readUint64(lockedTTLKey)
	if err != nil {
		t.Fatal(err)
	}
	if got != everLocked {
		t.Fatalf("locked total is %d, want it untouched at %d - a live chain's history was overwritten", got, everLocked)
	}
	rec, err := b.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rec.OutstandingNative != escrow {
		t.Fatalf("outstanding %d, want %d", rec.OutstandingNative, escrow)
	}
}
