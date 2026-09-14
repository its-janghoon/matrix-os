package node

import (
	"strings"
	"testing"
)

// A provider's id IS the account it is paid into.
//
// inference/settle.go builds the settlement transfer with To: <provider id>.
// Transaction.Verify authenticates the SENDER, and a block only refuses an
// EMPTY recipient, so any string is an acceptable payee. A typo in this one
// config field therefore produces a provider that sells normally and credits
// every sale to an account no key can sign for. Nothing fails, nothing logs,
// and the operator finds out when they go looking for the money.
//
// Not hypothetical on our own network: `matrixd -init` seeds
// echo_provider: demo-inference-provider, which is exactly such an id, and two
// validators were found advertising it on a live order book.

func TestSpendableProviderIDsAreAccepted(t *testing.T) {
	for _, id := range []string{
		"eth:0x856e3fff84a5e833420b43cec0b4e13c16779817", // a wallet holds the key
		strings.Repeat("a", 64),                          // an ed25519 account
	} {
		if err := validateProductionAccountID(id); err != nil {
			t.Errorf("%q should be a valid payout account: %v", id, err)
		}
	}
}

func TestUnspendableProviderIDsAreRejected(t *testing.T) {
	cases := []struct{ name, id string }{
		{"the -init demo stub", "demo-inference-provider"},
		{"a friendly name", "my-gpu-box"},
		// Accounts are lowercased, so a checksummed address names a DIFFERENT
		// account than the one the wallet controls - the worst kind of typo,
		// because it looks right.
		{"a checksummed address", "eth:0x856E3FFf84A5e833420b43cec0B4e13c16779817"},
		{"an address missing a digit", "eth:0x856e3fff84a5e833420b43cec0b4e13c1677981"},
		{"hex of the wrong length", strings.Repeat("a", 63)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateProductionAccountID(tc.id); err == nil {
				t.Fatalf("%q was accepted; every sale would credit an account nothing can sign for", tc.id)
			}
		})
	}
}
