package consensus

import (
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// A close signed by the wrong key used to be a perfectly valid transaction that
// moved nothing: gossiped, proposed, committed, and silently without effect,
// because applySpendOperation returns no effect when a pre-expiry close is not
// the buyer's. The submitter was told it worked and the balance did not move.
//
// The delegate is the key that gets this wrong in practice, because it is the
// key the page holds and the one that signs everything else.
func TestAPreExpiryCloseSignedByTheDelegateIsRefusedAtSubmit(t *testing.T) {
	nodes, stop := newCluster(t, 1, func(c *Config) {
		c.ProtocolUpgrades = append(c.ProtocolUpgrades,
			ProtocolUpgrade{Height: 1, Version: ProtocolVersionSpendBudgets})
	})
	defer stop()
	e := nodes[0].engine

	buyer, _ := token.GenerateAccount()
	delegate, _ := token.GenerateAccount()
	expiry := time.Now().Add(time.Hour).Unix()
	budget := budgetFor(t, buyer, delegate.PublicKey, expiry)

	// Signed by the delegate, which may DRAW and may not close.
	byDelegate := signedTransfer(t, delegate, budget.CloseRecipient(), 0, 1)
	if err := e.Submit(byDelegate); err == nil {
		t.Fatal("a close that will move nothing was accepted, so the submitter is told it worked")
	}

	// The buyer's own close is the whole point and must still go through.
	byBuyer := signedTransfer(t, buyer, budget.CloseRecipient(), 0, 1)
	if err := e.Submit(byBuyer); err != nil {
		t.Fatalf("the owner's own close was refused: %v", err)
	}
}

// At and after expiry anyone may close, which is what stops a balance being
// stranded by a buyer who has lost their key. Submit must not narrow that.
func TestAfterExpiryAnybodyMayCloseAtSubmit(t *testing.T) {
	nodes, stop := newCluster(t, 1, func(c *Config) {
		c.ProtocolUpgrades = append(c.ProtocolUpgrades,
			ProtocolUpgrade{Height: 1, Version: ProtocolVersionSpendBudgets})
	})
	defer stop()
	e := nodes[0].engine

	buyer, _ := token.GenerateAccount()
	delegate, _ := token.GenerateAccount()
	stranger, _ := token.GenerateAccount()
	expired := time.Now().Add(-time.Hour).Unix()
	budget := budgetFor(t, buyer, delegate.PublicKey, expired)

	for i, signer := range []*token.Account{delegate, stranger} {
		tx := signedTransfer(t, signer, budget.CloseRecipient(), 0, uint64(i+1))
		if err := e.Submit(tx); err != nil {
			t.Errorf("an expired budget's close was refused, which is how a balance strands: %v", err)
		}
	}
}

// The rule is about closes and must not answer for anything else. A DRAW is the
// delegate's to sign, and refusing one here would break every paid message.
func TestTheSubmitRuleOnlyJudgesCloses(t *testing.T) {
	buyer, _ := token.GenerateAccount()
	delegate, _ := token.GenerateAccount()
	now := time.Now().Unix()
	budget := budgetFor(t, buyer, delegate.PublicKey, now+3600)

	notCloses := []string{
		budget.Account(),
		"bridge/escrow",
		buyer.AccountID(),
	}
	for _, to := range notCloses {
		tx := &token.Transaction{To: to}
		if err := rejectUnauthorizedBudgetClose(tx, now); err != nil {
			t.Errorf("a recipient this rule has no business judging was refused: %q: %v", to, err)
		}
	}
}
