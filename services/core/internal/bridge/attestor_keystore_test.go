package bridge

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// The attestor key lives in the same encrypted keystore an account key does.
//
// It used to live nowhere: the node held no attestor at all, and the only signer
// in the tree was cmd/bridge-attest's deterministic test seed. A validator's
// attestor key is unilateral authority to mint wrapped tokens against escrow, so
// the alternative on the table - a hex string in a config file - was the wrong
// answer to a question the repo had already answered once for wallet keys.

func writeKS(t *testing.T, ks *token.Keystore) string {
	t.Helper()
	body, err := token.MarshalKeystore(ks)
	if err != nil {
		t.Fatalf("MarshalKeystore: %v", err)
	}
	path := filepath.Join(t.TempDir(), "attestor.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestAnAttestorSurvivesAKeystoreRoundTrip(t *testing.T) {
	ks, att, err := NewAttestorKeystore("correct horse battery staple")
	if err != nil {
		t.Fatalf("NewAttestorKeystore: %v", err)
	}
	path := writeKS(t, ks)

	back, err := LoadAttestorKeystore(path, "correct horse battery staple")
	if err != nil {
		t.Fatalf("LoadAttestorKeystore: %v", err)
	}
	if back.Address() != att.Address() {
		t.Fatalf("unlocked %x, want %x", back.Address(), att.Address())
	}

	// And it actually signs the same way: an attestation is only useful if the
	// recovered signer matches the registered address.
	digest := make([]byte, 32)
	for i := range digest {
		digest[i] = byte(i)
	}
	a, err := att.SignDigest(digest)
	if err != nil {
		t.Fatalf("SignDigest: %v", err)
	}
	b, err := back.SignDigest(digest)
	if err != nil {
		t.Fatalf("SignDigest (reloaded): %v", err)
	}
	if string(a) != string(b) {
		t.Fatal("the reloaded attestor signs differently from the original")
	}
}

func TestTheWrongPassphraseUnlocksNothing(t *testing.T) {
	ks, _, err := NewAttestorKeystore("right")
	if err != nil {
		t.Fatalf("NewAttestorKeystore: %v", err)
	}
	path := writeKS(t, ks)
	if _, err := LoadAttestorKeystore(path, "wrong"); !errors.Is(err, token.ErrWrongPassphrase) {
		t.Fatalf("err = %v, want ErrWrongPassphrase", err)
	}
}

// TestAnAccountKeystoreCannotBeUnlockedAsAnAttestor is the guard the KeyType
// field exists for. Both secrets are 32 bytes, so without it a wallet file would
// decrypt here and become a live minting key derived from someone's seed -
// silently, with everything appearing to work.
func TestAnAccountKeystoreCannotBeUnlockedAsAnAttestor(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	ks, err := token.EncryptKeystore(acct, "pass", false)
	if err != nil {
		t.Fatalf("EncryptKeystore: %v", err)
	}
	path := writeKS(t, ks)

	_, err = LoadAttestorKeystore(path, "pass")
	if err == nil {
		t.Fatal("a wallet keystore was unlocked as an attestor, turning an account seed into " +
			"a minting key")
	}
	if !strings.Contains(err.Error(), "ed25519") {
		t.Fatalf("err = %v, want it to name the type mismatch", err)
	}
}

// TestAnAttestorKeystoreCannotBeUnlockedAsAnAccount is the same guard in the
// other direction.
func TestAnAttestorKeystoreCannotBeUnlockedAsAnAccount(t *testing.T) {
	ks, _, err := NewAttestorKeystore("pass")
	if err != nil {
		t.Fatalf("NewAttestorKeystore: %v", err)
	}
	if _, err := token.DecryptKeystore(ks, "pass"); err == nil {
		t.Fatal("an attestor keystore was unlocked as an account key")
	}
}

// TestTheLabelCannotLieAboutWhichAttestorThisIs. The on-chain attestor set is
// matched by ADDRESS, so a file whose label disagrees with its key makes every
// signature this node produces rejected on-chain for no visible reason.
func TestTheLabelCannotLieAboutWhichAttestorThisIs(t *testing.T) {
	ks, _, err := NewAttestorKeystore("pass")
	if err != nil {
		t.Fatalf("NewAttestorKeystore: %v", err)
	}
	ks.PublicKey = "0x000000000000000000000000000000000000dead"
	path := writeKS(t, ks)

	if _, err := LoadAttestorKeystore(path, "pass"); err == nil {
		t.Fatal("a keystore advertising one attestor and unlocking another was accepted")
	}
}

// TestTheCiphertextIsBoundToTheFilesId. The id is authenticated additional data,
// so moving a ciphertext into a file that claims a different attestor must fail
// rather than produce a working key under a false label.
func TestTheCiphertextIsBoundToTheFilesId(t *testing.T) {
	ks, _, err := NewAttestorKeystore("pass")
	if err != nil {
		t.Fatalf("NewAttestorKeystore: %v", err)
	}
	ks.AccountID = "attestor:0x000000000000000000000000000000000000beef"
	if _, err := token.DecryptSecretKeystore(ks, token.KeyTypeSecp256k1, "pass"); !errors.Is(err, token.ErrWrongPassphrase) {
		t.Fatalf("err = %v; a ciphertext moved under a different label decrypted anyway", err)
	}
}

func TestAnAttestorKeystoreRefusesAnEmptyPassphrase(t *testing.T) {
	if _, _, err := NewAttestorKeystore(""); err == nil {
		t.Fatal("an empty passphrase produced a keystore, which is a plaintext key with " +
			"extra steps")
	}
}
