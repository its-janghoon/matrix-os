package token

import (
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
)

// The EIP-55 vectors from the standard itself, so these are not this code's own
// idea of what a checksummed address looks like.
const (
	checksummed  = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	allLowercase = "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"
)

// The whole bug in one assertion: the id a person is shown and the id their key
// controls used to be two different accounts, and sending to the first put the
// money somewhere nobody can reach.
func TestTheFormAPersonCopiesReachesTheAccountTheirKeyControls(t *testing.T) {
	addr, err := ethsig.ParseAddress(checksummed)
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	controlled := EthAccountID(addr)

	got, err := CanonicalAccountID(EthAccountPrefix + checksummed)
	if err != nil {
		t.Fatalf("the form every wallet displays was refused: %v", err)
	}
	if got != controlled {
		t.Fatalf("pasting the displayed form reaches %s, but the key controls %s", got, controlled)
	}
}

// A mistyped address must not be quietly "repaired" into another unreachable
// one. The checksum exists to catch exactly this.
func TestAMistypedAddressIsRefusedRatherThanLowercased(t *testing.T) {
	// One character of the checksummed form changed: still 40 valid hex
	// characters, so nothing but the checksum can tell.
	typo := EthAccountPrefix + "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD"
	got, err := CanonicalAccountID(typo)
	if err == nil {
		t.Fatalf("a typo was accepted and turned into %s, which no key controls", got)
	}
	if !strings.Contains(err.Error(), "will not reach anybody") {
		t.Errorf("the error does not say the money would be lost: %v", err)
	}
	// And it names the string the person should have used, so the fix is
	// copy-and-paste rather than a puzzle.
	if !strings.Contains(err.Error(), checksummed) {
		t.Errorf("the error does not name the correct form: %v", err)
	}
}

// The machine form. Every client sends lowercase and it carries no checksum to
// check, so refusing it would break them all to guard against a form nobody
// types by hand.
func TestAnAllLowercaseAddressIsTakenAtFaceValue(t *testing.T) {
	got, err := CanonicalAccountID(EthAccountPrefix + allLowercase)
	if err != nil {
		t.Fatalf("the machine form was refused: %v", err)
	}
	if got != EthAccountPrefix+allLowercase {
		t.Errorf("canonicalizing changed the machine form: %s", got)
	}
}

// Anything that is not an ethereum id is not this function's business.
func TestOtherAccountIdsComeBackUntouched(t *testing.T) {
	for _, id := range []string{
		"5aaeb6053f3e94c9b9a09f33669435e7ef1beaed5aaeb6053f3e94c9b9a09f33", // ed25519
		"bridge/escrow",
		"consensus/stake/bond/abc123",
		// A reserved recipient carries its terms in the NAME, so case there is
		// meaning and not notation. Touching it would change what it says.
		"infer/escrow/JOB-1/4000000.1700000000/provider/payer",
		"",
	} {
		got, err := CanonicalAccountID(id)
		if err != nil {
			t.Errorf("%q: %v", id, err)
			continue
		}
		if got != id {
			t.Errorf("%q came back as %q", id, got)
		}
	}
}

// Surrounding whitespace is what a paste brings with it, not a different
// account.
func TestAPastedIdKeepsItsMeaningThroughWhitespace(t *testing.T) {
	got, err := CanonicalAccountID("  " + EthAccountPrefix + checksummed + "\n")
	if err != nil {
		t.Fatalf("a pasted id was refused: %v", err)
	}
	if got != EthAccountPrefix+allLowercase {
		t.Errorf("got %q", got)
	}
}

// Malformed input is refused, and never silently turned into an account.
func TestAMalformedEthIdIsRefused(t *testing.T) {
	for name, id := range map[string]string{
		"too short":          EthAccountPrefix + "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAe",
		"too long":           EthAccountPrefix + "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAedd",
		"not hex":            EthAccountPrefix + "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeZ",
		"nothing at all":     EthAccountPrefix,
		"an ethereum-ish id": EthAccountPrefix + "not-an-address",
	} {
		if got, err := CanonicalAccountID(id); err == nil {
			t.Errorf("%s: accepted %q as %q", name, id, got)
		}
	}
}

// An all-uppercase address is a form some older tools emit. It has a checksum
// to check and fails it, so it is refused - and the refusal names the form that
// works, which is the useful answer.
func TestAnAllUppercaseAddressIsRefusedWithTheFormThatWorks(t *testing.T) {
	upper := EthAccountPrefix + "0x" + strings.ToUpper(strings.TrimPrefix(allLowercase, "0x"))
	_, err := CanonicalAccountID(upper)
	if err == nil {
		t.Fatal("an all-uppercase address was accepted")
	}
	if !strings.Contains(err.Error(), checksummed) {
		t.Errorf("the refusal does not name the correct form: %v", err)
	}
}

// Canonicalizing twice is canonicalizing once. A caller that cannot tell
// whether an id has already been through this must not be punished for asking.
func TestCanonicalizingIsIdempotent(t *testing.T) {
	once, err := CanonicalAccountID(EthAccountPrefix + checksummed)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	twice, err := CanonicalAccountID(once)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if once != twice {
		t.Errorf("%q then %q", once, twice)
	}
}
