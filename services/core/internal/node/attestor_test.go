package node

import (
	"bytes"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/bridge"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Loading this validator's bridge attestor key.
//
// Before this the node held no attestor at all, so no running node could sign a
// mint authorization and no wMATRIX could come into existence. The key is a
// keystore rather than a hex config field because it is unilateral authority to
// mint against escrow.

func writeAttestor(t *testing.T, pass string) (path, addr string) {
	t.Helper()
	ks, att, err := bridge.NewAttestorKeystore(pass)
	if err != nil {
		t.Fatalf("NewAttestorKeystore: %v", err)
	}
	body, err := token.MarshalKeystore(ks)
	if err != nil {
		t.Fatalf("MarshalKeystore: %v", err)
	}
	path = filepath.Join(t.TempDir(), "attestor.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path, att.AddressHex()
}

func TestNoAttestorConfiguredIsNotAnError(t *testing.T) {
	att, err := loadAttestor(BridgeConfig{})
	if err != nil {
		t.Fatalf("a node with no attestor configured failed to start: %v", err)
	}
	if att != nil {
		t.Fatal("an attestor appeared from nowhere")
	}
}

func TestTheConfiguredAttestorIsUnlocked(t *testing.T) {
	path, addr := writeAttestor(t, "s3cret")
	t.Setenv(AttestorPassphraseEnv, "s3cret")

	att, err := loadAttestor(BridgeConfig{AttestorKeystore: path})
	if err != nil {
		t.Fatalf("loadAttestor: %v", err)
	}
	if att == nil || att.AddressHex() != addr {
		t.Fatalf("unlocked %v, want %s", att, addr)
	}
}

// TestAConfiguredButUnusableKeyStopsTheNode is the property that matters. A
// validator that looks like it is attesting and is not means mints silently stop
// reaching quorum on a threshold bridge, with nothing pointing at the node
// responsible.
func TestAConfiguredButUnusableKeyStopsTheNode(t *testing.T) {
	path, _ := writeAttestor(t, "right")

	t.Run("no passphrase in the environment", func(t *testing.T) {
		t.Setenv(AttestorPassphraseEnv, "")
		_, err := loadAttestor(BridgeConfig{AttestorKeystore: path})
		if err == nil {
			t.Fatal("the node started with an attestor it could not unlock")
		}
		if !strings.Contains(err.Error(), AttestorPassphraseEnv) {
			t.Fatalf("the error does not name where the passphrase comes from: %v", err)
		}
	})

	t.Run("wrong passphrase", func(t *testing.T) {
		t.Setenv(AttestorPassphraseEnv, "wrong")
		if _, err := loadAttestor(BridgeConfig{AttestorKeystore: path}); err == nil {
			t.Fatal("a wrong passphrase produced an attestor")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		t.Setenv(AttestorPassphraseEnv, "right")
		if _, err := loadAttestor(BridgeConfig{AttestorKeystore: path + ".nope"}); err == nil {
			t.Fatal("a missing keystore was tolerated")
		}
	})
}

// TestAttestingNeedsBothHalves. The bridge holds the lock records and the
// attestor holds the key; a node with one but not the other must refuse rather
// than half-answer, and the interface must be a real nil so the RPC's
// "no attestor -> FailedPrecondition" check fires instead of a panic.
func TestAttestingNeedsBothHalves(t *testing.T) {
	path, _ := writeAttestor(t, "p")
	t.Setenv(AttestorPassphraseEnv, "p")
	att, err := loadAttestor(BridgeConfig{AttestorKeystore: path})
	if err != nil {
		t.Fatalf("loadAttestor: %v", err)
	}

	if got := lockAttestorFor(nil, att); got != nil {
		t.Fatal("a node with a key but no bridge claimed it could attest")
	}
	if got := lockAttestorFor(nil, nil); got != nil {
		t.Fatal("a node with neither claimed it could attest")
	}
}

func TestBridgeReadinessNeedsBothHalvesAndSignsDeployment(t *testing.T) {
	att, err := bridge.NewAttestorFromBytes(bytes.Repeat([]byte{0x31}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if bridgeReadinessFor(nil, att) != nil || bridgeReadinessFor(nil, nil) != nil {
		t.Fatal("readiness was exposed without both bridge and key")
	}
	params := bridge.AttestationParams{
		ChainID:        big.NewInt(84532),
		BridgeContract: bridge.Address{0x22},
	}
	b := bridge.New(nil, nil, params)
	if bridgeReadinessFor(b, nil) != nil {
		t.Fatal("readiness was exposed without an attestor key")
	}
	signer := bridgeReadinessFor(b, att)
	challenge := bytes.Repeat([]byte{0xa7}, bridge.ReadinessChallengeLen)
	proof, err := signer.SignBridgeReadiness(challenge)
	if err != nil {
		t.Fatal(err)
	}
	if proof.ChainID != 84532 || proof.Contract != params.BridgeContract.Hex() || proof.Attestor != att.AddressHex() || proof.MinLockNative != token.MinBridgeLockAmount {
		t.Fatalf("unexpected proof fields: %+v", proof)
	}
	var nonce [bridge.ReadinessChallengeLen]byte
	copy(nonce[:], challenge)
	recovered, err := bridge.RecoverAddress(bridge.ReadinessDigest(nonce, params, att.Address(), token.MinBridgeLockAmount), proof.Signature)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != att.Address() {
		t.Fatalf("recovered %s, want %s", recovered, att.Address())
	}
}
