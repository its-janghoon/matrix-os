package token

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	bip39 "github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/scrypt"
)

// This file gives an account a RECOVERY PHRASE and gives a wallet file
// ENCRYPTION AT REST. Both were missing, and their absence was the sharpest
// thing about holding value on this chain.
//
// A wallet was `{"public_key": "<hex>", "private_key": "<hex>"}` in plaintext at
// mode 0600. Anyone who could read that file owned the account: a stray backup,
// a container image layer, a shared dev box, a `cat` in a screen share. And
// there was no derivation of any kind, so the only copy of a key was that file -
// lose it and the account is gone, with no phrase to write down and no way to
// restore it anywhere else.
//
// Two standards rather than something of our own, because the point is that a
// phrase written on paper still works in five years and in other software:
//
//	BIP-39      the 12/24-word phrase and its seed
//	SLIP-0010   deriving an ed25519 key from that seed
//
// SLIP-0010 is what Solana, Near, Aptos and Sui use for ed25519, so a phrase
// from here is a phrase those tools understand at the same derivation path. The
// alternative - hashing a phrase ourselves - would work exactly once, in our own
// software, forever.

var (
	// ErrWrongPassphrase is returned when a keystore cannot be decrypted. It is
	// deliberately indistinguishable from a corrupted file: AES-GCM cannot tell
	// them apart, and pretending otherwise would be inventing information.
	ErrWrongPassphrase = errors.New("token: wrong passphrase, or the keystore is corrupt")
	// ErrInvalidMnemonic is returned for a phrase that fails its checksum, which
	// is how BIP-39 catches a mistyped or misremembered word.
	ErrInvalidMnemonic = errors.New("token: not a valid recovery phrase")
	// ErrUnsupportedKeystore is returned for a keystore this build cannot read.
	ErrUnsupportedKeystore = errors.New("token: unsupported keystore")
)

// DefaultDerivationPath is the SLIP-0010 path an account is derived at:
// m/44'/9004'/0'/0'. 44' is BIP-44, 9004 is the coin type, and every level is
// hardened because SLIP-0010 ed25519 supports only hardened derivation.
//
// It is fixed rather than configurable. A path is part of the recovery
// procedure, and a phrase that restores nothing because it was derived at a path
// nobody recorded is the same as a lost phrase.
var DefaultDerivationPath = []uint32{44, 9004, 0, 0}

// MnemonicWords is how many words a generated phrase has. Twelve words is 128
// bits of entropy, which is the standard trade-off: a 24-word phrase adds
// strength nothing else in this system relies on, and costs the user twice the
// transcription risk.
const MnemonicWords = 12

// keystore scrypt parameters. N is the cost, and 2^17 is chosen so a single
// guess takes long enough to make an offline dictionary attack on a human
// passphrase expensive, while a legitimate unlock stays under a second on
// ordinary hardware.
const (
	scryptN      = 1 << 17
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 32
	saltLen      = 32
)

// keystoreVersion is bumped when the format changes incompatibly, so an old
// build refuses a new file rather than misreading it.
const keystoreVersion = 1

// Keystore is the on-disk encrypted wallet. The public key and account id are
// PLAINTEXT on purpose: an operator has to be able to see which account a file
// holds without unlocking it, and neither is a secret.
type Keystore struct {
	Version   int    `json:"version"`
	AccountID string `json:"account_id"`
	PublicKey string `json:"public_key"`
	// Cipher names the construction, so a future format change is legible in the
	// file rather than implied by its version alone.
	Cipher string `json:"cipher"`
	KDF    string `json:"kdf"`
	// KDFParams records the cost parameters the file was written with, so raising
	// them later does not lock anyone out of an older file.
	KDFParams scryptParams `json:"kdf_params"`
	Salt      string       `json:"salt"`
	Nonce     string       `json:"nonce"`
	// Ciphertext is the encrypted secret: the 32-byte ed25519 seed, not the
	// expanded 64-byte private key. The seed is what a phrase reproduces, and
	// storing the smaller thing keeps one source of truth.
	Ciphertext string `json:"ciphertext"`
	// MnemonicBackedUp records whether this key came from a phrase the user was
	// shown. It is not a secret and it is not a security control; it exists so a
	// tool can warn about a key that has no recovery phrase at all.
	MnemonicBackedUp bool `json:"mnemonic_backed_up"`
}

type scryptParams struct {
	N int `json:"n"`
	R int `json:"r"`
	P int `json:"p"`
}

// NewMnemonic generates a fresh BIP-39 recovery phrase.
func NewMnemonic() (string, error) {
	entropy, err := bip39.NewEntropy(MnemonicWords * 32 / 3)
	if err != nil {
		return "", fmt.Errorf("token: generate entropy: %w", err)
	}
	phrase, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return "", fmt.Errorf("token: generate mnemonic: %w", err)
	}
	return phrase, nil
}

// ValidateMnemonic reports whether a phrase is well-formed, including its
// checksum. The checksum is the whole reason to validate rather than just derive:
// a mistyped word otherwise silently produces a different, empty account, and
// the user concludes their funds are gone.
func ValidateMnemonic(phrase string) error {
	if !bip39.IsMnemonicValid(phrase) {
		return ErrInvalidMnemonic
	}
	return nil
}

// AccountFromMnemonic derives the account at DefaultDerivationPath. The
// passphrase is BIP-39's optional 25th word, not the keystore passphrase; empty
// is the ordinary case.
func AccountFromMnemonic(phrase, bip39Passphrase string) (*Account, error) {
	if err := ValidateMnemonic(phrase); err != nil {
		return nil, err
	}
	seed := bip39.NewSeed(phrase, bip39Passphrase)
	return accountFromSeed(seed, DefaultDerivationPath)
}

// slip10Ed25519Curve is SLIP-0010's HMAC key for the ed25519 curve. The string
// is part of the standard.
var slip10Ed25519Curve = []byte("ed25519 seed")

// accountFromSeed walks the SLIP-0010 hardened path from a BIP-39 seed.
func accountFromSeed(seed []byte, path []uint32) (*Account, error) {
	mac := hmac.New(sha512.New, slip10Ed25519Curve)
	mac.Write(seed)
	sum := mac.Sum(nil)
	key, chain := sum[:32], sum[32:]

	for _, index := range path {
		// SLIP-0010 ed25519 has no public derivation, so every index is hardened.
		// The 0x00 prefix distinguishes the hardened data layout.
		data := make([]byte, 0, 1+32+4)
		data = append(data, 0x00)
		data = append(data, key...)
		data = append(data, byte(0x80|((index>>24)&0x7f)), byte(index>>16), byte(index>>8), byte(index))

		mac = hmac.New(sha512.New, chain)
		mac.Write(data)
		sum = mac.Sum(nil)
		key, chain = sum[:32], sum[32:]
	}

	priv := ed25519.NewKeyFromSeed(key)
	return &Account{PublicKey: priv.Public().(ed25519.PublicKey), PrivateKey: priv}, nil
}

// EncryptKeystore encrypts an account under a passphrase.
//
// It refuses an empty passphrase rather than writing a file that only looks
// encrypted. A keystore whose passphrase is "" is a plaintext key with extra
// steps, which is worse than an honest plaintext file because it invites
// misplaced confidence.
func EncryptKeystore(acct *Account, passphrase string, fromMnemonic bool) (*Keystore, error) {
	return encryptKeystoreWith(acct, passphrase,
		scryptParams{N: scryptN, R: scryptR, P: scryptP}, fromMnemonic)
}

// encryptKeystoreWith takes the KDF parameters explicitly. EncryptKeystore
// supplies this build's, and a test can supply an older build's to prove a file
// still unlocks after the cost is raised.
func encryptKeystoreWith(acct *Account, passphrase string, params scryptParams, fromMnemonic bool) (*Keystore, error) {
	if acct == nil || len(acct.PrivateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("token: an account with a private key is required")
	}
	if passphrase == "" {
		return nil, errors.New("token: a passphrase is required; an empty one would not encrypt anything")
	}

	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("token: read salt: %w", err)
	}
	key, err := scrypt.Key([]byte(passphrase), salt, params.N, params.R, params.P, scryptKeyLen)
	if err != nil {
		return nil, fmt.Errorf("token: derive key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("token: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("token: new gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("token: read nonce: %w", err)
	}

	// The ACCOUNT ID is authenticated additional data, so a ciphertext cannot be
	// moved into a keystore claiming a different account: the plaintext id is
	// what a caller reads before unlocking, and it must not be able to lie.
	seed := acct.PrivateKey.Seed()
	ciphertext := gcm.Seal(nil, nonce, seed, []byte(acct.AccountID()))

	return &Keystore{
		Version:          keystoreVersion,
		AccountID:        acct.AccountID(),
		PublicKey:        hex.EncodeToString(acct.PublicKey),
		Cipher:           "aes-256-gcm",
		KDF:              "scrypt",
		KDFParams:        params,
		Salt:             hex.EncodeToString(salt),
		Nonce:            hex.EncodeToString(nonce),
		Ciphertext:       hex.EncodeToString(ciphertext),
		MnemonicBackedUp: fromMnemonic,
	}, nil
}

// DecryptKeystore recovers the account, verifying that the derived public key
// matches the one the file advertises.
func DecryptKeystore(ks *Keystore, passphrase string) (*Account, error) {
	if ks == nil {
		return nil, errors.New("token: no keystore supplied")
	}
	if ks.Version != keystoreVersion {
		return nil, fmt.Errorf("%w: version %d, this build reads %d",
			ErrUnsupportedKeystore, ks.Version, keystoreVersion)
	}
	if ks.KDF != "scrypt" || ks.Cipher != "aes-256-gcm" {
		return nil, fmt.Errorf("%w: kdf %q with cipher %q", ErrUnsupportedKeystore, ks.KDF, ks.Cipher)
	}

	salt, err := hex.DecodeString(ks.Salt)
	if err != nil {
		return nil, fmt.Errorf("%w: salt is not hex", ErrUnsupportedKeystore)
	}
	nonce, err := hex.DecodeString(ks.Nonce)
	if err != nil {
		return nil, fmt.Errorf("%w: nonce is not hex", ErrUnsupportedKeystore)
	}
	ciphertext, err := hex.DecodeString(ks.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("%w: ciphertext is not hex", ErrUnsupportedKeystore)
	}

	// The file's own parameters, not this build's, so raising the cost later does
	// not lock anyone out of a file written before the change.
	params := ks.KDFParams
	if params.N == 0 || params.R == 0 || params.P == 0 {
		return nil, fmt.Errorf("%w: kdf parameters are missing", ErrUnsupportedKeystore)
	}
	key, err := scrypt.Key([]byte(passphrase), salt, params.N, params.R, params.P, scryptKeyLen)
	if err != nil {
		return nil, fmt.Errorf("token: derive key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("token: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("token: new gcm: %w", err)
	}
	seed, err := gcm.Open(nil, nonce, ciphertext, []byte(ks.AccountID))
	if err != nil {
		return nil, ErrWrongPassphrase
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%w: decrypted secret is %d bytes, want %d",
			ErrUnsupportedKeystore, len(seed), ed25519.SeedSize)
	}

	priv := ed25519.NewKeyFromSeed(seed)
	acct := &Account{PublicKey: priv.Public().(ed25519.PublicKey), PrivateKey: priv}

	// The id is authenticated, so this cannot disagree unless the file is
	// self-inconsistent. Check it anyway: a file that says one account and
	// unlocks another would be a silent wrong-account signature.
	want, err := hex.DecodeString(ks.PublicKey)
	if err != nil || subtle.ConstantTimeCompare(want, acct.PublicKey) != 1 {
		return nil, fmt.Errorf("%w: the decrypted key does not match the advertised public key",
			ErrUnsupportedKeystore)
	}
	return acct, nil
}

// MarshalKeystore encodes a keystore for writing to disk.
func MarshalKeystore(ks *Keystore) ([]byte, error) {
	return json.MarshalIndent(ks, "", "  ")
}

// UnmarshalKeystore decodes a keystore file. It reports whether the bytes are a
// keystore at all, so a caller can fall back to reading the legacy plaintext
// wallet format rather than failing outright.
func UnmarshalKeystore(data []byte) (*Keystore, bool) {
	var ks Keystore
	if err := json.Unmarshal(data, &ks); err != nil {
		return nil, false
	}
	// The discriminator is the ciphertext: a legacy wallet file has none, and a
	// keystore is useless without one.
	if ks.Ciphertext == "" {
		return nil, false
	}
	return &ks, true
}
