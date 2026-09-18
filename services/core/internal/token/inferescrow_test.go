package token

import (
	"errors"
	"strings"
	"testing"
)

const (
	testJob      = "3f2b7c11-4e5a-4c8d-9b0f-1a2b3c4d5e6f"
	testProvider = "eth:0x856e3fff84a5e833420b43cec0b4e13c16779817"
	testPayer    = "eth:0xf9cd1de365a4ad85b295260c36dd9e3113991f54"
)

func escrow() InferEscrow {
	return InferEscrow{
		JobID:    testJob,
		Reserved: 200000,
		Expiry:   1789623713,
		Provider: testProvider,
		Payer:    testPayer,
	}
}

// The account name is a wire format the moment money sits in it, so it is
// asserted against a literal rather than against the code that produced it.
func TestTheAccountNameIsExactlyThis(t *testing.T) {
	const want = "infer/escrow/3f2b7c11-4e5a-4c8d-9b0f-1a2b3c4d5e6f/200000.1789623713/" +
		"eth:0x856e3fff84a5e833420b43cec0b4e13c16779817/eth:0xf9cd1de365a4ad85b295260c36dd9e3113991f54"
	if got := escrow().Account(); got != want {
		t.Fatalf("account name changed\n got %q\nwant %q", got, want)
	}
}

func TestEveryRecipientRoundTripsToTheSameTerms(t *testing.T) {
	e := escrow()
	for _, tc := range []struct {
		name  string
		to    string
		parse func(string) (InferEscrow, error)
	}{
		{"escrow", e.Account(), ParseInferEscrow},
		{"settle", e.SettleRecipient(), ParseInferSettle},
		{"claim", e.ClaimRecipient(), ParseInferClaim},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.parse(tc.to)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got != e {
				t.Fatalf("round trip lost terms\n got %+v\nwant %+v", got, e)
			}
		})
	}
}

// The reason the payer is the last segment. A budget account's own name carries
// slashes and dots, and if it does not survive a round trip then a job funded by
// a budget refunds into an account nobody can name.
func TestABudgetCanBeThePayer(t *testing.T) {
	budget := SpendEscrow{
		Buyer:           testPayer,
		Delegate:        strings.Repeat("ab", 32),
		PerJobCap:       10000,
		MaxPricePerUnit: 10000,
		Expiry:          1789623713,
		Nonce:           0,
	}
	e := escrow()
	e.Payer = budget.Account()
	if !strings.Contains(e.Payer, "/") || !strings.Contains(e.Payer, ".") {
		t.Fatalf("this test is pointless unless a budget name has both separators in it: %q", e.Payer)
	}

	got, err := ParseInferEscrow(e.Account())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Payer != e.Payer {
		t.Fatalf("budget payer did not survive\n got %q\nwant %q", got.Payer, e.Payer)
	}
	if got != e {
		t.Fatalf("round trip lost terms\n got %+v\nwant %+v", got, e)
	}
}

func TestRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		to   string
	}{
		{"not an escrow at all", "eth:0x856e3fff84a5e833420b43cec0b4e13c16779817"},
		{"too few segments", InferEscrowPrefix + testJob + "/200000.1789623713/" + testProvider},
		{"job id is not a uuid", InferEscrowPrefix + "nope/200000.1789623713/" + testProvider + "/" + testPayer},
		{"job id has uppercase", InferEscrowPrefix + strings.ToUpper(testJob) + "/200000.1789623713/" + testProvider + "/" + testPayer},
		{"amounts are not a pair", InferEscrowPrefix + testJob + "/200000/" + testProvider + "/" + testPayer},
		{"reserved is zero", InferEscrowPrefix + testJob + "/0.1789623713/" + testProvider + "/" + testPayer},
		{"reserved has a leading zero", InferEscrowPrefix + testJob + "/0200000.1789623713/" + testProvider + "/" + testPayer},
		{"expiry is zero", InferEscrowPrefix + testJob + "/200000.0/" + testProvider + "/" + testPayer},
		{"provider is checksummed", InferEscrowPrefix + testJob + "/200000.1789623713/eth:0x856E3fff84a5e833420b43cec0b4e13c16779817/" + testPayer},
		{"provider is a reserved recipient", InferEscrowPrefix + testJob + "/200000.1789623713/infer/escrow/x/" + testPayer},
		{"payer equals provider", InferEscrowPrefix + testJob + "/200000.1789623713/" + testProvider + "/" + testProvider},
		{"payer is a claim", InferEscrowPrefix + testJob + "/200000.1789623713/" + testProvider + "/" + InferClaimPrefix + "x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseInferEscrow(tc.to); err == nil {
				t.Fatalf("accepted %q", tc.to)
			} else if !errors.Is(err, ErrInvalidAuthorization) {
				t.Fatalf("wrong error kind for %q: %v", tc.to, err)
			}
		})
	}
}

// A settlement and a claim name the same reservation, so their terms must be
// byte-identical: a provider whose claim named a different account than the
// buyer's settlement would be claiming an escrow that does not exist.
func TestSettleAndClaimNameTheSameReservation(t *testing.T) {
	e := escrow()
	settleTerms := strings.TrimPrefix(e.SettleRecipient(), InferSettlePrefix)
	claimTerms := strings.TrimPrefix(e.ClaimRecipient(), InferClaimPrefix)
	escrowTerms := strings.TrimPrefix(e.Account(), InferEscrowPrefix)
	if settleTerms != escrowTerms || claimTerms != escrowTerms {
		t.Fatalf("terms differ between operations:\nescrow %q\nsettle %q\nclaim  %q",
			escrowTerms, settleTerms, claimTerms)
	}
}

func TestRecipientPredicates(t *testing.T) {
	e := escrow()
	for _, to := range []string{e.Account(), e.SettleRecipient(), e.ClaimRecipient()} {
		if !IsInferRecipient(to) {
			t.Fatalf("%q should be an inference recipient", to)
		}
	}
	if !IsInferEscrowAccount(e.Account()) {
		t.Fatal("the escrow account should be the one that holds a balance")
	}
	for _, to := range []string{e.SettleRecipient(), e.ClaimRecipient()} {
		if IsInferEscrowAccount(to) {
			t.Fatalf("%q holds no balance and must not look like it does", to)
		}
	}
	if IsInferRecipient(testProvider) {
		t.Fatal("an ordinary account is not an inference recipient")
	}
}

func TestValidateMatchesParsing(t *testing.T) {
	if err := escrow().Validate(); err != nil {
		t.Fatalf("a well-formed escrow did not validate: %v", err)
	}
	bad := escrow()
	bad.Reserved = 0
	if err := bad.Validate(); err == nil {
		t.Fatal("a zero reservation validated")
	}
}
