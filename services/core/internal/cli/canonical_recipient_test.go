package cli

import (
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// The EIP-55 form of a real address: what MetaMask, Etherscan and every block of
// documentation put in front of a person, and therefore what they copy.
const displayedAddress = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"

func controlledAccount(t *testing.T) string {
	t.Helper()
	addr, err := ethsig.ParseAddress(displayedAddress)
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	return token.EthAccountID(addr)
}

// Money must arrive at the account the key controls, not at the string that was
// pasted.
//
// `fund` moves genesis supply, which there is only one of, and it used to credit
// whatever id it was handed: pasting the displayed form put the money at a key
// nobody holds, printed a success line with a balance on it, and left nothing
// anyone could sign to get it back.
func TestFundingTheDisplayedFormReachesTheAccountTheKeyControls(t *testing.T) {
	addr, mkt := startFundServer(t, 1_000_000)
	pasted := token.EthAccountPrefix + displayedAddress
	controlled := controlledAccount(t)

	out, err := runFund(t, addr, "--api-key", fundServerAPIKey,
		"fund", "--account", pasted, "--amount", "250000")
	if err != nil {
		t.Fatalf("fund: %v (%s)", err, out)
	}

	if bal, _ := mkt.Ledger().Balance(controlled); bal != 250000 {
		t.Errorf("the account the key controls holds %d, want 250000", bal)
	}
	// And nothing is sitting at the pasted string, which is where it used to go.
	if bal, _ := mkt.Ledger().Balance(pasted); bal != 0 {
		t.Errorf("%d base units are stranded at %q, which no key controls", bal, pasted)
	}
	// The output names the account that actually holds it, so a reader is not
	// told a different id from the one their money is at.
	if !strings.Contains(out, controlled) {
		t.Errorf("output does not name the credited account %s: %s", controlled, out)
	}
}

// A mistyped address is refused before anything moves, rather than repaired into
// another unreachable one.
func TestFundingARefusedAddressMovesNothing(t *testing.T) {
	addr, mkt := startFundServer(t, 1_000_000)
	// One character of the checksummed form changed: still 40 valid hex
	// characters, so only the checksum can tell.
	typo := token.EthAccountPrefix + "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD"

	out, err := runFund(t, addr, "--api-key", fundServerAPIKey,
		"fund", "--account", typo, "--amount", "250000")
	if err == nil {
		t.Fatalf("a mistyped address was funded: %s", out)
	}
	if !strings.Contains(err.Error(), "will not reach anybody") {
		t.Errorf("the refusal does not say the money would be lost: %v", err)
	}
	if bal, _ := mkt.Ledger().Balance(typo); bal != 0 {
		t.Errorf("%d base units moved to the mistyped id anyway", bal)
	}
	if bal, _ := mkt.Ledger().Balance(controlledAccount(t)); bal != 0 {
		t.Errorf("%d base units moved to the address the typo resembles, which is not what was asked for", bal)
	}
}

// Reading a balance with the form you were shown must answer with your balance.
// It used to answer 0, which reads as "your money is gone".
func TestReadingABalanceWithTheDisplayedFormFindsIt(t *testing.T) {
	addr, mkt := startFundServer(t, 1_000_000)
	if err := mkt.Ledger().Credit(controlledAccount(t), 777); err != nil {
		t.Fatalf("credit: %v", err)
	}

	out, err := runFund(t, addr, "--api-key", fundServerAPIKey,
		"balance", "--account", token.EthAccountPrefix+displayedAddress)
	if err != nil {
		t.Fatalf("balance: %v (%s)", err, out)
	}
	if !strings.Contains(out, "777") {
		t.Errorf("the balance was not found by the form every wallet displays: %s", out)
	}
}
