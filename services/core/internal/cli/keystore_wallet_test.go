package cli

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/token"
)

func tempWallet(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "wallet.json")
}

// TestAnEncryptedWalletRoundTripsAndKeepsNoSecretInTheClear is the whole point
// of the change: the file this replaces was the private key in hex.
func TestAnEncryptedWalletRoundTripsAndKeepsNoSecretInTheClear(t *testing.T) {
	path := tempWallet(t)

	acct, phrase, err := createEncryptedWallet(path, "a passphrase")
	if err != nil {
		t.Fatalf("createEncryptedWallet: %v", err)
	}
	if err := token.ValidateMnemonic(phrase); err != nil {
		t.Fatalf("the phrase it showed is not valid: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw), hex.EncodeToString(acct.PrivateKey.Seed())) {
		t.Fatal("the seed is in the file in the clear")
	}
	if strings.Contains(string(raw), phrase) {
		t.Fatal("the recovery phrase is in the file; it exists on paper, not on disk")
	}

	loaded, err := loadWallet(path, func() (string, error) { return "a passphrase", nil })
	if err != nil {
		t.Fatalf("loadWallet: %v", err)
	}
	if loaded.AccountID() != acct.AccountID() {
		t.Fatalf("loaded %s, want %s", loaded.AccountID(), acct.AccountID())
	}
}

func TestTheFileIsOwnerOnly(t *testing.T) {
	path := tempWallet(t)
	if _, _, err := createEncryptedWallet(path, "pass"); err != nil {
		t.Fatalf("createEncryptedWallet: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// Encryption is the first lock; permissions are the cheap second one, not a
	// replacement for it.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want 600", perm)
	}
}

// TestTheRecoveryPhraseRestoresTheSameAccount is the promise the phrase makes.
func TestTheRecoveryPhraseRestoresTheSameAccount(t *testing.T) {
	original := tempWallet(t)
	acct, phrase, err := createEncryptedWallet(original, "pass")
	if err != nil {
		t.Fatalf("createEncryptedWallet: %v", err)
	}

	// Somewhere else entirely, as if the first machine were gone.
	restoredPath := tempWallet(t)
	restored, err := importEncryptedWallet(restoredPath, phrase, "a different passphrase")
	if err != nil {
		t.Fatalf("importEncryptedWallet: %v", err)
	}
	if restored.AccountID() != acct.AccountID() {
		t.Fatalf("restored %s, want %s", restored.AccountID(), acct.AccountID())
	}

	// And the new file's passphrase is its own: a phrase restores the KEY, not
	// the old passphrase.
	if _, err := loadWallet(restoredPath, func() (string, error) { return "pass", nil }); err == nil {
		t.Fatal("the restored wallet should not open with the original's passphrase")
	}
	if _, err := loadWallet(restoredPath, func() (string, error) { return "a different passphrase", nil }); err != nil {
		t.Fatalf("the restored wallet should open with its own passphrase: %v", err)
	}
}

func TestAMistypedPhraseIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	path := tempWallet(t)

	if _, err := importEncryptedWallet(path, "not even close to a phrase", "pass"); err == nil {
		t.Fatal("want a refusal for an invalid phrase")
	}
	// Nothing may be left behind: a half-written wallet at the path would then
	// block a correct retry with "wallet already exists".
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a refused import must not leave a file behind")
	}
}

func TestCreatingOverAnExistingWalletIsRefused(t *testing.T) {
	path := tempWallet(t)
	if _, _, err := createEncryptedWallet(path, "pass"); err != nil {
		t.Fatalf("createEncryptedWallet: %v", err)
	}
	if _, _, err := createEncryptedWallet(path, "pass"); err == nil {
		t.Fatal("want a refusal rather than destroying an existing key")
	}
	if _, err := importEncryptedWallet(path, "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "pass"); err == nil {
		t.Fatal("import must not overwrite an existing wallet either")
	}
}

func TestTheWrongPassphraseSaysSo(t *testing.T) {
	path := tempWallet(t)
	if _, _, err := createEncryptedWallet(path, "right"); err != nil {
		t.Fatalf("createEncryptedWallet: %v", err)
	}
	_, err := loadWallet(path, func() (string, error) { return "wrong", nil })
	if err == nil {
		t.Fatal("want a failure")
	}
	if !errors.Is(err, token.ErrWrongPassphrase) {
		t.Fatalf("err = %v, want it to wrap ErrWrongPassphrase", err)
	}
}

// TestTheAccountIDIsReadableWithoutThePassphrase: printing your own address, or
// reading its balance, is not a reason to type a passphrase.
func TestTheAccountIDIsReadableWithoutThePassphrase(t *testing.T) {
	path := tempWallet(t)
	acct, _, err := createEncryptedWallet(path, "pass")
	if err != nil {
		t.Fatalf("createEncryptedWallet: %v", err)
	}

	got, err := walletAccountID(path)
	if err != nil {
		t.Fatalf("walletAccountID: %v", err)
	}
	if got != acct.AccountID() {
		t.Fatalf("got %s, want %s", got, acct.AccountID())
	}
}

func TestTheAccountIDIsReadableFromALegacyWalletToo(t *testing.T) {
	path := tempWallet(t)
	acct, err := createWallet(path)
	if err != nil {
		t.Fatalf("createWallet: %v", err)
	}
	got, err := walletAccountID(path)
	if err != nil {
		t.Fatalf("walletAccountID: %v", err)
	}
	if got != acct.AccountID() {
		t.Fatalf("got %s, want %s", got, acct.AccountID())
	}
}

// TestALegacyWalletStillSigns: refusing these would strand every account made
// before encryption existed, and no tool can migrate one unasked because it
// cannot invent a passphrase.
func TestALegacyWalletStillSigns(t *testing.T) {
	path := tempWallet(t)
	created, err := createWallet(path)
	if err != nil {
		t.Fatalf("createWallet: %v", err)
	}
	loaded, err := loadWallet(path, nil)
	if err != nil {
		t.Fatalf("a legacy wallet must still load: %v", err)
	}
	if loaded.AccountID() != created.AccountID() {
		t.Fatal("a legacy wallet loaded as a different account")
	}
}

// TestAKeystoreWithoutAPassphraseFunctionIsRefusedRatherThanGuessed.
func TestAKeystoreWithoutAPassphraseFunctionIsRefused(t *testing.T) {
	path := tempWallet(t)
	if _, _, err := createEncryptedWallet(path, "pass"); err != nil {
		t.Fatalf("createEncryptedWallet: %v", err)
	}
	if _, err := loadWallet(path, nil); err == nil {
		t.Fatal("want a refusal when there is no way to ask for the passphrase")
	}
}

// TestALegacyWalletsPublicKeyIsStillChecked keeps the older robustness
// behaviour: a hand-edited file whose halves disagree must fail on load, not at
// signature time.
func TestALegacyWalletsPublicKeyIsStillChecked(t *testing.T) {
	a, _ := token.GenerateAccount()
	b, _ := token.GenerateAccount()
	data, err := json.MarshalIndent(walletFile{
		PublicKey:  hex.EncodeToString(b.PublicKey),
		PrivateKey: hex.EncodeToString(a.PrivateKey),
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := tempWallet(t)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadWallet(path, nil); err == nil {
		t.Fatal("want a mismatched legacy wallet refused")
	}
}
