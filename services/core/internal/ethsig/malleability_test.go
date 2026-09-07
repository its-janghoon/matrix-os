package ethsig

import (
	"errors"
	"math/big"
	"testing"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// secp256k1N is the group order. s and n-s are the two encodings of one
// signature.
var secp256k1N = new(big.Int).SetBytes([]byte{
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFE,
	0xBA, 0xAE, 0xDC, 0xE6, 0xAF, 0x48, 0xA0, 0x3B,
	0xBF, 0xD2, 0x5E, 0x8C, 0xD0, 0x36, 0x41, 0x41,
})

// malleate returns the other valid encoding of the same signature: s becomes
// n-s, and v flips. It recovers the SAME address.
func malleate(sig []byte) []byte {
	out := make([]byte, SignatureLen)
	copy(out, sig)
	s := new(big.Int).SetBytes(sig[32:64])
	flipped := new(big.Int).Sub(secp256k1N, s)
	b := flipped.Bytes()
	// Left-pad to 32 bytes.
	for i := range out[32:64] {
		out[32+i] = 0
	}
	copy(out[64-len(b):64], b)
	v := sig[64]
	if v < 27 {
		v += 27
	}
	if v == 27 {
		out[64] = 28
	} else {
		out[64] = 27
	}
	return out
}

// TestAMalleatedSignatureIsRefused
//
// THE PROBLEM. For any valid ECDSA signature (r, s) there is a second encoding
// (r, n-s) that recovers the SAME signer. Two byte strings, one authorization.
//
// WrappedMatrix.sol._recover rejects the high-S form (EIP-2) and this package
// did not, so a signature Solidity refuses was accepted here: two verifiers of
// the same signature disagreeing. It also made anything keyed on signature
// BYTES weaker than it looked - the run-authorization replay set was keyed that
// way, so a malleated twin read as a brand-new authorization and the work ran
// again.
func TestAMalleatedSignatureIsRefused(t *testing.T) {
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	digest := Keccak256([]byte("authorize this exact work"))

	sig, err := SignDigest(priv, digest)
	if err != nil {
		t.Fatalf("SignDigest: %v", err)
	}

	// The canonical signature verifies and recovers the signer.
	want := AddressFromPubKey(priv.PubKey())
	got, err := RecoverAddress(digest, sig)
	if err != nil {
		t.Fatalf("RecoverAddress on a canonical signature: %v", err)
	}
	if got != want {
		t.Fatalf("recovered %s, want %s", got, want)
	}

	// Its twin must be refused, not accepted as a second signature.
	twin := malleate(sig)
	if string(twin) == string(sig) {
		t.Fatal("malleate produced the same bytes; the test proves nothing")
	}
	if _, err := RecoverAddress(digest, twin); err == nil {
		t.Fatal("a malleated (high-S) signature was accepted; it is a second valid encoding " +
			"of one authorization, and Solidity refuses it")
	} else if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("error = %v, want ErrInvalidSignature", err)
	}
}

// TestSignDigestNeverProducesAHighS. If our own signer could emit the
// non-canonical form, the check above would reject signatures this repo
// produced. dcrd's SignCompact is expected to normalize; this pins it.
func TestSignDigestNeverProducesAHighS(t *testing.T) {
	for i := 0; i < 200; i++ {
		priv, err := secp256k1.GeneratePrivateKey()
		if err != nil {
			t.Fatalf("GeneratePrivateKey: %v", err)
		}
		digest := Keccak256([]byte{byte(i), byte(i >> 8)})
		sig, err := SignDigest(priv, digest)
		if err != nil {
			t.Fatalf("SignDigest: %v", err)
		}
		if isHighS(sig[32:64]) {
			t.Fatalf("iteration %d: our own signer produced a high-S signature", i)
		}
		if _, err := RecoverAddress(digest, sig); err != nil {
			t.Fatalf("iteration %d: our own signature was refused: %v", i, err)
		}
	}
}

// TestTheHalfOrderBoundaryMatchesSolidity. Solidity's check is `s > halfOrder`,
// so s exactly equal to the half order is allowed. An off-by-one here would be
// the same class of inconsistency this fixes.
func TestTheHalfOrderBoundaryMatchesSolidity(t *testing.T) {
	half := secp256k1HalfOrder
	if isHighS(half[:]) {
		t.Fatal("s exactly at the half order was rejected; Solidity's check is strictly greater")
	}

	justOver := secp256k1HalfOrder
	justOver[31]++
	if !isHighS(justOver[:]) {
		t.Fatal("s one above the half order was accepted")
	}

	justUnder := secp256k1HalfOrder
	justUnder[31]--
	if isHighS(justUnder[:]) {
		t.Fatal("s one below the half order was rejected")
	}
}

// TestAMalformedSIsNotCanonical: a short or long s must not be treated as low-S
// by accident.
func TestAMalformedSIsNotCanonical(t *testing.T) {
	for _, n := range []int{0, 1, 31, 33, 64} {
		if !isHighS(make([]byte, n)) {
			t.Fatalf("a %d-byte s was treated as canonical", n)
		}
	}
}
