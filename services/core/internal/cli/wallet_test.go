package cli

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// TestLoadWallet_RoundTrip asserts a wallet written by createWallet loads back
// into the same account (the happy path the key-pairing check must not break).
func TestLoadWallet_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallet.json")
	created, err := createWallet(path)
	if err != nil {
		t.Fatalf("createWallet: %v", err)
	}
	loaded, err := loadWallet(path)
	if err != nil {
		t.Fatalf("loadWallet: %v", err)
	}
	if loaded.AccountID() != created.AccountID() {
		t.Fatalf("loaded account id = %s, want %s", loaded.AccountID(), created.AccountID())
	}
}

// TestLoadWallet_RejectsMismatchedKeyPair is the robustness regression for a
// corrupt/hand-edited wallet: a file whose public key does not correspond to its
// private key must fail fast on load rather than resolve to an account that only
// fails at signature time.
func TestLoadWallet_RejectsMismatchedKeyPair(t *testing.T) {
	// Two independent key pairs; splice A's private key with B's public key.
	a, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount a: %v", err)
	}
	b, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount b: %v", err)
	}

	wf := walletFile{
		PublicKey:  hex.EncodeToString(b.PublicKey),  // mismatched: B's public
		PrivateKey: hex.EncodeToString(a.PrivateKey), // A's private
	}
	data, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(t.TempDir(), "wallet.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err = loadWallet(path)
	if err == nil {
		t.Fatal("expected loadWallet to reject a mismatched key pair, got nil error")
	}
	if !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("expected a corrupt-wallet error, got: %v", err)
	}
}

// sanity: the derived helper agrees with ed25519's own derivation.
func TestDerivedMatchesPublic(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	if !derived(acct.PrivateKey).Equal(ed25519.PublicKey(acct.PublicKey)) {
		t.Fatal("derived(priv) does not equal the account public key")
	}
}
