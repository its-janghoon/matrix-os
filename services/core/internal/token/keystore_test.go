package token

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// TestSlip10Ed25519OfficialVectors checks the derivation against SLIP-0010's own
// published test vectors, not against my reading of the spec.
//
// This matters more than it looks. A derivation that is subtly wrong still
// produces valid keys and a working wallet, so nothing fails - until someone
// restores their phrase in another tool and finds a different, empty account,
// concludes the funds are gone, and is right to.
//
// Vectors: https://github.com/satoshilabs/slips/blob/master/slip-0010.md
func TestSlip10Ed25519OfficialVectors(t *testing.T) {
	// Transcribed from the spec's "Test vector 1/2 for ed25519" sections, private
	// keys only: the private key IS the 32-byte ed25519 seed at each level, which
	// is the value this implementation has to reproduce.
	const seed1 = "000102030405060708090a0b0c0d0e0f"
	const seed2 = "fffcf9f6f3f0edeae7e4e1dedbd8d5d2cfccc9c6c3c0bdbab7b4b1aeaba8a5a2" +
		"9f9c999693908d8a8784817e7b7875726f6c696663605d5a5754514e4b484542"

	tests := []struct {
		name string
		seed string
		path []uint32
		want string
	}{
		{"v1 m", seed1, nil,
			"2b4be7f19ee27bbf30c667b642d5f4aa69fd169872f8fc3059c08ebae2eb19e7"},
		{"v1 m/0'", seed1, []uint32{0},
			"68e0fe46dfb67e368c75379acec591dad19df3cde26e63b93a8e704f1dade7a3"},
		{"v1 m/0'/1'", seed1, []uint32{0, 1},
			"b1d0bad404bf35da785a64ca1ac54b2617211d2777696fbffaf208f746ae84f2"},
		{"v1 m/0'/1'/2'", seed1, []uint32{0, 1, 2},
			"92a5b23c0b8a99e37d07df3fb9966917f5d06e02ddbd909c7e184371463e9fc9"},
		{"v1 m/0'/1'/2'/2'", seed1, []uint32{0, 1, 2, 2},
			"30d1dc7e5fc04c31219ab25a27ae00b50f6fd66622f6e9c913253d6511d1e662"},
		{"v1 m/0'/1'/2'/2'/1000000000'", seed1, []uint32{0, 1, 2, 2, 1000000000},
			"8f94d394a8e8fd6b1bc2f3f49f5c47e385281d5c17e65324b0f62483e37e8793"},

		{"v2 m", seed2, nil,
			"171cb88b1b3c1db25add599712e36245d75bc65a1a5c9e18d76f9f2b1eab4012"},
		{"v2 m/0'", seed2, []uint32{0},
			"1559eb2bbec5790b0c65d8693e4d0875b1747f4970ae8b650486ed7470845635"},
		{"v2 m/0'/2147483647'", seed2, []uint32{0, 2147483647},
			"ea4f5bfe8694d8bb74b7b59404632fd5968b774ed545e810de9c32a4fb4192f4"},
		{"v2 m/0'/2147483647'/1'", seed2, []uint32{0, 2147483647, 1},
			"3757c7577170179c7868353ada796c839135b3d30554bbb74a4b1e4a5a58505c"},
		{"v2 m/0'/2147483647'/1'/2147483646'", seed2, []uint32{0, 2147483647, 1, 2147483646},
			"5837736c89570de861ebc173b1086da4f505d4adb387c6a1b1342d5e4ac9ec72"},
		{"v2 m/0'/2147483647'/1'/2147483646'/2'", seed2, []uint32{0, 2147483647, 1, 2147483646, 2},
			"551d333177df541ad876a60ea71f00447931c0a9da16f227c11ea080d7391b8d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seed, err := hex.DecodeString(tt.seed)
			if err != nil {
				t.Fatalf("bad seed hex: %v", err)
			}
			acct, err := accountFromSeed(seed, tt.path)
			if err != nil {
				t.Fatalf("accountFromSeed: %v", err)
			}
			got := hex.EncodeToString(acct.PrivateKey.Seed())
			if got != tt.want {
				t.Fatalf("derived %s, want %s", got, tt.want)
			}
		})
	}
}

func TestAMnemonicRoundTripsToTheSameAccount(t *testing.T) {
	phrase, err := NewMnemonic()
	if err != nil {
		t.Fatalf("NewMnemonic: %v", err)
	}
	if words := len(strings.Fields(phrase)); words != MnemonicWords {
		t.Fatalf("phrase has %d words, want %d", words, MnemonicWords)
	}

	first, err := AccountFromMnemonic(phrase, "")
	if err != nil {
		t.Fatalf("AccountFromMnemonic: %v", err)
	}
	again, err := AccountFromMnemonic(phrase, "")
	if err != nil {
		t.Fatalf("AccountFromMnemonic: %v", err)
	}

	// The whole promise of a recovery phrase.
	if first.AccountID() != again.AccountID() {
		t.Fatalf("the same phrase gave %s then %s", first.AccountID(), again.AccountID())
	}
}

// TestTheChecksumCatchesAMistypedWord is why validation exists at all: without
// it a wrong word derives a different, empty account and the user concludes
// their funds are gone.
func TestTheChecksumCatchesAMistypedWord(t *testing.T) {
	const valid = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	if err := ValidateMnemonic(valid); err != nil {
		t.Fatalf("known BIP-39 vector is invalid: %v", err)
	}
	words := strings.Fields(valid)
	// Replace the first word with another real BIP-39 word. For this fixed
	// vector, "zoo" changes the entropy while the final checksum bits remain
	// unchanged, so the phrase is deterministically invalid. Generating a random
	// phrase here made the test flaky because any one-word replacement has a
	// 1-in-16 chance of accidentally producing another valid 12-word checksum.
	words[0] = "zoo"
	mistyped := strings.Join(words, " ")

	if err := ValidateMnemonic(mistyped); !errors.Is(err, ErrInvalidMnemonic) {
		t.Fatalf("err = %v, want ErrInvalidMnemonic for a mistyped phrase", err)
	}
	if _, err := AccountFromMnemonic(mistyped, ""); !errors.Is(err, ErrInvalidMnemonic) {
		t.Fatalf("deriving from a mistyped phrase gave %v, want a refusal", err)
	}
}

// TestABip39PassphraseChangesTheAccount: the optional 25th word is a different
// wallet, not a password on the same one, and a user who sets one by accident
// must not silently get a different account back with no explanation.
func TestABip39PassphraseChangesTheAccount(t *testing.T) {
	phrase, err := NewMnemonic()
	if err != nil {
		t.Fatalf("NewMnemonic: %v", err)
	}
	plain, _ := AccountFromMnemonic(phrase, "")
	withExtra, _ := AccountFromMnemonic(phrase, "an extra word")
	if plain.AccountID() == withExtra.AccountID() {
		t.Fatal("a BIP-39 passphrase must derive a different account")
	}
}

func TestAKeystoreRoundTrips(t *testing.T) {
	acct, err := GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}

	ks, err := EncryptKeystore(acct, "correct horse battery staple", false)
	if err != nil {
		t.Fatalf("EncryptKeystore: %v", err)
	}
	data, err := MarshalKeystore(ks)
	if err != nil {
		t.Fatalf("MarshalKeystore: %v", err)
	}

	// The secret must not be in the file in any readable form. This is the whole
	// difference from the plaintext wallet this replaces.
	if strings.Contains(string(data), hex.EncodeToString(acct.PrivateKey.Seed())) {
		t.Fatal("the seed appears in the keystore in the clear")
	}
	if strings.Contains(string(data), hex.EncodeToString(acct.PrivateKey)) {
		t.Fatal("the private key appears in the keystore in the clear")
	}
	// The public half is deliberately readable: an operator has to see which
	// account a file holds without unlocking it.
	if !strings.Contains(string(data), acct.AccountID()) {
		t.Fatal("the account id should be plaintext in the keystore")
	}

	parsed, ok := UnmarshalKeystore(data)
	if !ok {
		t.Fatal("a keystore we just wrote is not recognised as one")
	}
	back, err := DecryptKeystore(parsed, "correct horse battery staple")
	if err != nil {
		t.Fatalf("DecryptKeystore: %v", err)
	}
	if back.AccountID() != acct.AccountID() {
		t.Fatalf("recovered %s, want %s", back.AccountID(), acct.AccountID())
	}
	if !back.PrivateKey.Equal(acct.PrivateKey) {
		t.Fatal("the recovered private key differs")
	}
}

func TestTheWrongPassphraseIsRefused(t *testing.T) {
	acct, _ := GenerateAccount()
	ks, err := EncryptKeystore(acct, "right", false)
	if err != nil {
		t.Fatalf("EncryptKeystore: %v", err)
	}
	if _, err := DecryptKeystore(ks, "wrong"); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("err = %v, want ErrWrongPassphrase", err)
	}
}

// TestAnEmptyPassphraseIsRefused: a keystore whose passphrase is "" is a
// plaintext key with extra steps, and worse than an honest plaintext file
// because it invites misplaced confidence.
func TestAnEmptyPassphraseIsRefused(t *testing.T) {
	acct, _ := GenerateAccount()
	if _, err := EncryptKeystore(acct, "", false); err == nil {
		t.Fatal("want a refusal for an empty passphrase")
	}
}

// TestTheAccountIDIsAuthenticated: it is plaintext, so it must not be able to
// lie. A ciphertext moved into a keystore claiming a different account has to
// fail rather than unlock the wrong one.
func TestTheAccountIDIsAuthenticated(t *testing.T) {
	acct, _ := GenerateAccount()
	other, _ := GenerateAccount()

	ks, err := EncryptKeystore(acct, "pass", false)
	if err != nil {
		t.Fatalf("EncryptKeystore: %v", err)
	}
	ks.AccountID = other.AccountID()

	if _, err := DecryptKeystore(ks, "pass"); err == nil {
		t.Fatal("a keystore claiming a different account must not decrypt")
	}
}

func TestTamperingWithTheCiphertextIsCaught(t *testing.T) {
	acct, _ := GenerateAccount()
	ks, err := EncryptKeystore(acct, "pass", false)
	if err != nil {
		t.Fatalf("EncryptKeystore: %v", err)
	}
	raw, err := hex.DecodeString(ks.Ciphertext)
	if err != nil {
		t.Fatalf("bad ciphertext hex: %v", err)
	}
	raw[0] ^= 0xff
	ks.Ciphertext = hex.EncodeToString(raw)

	if _, err := DecryptKeystore(ks, "pass"); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("err = %v, want the tamper caught", err)
	}
}

// TestAKeystoreUnlocksWithTheParametersItWasWrittenWith: raising the cost later
// must not lock anyone out of an older file.
func TestAKeystoreUnlocksWithTheParametersItWasWrittenWith(t *testing.T) {
	acct, _ := GenerateAccount()
	ks, err := EncryptKeystore(acct, "pass", false)
	if err != nil {
		t.Fatalf("EncryptKeystore: %v", err)
	}
	// Pretend the file was written by an older, cheaper build. The recorded
	// parameters are what must be used, not this build's constants.
	cheap, err := reencryptWith(acct, "pass", scryptParams{N: 1 << 14, R: 8, P: 1})
	if err != nil {
		t.Fatalf("reencryptWith: %v", err)
	}
	if cheap.KDFParams.N == ks.KDFParams.N {
		t.Fatal("the test did not actually change the parameters")
	}
	if _, err := DecryptKeystore(cheap, "pass"); err != nil {
		t.Fatalf("a file written with cheaper parameters should still unlock: %v", err)
	}
}

// TestUnmarshalDistinguishesAKeystoreFromTheLegacyWallet: the CLI has to be able
// to read both, and mistaking one for the other is a confusing failure at the
// worst moment.
func TestUnmarshalDistinguishesAKeystoreFromTheLegacyWallet(t *testing.T) {
	legacy := []byte(`{"public_key":"aa","private_key":"bb"}`)
	if _, ok := UnmarshalKeystore(legacy); ok {
		t.Fatal("a legacy plaintext wallet must not be read as a keystore")
	}

	acct, _ := GenerateAccount()
	ks, _ := EncryptKeystore(acct, "pass", false)
	data, _ := MarshalKeystore(ks)
	if _, ok := UnmarshalKeystore(data); !ok {
		t.Fatal("a keystore must be recognised as one")
	}
}

func TestADerivedAccountSignsAndVerifies(t *testing.T) {
	phrase, _ := NewMnemonic()
	acct, err := AccountFromMnemonic(phrase, "")
	if err != nil {
		t.Fatalf("AccountFromMnemonic: %v", err)
	}
	if len(acct.PrivateKey) != ed25519.PrivateKeySize {
		t.Fatalf("private key is %d bytes, want %d", len(acct.PrivateKey), ed25519.PrivateKeySize)
	}

	tx := &Transaction{From: acct.PublicKey, To: "somebody", Amount: 1, Nonce: 0}
	if err := tx.Sign(acct.PrivateKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := tx.Verify(); err != nil {
		t.Fatalf("a derived account's signature does not verify: %v", err)
	}
}

// reencryptWith writes a keystore with explicit KDF parameters, standing in for
// a file produced by an older build.
func reencryptWith(acct *Account, passphrase string, params scryptParams) (*Keystore, error) {
	ks, err := encryptKeystoreWith(acct, passphrase, params, false)
	if err != nil {
		return nil, err
	}
	return ks, nil
}
