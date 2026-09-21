package token

import (
	"errors"
	"strings"
	"testing"
)

// The consensus half must accept exactly what the client half produces. Stated as
// a property rather than a table, because a table can drift: the only way these
// two disagree is if one of them stops calling the other.
func TestAnythingCanonicalAccountIDProducesIsAccepted(t *testing.T) {
	inputs := []string{
		EthAccountPrefix + checksummed,
		EthAccountPrefix + allLowercase,
		EthAccountPrefix + strings.ToUpper(allLowercase[2:]),
		"  " + EthAccountPrefix + checksummed + "\n",
		"bridge/escrow",
		"spend/budget/whatever",
		strings.Repeat("ab", 32),
	}
	for _, raw := range inputs {
		canonical, err := CanonicalAccountID(raw)
		if err != nil {
			continue // refused by both halves, which is agreement
		}
		if err := RequireCanonicalEthRecipient(canonical); err != nil {
			t.Errorf("CanonicalAccountID(%q) produced %q, which the chain rule refuses: %v", raw, canonical, err)
		}
	}
}

// Every form that reaches an account nobody holds a key for.
func TestTheFormsThatStrandMoneyAreRefused(t *testing.T) {
	stranding := []string{
		EthAccountPrefix + checksummed,                       // what a wallet displays
		EthAccountPrefix + strings.ToUpper(allLowercase[2:]), // all caps, no checksum to pass
		EthAccountPrefix + "0X" + allLowercase[2:],           // a prefix spelling
		" " + EthAccountPrefix + allLowercase,                // padded, keyed under the padding
		EthAccountPrefix + allLowercase + "\n",               // trailing newline from a paste
	}
	for _, id := range stranding {
		if err := RequireCanonicalEthRecipient(id); err == nil {
			t.Errorf("%q was accepted, and value sent to it would be unrecoverable", id)
		}
	}
}

// A mistyped mixed-case address fails its checksum, and that is a different
// refusal from "not the canonical form" - the caller should be able to tell a
// typo from a notation difference.
func TestATypoIsRefusedAsAChecksumFailure(t *testing.T) {
	typo := EthAccountPrefix + "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD"
	err := RequireCanonicalEthRecipient(typo)
	if err == nil {
		t.Fatal("a mistyped address was accepted")
	}
	if errors.Is(err, ErrNonCanonicalRecipient) {
		t.Error("a checksum failure was reported as a notation difference, which hides the typo")
	}
}

// The id the ledger keys the account by, and nothing else, passes untouched.
func TestTheKeyedFormPasses(t *testing.T) {
	if err := RequireCanonicalEthRecipient(EthAccountPrefix + allLowercase); err != nil {
		t.Fatalf("the form every client sends was refused: %v", err)
	}
}
