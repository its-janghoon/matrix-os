// Package token implements a real cryptographic token settlement layer for the
// Matrix OS compute marketplace.
//
// Accounts are ed25519 keypairs. Value transfers are expressed as
// cryptographically-signed transactions (see Transaction) that a verifier
// authenticates before applying. Every applied transaction is recorded in an
// append-only, SHA-256 hash-chained transaction log (see Chain) persisted in
// the shared Pebble kv.Store, forming a minimal blockchain-style ledger of
// settlements. The SettledLedger type integrates the existing market.Ledger so
// native MATRIX moves only through verified, signed, chained transactions.
//
// This package also defines the native MATRIX monetary model: supply.go holds
// the single source of truth for the native base-unit scale and its exact
// conversion to the wrapped ERC-20, and genesis.go (Treasury) provides the
// honest issuance path (a one-time genesis allocation, a supply cap, and a
// genesis-funded reward pool) that replaces ad-hoc unbounded minting.
//
// This package deliberately depends only on the Go standard library
// (crypto/ed25519, crypto/sha256, encoding/hex, encoding/binary) so it adds no
// new module dependencies.
package token

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

// Errors related to account identity and key handling.
var (
	// ErrInvalidPublicKey is returned when a byte slice is not a valid ed25519
	// public key.
	ErrInvalidPublicKey = errors.New("token: invalid ed25519 public key")
	// ErrInvalidAccountID is returned when a string is not a valid account ID.
	ErrInvalidAccountID = errors.New("token: invalid account id")
)

// Account is an identity backed by an ed25519 keypair. The public key is the
// canonical identity material; AccountID derives a stable, human-readable
// string identifier from it. The private key is present only for accounts that
// can sign (i.e. those generated locally or imported with their secret key).
type Account struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

// GenerateAccount creates a fresh Account with a new ed25519 keypair using a
// cryptographically secure random source.
func GenerateAccount() (*Account, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("token: failed to generate account key: %w", err)
	}
	return &Account{PublicKey: pub, PrivateKey: priv}, nil
}

// AccountID returns the stable string identifier for this account, derived from
// its ed25519 public key. The derivation is lowercase hex of the 32-byte public
// key, which is deterministic and collision-resistant for distinct keys.
func (a *Account) AccountID() string {
	return AccountIDFromPublicKey(a.PublicKey)
}

// AccountIDFromPublicKey derives the stable account identifier for a public key.
// It is defined independently of Account so verifiers can compute the ID of a
// transaction sender directly from the key embedded in the transaction.
func AccountIDFromPublicKey(pub ed25519.PublicKey) string {
	return hex.EncodeToString(pub)
}

// MarshalPublicKey returns the raw 32-byte encoding of an ed25519 public key.
func MarshalPublicKey(pub ed25519.PublicKey) []byte {
	out := make([]byte, len(pub))
	copy(out, pub)
	return out
}

// ParsePublicKey validates and copies raw public-key bytes into an
// ed25519.PublicKey. It returns ErrInvalidPublicKey when the length is wrong.
func ParsePublicKey(data []byte) (ed25519.PublicKey, error) {
	if len(data) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: expected %d bytes, got %d", ErrInvalidPublicKey, ed25519.PublicKeySize, len(data))
	}
	pub := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(pub, data)
	return pub, nil
}

// ParsePublicKeyHex parses a hex-encoded account ID back into an ed25519 public
// key, validating both the hex encoding and the resulting key length.
func ParsePublicKeyHex(id string) (ed25519.PublicKey, error) {
	data, err := hex.DecodeString(id)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAccountID, err)
	}
	return ParsePublicKey(data)
}
