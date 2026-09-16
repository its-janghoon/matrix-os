package consensus

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// A budget, from the buyer's one signature to the money coming back.
//
// The unit tests beside this one check that a budget has exactly one spelling.
// These check that four nodes agree about what spending one DOES, which is the
// property the account name exists to produce: the terms are in the name, the
// remaining budget is that name's balance, and a balance is already inside the
// state root that every validator compares.

// budgetsActive schedules the activation at height 1, which is the earliest a
// schedule can name: normalizeUpgrades puts the genesis version at height 0, so
// a second version there would be two versions activating at one height.
func budgetsActive(c *Config) {
	c.ProtocolUpgrades = append(c.ProtocolUpgrades, ProtocolUpgrade{Height: 1, Version: ProtocolVersionSpendBudgets})
}

func budgetFor(t *testing.T, buyer *token.Account, delegate ed25519.PublicKey, expiry int64) SpendEscrow {
	t.Helper()
	return SpendEscrow{
		Buyer:           buyer.AccountID(),
		Delegate:        hex.EncodeToString(delegate),
		PerJobCap:       200_000,
		MaxPricePerUnit: 500,
		Expiry:          expiry,
		Nonce:           0,
	}
}

func TestABuyerSignsOnceAndADelegateSpendsTheBudget(t *testing.T) {
	nodes, stop := newCluster(t, 4, budgetsActive)
	defer stop()

	buyer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate buyer: %v", err)
	}
	// The delegate is a key that holds nothing. That is the whole point: it can
	// move the buyer's money only inside the bounds the buyer's own signature
	// put in the account's name, and it can do nothing else with it.
	delegate, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate delegate: %v", err)
	}

	const funded = 1_000_000
	const budgetSize = 600_000
	const draw = 150_000
	mintAll(t, nodes, buyer.AccountID(), funded)

	budget := budgetFor(t, buyer, delegate.PublicKey, time.Now().Add(time.Hour).Unix())
	seller := hex.EncodeToString(make([]byte, 32))

	// ONE signature from the buyer opens it.
	open := signedTransfer(t, buyer, budget.Account(), budgetSize, 1)
	submitAll(t, nodes, open)
	waitFor(t, 20*time.Second, "the budget to be funded on every node", func() bool {
		return everyNodeHas(nodes, budget.Account(), budgetSize)
	})
	if bal, _ := nodes[0].ledger.Balance(buyer.AccountID()); bal != funded-budgetSize {
		t.Fatalf("buyer balance %d, want %d - opening a budget must not cost a fee", bal, funded-budgetSize)
	}

	// The delegate spends it, and the buyer is not asked.
	drawTx := signedTransfer(t, delegate, budget.DrawRecipient(seller), draw, 1)
	submitAll(t, nodes, drawTx)
	waitFor(t, 20*time.Second, "the seller to be paid on every node", func() bool {
		return everyNodeHas(nodes, seller, draw)
	})
	waitFor(t, 20*time.Second, "the budget to be debited on every node", func() bool {
		return everyNodeHas(nodes, budget.Account(), budgetSize-draw)
	})

	// And the remainder comes back, to the buyer, on a close the buyer signs.
	closeTx := signedTransfer(t, buyer, budget.CloseRecipient(), 0, 2)
	submitAll(t, nodes, closeTx)
	waitFor(t, 20*time.Second, "the remainder to return to the buyer", func() bool {
		return everyNodeHas(nodes, buyer.AccountID(), funded-draw)
	})
	if bal, _ := nodes[0].ledger.Balance(budget.Account()); bal != 0 {
		t.Fatalf("closed budget still holds %d", bal)
	}

	// Nothing was created or destroyed on the way through: what the buyer no
	// longer has is exactly what the seller got.
	for i, nd := range nodes {
		buyerBal, _ := nd.ledger.Balance(buyer.AccountID())
		sellerBal, _ := nd.ledger.Balance(seller)
		if buyerBal+sellerBal != funded {
			t.Fatalf("node %d: buyer %d + seller %d != %d", i, buyerBal, sellerBal, funded)
		}
	}
}

// Everything a delegate must not be able to do, refused where a caller finds
// out about it rather than silently at apply time.
func TestABudgetRefusesWhatItWasBoundedAgainst(t *testing.T) {
	nodes, stop := newCluster(t, 1, budgetsActive)
	defer stop()
	engine := nodes[0].engine

	buyer, _ := token.GenerateAccount()
	delegate, _ := token.GenerateAccount()
	stranger, _ := token.GenerateAccount()
	budget := budgetFor(t, buyer, delegate.PublicKey, time.Now().Add(time.Hour).Unix())
	seller := hex.EncodeToString(make([]byte, 32))

	cases := []struct {
		name string
		tx   *token.Transaction
	}{
		{
			// The signature says who is spending. A stranger holding no grant
			// is the case the whole design exists to refuse.
			"a stranger draws",
			signedTransfer(t, stranger, budget.DrawRecipient(seller), 1_000, 1),
		},
		{
			// The buyer's own key is not the delegate either. Terms name one
			// key and consensus checks that one, which is what makes a
			// superseded or revoked delegate actually powerless.
			"the buyer draws on their own delegate's budget",
			signedTransfer(t, buyer, budget.DrawRecipient(seller), 1_000, 1),
		},
		{
			"a draw over the per-job cap",
			signedTransfer(t, delegate, budget.DrawRecipient(seller), budget.PerJobCap+1, 1),
		},
		{
			"a draw of nothing",
			signedTransfer(t, delegate, budget.DrawRecipient(seller), 0, 1),
		},
		{
			// A refund wearing a draw's clothes: it would move money back to
			// the buyer without obeying the expiry rule a close obeys.
			"a draw that pays the buyer",
			signedTransfer(t, delegate, budget.DrawRecipient(buyer.AccountID()), 1_000, 1),
		},
		{
			// A delegate whose output could be a bond, a set change or a bridge
			// lock is not bounded to paying sellers any more. The namespace list
			// is consensus's, which is why this refusal lives here and not in
			// the naming.
			"a draw into a bond",
			signedTransfer(t, delegate, budget.DrawRecipient("consensus/stake/bond/"+buyer.AccountID()), 1_000, 1),
		},
		{
			"a draw into the bridge escrow",
			signedTransfer(t, delegate, budget.DrawRecipient("bridge/escrow"), 1_000, 1),
		},
		{
			// A budget nobody holds a key for is a deposit nothing can close.
			"a budget owned by a reserved account",
			signedTransfer(t, buyer, SpendEscrow{
				Buyer:           "consensus/stake/bond/" + buyer.AccountID(),
				Delegate:        budget.Delegate,
				PerJobCap:       budget.PerJobCap,
				MaxPricePerUnit: budget.MaxPricePerUnit,
				Expiry:          budget.Expiry,
			}.Account(), 1_000, 1),
		},
		{
			// The amount is the whole remaining balance and is not the caller's
			// to choose, exactly as a bond withdrawal's is not.
			"a close that names an amount",
			signedTransfer(t, buyer, budget.CloseRecipient(), 1, 1),
		},
		{
			"opening a budget with nothing in it",
			signedTransfer(t, buyer, budget.Account(), 0, 1),
		},
		{
			// A deposit into someone else's budget is money only they can ever
			// close, which is a way to lose it by typing.
			"a stranger funds somebody else's budget",
			signedTransfer(t, stranger, budget.Account(), 1_000, 1),
		},
	}
	for _, tc := range cases {
		if err := engine.Submit(tc.tx); err == nil {
			t.Errorf("%s was accepted", tc.name)
		}
	}
}

// Expiry and the balance are the two bounds consensus enforces, and both are
// apply-time facts: the clock is the BLOCK's timestamp and the balance is
// whatever the committed prefix left. They are exercised against the apply path
// directly, because reaching them through a cluster would mean waiting out a
// real expiry and asserting on a wall clock.
func TestTheBlockClockAndTheBalanceAreWhatBoundADelegate(t *testing.T) {
	nodes, stop := newCluster(t, 1, budgetsActive)
	defer stop()
	engine, ledger := nodes[0].engine, nodes[0].ledger

	buyer, _ := token.GenerateAccount()
	delegate, _ := token.GenerateAccount()
	stranger, _ := token.GenerateAccount()
	const expiry int64 = 2_000_000_000
	budget := budgetFor(t, buyer, delegate.PublicKey, expiry)
	seller := hex.EncodeToString(make([]byte, 32))

	apply := func(tx *token.Transaction, blockTime int64) spendEffect {
		t.Helper()
		var eff spendEffect
		if err := ledger.Atomically(func(ltx market.LedgerTx) error {
			var err error
			eff, err = engine.applySpendOperation(ltx, tx, blockTime)
			return err
		}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		return eff
	}
	balance := func(account string) uint64 {
		t.Helper()
		bal, err := ledger.Balance(account)
		if err != nil {
			t.Fatalf("balance %s: %v", account, err)
		}
		return bal
	}

	mintAll(t, nodes, buyer.AccountID(), 1_000_000)

	// A budget opened at or after its own expiry would be money nothing can
	// draw and only a second transaction can retrieve.
	if eff := apply(signedTransfer(t, buyer, budget.Account(), 500_000, 1), expiry); eff.applied {
		t.Fatal("a budget was opened at its expiry")
	}
	if eff := apply(signedTransfer(t, buyer, budget.Account(), 500_000, 1), expiry-1); !eff.applied {
		t.Fatal("a budget could not be opened one second before expiry")
	}
	if got := balance(budget.Account()); got != 500_000 {
		t.Fatalf("budget holds %d, want 500000", got)
	}

	// Expiry is the moment it is dead, not the last moment it lives: a boundary
	// written the other way gives one free second, and a second in which four
	// nodes can disagree is a fork.
	if eff := apply(signedTransfer(t, delegate, budget.DrawRecipient(seller), 1_000, 1), expiry); eff.applied {
		t.Fatal("a draw succeeded at the expiry second")
	}
	if eff := apply(signedTransfer(t, delegate, budget.DrawRecipient(seller), 1_000, 1), expiry-1); !eff.applied {
		t.Fatal("a draw failed one second before expiry")
	}

	// A draw larger than what is left is a deterministic skip and not an error:
	// every node sees the same remaining balance from the same committed
	// prefix, so every node makes the same decision.
	big := signedTransfer(t, delegate, budget.DrawRecipient(seller), budget.PerJobCap, 2)
	for balance(budget.Account()) >= budget.PerJobCap {
		if eff := apply(big, expiry-1); !eff.applied {
			t.Fatal("a draw inside the remaining balance was skipped")
		}
	}
	if eff := apply(big, expiry-1); eff.applied {
		t.Fatal("a draw larger than the remaining budget was applied")
	}

	// Before expiry a close is the buyer's decision, and revocation is what it
	// is for. A stranger closing it would be a stranger deciding when somebody
	// else's delegate stops working.
	remaining := balance(budget.Account())
	if remaining == 0 {
		t.Fatal("the test needs something left to close")
	}
	if eff := apply(signedTransfer(t, stranger, budget.CloseRecipient(), 0, 1), expiry-1); eff.applied {
		t.Fatal("a stranger closed a live budget")
	}
	// At and after expiry it is not a decision any more - the money is owed
	// back - so anyone may trigger it. That is what stops a balance being
	// stranded by a buyer who lost interest or lost their key.
	if eff := apply(signedTransfer(t, stranger, budget.CloseRecipient(), 0, 2), expiry); !eff.applied {
		t.Fatal("a stranger could not close an expired budget")
	}
	if got := balance(budget.Account()); got != 0 {
		t.Fatalf("closed budget still holds %d", got)
	}

	// Closing twice is safe, so nobody has to check first.
	if eff := apply(signedTransfer(t, stranger, budget.CloseRecipient(), 0, 3), expiry); !eff.applied {
		t.Fatal("closing an empty budget reported failure")
	}
}

func submitAll(t *testing.T, nodes []*testNode, tx *token.Transaction) {
	t.Helper()
	for _, nd := range nodes {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
}

func everyNodeHas(nodes []*testNode, account string, want uint64) bool {
	for _, nd := range nodes {
		bal, err := nd.ledger.Balance(account)
		if err != nil || bal != want {
			return false
		}
	}
	return true
}

// The rules move money differently rather than only refusing more: a node
// without them reads a budget account as an ordinary recipient, charges the fee
// opening one is exempt from, and credits a string as a seller. Two nodes would
// apply the same block and reach different balances - which is why this is
// gated on a version and not simply shipped.
func TestABudgetIsNotAConsensusOperationUntilItsActivationHeight(t *testing.T) {
	const activation uint64 = 100
	nodes, stop := newCluster(t, 1, func(c *Config) {
		c.ProtocolUpgrades = append(c.ProtocolUpgrades,
			ProtocolUpgrade{Height: activation, Version: ProtocolVersionSpendBudgets})
	})
	defer stop()
	e := nodes[0].engine

	buyer, _ := token.GenerateAccount()
	delegate, _ := token.GenerateAccount()
	budget := budgetFor(t, buyer, delegate.PublicKey, time.Now().Add(time.Hour).Unix())
	tx := signedTransfer(t, buyer, budget.Account(), 1_000, 1)

	e.mu.Lock()
	before := e.verifyReservedRecipientLocked(tx, activation-1)
	at := e.verifyReservedRecipientLocked(tx, activation)
	e.mu.Unlock()

	if before == nil {
		t.Error("a budget was accepted at a height whose rules do not have budgets in them")
	}
	if at != nil {
		t.Errorf("a budget was refused at its own activation height: %v", at)
	}

	// And the refusal must be "not yet" rather than "never": Submit keeps a
	// transaction that will become valid, and drops one that cannot. A budget
	// opened before the activation lands by itself once the height arrives.
	if err := isPermanentlyInvalidReserved(tx); err != nil {
		t.Errorf("a well-formed budget was called permanently invalid, so it would never land: %v", err)
	}
}
