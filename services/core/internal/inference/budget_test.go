package inference

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// Buying out of a budget, which is the whole point of one: the buyer's wallet
// signed once, when the budget was opened, and is not asked again. Everything
// after that is signed by a key the budget's own name authorises - in a browser,
// a non-extractable one that cannot leave the page.

func budgetOf(t *testing.T, buyer *token.Account, delegate *token.Account, maxPrice uint64) token.SpendEscrow {
	t.Helper()
	return token.SpendEscrow{
		Buyer:           buyer.AccountID(),
		Delegate:        hex.EncodeToString(delegate.PublicKey),
		PerJobCap:       1_000_000,
		MaxPricePerUnit: maxPrice,
		Expiry:          time.Now().Add(time.Hour).Unix(),
	}
}

func TestABudgetPaysAndTheBuyerIsNeverAskedAgain(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	// The BUYER holds nothing here. Every base unit this spends is in the
	// budget, which is what proves the purchase went through it.
	svc, buyer, provider := clientSignedService(t, fs, 3, 0)
	delegate, _ := token.GenerateAccount()
	budget := budgetOf(t, buyer, delegate, 10)
	if err := svc.market.Ledger().Credit(budget.Account(), 1_000_000); err != nil {
		t.Fatalf("fund the budget: %v", err)
	}

	req := InferenceRequest{Prompt: "hello", Model: "m"}
	// The delegate authorises the run. No wallet is involved and none is
	// available: the service's account resolver knows nobody.
	auth := authFor(t, delegate, provider, "m", req, time.Now())
	if err := svc.VerifyRunAuthorization(budget.Account(), req, auth); err != nil {
		t.Fatalf("the delegate must be able to authorise a run on its budget: %v", err)
	}

	pr, err := svc.RunUnsettled(context.Background(), budget.Account(), provider, req, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}
	// The invoice is addressed to a draw and is for the delegate to sign. Both
	// halves matter: the first is what consensus debits from the budget rather
	// than from whoever signed, and the second is what lets the page pay at all.
	if pr.From != budget.Delegate {
		t.Fatalf("invoice is for %s to sign, want the delegate %s", pr.From, budget.Delegate)
	}
	if want := budget.DrawRecipient(provider); pr.To != want {
		t.Fatalf("invoice pays %s, want the draw %s", pr.To, want)
	}

	job, err := svc.SettleSigned(context.Background(), pr.JobID, signedFor(t, pr, delegate))
	if err != nil {
		t.Fatalf("SettleSigned with the delegate's signature: %v", err)
	}
	if job.Completion == "" {
		t.Fatal("the completion was withheld from a settled job")
	}
}

// The budget names one key and only that key. This is what makes closing a
// budget an actual revocation rather than a preference.
func TestOnlyTheKeyABudgetNamesMaySpendIt(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)
	delegate, _ := token.GenerateAccount()
	stranger, _ := token.GenerateAccount()
	budget := budgetOf(t, buyer, delegate, 10)
	req := InferenceRequest{Prompt: "hello", Model: "m"}

	for name, acct := range map[string]*token.Account{
		"a stranger":      stranger,
		"the buyer's own": buyer,
	} {
		auth := authFor(t, acct, provider, "m", req, time.Now())
		if err := svc.VerifyRunAuthorization(budget.Account(), req, auth); !errors.Is(err, ErrRunUnauthorized) {
			t.Errorf("%s key authorised a run on somebody's budget: %v", name, err)
		}
	}
}

// A budget carries the buyer's price ceiling, and this is the one moment both
// numbers are in the same place. Consensus cannot check it - a draw is an amount
// with no unit count in it to divide by - so the refusal has to happen here,
// before a provider spends anything.
func TestABudgetRefusesASellerDearerThanItsCeiling(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 0)
	delegate, _ := token.GenerateAccount()

	tooDear := budgetOf(t, buyer, delegate, 2) // the provider charges 3
	if err := svc.market.Ledger().Credit(tooDear.Account(), 1_000_000); err != nil {
		t.Fatalf("fund the budget: %v", err)
	}
	_, err := svc.SubmitInferenceJob(tooDear.Account(), provider, InferenceRequest{Prompt: "hello"}, 1000)
	if !errors.Is(err, ErrPriceAboveCeiling) {
		t.Fatalf("err = %v, want the seller refused for being over the ceiling", err)
	}

	// And exactly at the ceiling is allowed: a bound refuses what is over it,
	// not what reaches it.
	atCeiling := budgetOf(t, buyer, delegate, 3)
	if err := svc.market.Ledger().Credit(atCeiling.Account(), 1_000_000); err != nil {
		t.Fatalf("fund the budget: %v", err)
	}
	if _, err := svc.SubmitInferenceJob(atCeiling.Account(), provider, InferenceRequest{Prompt: "hello"}, 1000); err != nil {
		t.Fatalf("a seller priced exactly at the ceiling was refused: %v", err)
	}
}

// An ordinary buyer must be completely undisturbed by any of this: the budget
// path is entered by NAMING a budget, and nothing else should take that branch.
func TestAnOrdinaryPurchaseIsUnchanged(t *testing.T) {
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := clientSignedService(t, fs, 3, 1_000_000)

	pr, err := svc.RunUnsettled(context.Background(), buyer.AccountID(), provider,
		InferenceRequest{Prompt: "hello"}, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}
	if pr.From != buyer.AccountID() {
		t.Fatalf("invoice is for %s to sign, want the buyer", pr.From)
	}
	if pr.To != provider {
		t.Fatalf("invoice pays %s, want the provider directly", pr.To)
	}
}
