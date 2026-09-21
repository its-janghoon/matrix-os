package node

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// Returning an expired budget without waiting for its owner to come back.
//
// THE GAP THIS CLOSES. A budget's remaining balance sits in an escrow account
// until somebody submits a close. Before the expiry only the buyer may, which is
// right - it is their revocation to make. At and after the expiry ANYONE may, and
// that rule exists precisely so a balance is not stranded by a buyer who has lost
// interest or their key. But "anyone may" is not "somebody does": nothing in the
// network was doing it, so the rule protected nobody and the money stayed put.
//
// So a node does it, for every expired budget it can see. This needs no new
// permission and no protocol version, because the close it submits is the one the
// rules already allow after the expiry. The refund goes to the terms' own buyer
// and cannot be redirected: the destination is inside the account's NAME, so the
// sweeper chooses nothing except when to ask.
//
// WHY EVERY NODE AND NOT AN ELECTED ONE. Several nodes sweeping the same budget
// costs one committed close and a few no-ops - closing an empty budget is
// deliberately a no-op rather than a failure. Electing one node instead would add
// a leader to get wrong, and a single sweeper is a single thing to be down.

// budgetSweepInterval is how often a node looks for expired budgets.
//
// A minute, because the deadline being swept is an expiry the buyer chose in
// hours: arriving within a minute of it is indistinguishable from arriving on it,
// and the scan walks every account with a balance, which is not work to repeat
// every second.
const budgetSweepInterval = time.Minute

// sweepExpiredBudgets closes expired budgets until the node context is
// cancelled. It is the same shape as expireUnpaidInferenceJobs: one ticker, one
// pass per tick, and no state carried between passes.
func (n *Node) sweepExpiredBudgets() {
	ticker := time.NewTicker(budgetSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.sweepExpiredBudgetsOnce(time.Now().UTC())
		}
	}
}

// sweepExpiredBudgetsOnce submits a close for every expired budget that still
// holds something.
//
// Reads first, submits second, deliberately. ForEachBalance walks the ledger
// under its own lock, and submitting from inside the walk would hold that lock
// across consensus work.
func (n *Node) sweepExpiredBudgetsOnce(now time.Time) {
	if n.consensus == nil || n.consensusAccount == nil || n.market == nil || n.market.Ledger() == nil {
		return
	}

	expired := n.expiredBudgetsLocked(now)
	for _, terms := range expired {
		tx, err := n.signedBudgetClose(terms)
		if err != nil {
			// A node that cannot sign is a node with no consensus identity, which
			// is a startup condition rather than a per-budget one. Reported once
			// per budget is still better than silence.
			fmt.Printf("Market: could not sign a close for an expired budget: %v\n", err)
			continue
		}
		// An already-committed close comes back as a replay and an already-empty
		// budget applies as a no-op. Neither is news, and a line per tick per
		// budget would bury the one that matters.
		if err := n.consensus.Submit(tx); err != nil {
			continue
		}
		fmt.Printf("Market: budget %s expired at %s; submitted a close returning the rest to %s.\n",
			terms.Account(), time.Unix(terms.Expiry, 0).UTC().Format(time.RFC3339), terms.Buyer)
	}
}

// expiredBudgetsLocked collects the budgets that are past their expiry and still
// hold a balance.
//
// An account whose name does not parse is skipped rather than reported. The only
// way one exists is a deposit into a malformed budget name, which is money
// already stranded by something this sweeper cannot reach - and a line about it
// every minute would be noise, not a finding.
func (n *Node) expiredBudgetsLocked(now time.Time) []token.SpendEscrow {
	var expired []token.SpendEscrow
	unix := now.Unix()
	err := n.market.Ledger().ForEachBalance(func(account string, balance uint64) error {
		if terms, ok := sweepableBudget(account, balance, unix); ok {
			expired = append(expired, terms)
		}
		return nil
	})
	if err != nil {
		fmt.Printf("Market: could not scan for expired budgets: %v\n", err)
		return nil
	}
	return expired
}

// sweepableBudget is the whole selection rule, as one function so the scan and
// its test cannot hold two versions of it.
//
// Four things are passed over, and each for its own reason. An account that is
// not a budget is not this sweeper's business. An empty budget has nothing to
// return, and closing it would commit a transaction that moves nothing. A budget
// before its expiry is the BUYER's to revoke and nobody else's - sweeping one
// early would take away a spending authority they still wanted. And a name that
// does not parse is money already stranded at an account nothing can close, which
// no close this function could build would reach.
func sweepableBudget(account string, balance uint64, unix int64) (token.SpendEscrow, bool) {
	if balance == 0 || !token.IsSpendEscrowAccount(account) {
		return token.SpendEscrow{}, false
	}
	terms, err := token.ParseSpendEscrow(account)
	if err != nil {
		return token.SpendEscrow{}, false
	}
	if unix < terms.Expiry {
		return token.SpendEscrow{}, false
	}
	return terms, true
}

// signedBudgetClose builds this node's close for one budget.
//
// THE NONCE IS DERIVED FROM THE TERMS AND NOT RANDOM. A budget operation is a
// reserved recipient, so it is exempt from the per-sender nonce sequence and the
// nonce is only a uniquifier. Random would make every tick a DIFFERENT
// transaction: the mempool would hold one per tick for as long as the close took
// to commit, and nothing would recognise the duplicates. Derived, every tick
// produces byte-identical bytes, so the mempool dedups them and the engine's
// committed set refuses the rest as a replay. Sweeping is then idempotent by
// construction rather than by a timer this node would have to keep.
func (n *Node) signedBudgetClose(terms token.SpendEscrow) (*token.Transaction, error) {
	tx := &token.Transaction{
		From:  n.consensusAccount.PublicKey,
		To:    terms.CloseRecipient(),
		Nonce: budgetCloseNonce(terms),
		// A close carries no amount: it returns whatever is left, and the amount
		// is not the submitter's to choose.
		Amount: 0,
		// Fixed rather than time.Now(), for the same reason as the nonce: a
		// timestamp that moved would make each tick a new transaction. The
		// expiry is what bounds this operation in time, and it is in the
		// recipient, which the signature covers.
		Timestamp: terms.Expiry,
	}
	if err := tx.Sign(n.consensusAccount.PrivateKey); err != nil {
		return nil, err
	}
	return tx, nil
}

// budgetCloseNonce derives a stable nonce from the budget's own name, so two
// sweeps of the same budget by the same node are the same transaction.
func budgetCloseNonce(terms token.SpendEscrow) uint64 {
	sum := sha256.Sum256([]byte(terms.Account()))
	return binary.BigEndian.Uint64(sum[:8])
}
