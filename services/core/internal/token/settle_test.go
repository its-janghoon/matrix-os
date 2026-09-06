package token

import (
	"sync"
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

func TestSettledLedger_RejectsSelfTransfer(t *testing.T) {
	settled, ledger, chain := newSettledLedger(t)
	alice := mustAccount(t)

	if err := ledger.Credit(alice.AccountID(), 100); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}

	prev, _ := chain.HeadHash()
	tx := signedTx(t, alice, alice.AccountID(), 30, 0, prev) // From == To

	_, err := settled.Settle(tx)
	if err == nil {
		t.Fatal("Settle() self-transfer succeeded, want error")
	}
	if !IsSelfTransfer(err) {
		t.Fatalf("Settle() error = %v, want self-transfer", err)
	}

	// A self-transfer must not consume a nonce or append an empty record.
	if l, _ := chain.Len(); l != 0 {
		t.Errorf("chain Len() = %d, want 0 (no record for self-transfer)", l)
	}
	bal, _ := ledger.Balance(alice.AccountID())
	if bal != 100 {
		t.Errorf("alice balance = %d, want 100 (unchanged)", bal)
	}
}

func TestSettledLedger_RejectsEmptyRecipient(t *testing.T) {
	settled, ledger, chain := newSettledLedger(t)
	alice := mustAccount(t)

	if err := ledger.Credit(alice.AccountID(), 100); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}

	prev, _ := chain.HeadHash()
	tx := signedTx(t, alice, "", 30, 0, prev) // empty recipient

	if _, err := settled.Settle(tx); err == nil {
		t.Fatal("Settle() with empty recipient succeeded, want error")
	}
	if l, _ := chain.Len(); l != 0 {
		t.Errorf("chain Len() = %d, want 0 (no record for empty recipient)", l)
	}
}

// TestSettledLedger_ConcurrentCompletionPreservesInvariant is the regression
// test for the append-then-transfer race: a settlement and a direct ledger
// transfer (the path market.CompleteJob uses) run concurrently against the same
// funded sender. Under the old design (affordability pre-check under a separate
// SettledLedger lock, then a lock-free window before Transfer) the direct
// transfer could drain the sender between Settle's check and its transfer,
// persisting a chain record whose transfer failed and breaking chain/ledger
// consistency. Because Settle now runs the check+append+transfer as one
// critical section under the ledger write lock, the invariant "chain length
// equals the number of successful settlements, and every settled amount is
// backed by a real balance movement" holds. Run under -race.
func TestSettledLedger_ConcurrentCompletionPreservesInvariant(t *testing.T) {
	const iterations = 200
	for i := 0; i < iterations; i++ {
		settled, ledger, chain := newSettledLedger(t)
		alice := mustAccount(t)
		bob := mustAccount(t)
		carol := mustAccount(t)

		// Fund alice with exactly enough for ONE of the two competing debits.
		if err := ledger.Credit(alice.AccountID(), 50); err != nil {
			t.Fatalf("Credit() error = %v", err)
		}

		prev, _ := chain.HeadHash()
		tx := signedTx(t, alice, bob.AccountID(), 50, 0, prev)

		var wg sync.WaitGroup
		wg.Add(2)

		var settleErr error
		go func() {
			defer wg.Done()
			_, settleErr = settled.Settle(tx)
		}()
		go func() {
			defer wg.Done()
			// Direct transfer mirrors market.CompleteJob draining the same account.
			_ = ledger.Transfer(alice.AccountID(), carol.AccountID(), 50)
		}()
		wg.Wait()

		// Invariant: the chain advanced if and only if the settlement succeeded.
		l, _ := chain.Len()
		if settleErr == nil && l != 1 {
			t.Fatalf("iter %d: settle ok but chain Len()=%d, want 1", i, l)
		}
		if settleErr != nil && l != 0 {
			t.Fatalf("iter %d: settle failed (%v) but chain Len()=%d, want 0 (record without transfer)", i, settleErr, l)
		}

		// Cross-check: total credits conserved (alice started with 50, the two
		// competitors move 50 to either bob or carol, never both).
		aliceBal, _ := ledger.Balance(alice.AccountID())
		bobBal, _ := ledger.Balance(bob.AccountID())
		carolBal, _ := ledger.Balance(carol.AccountID())
		if total := aliceBal + bobBal + carolBal; total != 50 {
			t.Fatalf("iter %d: credits not conserved: alice=%d bob=%d carol=%d total=%d, want 50",
				i, aliceBal, bobBal, carolBal, total)
		}
		// And the chain must remain valid.
		if err := chain.ValidateChain(); err != nil {
			t.Fatalf("iter %d: ValidateChain() error = %v", i, err)
		}
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
