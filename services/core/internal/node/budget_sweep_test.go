package node

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// "Anyone may close an expired budget" was a rule nobody acted on. These cover
// what the sweeper must and must not pick up, and the property that makes running
// it every minute safe.

func budgetWith(t *testing.T, buyer *token.Account, delegate ed25519.PublicKey, expiry int64) token.SpendEscrow {
	t.Helper()
	return token.SpendEscrow{
		Buyer:           buyer.AccountID(),
		Delegate:        hex.EncodeToString(delegate),
		PerJobCap:       200_000,
		MaxPricePerUnit: 500,
		Expiry:          expiry,
		Nonce:           0,
	}
}

// Every tick must produce byte-identical bytes for the same budget. Otherwise the
// mempool holds one transaction per tick for as long as the close takes to
// commit, and nothing recognises them as the same intent.
func TestSweepingTheSameBudgetTwiceIsTheSameTransaction(t *testing.T) {
	node, _ := token.GenerateAccount()
	buyer, _ := token.GenerateAccount()
	delegate, _ := token.GenerateAccount()
	n := &Node{consensusAccount: node}
	terms := budgetWith(t, buyer, delegate.PublicKey, time.Now().Add(-time.Hour).Unix())

	first, err := n.signedBudgetClose(terms)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	second, err := n.signedBudgetClose(terms)
	if err != nil {
		t.Fatalf("sign again: %v", err)
	}

	if first.Nonce != second.Nonce {
		t.Error("the nonce moved between sweeps, so each tick is a new transaction the mempool cannot dedup")
	}
	if first.Timestamp != second.Timestamp {
		t.Error("the timestamp moved between sweeps, which makes every tick a distinct transaction")
	}
	if string(first.Signature) != string(second.Signature) {
		t.Error("two sweeps of one budget signed differently, so neither the mempool nor the replay set matches them")
	}
}

// The refund destination is not the sweeper's to choose: it is inside the
// account's name, which the signature covers. This asserts the submitter cannot
// point the money at itself.
func TestTheSweeperCannotRedirectTheRefund(t *testing.T) {
	node, _ := token.GenerateAccount()
	buyer, _ := token.GenerateAccount()
	delegate, _ := token.GenerateAccount()
	n := &Node{consensusAccount: node}
	terms := budgetWith(t, buyer, delegate.PublicKey, time.Now().Add(-time.Hour).Unix())

	tx, err := n.signedBudgetClose(terms)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if tx.To != terms.CloseRecipient() {
		t.Fatalf("the close names %q rather than the budget's own close recipient", tx.To)
	}
	// The buyer is in the recipient, and the node's own id is not.
	if !strings.Contains(tx.To, terms.Buyer) {
		t.Error("the recipient does not carry the buyer, so the refund has no fixed destination")
	}
	if strings.Contains(tx.To, node.AccountID()) {
		t.Error("the sweeper's own account appears in the recipient")
	}
	// A close carries no amount. The whole remaining balance is what comes back.
	if tx.Amount != 0 {
		t.Errorf("a close carried an amount (%d), which is not the submitter's to choose", tx.Amount)
	}
	if err := tx.Verify(); err != nil {
		t.Errorf("the close does not verify: %v", err)
	}
}

// A budget that has not expired is the buyer's to revoke and nobody else's. This
// calls the selection rule itself rather than re-stating it, because a test that
// re-implements what it tests proves only that the copy agrees with itself.
func TestOnlyExpiredBudgetsWithABalanceAreSwept(t *testing.T) {
	buyer, _ := token.GenerateAccount()
	delegate, _ := token.GenerateAccount()
	now := time.Now().UTC().Unix()

	live := budgetWith(t, buyer, delegate.PublicKey, now+3600)
	dead := budgetWith(t, buyer, delegate.PublicKey, now-3600)
	emptyDead := budgetWith(t, buyer, delegate.PublicKey, now-7200)
	emptyDead.Nonce = 9

	cases := []struct {
		what    string
		account string
		balance uint64
		want    bool
	}{
		{"an expired budget holding a balance", dead.Account(), 7_000, true},
		{"a budget whose expiry has not come", live.Account(), 5_000, false},
		{"an expired budget already emptied", emptyDead.Account(), 0, false},
		{"an ordinary account", buyer.AccountID(), 1_000, false},
		{"a reserved account that is not a budget", "bridge/escrow", 9_000, false},
		{"a budget-shaped name that does not parse", token.SpendEscrowPrefix + "not.enough.fields", 1, false},
	}
	for _, c := range cases {
		terms, got := sweepableBudget(c.account, c.balance, now)
		if got != c.want {
			t.Errorf("%s: swept=%v, want %v", c.what, got, c.want)
			continue
		}
		if got && terms.Account() != c.account {
			t.Errorf("%s: parsed back as %q", c.what, terms.Account())
		}
	}
}
