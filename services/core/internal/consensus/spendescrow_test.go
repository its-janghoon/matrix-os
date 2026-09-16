package consensus

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
)

// The terms ARE the account's identity, so the decoder is the thing worth
// testing: two spellings of one budget would put a buyer's money in an account
// their own close cannot name.

func testEscrow(t *testing.T) SpendEscrow {
	t.Helper()
	buyerPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	delegatePub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return SpendEscrow{
		Buyer:           hex.EncodeToString(buyerPub),
		Delegate:        hex.EncodeToString(delegatePub),
		PerJobCap:       100_000,
		MaxPricePerUnit: 500,
		Expiry:          1893456000,
		Nonce:           0,
	}
}

func TestABudgetSurvivesBeingWrittenDownAndReadBack(t *testing.T) {
	want := testEscrow(t)
	want.Nonce = 7

	got, err := ParseSpendEscrow(want.Account())
	if err != nil {
		t.Fatalf("ParseSpendEscrow: %v", err)
	}
	if got != want {
		t.Fatalf("round trip changed the terms:\n got %+v\nwant %+v", got, want)
	}

	payee := strings.Repeat("ab", 32)
	gotPayee, gotEscrow, err := ParseSpendDraw(want.DrawRecipient(payee))
	if err != nil {
		t.Fatalf("ParseSpendDraw: %v", err)
	}
	if gotPayee != payee || gotEscrow != want {
		t.Fatalf("a draw lost something: payee %q, terms %+v", gotPayee, gotEscrow)
	}

	gotClose, err := ParseSpendClose(want.CloseRecipient())
	if err != nil {
		t.Fatalf("ParseSpendClose: %v", err)
	}
	if gotClose != want {
		t.Fatalf("a close lost something: %+v", gotClose)
	}
}

// One budget must have exactly one account name. Every case here is a second
// spelling of the same terms, and accepting any of them would mean a deposit
// and a close could name different accounts.
func TestOneBudgetHasExactlyOneSpelling(t *testing.T) {
	base := testEscrow(t)

	for name, to := range map[string]string{
		"a padded per-job cap":   spendEscrowPrefix + base.Buyer + "." + base.Delegate + ".0100000.500.1893456000.0",
		"a padded nonce":         spendEscrowPrefix + base.Buyer + "." + base.Delegate + ".100000.500.1893456000.00",
		"a signed expiry":        spendEscrowPrefix + base.Buyer + "." + base.Delegate + ".100000.500.+1893456000.0",
		"an uppercase delegate":  spendEscrowPrefix + base.Buyer + "." + strings.ToUpper(base.Delegate) + ".100000.500.1893456000.0",
		"a short delegate":       spendEscrowPrefix + base.Buyer + ".abcd.100000.500.1893456000.0",
		"a missing field":        spendEscrowPrefix + base.Buyer + "." + base.Delegate + ".100000.500.1893456000",
		"an extra field":         spendEscrowPrefix + base.Buyer + "." + base.Delegate + ".100000.500.1893456000.0.9",
		"a zero per-job cap":     spendEscrowPrefix + base.Buyer + "." + base.Delegate + ".0.500.1893456000.0",
		"a zero price ceiling":   spendEscrowPrefix + base.Buyer + "." + base.Delegate + ".100000.0.1893456000.0",
		"a zero expiry":          spendEscrowPrefix + base.Buyer + "." + base.Delegate + ".100000.500.0.0",
		"a negative expiry":      spendEscrowPrefix + base.Buyer + "." + base.Delegate + ".100000.500.-1.0",
		"a buyer that is a path": spendEscrowPrefix + "consensus/stake/bond/x." + base.Delegate + ".100000.500.1893456000.0",
	} {
		if _, err := ParseSpendEscrow(to); err == nil {
			t.Errorf("%s was accepted as a budget account", name)
		}
	}
}

// A mixed-case eth buyer is the same trap a transfer recipient has: the ledger
// keys on the string verbatim, while an eth signature always resolves to the
// lowercase id. A budget named with the checksummed spelling could be funded and
// never closed.
func TestAnEthBuyerMustBeSpelledTheWayASignatureResolves(t *testing.T) {
	base := testEscrow(t)
	base.Buyer = "eth:0xf9cd1de365a4ad85b295260c36dd9e3113991f54"
	if _, err := ParseSpendEscrow(base.Account()); err != nil {
		t.Fatalf("a lowercase eth buyer must be accepted: %v", err)
	}

	checksummed := base
	checksummed.Buyer = "eth:0xF9cd1dE365a4Ad85b295260c36Dd9e3113991f54"
	if _, err := ParseSpendEscrow(checksummed.Account()); err == nil {
		t.Fatal("a checksummed eth buyer was accepted, which is an account nothing can close")
	}
}

// The delegate's whole value is that what it can do is a payment. A draw into a
// reserved recipient would be it moving the money somewhere consensus treats
// specially - another budget, a bond, a validator set change - which is the
// bound coming off.
func TestADrawPaysAnAccountAndNotAnOperation(t *testing.T) {
	base := testEscrow(t)
	other := testEscrow(t)

	for name, payee := range map[string]string{
		"another budget":     other.Account(),
		"a bond":             "consensus/stake/bond/" + base.Buyer,
		"a validator change": "consensus/set/add/" + base.Delegate,
		"a bridge escrow":    "bridge/escrow",
		"nothing at all":     "",
		"a bare word":        "somebody",
	} {
		if _, _, err := ParseSpendDraw(base.DrawRecipient(payee)); err == nil {
			t.Errorf("a draw to %s was accepted", name)
		}
	}
}

// A draw carries the escrow because the signature identifies who is spending and
// not what is being spent, so the two halves must not be confusable: a draw
// recipient is not an escrow account and cannot be parsed as one.
func TestTheThreeOperationsAreNotEachOther(t *testing.T) {
	base := testEscrow(t)
	payee := strings.Repeat("cd", 32)

	if _, err := ParseSpendEscrow(base.DrawRecipient(payee)); err == nil {
		t.Error("a draw was read as a budget account")
	}
	if _, err := ParseSpendEscrow(base.CloseRecipient()); err == nil {
		t.Error("a close was read as a budget account")
	}
	if _, _, err := ParseSpendDraw(base.Account()); err == nil {
		t.Error("a budget account was read as a draw")
	}
	if _, err := ParseSpendClose(base.Account()); err == nil {
		t.Error("a budget account was read as a close")
	}

	// Only the escrow holds a balance, and the apply path decides what to debit
	// from that. A draw or a close naming itself as an account would be a place
	// money could sit with no rule that moves it out.
	if !IsSpendEscrowAccount(base.Account()) {
		t.Error("a budget account is not recognised as one")
	}
	if IsSpendEscrowAccount(base.DrawRecipient(payee)) || IsSpendEscrowAccount(base.CloseRecipient()) {
		t.Error("a draw or a close was recognised as an account that holds money")
	}
	for _, to := range []string{base.Account(), base.DrawRecipient(payee), base.CloseRecipient()} {
		if !IsSpendRecipient(to) {
			t.Errorf("%q was not recognised as a budget operation", to)
		}
	}
	if IsSpendRecipient("consensus/stake/bond/x") || IsSpendRecipient(strings.Repeat("ab", 32)) {
		t.Error("something that is not a budget operation was recognised as one")
	}
}

// Two budgets differing only in nonce are different accounts, which is what lets
// a buyer open a second one without inheriting whatever is left in the first.
func TestTheNonceIsWhatMakesASecondBudgetASecondAccount(t *testing.T) {
	first := testEscrow(t)
	second := first
	second.Nonce = first.Nonce + 1

	if first.Account() == second.Account() {
		t.Fatal("two budgets with different nonces share an account")
	}
	// And every other term changes the account too, so a delegate cannot spend
	// against terms the buyer did not deposit under.
	for name, mutate := range map[string]func(*SpendEscrow){
		"a wider per-job cap":  func(s *SpendEscrow) { s.PerJobCap *= 2 },
		"a higher ceiling":     func(s *SpendEscrow) { s.MaxPricePerUnit *= 2 },
		"a later expiry":       func(s *SpendEscrow) { s.Expiry += 86400 },
		"a different delegate": func(s *SpendEscrow) { s.Delegate = strings.Repeat("ef", 32) },
	} {
		altered := first
		mutate(&altered)
		if altered.Account() == first.Account() {
			t.Errorf("%s did not change the account", name)
		}
	}
}
