// Package ethsig holds the Ethereum-side signature primitives: keccak256, an
// address, and recovering the address that produced a signature.
//
// It exists so there is ONE of each. internal/bridge needed them to verify
// validator attestations, and internal/token needs them to let an Ethereum key
// control a native account - and internal/bridge imports internal/token, so the
// alternative was a second copy of ecrecover living in token. Two
// implementations of a signature check is the same mistake as two copies of a
// byte layout, and it fails in the same way: silently, on the day they diverge.
package ethsig

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// ErrInvalidSignature is returned when a signature is malformed or does not
// recover to an address.
var ErrInvalidSignature = errors.New("ethsig: invalid signature")

// ErrInvalidAddress is returned for a malformed address.
var ErrInvalidAddress = errors.New("ethsig: invalid address")

// AddressLen is the length of an Ethereum address in bytes.
const AddressLen = 20

// SignatureLen is the length of an Ethereum {r,s,v} signature in bytes.
const SignatureLen = 65

// Address is a 20-byte Ethereum address.
type Address [AddressLen]byte

// Hex returns the 0x-prefixed lowercase hex form.
func (a Address) Hex() string { return "0x" + hex.EncodeToString(a[:]) }

// String returns Hex.
func (a Address) String() string { return a.Hex() }

// IsZero reports whether the address is all zero bytes, which is what a failed
// recovery yields and is never a real signer.
func (a Address) IsZero() bool {
	for _, b := range a {
		if b != 0 {
			return false
		}
	}
	return true
}

// ParseAddress parses a 0x-prefixed or bare 40-character hex address. It is
// case-insensitive and does not verify an EIP-55 checksum: a caller that has one
// should check it separately, and rejecting a lowercase address here would
// reject the form this package itself prints.
func ParseAddress(s string) (Address, error) {
	var out Address
	trimmed := strings.TrimPrefix(strings.TrimSpace(s), "0x")
	trimmed = strings.TrimPrefix(trimmed, "0X")
	if len(trimmed) != AddressLen*2 {
		return out, fmt.Errorf("%w: want %d hex characters, got %d", ErrInvalidAddress, AddressLen*2, len(trimmed))
	}
	raw, err := hex.DecodeString(strings.ToLower(trimmed))
	if err != nil {
		return out, fmt.Errorf("%w: %v", ErrInvalidAddress, err)
	}
	copy(out[:], raw)
	return out, nil
}

// AddressFromBytes builds an Address from exactly AddressLen bytes.
func AddressFromBytes(b []byte) (Address, error) {
	var out Address
	if len(b) != AddressLen {
		return out, fmt.Errorf("%w: want %d bytes, got %d", ErrInvalidAddress, AddressLen, len(b))
	}
	copy(out[:], b)
	return out, nil
}

// Keccak256 returns the Ethereum keccak256 (NOT the FIPS SHA3-256) hash of the
// concatenated inputs. sha3.NewLegacyKeccak256 uses the original Keccak padding
// that Ethereum's abi and ecrecover stack rely on; using SHA3-256 here would
// produce hashes no Ethereum tool agrees with.
func Keccak256(parts ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		_, _ = h.Write(p)
	}
	return h.Sum(nil)
}

// AddressFromPubKey derives the address from a secp256k1 public key: the last 20
// bytes of the keccak256 of its uncompressed form without the 0x04 prefix.
func AddressFromPubKey(pub *secp256k1.PublicKey) Address {
	uncompressed := pub.SerializeUncompressed()
	sum := Keccak256(uncompressed[1:])
	var out Address
	copy(out[:], sum[12:])
	return out
}

// RecoverAddress recovers the address that produced sig over digest. sig is the
// 65-byte r || s || v layout Ethereum tooling produces, with v in either the
// {0,1} or {27,28} convention.
func RecoverAddress(digest, sig []byte) (Address, error) {
	var zero Address
	if len(digest) != 32 {
		return zero, fmt.Errorf("%w: digest must be 32 bytes", ErrInvalidSignature)
	}
	if len(sig) != SignatureLen {
		return zero, fmt.Errorf("%w: signature must be %d bytes, got %d", ErrInvalidSignature, SignatureLen, len(sig))
	}
	// The recovery byte must be one of the four values the two conventions
	// define, and nothing else.
	//
	// Normalising "anything below 27" and passing everything else through looked
	// tolerant and was malleable: dcrd's RecoverCompact accepts codes 27-34 and
	// reads bit 2 as a COMPRESSED-PUBKEY flag, which this function then ignores
	// because it always serialises uncompressed. So one signature had four
	// byte-distinct encodings - v, v+4, v+27, v+31 - that all verified and all
	// recovered the same address.
	//
	// That is the same class of defect as high-S below, and it matters for the
	// same reason: anything keyed on the signature BYTES sees a malleated twin as
	// a new signature. A replay set keyed that way admits the twin, and an
	// operation its signer authorised once applies again - for a recipient
	// exempt from the nonce rule, as many times as there are spare encodings.
	v := sig[64]
	switch v {
	case 0, 1:
		// The {0,1} convention. Libraries differ and rejecting it would reject
		// half the ecosystem, so it is accepted - but only in this exact form.
		v += 27
	case 27, 28:
	default:
		return zero, fmt.Errorf("%w: recovery byte %d is not 0, 1, 27 or 28", ErrInvalidSignature, v)
	}
	// Reject the upper half of the S range (EIP-2). For any valid signature
	// (r, s) there is a second one (r, n-s) that recovers the SAME address, so
	// without this check every signature has a twin that verifies just as well.
	//
	// It matters because it is an inconsistency between two verifiers of the
	// same signature: WrappedMatrix.sol._recover rejects high-S and this did
	// not, so a signature Solidity refuses was accepted here. It also made
	// anything keyed on the signature BYTES weaker than it looked - a replay set
	// keyed that way sees a malleated twin as a new signature.
	if isHighS(sig[32:64]) {
		return zero, fmt.Errorf("%w: high-S signature (EIP-2): use the canonical low-S form",
			ErrInvalidSignature)
	}

	// dcrd's RecoverCompact wants <v><r><s>, not the <r><s><v> Ethereum uses.
	compact := make([]byte, SignatureLen)
	compact[0] = v
	copy(compact[1:33], sig[0:32])
	copy(compact[33:65], sig[32:64])

	pub, _, err := ecdsa.RecoverCompact(compact, digest)
	if err != nil {
		return zero, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	return AddressFromPubKey(pub), nil
}

// SignDigest signs a 32-byte digest with a secp256k1 key, producing the 65-byte
// r || s || v layout with v in {27,28}. It is here rather than only in the
// bridge so a test can produce a signature the same way a wallet would.
func SignDigest(priv *secp256k1.PrivateKey, digest []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, fmt.Errorf("%w: digest must be 32 bytes", ErrInvalidSignature)
	}
	compact := ecdsa.SignCompact(priv, digest, false)
	if len(compact) != SignatureLen {
		return nil, fmt.Errorf("%w: unexpected compact signature length %d", ErrInvalidSignature, len(compact))
	}
	// Back to Ethereum's <r><s><v>.
	out := make([]byte, SignatureLen)
	copy(out[0:32], compact[1:33])
	copy(out[32:64], compact[33:65])
	out[64] = compact[0]
	return out, nil
}

// secp256k1HalfOrder is (n-1)/2 for the secp256k1 group order n. A signature
// with s above this is the non-canonical twin of one below it. Same constant
// WrappedMatrix.sol compares against.
var secp256k1HalfOrder = [32]byte{
	0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0x5D, 0x57, 0x6E, 0x73, 0x57, 0xA4, 0x50, 0x1D,
	0xDF, 0xE9, 0x2F, 0x46, 0x68, 0x1B, 0x20, 0xA0,
}

// isHighS reports whether a 32-byte big-endian s is above (n-1)/2. Compared
// bytewise rather than via big.Int so it allocates nothing and needs no
// import.
//
// The loop returns as soon as the bytes differ, which is not constant-time and
// does not need to be: both operands are public. s arrives inside a submitted
// signature, and the constant is in the Solidity source. There is no secret
// here whose bytes the exit point could reveal.
func isHighS(s []byte) bool {
	if len(s) != 32 {
		return true // a malformed s is not canonical
	}
	for i := 0; i < 32; i++ {
		if s[i] != secp256k1HalfOrder[i] {
			return s[i] > secp256k1HalfOrder[i]
		}
	}
	return false // exactly the half order is allowed, matching Solidity's >
}

// CanonicalSignature returns sig with its recovery byte in the {27,28} form, so
// one signature has one spelling.
//
// Both conventions have to be ACCEPTED - this package's own envelope path emits
// {0,1} and wallets emit {27,28}, so rejecting either breaks a real caller - but
// accepting both means one authorisation has two byte-distinct encodings. That
// is harmless until something uses the signature as an IDENTITY: a replay set
// keyed on the bytes admits the twin, and an operation its signer authorised
// once applies twice.
//
// So verification stays permissive and identity is taken from this instead. A
// signature whose recovery byte is neither convention is returned unchanged,
// because RecoverAddress refuses it anyway and silently rewriting a malformed
// signature into a well-formed one would be worse than leaving it to fail.
func CanonicalSignature(sig []byte) []byte {
	if len(sig) != SignatureLen {
		return sig
	}
	out := make([]byte, SignatureLen)
	copy(out, sig)
	if out[64] == 0 || out[64] == 1 {
		out[64] += 27
	}
	return out
}
