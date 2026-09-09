package node

import (
	"math/big"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/bridge"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// fixedHeight is a heightSource test double returning a constant committed
// height, so the reconciler's block-height stamping can be asserted without a
// full consensus engine.
type fixedHeight uint64

func (h fixedHeight) Height() uint64 { return uint64(h) }

// TestBridgeReconciler_StampsSnapshotFromRealBridge builds the reconciler over
// a real direct-mode bridge test helper, drives a known lock and partial unlock,
// and asserts the marketapi snapshot mirrors Bridge.Reconcile with the committed
// height stamped in. matrixd itself always uses consensus-ordered burn release;
// direct mode remains useful here to isolate reconciliation from consensus.
func TestBridgeReconciler_StampsSnapshotFromRealBridge(t *testing.T) {
	store, ledger := newBridgeTestLedger(t)

	user, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	userID := user.AccountID()
	const funded = 2 * token.MinBridgeLockAmount
	const locked = token.MinBridgeLockAmount
	if err := ledger.Credit(userID, funded); err != nil {
		t.Fatalf("Credit: %v", err)
	}

	contract, err := bridge.ParseAddress(testContract)
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	b := bridge.New(ledger, store, bridge.AttestationParams{ChainID: big.NewInt(testChainID), BridgeContract: contract})

	// Lock native into escrow, then release part of it via a processed burn, so
	// locked != unlocked != 0 and the snapshot is non-trivial.
	var l1Recipient bridge.Address
	l1Recipient[19] = 0x01
	if _, err := b.Lock(userID, l1Recipient, locked); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	const unlockNative = token.MinBridgeLockAmount / 4
	if err := b.ProcessBurn(bridge.BurnEvent{
		ID:          "burn-recon-1",
		ToAccount:   userID,
		ERC20Amount: token.NativeToERC20(unlockNative),
	}); err != nil {
		t.Fatalf("ProcessBurn: %v", err)
	}

	const committedHeight = uint64(77)
	rec := newBridgeReconciler(b, fixedHeight(committedHeight))
	if rec == nil {
		t.Fatal("expected a reconciler for a configured bridge")
	}

	snap, err := rec.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	wantOutstanding := locked - unlockNative
	if snap.LockedNative != locked || snap.UnlockedNative != unlockNative {
		t.Fatalf("locked/unlocked = %d/%d, want %d/%d", snap.LockedNative, snap.UnlockedNative, locked, unlockNative)
	}
	if snap.OutstandingNative != wantOutstanding || snap.EscrowBalance != wantOutstanding {
		t.Fatalf("outstanding/escrow = %d/%d, want %d", snap.OutstandingNative, snap.EscrowBalance, wantOutstanding)
	}
	if want := token.NativeToERC20(wantOutstanding); snap.OutstandingERC20.Cmp(want) != 0 {
		t.Fatalf("outstanding erc20 = %s, want %s", snap.OutstandingERC20, want)
	}
	if snap.BlockHeight != committedHeight {
		t.Fatalf("block height = %d, want the stamped committed height %d", snap.BlockHeight, committedHeight)
	}
}

// TestBridgeReconciler_NilBridge asserts the adapter is nil when no bridge is
// configured, which is what makes the market service report FailedPrecondition
// rather than serving an empty snapshot.
func TestBridgeReconciler_NilBridge(t *testing.T) {
	if got := newBridgeReconciler(nil, fixedHeight(1)); got != nil {
		t.Fatalf("newBridgeReconciler(nil, ...) = %v, want nil", got)
	}
}

// TestBridgeReconciler_NilHeightSource asserts a nil height source degrades to a
// zero block height rather than panicking, so the reconciler still works on a
// node wired without consensus.
func TestBridgeReconciler_NilHeightSource(t *testing.T) {
	store, ledger := newBridgeTestLedger(t)
	b, err := newConfiguredBridge(ledger, store, BridgeConfig{Contract: testContract, ChainID: testChainID})
	if err != nil {
		t.Fatalf("newConfiguredBridge: %v", err)
	}
	rec := newBridgeReconciler(b, nil)
	snap, err := rec.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if snap.BlockHeight != 0 {
		t.Fatalf("block height = %d, want 0 with a nil height source", snap.BlockHeight)
	}
}

// TestBridgeReconciler_PropagatesMismatch asserts an escrow/accounting mismatch
// surfaces as an error (the handler maps it to codes.Internal). The mismatch is
// forced by draining escrow out from under the accounting via a direct ledger
// transfer, exactly the tampering Bridge.Reconcile exists to catch.
func TestBridgeReconciler_PropagatesMismatch(t *testing.T) {
	store, ledger := newBridgeTestLedger(t)

	user, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	userID := user.AccountID()
	if err := ledger.Credit(userID, token.MinBridgeLockAmount); err != nil {
		t.Fatalf("Credit: %v", err)
	}

	b, err := newConfiguredBridge(ledger, store, BridgeConfig{Contract: testContract, ChainID: testChainID})
	if err != nil {
		t.Fatalf("newConfiguredBridge: %v", err)
	}
	var l1Recipient bridge.Address
	l1Recipient[19] = 0x02
	if _, err := b.Lock(userID, l1Recipient, token.MinBridgeLockAmount); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	// Tamper: move escrow out of band so the on-ledger balance no longer matches
	// the (locked - unlocked) accounting.
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return ltx.Transfer(bridge.EscrowAccount, userID, token.NativeUnit)
	}); err != nil {
		t.Fatalf("tamper transfer: %v", err)
	}

	rec := newBridgeReconciler(b, fixedHeight(1))
	if _, err := rec.Reconcile(); err == nil {
		t.Fatal("expected a reconciliation mismatch error after tampering with escrow")
	}
}
