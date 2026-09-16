package consensus

import (
	"fmt"
	"strings"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Applying a spend budget. The NAMING of one - what an escrow account is called
// and how its terms are read back out - lives in internal/token, next to the
// signature types, so that the inference service can build a draw without
// importing a consensus engine to do it.
//
// What stays here is what only consensus can decide: whether an operation may
// enter a block, and what it does to the ledger when it lands.

// Re-exported so the apply path and its tests read as one thing.
var (
	IsSpendRecipient     = token.IsSpendRecipient
	IsSpendEscrowAccount = token.IsSpendEscrowAccount
)

// SpendEscrow is the budget's terms. See token.SpendEscrow.
type SpendEscrow = token.SpendEscrow

// spendEffect is what applying a budget operation did, in the terms the apply
// loop's accounting needs. The ledger moves happen inside applySpendOperation;
// this is only what the loop cannot see from outside.
type spendEffect struct {
	// applied is false for a deterministic skip - an underfunded budget - which
	// every node reaches from the same committed prefix.
	applied bool
	// payer and payee name an ordinary payment for the settled history. Both
	// are empty when the operation moved no money between accounts, which is
	// the case for opening a budget and for closing one: those are a buyer's
	// own coins going into and out of their own escrow, and counting them as
	// revenue would pay the provider emission to somebody funding themselves.
	payer string
	payee string
	// net is what the payee was credited, after the fee.
	net uint64
	// fee is what went to the fee accrual account.
	fee uint64
}

// verifySpendTx refuses a budget operation that can never be valid, before it
// enters a block. It reads nothing but the transaction, which is what lets the
// mempool gate and the block check share it - the two drifting apart is how a
// transaction that no validator will accept gets proposed forever.
//
// Time is NOT checked here and balance is not either. Both are apply-time
// questions with apply-time answers: the clock is the block's timestamp, which
// does not exist yet, and the balance is whatever the committed prefix left.
// Checking them here would mean checking them against this node's own wall
// clock and its own current ledger, which is how two nodes reach different
// verdicts about the same transaction.
func verifySpendTx(tx *token.Transaction) error {
	switch {
	case IsSpendEscrowAccount(tx.To):
		terms, err := token.ParseSpendEscrow(tx.To)
		if err != nil {
			return err
		}
		// A budget owned by a reserved account is not a budget: nobody holds a
		// key for one, so nothing could close it and the deposit is stranded.
		// The namespace list lives here rather than in token, because it is the
		// list consensus itself dispatches on and a second copy would drift.
		if IsReservedRecipient(terms.Buyer) {
			return fmt.Errorf("%w: a budget buyer must be an account, not the reserved "+
				"recipient %q", ErrInvalidMessage, terms.Buyer)
		}
		// A budget is the buyer's own money. Letting anyone fund anyone's
		// account would make a deposit a gift that only the named buyer can
		// ever close, which is a way to lose money by typing.
		if tx.SenderID() != terms.Buyer {
			return fmt.Errorf("%w: a budget is opened by its buyer %s, not by %s",
				ErrInvalidMessage, terms.Buyer, tx.SenderID())
		}
		if tx.Amount == 0 {
			return fmt.Errorf("%w: opening a budget with nothing in it", ErrInvalidMessage)
		}
		return nil

	case strings.HasPrefix(tx.To, token.SpendDrawPrefix):
		payee, terms, err := token.ParseSpendDraw(tx.To)
		if err != nil {
			return err
		}
		// A draw into a bond, a set change or any other reserved operation would
		// let a delegate move the money somewhere consensus treats specially
		// rather than pay a seller with it. The value of a bounded delegate is
		// that its output is a payment.
		if IsReservedRecipient(payee) {
			return fmt.Errorf("%w: a draw pays an account, not the reserved recipient %q",
				ErrInvalidMessage, payee)
		}
		// The delegate is the only key that may draw, and the check is against
		// the SENDER rather than against anything the recipient claims: a
		// signature says who is spending, and the recipient says what is being
		// spent, which is why the two are separate here.
		if tx.SenderID() != terms.Delegate {
			return fmt.Errorf("%w: only the delegate %s may draw on this budget, not %s",
				ErrInvalidMessage, terms.Delegate, tx.SenderID())
		}
		if tx.Amount == 0 {
			return fmt.Errorf("%w: a draw of nothing", ErrInvalidMessage)
		}
		if tx.Amount > terms.PerJobCap {
			return fmt.Errorf("%w: a draw of %d exceeds the per-job cap of %d",
				ErrInvalidMessage, tx.Amount, terms.PerJobCap)
		}
		if payee == terms.Buyer {
			// Paying the budget's own owner is a refund wearing a draw's
			// clothes, and it would skip the expiry rule that a close obeys.
			return fmt.Errorf("%w: a draw pays a seller; returning money to the buyer is a close",
				ErrInvalidMessage)
		}
		return nil

	case strings.HasPrefix(tx.To, token.SpendClosePrefix):
		if _, err := token.ParseSpendClose(tx.To); err != nil {
			return err
		}
		// The amount is the whole remaining balance and is not the caller's to
		// choose, exactly as a bond withdrawal's is not.
		if tx.Amount != 0 {
			return fmt.Errorf("%w: a close carries no amount; it returns whatever is left",
				ErrInvalidMessage)
		}
		return nil
	}
	return fmt.Errorf("%w: %q is not a budget operation", ErrInvalidMessage, tx.To)
}

// applySpendOperation performs one budget operation against the ledger.
//
// blockTime is the BLOCK's timestamp and never this node's clock. Four nodes
// reading a wall clock is four answers, and a budget that has expired on one of
// them and not the others is a fork rather than a refusal.
func (e *Engine) applySpendOperation(ltx market.LedgerTx, tx *token.Transaction, blockTime int64) (spendEffect, error) {
	switch {
	case IsSpendEscrowAccount(tx.To):
		terms, err := token.ParseSpendEscrow(tx.To)
		if err != nil {
			return spendEffect{}, err
		}
		if blockTime >= terms.Expiry {
			// Money into a budget nothing can draw from would sit there until
			// somebody closed it, which is a refund taking two transactions and
			// a block to do nothing.
			return spendEffect{}, nil
		}
		bal, err := ltx.Balance(tx.SenderID())
		if err != nil {
			return spendEffect{}, err
		}
		if bal < tx.Amount {
			return spendEffect{}, nil
		}
		// No fee. A buyer moving their own coins into their own budget has not
		// been paid by anyone, which is the same reason a bond is exempt.
		if err := ltx.Transfer(tx.SenderID(), tx.To, tx.Amount); err != nil {
			return spendEffect{}, err
		}
		return spendEffect{applied: true}, nil

	case strings.HasPrefix(tx.To, token.SpendDrawPrefix):
		payee, terms, err := token.ParseSpendDraw(tx.To)
		if err != nil {
			return spendEffect{}, err
		}
		if blockTime >= terms.Expiry {
			return spendEffect{}, nil
		}
		escrow := terms.Account()
		bal, err := ltx.Balance(escrow)
		if err != nil {
			return spendEffect{}, err
		}
		if bal < tx.Amount {
			// A deterministic skip and not an error: the budget ran out, which
			// every node sees identically from the same committed prefix.
			return spendEffect{}, nil
		}
		// The fee comes out of the amount, exactly as it does for an ordinary
		// transfer, so what the buyer authorised is what leaves the budget.
		fee := FeeFor(tx.Amount, e.feeBasisPoints)
		net := tx.Amount - fee
		if err := ltx.Transfer(escrow, payee, net); err != nil {
			return spendEffect{}, err
		}
		if fee > 0 {
			if err := ltx.Transfer(escrow, feeAccrualAccount, fee); err != nil {
				return spendEffect{}, err
			}
		}
		// The payer of record is the BUYER and not the delegate. The delegate
		// holds no money and the history should read as the person who did.
		return spendEffect{applied: true, payer: terms.Buyer, payee: payee, net: net, fee: fee}, nil

	case strings.HasPrefix(tx.To, token.SpendClosePrefix):
		terms, err := token.ParseSpendClose(tx.To)
		if err != nil {
			return spendEffect{}, err
		}
		// Before expiry a close is the buyer's decision and revocation is what
		// it is for. At and after expiry it is not a decision any more - the
		// money is owed back - so anyone may trigger it, which is what stops a
		// balance being stranded by a buyer who has lost interest or their key.
		if blockTime < terms.Expiry && tx.SenderID() != terms.Buyer {
			return spendEffect{}, nil
		}
		escrow := terms.Account()
		bal, err := ltx.Balance(escrow)
		if err != nil {
			return spendEffect{}, err
		}
		if bal == 0 {
			// Closing an empty budget is a no-op rather than a failure, so
			// closing twice is safe and nobody has to check first.
			return spendEffect{applied: true}, nil
		}
		// No fee: this is collateral going back to its owner, not value anyone
		// was paid. The bridge escrow release is exempt for the same reason.
		if err := ltx.Transfer(escrow, terms.Buyer, bal); err != nil {
			return spendEffect{}, err
		}
		return spendEffect{applied: true}, nil
	}
	return spendEffect{}, fmt.Errorf("%w: %q is not a budget operation", ErrInvalidMessage, tx.To)
}
