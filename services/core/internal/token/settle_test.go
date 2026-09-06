package token

import (
	"testing"

	"github.com/ecirlabs/matrix-core/internal/market"
)

// newSettledLedger builds a SettledLedger and its underlying market.Ledger and
// Chain over a single shared temp store.
func newSettledLedger(t *testing.T) (*SettledLedger, *market.Ledger, *Chain) {
	t.Helper()
	store := newTestStore(t)
	ledger := market.NewLedger(store)
	chain := NewChain(store)
	return NewSettledLedger(ledger, chain), ledger, chain
}

func TestSettledLedger_SettleMovesCreditsAndRecordsTx(t *testing.T) {
	settled, ledger, chain := newSettledLedger(t)
	alice := mustAccount(t)
	bob := mustAccount(t)

	// Fund alice through the internal (unsigned) ledger path (bootstrap/mint).
	if err := ledger.Credit(alice.AccountID(), 100); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}

	prev, _ := chain.HeadHash()
	tx := signedTx(t, alice, bob.AccountID(), 30, 0, prev)

	rec, err := settled.Settle(tx)
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if rec.Height != 0 {
		t.Errorf("record height = %d, want 0", rec.Height)
	}

	// Balances moved.
	aliceBal, _ := ledger.Balance(alice.AccountID())
	bobBal, _ := ledger.Balance(bob.AccountID())
	if aliceBal != 70 {
		t.Errorf("alice balance = %d, want 70", aliceBal)
	}
	if bobBal != 30 {
		t.Errorf("bob balance = %d, want 30", bobBal)
	}

	// A chained tx was recorded and the chain is valid.
	if l, _ := chain.Len(); l != 1 {
		t.Errorf("chain Len() = %d, want 1", l)
	}
	if err := chain.ValidateChain(); err != nil {
		t.Errorf("ValidateChain() error = %v", err)
	}
}

func TestSettledLedger_UnaffordableLeavesBalancesAndChainUnchanged(t *testing.T) {
	settled, ledger, chain := newSettledLedger(t)
	alice := mustAccount(t)
	bob := mustAccount(t)

	if err := ledger.Credit(alice.AccountID(), 10); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}

	prev, _ := chain.HeadHash()
	tx := signedTx(t, alice, bob.AccountID(), 50, 0, prev) // more than alice has

	_, err := settled.Settle(tx)
	if err == nil {
		t.Fatal("Settle() unaffordable transfer succeeded, want error")
	}
	if !IsUnaffordable(err) {
		t.Fatalf("Settle() error = %v, want unaffordable", err)
	}

	// No credits moved.
	aliceBal, _ := ledger.Balance(alice.AccountID())
	bobBal, _ := ledger.Balance(bob.AccountID())
	if aliceBal != 10 {
		t.Errorf("alice balance = %d, want 10 (unchanged)", aliceBal)
	}
	if bobBal != 0 {
		t.Errorf("bob balance = %d, want 0 (unchanged)", bobBal)
	}

	// No transaction appended.
	if l, _ := chain.Len(); l != 0 {
		t.Errorf("chain Len() = %d, want 0 (no tx appended)", l)
	}
}

func TestSettledLedger_InvalidSignatureAppendsNothing(t *testing.T) {
	settled, ledger, chain := newSettledLedger(t)
	alice := mustAccount(t)
	bob := mustAccount(t)

	if err := ledger.Credit(alice.AccountID(), 100); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}

	prev, _ := chain.HeadHash()
	tx := signedTx(t, alice, bob.AccountID(), 30, 0, prev)
	tx.Amount = 40 // invalidate signature by tampering after signing

	if _, err := settled.Settle(tx); err == nil {
		t.Fatal("Settle() with tampered tx succeeded, want error")
	}

	// Nothing moved, nothing appended.
	aliceBal, _ := ledger.Balance(alice.AccountID())
	if aliceBal != 100 {
		t.Errorf("alice balance = %d, want 100 (unchanged)", aliceBal)
	}
	if l, _ := chain.Len(); l != 0 {
		t.Errorf("chain Len() = %d, want 0", l)
	}
}

func TestSettledLedger_MultipleSettlementsChainAndNonce(t *testing.T) {
	settled, ledger, chain := newSettledLedger(t)
	alice := mustAccount(t)
	bob := mustAccount(t)

	if err := ledger.Credit(alice.AccountID(), 100); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}

	for i, amount := range []uint64{10, 20, 5} {
		prev, _ := chain.HeadHash()
		tx := signedTx(t, alice, bob.AccountID(), amount, uint64(i), prev)
		if _, err := settled.Settle(tx); err != nil {
			t.Fatalf("Settle() #%d error = %v", i, err)
		}
	}

	aliceBal, _ := ledger.Balance(alice.AccountID())
	bobBal, _ := ledger.Balance(bob.AccountID())
	if aliceBal != 65 {
		t.Errorf("alice balance = %d, want 65", aliceBal)
	}
	if bobBal != 35 {
		t.Errorf("bob balance = %d, want 35", bobBal)
	}
	if l, _ := chain.Len(); l != 3 {
		t.Errorf("chain Len() = %d, want 3", l)
	}
	if err := chain.ValidateChain(); err != nil {
		t.Errorf("ValidateChain() error = %v", err)
	}
}
