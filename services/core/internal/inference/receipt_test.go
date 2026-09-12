package inference

import (
	"context"
	"errors"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// settledWithReceipt runs one job to completion on a node that signs, and
// returns the job and the node's key.
func settledWithReceipt(t *testing.T, req InferenceRequest, claim int) (*InferenceJob, *token.Account) {
	t.Helper()
	node, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	backend := greedyBackend{completion: "the answer", claimTokens: claim}
	fs := &fakeSettler{committed: true, applied: true}
	svc, buyer, provider := serviceWithBackend(t, backend, fs, 7, 10_000_000)
	svc.node = node

	job, err := svc.SubmitInferenceJob(buyer, provider, req, 500)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	settled, err := svc.FulfillJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("FulfillJob: %v", err)
	}
	if settled.Receipt == nil {
		t.Fatal("a settled job carries no receipt")
	}
	return settled, node
}

// TestAReceiptIsEvidenceAboutOneExchange
//
// A signed statement about some unnamed request proves nothing: shown for any
// other job it would look equally valid, so a seller could issue one honest
// receipt and hand out copies. Bound to a digest of the actual prompt and
// completion, a receipt belongs to ONE request, and the buyer holding that text
// is the one who can prove which.
func TestAReceiptIsEvidenceAboutOneExchange(t *testing.T) {
	req := InferenceRequest{Prompt: "what is the capital of france", Model: "llama-3.3-70b"}
	settled, node := settledWithReceipt(t, req, 40)
	r := settled.Receipt

	if err := r.VerifyFor(settled.Buyer, req, settled.Completion); err != nil {
		t.Fatalf("the buyer's own receipt does not verify for their own exchange: %v", err)
	}
	if r.NodeID != node.AccountID() {
		t.Fatalf("receipt names node %s, want the signer %s", r.NodeID, node.AccountID())
	}

	// The same receipt, shown for a different question, is refused.
	other := InferenceRequest{Prompt: "what is the capital of spain", Model: "llama-3.3-70b"}
	if err := r.VerifyFor(settled.Buyer, other, settled.Completion); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("a receipt verified for a prompt it was not issued over: %v", err)
	}
	// And for a different answer to the same question.
	if err := r.VerifyFor(settled.Buyer, req, "a different completion"); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("a receipt verified for a completion it was not issued over: %v", err)
	}
}

// TestEveryClaimInAReceiptIsSigned. A receipt is only worth holding if the
// seller cannot later say a field was something else. The model is the one that
// matters most - it is what makes a substitution attributable rather than
// deniable - but a total or a token count a seller could edit after the fact
// would be just as useless.
func TestEveryClaimInAReceiptIsSigned(t *testing.T) {
	req := InferenceRequest{Prompt: "hello", Model: "llama-3.3-70b"}
	settled, _ := settledWithReceipt(t, req, 40)

	for _, tc := range []struct {
		field  string
		mutate func(*Receipt)
	}{
		{"the model it claims to have served", func(r *Receipt) { r.Model = "a-much-cheaper-model" }},
		{"the units billed", func(r *Receipt) { r.Units *= 2 }},
		{"the price per unit", func(r *Receipt) { r.PricePerUnit *= 2 }},
		{"the total charged", func(r *Receipt) { r.Total *= 2 }},
		{"the tokens it reported", func(r *Receipt) { r.TotalTokens *= 10 }},
		{"the buyer it names", func(r *Receipt) { r.Buyer = "someone-else" }},
		{"the payout account", func(r *Receipt) { r.Provider = "someone-else" }},
		{"the job it is for", func(r *Receipt) { r.JobID = "another-job" }},
		{"the exchange it covers", func(r *Receipt) { r.ExchangeDigest[0] ^= 0xff }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			// A fresh copy each time, including the digest, which is a slice.
			tampered := *settled.Receipt
			tampered.ExchangeDigest = append([]byte(nil), settled.Receipt.ExchangeDigest...)
			tc.mutate(&tampered)

			if err := tampered.Verify(); !errors.Is(err, ErrInvalidReceipt) {
				t.Fatalf("%s was changed and the receipt still verified", tc.field)
			}
		})
	}
}

// TestAReceiptCannotBeReSignedByAnybodyElse. A seller caught out could otherwise
// produce a "corrected" receipt over the same exchange with their own key and
// claim the first was a forgery. The node id is derived from the signing key and
// checked against it, so a receipt always names the party that signed it.
func TestAReceiptCannotBeReSignedByAnybodyElse(t *testing.T) {
	req := InferenceRequest{Prompt: "hello", Model: "llama-3.3-70b"}
	settled, node := settledWithReceipt(t, req, 40)

	impostor, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}

	// Keep the original node's name, sign with another key.
	forged := *settled.Receipt
	forged.PublicKey = impostor.PublicKey
	forged.Signature = nil
	forged.NodeID = node.AccountID()
	forged.Signature = signWith(t, impostor, &forged)

	if err := forged.Verify(); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("a receipt signed by one key while naming another node verified")
	}

	// Signing it honestly under the impostor's OWN name verifies - it is a valid
	// receipt from a different party, which is exactly what it is - and a reader
	// can see whose word it is.
	honest := *settled.Receipt
	if err := honest.Sign(impostor); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := honest.Verify(); err != nil {
		t.Fatalf("a receipt signed under its signer's own name should verify: %v", err)
	}
	if honest.NodeID == node.AccountID() {
		t.Fatal("re-signing kept the original node's name; the claim changed hands silently")
	}
}

// TestAReceiptsArithmeticIsCheckedNotTrusted. A total that does not follow from
// the units and the price is the simplest overcharge there is, and a signed
// document nobody read closely is where it would live.
func TestAReceiptsArithmeticIsCheckedNotTrusted(t *testing.T) {
	node, _ := token.GenerateAccount()
	r := &Receipt{
		JobID: "j", Buyer: "b", Provider: "p",
		Units: 10, PricePerUnit: 5, Total: 500, // 10 * 5 is 50
		ExchangeDigest: ExchangeDigest(InferenceRequest{Prompt: "x"}, "y"),
	}
	if err := r.Sign(node); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := r.Verify(); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("a receipt whose total does not match its own units and price verified")
	}
}

// TestAReceiptSurvivesBeingWrittenDownAndReadBack. It is only useful if a buyer
// can store it and a third party can check it later, holding nothing but the
// document and the text of the exchange.
func TestAReceiptSurvivesBeingWrittenDownAndReadBack(t *testing.T) {
	req := InferenceRequest{Prompt: "hello", Model: "llama-3.3-70b"}
	settled, _ := settledWithReceipt(t, req, 40)

	body, err := MarshalReceipt(settled.Receipt)
	if err != nil {
		t.Fatalf("MarshalReceipt: %v", err)
	}
	back, err := ParseReceipt(body)
	if err != nil {
		t.Fatalf("ParseReceipt: %v", err)
	}
	if err := back.VerifyFor(settled.Buyer, req, settled.Completion); err != nil {
		t.Fatalf("a receipt that went through JSON no longer verifies: %v", err)
	}
}

// TestTheBilledUnitsAndTheClaimedTokensAreBothOnTheReceipt. They are different
// numbers - the report is clamped to the reservation and to what the text can
// honestly have cost - and a buyer who can only see one cannot tell an honest
// bill from a capped one.
func TestTheBilledUnitsAndTheClaimedTokensAreBothOnTheReceipt(t *testing.T) {
	req := InferenceRequest{Prompt: "hi", Model: "m"}
	// Claims far more than two bytes of prompt could have cost.
	settled, _ := settledWithReceipt(t, req, 500)
	r := settled.Receipt

	if r.TotalTokens != 500 {
		t.Fatalf("the receipt records %d claimed tokens, want the 500 the seller reported", r.TotalTokens)
	}
	if r.Units >= 500 {
		t.Fatalf("billed %d units; the ceiling did not apply", r.Units)
	}
	// job.Units is the settled CHARGE - billable units already multiplied by the
	// price - while the receipt records both halves separately, which is the only
	// way a reader can check the arithmetic rather than take it.
	if r.Total != settled.Units {
		t.Fatalf("receipt total %d, the job settled %d", r.Total, settled.Units)
	}
	if r.Units*r.PricePerUnit != r.Total {
		t.Fatalf("%d units at %d each is not the %d charged", r.Units, r.PricePerUnit, r.Total)
	}
}

// signWith signs an already-populated receipt with a key, without letting Sign
// rewrite the identity fields.
func signWith(t *testing.T, acct *token.Account, r *Receipt) []byte {
	t.Helper()
	return ed25519Sign(acct, r.SigningBytes())
}
