package bridge

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
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

// keccak256 returns the Ethereum keccak256 (NOT the FIPS SHA3-256) hash of the
// concatenated inputs. sha3.NewLegacyKeccak256 uses the original Keccak padding
// that Ethereum's abi/ecrecover stack relies on.
func keccak256(parts ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
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
	var zero Address
	if len(digest) != 32 {
		return zero, fmt.Errorf("%w: digest must be 32 bytes", ErrInvalidSignature)
	}
	if len(sig) != 65 {
		return zero, fmt.Errorf("%w: signature must be 65 bytes, got %d", ErrInvalidSignature, len(sig))
	}
	v := sig[64]
	if v < 27 {
		// Accept both the {0,1} and {27,28} conventions for v.
		v += 27
	}
	// dcrd RecoverCompact wants <v><r><s> with v as produced by SignCompact.
	compact := make([]byte, 65)
	compact[0] = v
	copy(compact[1:33], sig[0:32])
	copy(compact[33:65], sig[32:64])
	pub, _, err := ecdsa.RecoverCompact(compact, digest)
	if err != nil {
		return zero, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	return addressFromPubKey(pub), nil
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
