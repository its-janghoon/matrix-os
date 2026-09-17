package consensus

import (
	"fmt"
	"strings"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Applying an inference escrow. The NAMING of one lives in internal/token next
// to the signature types; what stays here is what only consensus can decide:
// whether an operation may enter a block, and what it does to the ledger.
//
// Three operations, and the asymmetry between the last two is the whole design:
//
//	OPEN     the payer funds the reservation. No fee - their own coins moving
//	         into their own escrow, nobody has been paid.
//	SETTLE   the PAYER's side names the actual. Consensus pays it to the
//	         provider and returns the rest. This is the buyer's signature, and
//	         it is the buyer's because it is the only thing that has ever bounded
//	         a bill from their side.
//	CLAIM    the PROVIDER takes the whole reservation, but only at and after the
//	         expiry. This is what stops a buyer reading the answer and never
//	         settling, and it is why the provider can stream in the first place.
//
// Together they mean the provider is guaranteed the reservation and the buyer
// can only ever pay less by settling honestly - so the text has nothing left to
// protect and can go out as it is produced.

// Re-exported so the apply path and its tests read as one thing.
var (
	IsInferRecipient     = token.IsInferRecipient
	IsInferEscrowAccount = token.IsInferEscrowAccount
)

// InferEscrow is a reservation's terms. See token.InferEscrow.
type InferEscrow = token.InferEscrow

// inferEffect is what applying an escrow operation did, in the terms the apply
// loop's accounting needs.
type inferEffect struct {
	// applied is false for a deterministic skip - an underfunded payer, a claim
	// before its expiry - which every node reaches from the same committed
	// prefix rather than from its own clock or its own view.
	applied bool
	// payer and payee name a real payment for the settled history. Both are
	// empty when no value changed hands between accounts, which is the case for
	// opening a reservation and for the refund half of a settlement.
	payer string
	payee string
	// net is what the payee was credited, after the fee.
	net uint64
	// fee is what went to the fee accrual account.
	fee uint64
}

// spenderFor resolves who is allowed to move a reservation's money.
//
// An ordinary payer signs for themselves. A BUDGET payer cannot sign at all -
// it is an account, not a key - so the signature that counts is its delegate's,
// which is the same resolution the inference service already does when it
// addresses an invoice.
func spenderFor(payer string) (spender string, budget *token.SpendEscrow, err error) {
	if !token.IsSpendEscrowAccount(payer) {
		return payer, nil, nil
	}
	terms, err := token.ParseSpendEscrow(payer)
	if err != nil {
		return "", nil, err
	}
	return terms.Delegate, &terms, nil
}

// verifyInferTx refuses an escrow operation that can never be valid, before it
// enters a block. It reads nothing but the transaction, which is what lets the
// mempool gate and the block check share it.
//
// Time and balance are NOT checked here, for the reason they are not checked for
// a budget: both are apply-time questions whose answers come from the block, and
// asking them here would mean asking this node's wall clock and this node's
// current ledger - which is how two nodes reach different verdicts about one
// transaction.
func verifyInferTx(tx *token.Transaction) error {
	switch {
	case IsInferEscrowAccount(tx.To):
		terms, err := token.ParseInferEscrow(tx.To)
		if err != nil {
			return err
		}
		// A reservation paid to a reserved recipient would be a settlement into
		// a namespace with rules of its own, which is a way to reach another
		// apply path sideways.
		if IsReservedRecipient(terms.Provider) {
			return fmt.Errorf("%w: an inference provider must be an account, not the reserved "+
				"recipient %q", ErrInvalidMessage, terms.Provider)
		}
		spender, _, err := spenderFor(terms.Payer)
		if err != nil {
			return err
		}
		if tx.SenderID() != spender {
			return fmt.Errorf("%w: this reservation is funded by %s, not by %s",
				ErrInvalidMessage, spender, tx.SenderID())
		}
		// Exactly the reservation, never part of it. A half-funded escrow would
		// settle for less than the job was quoted, and the shortfall would
		// surface as a provider paid at random.
		if tx.Amount != terms.Reserved {
			return fmt.Errorf("%w: this reservation is %d and the deposit is %d",
				ErrInvalidMessage, terms.Reserved, tx.Amount)
		}
		return nil

	case strings.HasPrefix(tx.To, token.InferSettlePrefix):
		terms, err := token.ParseInferSettle(tx.To)
		if err != nil {
			return err
		}
		spender, _, err := spenderFor(terms.Payer)
		if err != nil {
			return err
		}
		// The settlement names the price within the cap, so the key that signs
		// it is the buyer's. Handing this to the provider would leave the bill
		// bounded only by a reservation that is generous on purpose.
		if tx.SenderID() != spender {
			return fmt.Errorf("%w: only %s may settle this reservation, not %s",
				ErrInvalidMessage, spender, tx.SenderID())
		}
		if tx.Amount == 0 {
			return fmt.Errorf("%w: a settlement of nothing; a job that cost nothing is a claim "+
				"the provider declines or an escrow that expires", ErrInvalidMessage)
		}
		if tx.Amount > terms.Reserved {
			return fmt.Errorf("%w: a settlement of %d exceeds the reservation of %d",
				ErrInvalidMessage, tx.Amount, terms.Reserved)
		}
		return nil

	case strings.HasPrefix(tx.To, token.InferClaimPrefix):
		terms, err := token.ParseInferClaim(tx.To)
		if err != nil {
			return err
		}
		if tx.SenderID() != terms.Provider {
			return fmt.Errorf("%w: only the provider %s may claim this reservation, not %s",
				ErrInvalidMessage, terms.Provider, tx.SenderID())
		}
		// The amount is the whole remaining balance and is not the caller's to
		// choose, exactly as a bond withdrawal's is not.
		if tx.Amount != 0 {
			return fmt.Errorf("%w: a claim carries no amount; it takes whatever is left",
				ErrInvalidMessage)
		}
		return nil
	}
	return fmt.Errorf("%w: %q is not an inference escrow operation", ErrInvalidMessage, tx.To)
}

// applyInferOperation performs one escrow operation against the ledger.
//
// blockTime is the BLOCK's timestamp and never this node's clock, for the reason
// every other expiry here is: four nodes reading a wall clock is four answers,
// and a reservation claimable on one of them and not the others is a fork rather
// than a refusal.
func (e *Engine) applyInferOperation(ltx market.LedgerTx, tx *token.Transaction, blockTime int64) (inferEffect, error) {
	switch {
	case IsInferEscrowAccount(tx.To):
		terms, err := token.ParseInferEscrow(tx.To)
		if err != nil {
			return inferEffect{}, err
		}
		if blockTime >= int64(terms.Expiry) {
			// Funding a reservation the provider could claim in the same block
			// is a gift, not an escrow.
			return inferEffect{}, nil
		}
		_, budget, err := spenderFor(terms.Payer)
		if err != nil {
			return inferEffect{}, err
		}
		// A budget-funded reservation obeys the BUDGET's rules too. Without
		// this, opening an escrow would be the way around a per-job cap: the
		// cap would bound a draw and not bound the reservation that replaces it.
		if budget != nil {
			if blockTime >= budget.Expiry {
				return inferEffect{}, nil
			}
			if tx.Amount > budget.PerJobCap {
				return inferEffect{}, nil
			}
		}
		bal, err := ltx.Balance(terms.Payer)
		if err != nil {
			return inferEffect{}, err
		}
		if bal < tx.Amount {
			return inferEffect{}, nil
		}
		// Debited from the PAYER and not from the sender, because for a budget
		// they are different: the delegate signs and the budget pays, which is
		// exactly what the budget's own name authorises.
		//
		// No fee. The payer's coins are moving into the payer's own reservation
		// and nobody has been paid yet; the fee lands at settlement, on the
		// amount that actually changed hands rather than on the cap.
		if err := ltx.Transfer(terms.Payer, tx.To, tx.Amount); err != nil {
			return inferEffect{}, err
		}
		return inferEffect{applied: true}, nil

	case strings.HasPrefix(tx.To, token.InferSettlePrefix):
		terms, err := token.ParseInferSettle(tx.To)
		if err != nil {
			return inferEffect{}, err
		}
		escrow := terms.Account()
		bal, err := ltx.Balance(escrow)
		if err != nil {
			return inferEffect{}, err
		}
		if bal == 0 {
			// Already settled, already claimed, or never funded. A no-op rather
			// than a failure, so settling twice is safe and nobody has to check
			// first - the same shape as closing a budget twice.
			return inferEffect{applied: true}, nil
		}
		if tx.Amount > bal {
			return inferEffect{}, nil
		}
		fee := FeeFor(tx.Amount, e.feeBasisPoints)
		net := tx.Amount - fee
		if err := ltx.Transfer(escrow, terms.Provider, net); err != nil {
			return inferEffect{}, err
		}
		if fee > 0 {
			if err := ltx.Transfer(escrow, feeAccrualAccount, fee); err != nil {
				return inferEffect{}, err
			}
		}
		// The refund. Not a payment to anyone - it is the buyer's own money
		// coming back - so it is deliberately outside the revenue this reports.
		if rest := bal - tx.Amount; rest > 0 {
			if err := ltx.Transfer(escrow, terms.Payer, rest); err != nil {
				return inferEffect{}, err
			}
		}
		return inferEffect{applied: true, payer: payerOfRecord(terms.Payer), payee: terms.Provider, net: net, fee: fee}, nil

	case strings.HasPrefix(tx.To, token.InferClaimPrefix):
		terms, err := token.ParseInferClaim(tx.To)
		if err != nil {
			return inferEffect{}, err
		}
		// Before the expiry the buyer still has the right to name the actual,
		// and taking the cap out from under them would make the expiry
		// decorative.
		if blockTime < int64(terms.Expiry) {
			return inferEffect{}, nil
		}
		escrow := terms.Account()
		bal, err := ltx.Balance(escrow)
		if err != nil {
			return inferEffect{}, err
		}
		if bal == 0 {
			return inferEffect{applied: true}, nil
		}
		fee := FeeFor(bal, e.feeBasisPoints)
		net := bal - fee
		if err := ltx.Transfer(escrow, terms.Provider, net); err != nil {
			return inferEffect{}, err
		}
		if fee > 0 {
			if err := ltx.Transfer(escrow, feeAccrualAccount, fee); err != nil {
				return inferEffect{}, err
			}
		}
		return inferEffect{applied: true, payer: payerOfRecord(terms.Payer), payee: terms.Provider, net: net, fee: fee}, nil
	}
	return inferEffect{}, fmt.Errorf("%w: %q is not an inference escrow operation", ErrInvalidMessage, tx.To)
}

// payerOfRecord is who the history should say paid.
//
// A budget holds the money but a person owns the budget, and a history naming
// the escrow account would read as a stranger paying for someone else's
// inference. The draw path makes the same choice for the same reason.
func payerOfRecord(payer string) string {
	if terms, err := token.ParseSpendEscrow(payer); err == nil {
		return terms.Buyer
	}
	return payer
}
