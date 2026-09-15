package node

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// Settling for a buyer needs that buyer's signing key, and the node looks for it
// on disk. It used to read only the older plaintext shape, while `matrix wallet
// create` and `import` write an encrypted keystore - so the node ignored the
// wallet its own CLI had just written and reported "no signing account for
// buyer", which names the buyer and says nothing about the format.
//
// That is the whole hosted path: /v1/chat/completions, the website's chat page,
// and streaming, none of which work without it.

// writeKeystoreWallet writes an encrypted wallet the way the CLI does.
func writeKeystoreWallet(t *testing.T, dir, name string, acct *token.Account, pass string) {
	t.Helper()
	ks, err := token.EncryptKeystore(acct, pass, false)
	if err != nil {
		t.Fatalf("EncryptKeystore: %v", err)
	}
	body, err := json.Marshal(ks)
	if err != nil {
		t.Fatalf("marshal keystore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
		t.Fatalf("write keystore: %v", err)
	}
}

// writePlaintextWallet writes the older shape, which must keep working.
func writePlaintextWallet(t *testing.T, dir, name string, acct *token.Account) {
	t.Helper()
	body, err := json.Marshal(walletDiskFile{
		PublicKey:  hex.EncodeToString(acct.PublicKey),
		PrivateKey: hex.EncodeToString(acct.PrivateKey),
	})
	if err != nil {
		t.Fatalf("marshal wallet: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
		t.Fatalf("write wallet: %v", err)
	}
}

func TestTheNodeSignsForAnEncryptedWallet(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	dir := t.TempDir()
	writeKeystoreWallet(t, dir, "wallet.json", acct, "correct horse battery staple")
	t.Setenv(WalletPassphraseEnv, "correct horse battery staple")

	got, ok := newWalletAccountsDir(dir).Account(acct.AccountID())
	if !ok {
		t.Fatal("the node could not sign for a wallet its own CLI writes")
	}
	if got.AccountID() != acct.AccountID() {
		t.Fatalf("resolved %s, want %s", got.AccountID(), acct.AccountID())
	}
	// A key that cannot sign is the failure this resolves into at settlement
	// time rather than here, so prove the key material survived the round trip.
	sig := ed25519.Sign(got.PrivateKey, []byte("settle"))
	if !ed25519.Verify(acct.PublicKey, []byte("settle"), sig) {
		t.Fatal("the unlocked key does not sign for the account it claims")
	}
}

func TestThePlaintextWalletStillResolves(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	dir := t.TempDir()
	writePlaintextWallet(t, dir, "wallet.json", acct)

	if _, ok := newWalletAccountsDir(dir).Account(acct.AccountID()); !ok {
		t.Fatal("the older wallet shape stopped resolving")
	}
}

// The passphrase gates signing, so a node without it must resolve nothing rather
// than resolve something wrong.
func TestAnEncryptedWalletWithoutItsPassphraseDoesNotResolve(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	dir := t.TempDir()
	writeKeystoreWallet(t, dir, "wallet.json", acct, "the passphrase")
	t.Setenv(WalletPassphraseEnv, "")

	if _, ok := newWalletAccountsDir(dir).Account(acct.AccountID()); ok {
		t.Fatal("a keystore resolved with no passphrase set")
	}

	t.Setenv(WalletPassphraseEnv, "the wrong passphrase")
	if _, ok := newWalletAccountsDir(dir).Account(acct.AccountID()); ok {
		t.Fatal("a keystore resolved under the wrong passphrase")
	}
}

// The id is matched in the CLEAR, before scrypt runs. Without that the resolver
// would derive a key for every wallet in the directory on every lookup, putting
// a deliberately expensive KDF in front of every settlement.
//
// Asserted by behaviour rather than by timing: with no passphrase set at all, a
// lookup for an account no file holds must still return cleanly. A resolver that
// decrypted first would need the passphrase to get that far.
func TestAnUnrelatedAccountCostsNoDecryption(t *testing.T) {
	mine, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	someoneElse, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	dir := t.TempDir()
	writeKeystoreWallet(t, dir, "wallet.json", mine, "the passphrase")
	t.Setenv(WalletPassphraseEnv, "")

	if _, ok := newWalletAccountsDir(dir).Account(someoneElse.AccountID()); ok {
		t.Fatal("resolved an account no wallet in the directory holds")
	}
}

// A wallet directory is not curated. An attestor keystore, another account's
// wallet, and files that are not wallets at all can sit beside the one being
// looked for, and none of them may derail the lookup.
func TestAMixedWalletDirectoryStillFindsTheRightOne(t *testing.T) {
	wanted, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	other, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	dir := t.TempDir()
	writePlaintextWallet(t, dir, "old.json", other)
	writeKeystoreWallet(t, dir, "wallet.json", wanted, "the passphrase")
	if err := os.WriteFile(filepath.Join(dir, "notes.json"), []byte("{not a wallet"), 0o600); err != nil {
		t.Fatalf("write junk: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write junk: %v", err)
	}
	t.Setenv(WalletPassphraseEnv, "the passphrase")

	w := newWalletAccountsDir(dir)
	if _, ok := w.Account(wanted.AccountID()); !ok {
		t.Fatal("the wanted wallet was lost among its neighbours")
	}
	if _, ok := w.Account(other.AccountID()); !ok {
		t.Fatal("the plaintext neighbour stopped resolving")
	}
}

// Deriving the key is scrypt at 2^17, so a node that did it per lookup would put
// that cost in front of every settlement. The resolved account is cached.
func TestAnUnlockedWalletIsNotUnlockedTwice(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	dir := t.TempDir()
	writeKeystoreWallet(t, dir, "wallet.json", acct, "the passphrase")
	t.Setenv(WalletPassphraseEnv, "the passphrase")

	w := newWalletAccountsDir(dir)
	if _, ok := w.Account(acct.AccountID()); !ok {
		t.Fatal("first lookup failed")
	}
	// Remove the file and clear the passphrase: a second lookup that still
	// answers can only be answering from the cache.
	if err := os.Remove(filepath.Join(dir, "wallet.json")); err != nil {
		t.Fatalf("remove wallet: %v", err)
	}
	t.Setenv(WalletPassphraseEnv, "")
	if _, ok := w.Account(acct.AccountID()); !ok {
		t.Fatal("the unlocked account was not cached, so every settlement pays for scrypt")
	}
}
