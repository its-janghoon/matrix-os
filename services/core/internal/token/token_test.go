package token

import (
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
)

// newTestStore creates a real Pebble kv.Store on a temp dir and registers
// cleanup, mirroring internal/market tests so we exercise real persistence.
func newTestStore(t *testing.T) *kv.Store {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create kv store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("failed to close store: %v", err)
		}
	})
	return store
}

// mustAccount generates an account or fails the test.
func mustAccount(t *testing.T) *Account {
	t.Helper()
	acct, err := GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount() error = %v", err)
	}
	return acct
}

func TestGenerateAccount_StableAccountID(t *testing.T) {
	acct := mustAccount(t)

	id1 := acct.AccountID()
	id2 := acct.AccountID()
	if id1 != id2 {
		t.Errorf("AccountID() not stable: %q vs %q", id1, id2)
	}
	if id1 == "" {
		t.Fatal("AccountID() is empty")
	}

	// Round-trip the derived ID back to a public key.
	pub, err := ParsePublicKeyHex(id1)
	if err != nil {
		t.Fatalf("ParsePublicKeyHex() error = %v", err)
	}
	if !pub.Equal(acct.PublicKey) {
		t.Error("round-tripped public key does not match original")
	}

	// Distinct accounts yield distinct IDs.
	other := mustAccount(t)
	if other.AccountID() == id1 {
		t.Error("distinct accounts produced identical AccountIDs")
	}
}

func TestParsePublicKey_Invalid(t *testing.T) {
	if _, err := ParsePublicKey([]byte{1, 2, 3}); err == nil {
		t.Error("ParsePublicKey() accepted a short key")
	}
	if _, err := ParsePublicKeyHex("zzzz"); err == nil {
		t.Error("ParsePublicKeyHex() accepted non-hex input")
	}
}
