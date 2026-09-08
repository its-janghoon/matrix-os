package bridge

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Errors related to attestation signing and verification.
var (
	// ErrInvalidAddress is returned when a byte slice or hex string is not a
	// valid 20-byte Ethereum address.
	ErrInvalidAddress = errors.New("bridge: invalid ethereum address")
	// ErrInvalidSignature is returned when an attestation signature is malformed
	// or does not recover to a registered attestor.
	ErrInvalidSignature = errors.New("bridge: invalid attestation signature")
	// ErrInvalidPrivateKey is returned when a secp256k1 private key cannot be
	// parsed.
	ErrInvalidPrivateKey = errors.New("bridge: invalid secp256k1 private key")
)

// AddressLen is the length in bytes of an Ethereum address.
const AddressLen = 20

// Address is a 20-byte Ethereum address (the keccak256-derived identity of a
// secp256k1 attestor key), used to match Go-produced signatures against the
// attestor set registered in the Solidity contract.
type Address [AddressLen]byte

// Hex returns the 0x-prefixed lowercase hex encoding of the address.
func (a Address) Hex() string {
	return "0x" + hex.EncodeToString(a[:])
}

// String implements fmt.Stringer.
func (a Address) String() string { return a.Hex() }

// ParseAddress parses a 0x-optional hex string into an Address.
func ParseAddress(s string) (Address, error) {
	var addr Address
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		s = s[2:]
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return addr, fmt.Errorf("%w: %v", ErrInvalidAddress, err)
	}
	if len(b) != AddressLen {
		return addr, fmt.Errorf("%w: expected %d bytes, got %d", ErrInvalidAddress, AddressLen, len(b))
	}
	copy(addr[:], b)
	return addr, nil
}

// keccak256 delegates to internal/ethsig, which owns the one implementation.
// internal/token needs the same primitive and cannot import this package
// (bridge imports token), so a second copy of it would have lived there.
func keccak256(parts ...[]byte) []byte {
	return ethsig.Keccak256(parts...)
}

// Attestor is a validator's secp256k1 signing identity for the Ethereum side of
// the bridge. It is deliberately distinct from the validator's ed25519 consensus
// account because the EVM can only verify secp256k1/ECDSA (ecrecover), not
// ed25519. The Address is what the Solidity contract registers and matches
// recovered signatures against.
type Attestor struct {
	priv *secp256k1.PrivateKey
	addr Address
}

// NewAttestorFromBytes builds an Attestor from a 32-byte secp256k1 private key.
// The private key material is for LOCAL TEST/OPERATOR keys supplied via env or a
// keystore; never hardcode a real key.
func NewAttestorFromBytes(priv []byte) (*Attestor, error) {
	if len(priv) != 32 {
		return nil, fmt.Errorf("%w: expected 32 bytes, got %d", ErrInvalidPrivateKey, len(priv))
	}
	// SetBytes rejects zero and values >= N by clamping into range; guard the
	// zero key explicitly since it is not a valid signing key.
	if isZero(priv) {
		return nil, fmt.Errorf("%w: zero key", ErrInvalidPrivateKey)
	}
	sk := secp256k1.PrivKeyFromBytes(priv)
	addr := addressFromPubKey(sk.PubKey())
	return &Attestor{priv: sk, addr: addr}, nil
}

// NewAttestorFromHex builds an Attestor from a 0x-optional hex-encoded 32-byte
// secp256k1 private key.
func NewAttestorFromHex(s string) (*Attestor, error) {
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		s = s[2:]
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPrivateKey, err)
	}
	return NewAttestorFromBytes(b)
}

func isZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// Address returns the Ethereum address of this attestor.
func (a *Attestor) Address() Address { return a.addr }

// SignDigest signs a 32-byte digest with the attestor's secp256k1 key and
// returns a 65-byte Ethereum-style signature laid out as r(32) || s(32) || v(1)
// with v in {27,28}. The signature is verifiable by Solidity's ecrecover on the
// same digest.
//
// The dcrd SignCompact routine returns <recoveryCode+27><r><s> with a canonical
// (low-S) s, which is exactly what EIP-2/ecrecover requires; we only reorder the
// components to Ethereum's {r,s,v} layout.
func (a *Attestor) SignDigest(digest []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, fmt.Errorf("%w: digest must be 32 bytes, got %d", ErrInvalidSignature, len(digest))
	}
	compact := ecdsa.SignCompact(a.priv, digest, false) // uncompressed => v in {27,28}
	// compact = [v || r(32) || s(32)]. Reorder to [r || s || v].
	sig := make([]byte, 65)
	copy(sig[0:32], compact[1:33])
	copy(sig[32:64], compact[33:65])
	sig[64] = compact[0]
	return sig, nil
}

// addressFromPubKey derives the 20-byte Ethereum address of a secp256k1 public
// key: the low 20 bytes of keccak256 of the 64-byte uncompressed public key
// (without the 0x04 prefix), matching how Ethereum derives addresses.
func addressFromPubKey(pub *secp256k1.PublicKey) Address {
	uncompressed := pub.SerializeUncompressed() // 65 bytes: 0x04 || X(32) || Y(32)
	hash := keccak256(uncompressed[1:])
	var addr Address
	copy(addr[:], hash[12:])
	return addr
}

// RecoverAddress recovers the Ethereum address that produced sig over digest.
// sig is the 65-byte r || s || v layout returned by SignDigest. It returns
// ErrInvalidSignature when the signature is malformed or does not recover.
func RecoverAddress(digest, sig []byte) (Address, error) {
	got, err := ethsig.RecoverAddress(digest, sig)
	if err != nil {
		// Keep this package's own error identity, which its callers match on.
		return Address{}, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	return Address(got), nil
}

// leftPad32 returns b left-padded with zero bytes to 32 bytes. It panics if b is
// longer than 32 bytes; callers pass values known to fit.
func leftPad32(b []byte) []byte {
	if len(b) > 32 {
		panic("bridge: value exceeds 32 bytes")
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

// bigTo32 encodes a non-negative *big.Int as a 32-byte big-endian value,
// matching Solidity's abi.encodePacked(uint256).
func bigTo32(v *big.Int) []byte {
	return leftPad32(v.Bytes())
}

// AttestorKeystoreID is the plaintext label an attestor keystore carries, given
// its Ethereum address. It is the file's authenticated additional data, so a
// ciphertext cannot be moved into a file advertising a different attestor.
func AttestorKeystoreID(addr Address) string {
	return "attestor:" + strings.ToLower(hexAddress(addr))
}

// NewAttestorKeystore encrypts a fresh secp256k1 attestor key under a
// passphrase, returning the keystore and the address it attests as.
//
// It uses the account keystore's construction - scrypt, AES-256-GCM, the file
// carrying its own KDF parameters - rather than anything invented here. A
// validator's attestor key is unilateral authority to mint wrapped tokens
// against escrow, so it deserves at least what a wallet key gets, and a second
// bespoke format would be a second thing to get right.
func NewAttestorKeystore(passphrase string) (*token.Keystore, *Attestor, error) {
	if passphrase == "" {
		return nil, nil, errors.New("bridge: a passphrase is required for an attestor keystore")
	}
	secret := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, secret); err != nil {
		return nil, nil, fmt.Errorf("bridge: read attestor key: %w", err)
	}
	att, err := NewAttestorFromBytes(secret)
	if err != nil {
		return nil, nil, err
	}
	addr := att.Address()
	ks, err := token.EncryptSecretKeystore(
		secret, token.KeyTypeSecp256k1, AttestorKeystoreID(addr), hexAddress(addr), passphrase)
	if err != nil {
		return nil, nil, err
	}
	return ks, att, nil
}

// LoadAttestorKeystore reads and unlocks an attestor keystore from disk.
//
// It checks the recovered key against the ADDRESS the file advertises. That is
// not redundant with the authenticated decryption: the id is authenticated, but
// a file that decrypted to a key for a different address would be a file whose
// plaintext label lies, and an operator reading the label to decide which
// attestor a node is would be reading the wrong thing. The registered attestor
// set on-chain is matched by address, so a mismatch here means every signature
// this node produces is rejected on-chain for no visible reason.
func LoadAttestorKeystore(path, passphrase string) (*Attestor, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("bridge: read attestor keystore: %w", err)
	}
	ks, ok := token.UnmarshalKeystore(data)
	if !ok {
		return nil, fmt.Errorf("bridge: %s is not a keystore file", path)
	}
	secret, err := token.DecryptSecretKeystore(ks, token.KeyTypeSecp256k1, passphrase)
	if err != nil {
		return nil, err
	}
	att, err := NewAttestorFromBytes(secret)
	if err != nil {
		return nil, err
	}
	if want := strings.ToLower(ks.PublicKey); want != "" && want != strings.ToLower(hexAddress(att.Address())) {
		return nil, fmt.Errorf("bridge: attestor keystore advertises %s but unlocks %s; the "+
			"file's label does not match its key", ks.PublicKey, hexAddress(att.Address()))
	}
	return att, nil
}

// hexAddress renders an address as lowercase 0x hex.
func hexAddress(a Address) string {
	return "0x" + hex.EncodeToString(a[:])
}

// AddressHex renders this attestor's Ethereum address as lowercase 0x hex. It is
// what goes in the contract's registered attestor set, so it is worth printing
// rather than making an operator derive it.
func (a *Attestor) AddressHex() string { return hexAddress(a.addr) }
