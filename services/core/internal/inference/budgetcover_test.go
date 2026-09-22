package inference

import (
	"errors"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// THE LIVE FAILURE THIS COVERS.
//
// /chat reserves 4096 units, the seller charges 1000/unit, so a reservation is
// 4,096,000 base units. A budget opened through the same page sets its per-job cap
// to a TENTH of the deposit, so a 4,177,100 budget caps one job at 417,710.
//
// applyInferOperation refuses a reservation larger than the cap by returning no
// effect. Correct - an escrow must not be the way around a cap that bounds a draw -
// but silent: the transaction commits, moves nothing, and the buyer reads
//
//	inference: the deposit for job "07203a7e-..." did not apply
//
// after signing, with no number in it and nothing to act on. Observed on the live
// chain at transfer index 215, block 4140.
//
// Both refusals are knowable from two strings before anything is signed.

func budgetTerms(t *testing.T, perJobCap uint64, expiry int64) token.SpendEscrow {
	t.Helper()
	buyer, _ := token.GenerateAccount()
	delegate, _ := token.GenerateAccount()
	return token.SpendEscrow{
		Buyer:           buyer.AccountID(),
		Delegate:        delegate.AccountID(),
		PerJobCap:       perJobCap,
		MaxPricePerUnit: 10_000,
		Expiry:          expiry,
		Nonce:           0,
	}
}

// The numbers from the live failure, asserted on the rule rather than on a whole
// reservation flow: the reservation is compared against the cap, and the refusal
// carries both figures plus what to do about it.
func TestAReservationOverThePerJobCapIsRefusedWithTheNumbers(t *testing.T) {
	const reserved uint64 = 4_096_000
	const cap uint64 = 417_710

	budget := budgetTerms(t, cap, time.Now().Add(time.Hour).Unix())
	err := budgetCanCover(reserved, &budget, time.Now().UTC().Unix())
	if err == nil {
		t.Fatal("a reservation ten times the per-job cap was accepted, so the buyer signs and the deposit moves nothing")
	}
	if !errors.Is(err, ErrBudgetCannotCover) {
		t.Errorf("refused for a reason a caller cannot classify: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"4096000", "417710"} {
		if !contains(msg, want) {
			t.Errorf("the refusal does not name %s, so the reader cannot see which bound they hit: %q", want, msg)
		}
	}
}

// A reservation the budget can cover must pass untouched. This is the ordinary
// case and the gate has to be invisible in it.
func TestAReservationWithinThePerJobCapIsAllowed(t *testing.T) {
	budget := budgetTerms(t, 4_096_000, time.Now().Add(time.Hour).Unix())
	if err := budgetCanCover(4_096_000, &budget, time.Now().UTC().Unix()); err != nil {
		t.Fatalf("a reservation exactly at the cap was refused: %v", err)
	}
	if err := budgetCanCover(1, &budget, time.Now().UTC().Unix()); err != nil {
		t.Fatalf("a small reservation was refused: %v", err)
	}
}

// An expired budget is the other silent refusal in the same block of consensus
// code, and it reaches the reader as the same opaque line.
func TestAnExpiredBudgetIsRefusedBeforeSigning(t *testing.T) {
	budget := budgetTerms(t, 10_000_000, time.Now().Add(-time.Hour).Unix())
	err := budgetCanCover(1_000, &budget, time.Now().UTC().Unix())
	if err == nil {
		t.Fatal("an expired budget accepted a reservation")
	}
	if !errors.Is(err, ErrBudgetCannotCover) {
		t.Errorf("refused for a reason a caller cannot classify: %v", err)
	}
	if !contains(err.Error(), "expired") {
		t.Errorf("the refusal does not say the budget expired: %q", err.Error())
	}
}

// An ordinary account is not a budget and has none of these bounds. Applying them
// would refuse every non-budget purchase on the network.
func TestAnOrdinaryPayerIsNotJudged(t *testing.T) {
	if err := budgetCanCover(999_999_999, nil, time.Now().UTC().Unix()); err != nil {
		t.Fatalf("a payer that is not a budget was judged by a budget's rules: %v", err)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
