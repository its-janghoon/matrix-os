package inference

import (
	"fmt"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// Telling a buyer their budget cannot cover a reservation BEFORE they sign it.
//
// THE SILENCE THIS REPLACES. applyInferOperation refuses a budget-funded
// reservation in two cases - one larger than the per-job cap, and one opened at or
// after the expiry - and it refuses both by returning no effect. That is the right
// policy: an escrow must not become the way around a cap that bounds a draw. But a
// refusal with no effect is a transaction that COMMITS and moves nothing, and the
// buyer's whole account of it is
//
//	inference: the deposit for job "07203a7e-..." did not apply
//
// which arrives after they have signed, names no number, and does not say which
// bound they hit or what would work instead.
//
// Observed on the live chain: transfer index 215 at block 4140 reserved 4,096,000
// base units against a budget whose per-job cap was 417,710. /chat reserves 4096
// units, the seller charges 1000/unit, and the same page sets a budget's cap to a
// tenth of its deposit - so every budget under about 41,000,000 base units was
// unusable for a single message, and only said so once the money was committed.
//
// Both are knowable from two strings before anything is signed, which is the same
// argument the self-purchase check in SubmitInferenceJob makes.

// budgetCanCover reports whether a budget's own terms allow a reservation of this
// size right now. A nil budget is an ordinary payer, which has none of these
// bounds - judging one by a budget's rules would refuse every direct purchase on
// the network.
//
// now is passed rather than read so the caller decides which clock this is
// measured against. It is advice to a buyer, not a consensus judgement: the
// authoritative check is applyInferOperation against the BLOCK's timestamp, and
// this one only has to be right enough to save them a signature.
func budgetCanCover(reserved uint64, budget *token.SpendEscrow, now int64) error {
	if budget == nil {
		return nil
	}
	if now >= budget.Expiry {
		return fmt.Errorf("%w: this budget expired at %s, so nothing more can be reserved against "+
			"it; close it to take back what is left, then open another",
			ErrBudgetCannotCover, time.Unix(budget.Expiry, 0).UTC().Format(time.RFC3339))
	}
	if reserved > budget.PerJobCap {
		return fmt.Errorf("%w: this request reserves %d base units and this budget caps one job at "+
			"%d, so consensus would accept the deposit and move nothing. It needs a budget whose "+
			"per-job cap is at least %d - through /chat that means putting in at least %d, because "+
			"it sets the cap to a tenth of the deposit",
			ErrBudgetCannotCover, reserved, budget.PerJobCap, reserved, reserved*10)
	}
	return nil
}
