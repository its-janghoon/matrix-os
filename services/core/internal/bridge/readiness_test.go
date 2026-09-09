package bridge

import (
	"bytes"
	"math/big"
	"testing"
)

func TestReadinessProofUsesDistinctDeploymentDomain(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	attestor, err := NewAttestorFromBytes(key)
	if err != nil {
		t.Fatalf("NewAttestorFromBytes: %v", err)
	}
	params := AttestationParams{
		ChainID:        big.NewInt(84532),
		BridgeContract: Address{0x22},
	}
	var challenge [ReadinessChallengeLen]byte
	for i := range challenge {
		challenge[i] = byte(i + 1)
	}

	digest := ReadinessDigest(challenge, params, attestor.Address(), 100_000_000_000)
	sig, err := SignReadiness(attestor, challenge, params, 100_000_000_000)
	if err != nil {
		t.Fatalf("SignReadiness: %v", err)
	}
	got, err := RecoverAddress(digest, sig)
	if err != nil {
		t.Fatalf("RecoverAddress: %v", err)
	}
	if got != attestor.Address() {
		t.Fatalf("recovered %s, want %s", got, attestor.Address())
	}

	mintDigest := AttestationDigest(Address{0x11}, big.NewInt(42), [LockIDLen]byte{0xab}, params)
	if bytes.Equal(digest, mintDigest) {
		t.Fatal("readiness digest reused the mint-attestation digest")
	}
	mintSigner, err := RecoverAddress(mintDigest, sig)
	if err == nil && mintSigner == attestor.Address() {
		t.Fatal("readiness signature remained valid for the attestor under the mint digest")
	}
}

func TestReadinessProofBindsEveryAdvertisedField(t *testing.T) {
	attestor, err := NewAttestorFromBytes(bytes.Repeat([]byte{0x24}, 32))
	if err != nil {
		t.Fatal(err)
	}
	params := AttestationParams{ChainID: big.NewInt(8453), BridgeContract: Address{0x33}}
	var challenge [ReadinessChallengeLen]byte
	challenge[0] = 1
	sig, err := SignReadiness(attestor, challenge, params, 100)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		challenge [ReadinessChallengeLen]byte
		params    AttestationParams
		attestor  Address
		minimum   uint64
	}{
		{name: "challenge", challenge: func() [ReadinessChallengeLen]byte { c := challenge; c[31] = 1; return c }(), params: params, attestor: attestor.Address(), minimum: 100},
		{name: "chain", challenge: challenge, params: AttestationParams{ChainID: big.NewInt(84532), BridgeContract: params.BridgeContract}, attestor: attestor.Address(), minimum: 100},
		{name: "contract", challenge: challenge, params: AttestationParams{ChainID: params.ChainID, BridgeContract: Address{0x44}}, attestor: attestor.Address(), minimum: 100},
		{name: "attestor", challenge: challenge, params: params, attestor: Address{0x55}, minimum: 100},
		{name: "minimum", challenge: challenge, params: params, attestor: attestor.Address(), minimum: 101},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrong := ReadinessDigest(tc.challenge, tc.params, tc.attestor, tc.minimum)
			got, err := RecoverAddress(wrong, sig)
			if err == nil && got == attestor.Address() {
				t.Fatalf("signature remained valid after changing %s", tc.name)
			}
		})
	}
}
