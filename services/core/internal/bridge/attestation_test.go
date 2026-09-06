package bridge

import (
	"crypto/rand"
	"errors"
	"math/big"
	"testing"
)

// newTestAttestor generates a fresh random secp256k1 attestor for tests. It uses
// crypto/rand so keys are never hardcoded.
func newTestAttestor(t *testing.T) *Attestor {
	t.Helper()
	var key [32]byte
	for {
		if _, err := rand.Read(key[:]); err != nil {
			t.Fatalf("rand: %v", err)
		}
		a, err := NewAttestorFromBytes(key[:])
		if err == nil {
			return a
		}
	}
}

func testParams() AttestationParams {
	return AttestationParams{
		ChainID:        big.NewInt(31337), // hardhat default chain id
		BridgeContract: Address{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x01, 0x02, 0x03, 0x04},
	}
}

func TestSignAndRecoverRoundTrip(t *testing.T) {
	a := newTestAttestor(t)
	digest := keccak256([]byte("hello matrix bridge"))
	sig, err := a.SignDigest(digest)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if len(sig) != 65 {
		t.Fatalf("expected 65-byte signature, got %d", len(sig))
	}
	if sig[64] != 27 && sig[64] != 28 {
		t.Fatalf("expected v in {27,28}, got %d", sig[64])
	}
	got, err := RecoverAddress(digest, sig)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if got != a.Address() {
		t.Fatalf("recovered %s, want %s", got, a.Address())
	}
}

func TestRecoverRejectsTamperedDigest(t *testing.T) {
	a := newTestAttestor(t)
	digest := keccak256([]byte("original"))
	sig, err := a.SignDigest(digest)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	tampered := keccak256([]byte("tampered"))
	got, err := RecoverAddress(tampered, sig)
	// Recovery still yields *some* address, but it must not be the signer.
	if err == nil && got == a.Address() {
		t.Fatalf("tampered digest recovered the original signer")
	}
}

func TestAttestationVerify(t *testing.T) {
	params := testParams()
	v1 := newTestAttestor(t)
	v2 := newTestAttestor(t)
	v3 := newTestAttestor(t)
	outsider := newTestAttestor(t)

	attestors := map[Address]bool{
		v1.Address(): true,
		v2.Address(): true,
		v3.Address(): true,
	}

	recipient := Address{0xde, 0xad, 0xbe, 0xef}
	amount := big.NewInt(1_000_000_000) // 1 native unit worth of erc20 base units
	var lockID [LockIDLen]byte
	lockID[0] = 0xaa

	signers := []ValidatorSigner{
		{Label: "v1", Attestor: v1},
		{Label: "v2", Attestor: v2},
	}
	att, err := SignAttestation(recipient, amount, lockID, params, signers)
	if err != nil {
		t.Fatalf("sign attestation: %v", err)
	}

	t.Run("valid threshold met", func(t *testing.T) {
		if err := att.Verify(attestors, 2, params); err != nil {
			t.Fatalf("expected valid, got %v", err)
		}
	})

	t.Run("insufficient threshold", func(t *testing.T) {
		if err := att.Verify(attestors, 3, params); !errors.Is(err, ErrThresholdNotMet) {
			t.Fatalf("expected ErrThresholdNotMet, got %v", err)
		}
	})

	t.Run("unregistered signer rejected", func(t *testing.T) {
		bad, err := SignAttestation(recipient, amount, lockID, params, []ValidatorSigner{
			{Label: "v1", Attestor: v1},
			{Label: "outsider", Attestor: outsider},
		})
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		if err := bad.Verify(attestors, 2, params); !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("expected ErrInvalidSignature for outsider, got %v", err)
		}
	})

	t.Run("duplicate signer rejected", func(t *testing.T) {
		dup, err := SignAttestation(recipient, amount, lockID, params, []ValidatorSigner{
			{Label: "v1", Attestor: v1},
			{Label: "v1-again", Attestor: v1},
		})
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		if err := dup.Verify(attestors, 2, params); !errors.Is(err, ErrDuplicateAttestor) {
			t.Fatalf("expected ErrDuplicateAttestor, got %v", err)
		}
	})

	t.Run("tampered amount rejected", func(t *testing.T) {
		tampered := &Attestation{
			Recipient:  att.Recipient,
			Amount:     new(big.Int).Add(att.Amount, big.NewInt(1)),
			LockID:     att.LockID,
			Signatures: att.Signatures,
		}
		// Verify recomputes the digest with the mutated amount; recovered
		// addresses will not be registered attestors.
		if err := tampered.Verify(attestors, 2, params); err == nil {
			t.Fatalf("expected verification failure for tampered amount")
		}
	})

	t.Run("wrong chain params rejected", func(t *testing.T) {
		wrong := AttestationParams{ChainID: big.NewInt(1), BridgeContract: params.BridgeContract}
		if err := att.Verify(attestors, 2, wrong); err == nil {
			t.Fatalf("expected failure with wrong chain params")
		}
	})
}

func TestAttestationDigestDeterministic(t *testing.T) {
	params := testParams()
	recipient := Address{0x01, 0x02, 0x03}
	amount := big.NewInt(42)
	var lockID [LockIDLen]byte
	lockID[31] = 0x09

	d1 := AttestationDigest(recipient, amount, lockID, params)
	d2 := AttestationDigest(recipient, amount, lockID, params)
	if string(d1) != string(d2) {
		t.Fatalf("digest not deterministic")
	}
	if len(d1) != 32 {
		t.Fatalf("digest should be 32 bytes, got %d", len(d1))
	}
}

func TestParseAddressRoundTrip(t *testing.T) {
	a := newTestAttestor(t)
	s := a.Address().Hex()
	parsed, err := ParseAddress(s)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed != a.Address() {
		t.Fatalf("round-trip mismatch: %s vs %s", parsed, a.Address())
	}
	if _, err := ParseAddress("0x1234"); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("expected ErrInvalidAddress for short input, got %v", err)
	}
}
