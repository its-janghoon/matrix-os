package consensus

import (
	"testing"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// TestOneAuthorisationHasOneMempoolIdentity
//
// mempoolKey is what the engine uses to decide "have I seen this transaction
// before". It feeds three sets that all have to agree on what "this
// transaction" means: the mempool's own dedup, committedTxs (the replay set),
// and appliedTxs (which receipt belongs to which submission).
//
// An ecrecover signature is accepted in BOTH the {0,1} and {27,28} recovery-byte
// conventions, because this repo's envelope path emits one and wallets emit the
// other, so neither can be refused. That leaves one authorisation with two
// byte-distinct spellings. Keyed on the raw bytes, the twin reads as a NEW
// transaction: the replay set misses, and for a reserved recipient - exempt from
// the nonce-uniqueness rule by design, since the engine mints those nonces from
// its own counter - the operation its signer authorised once applies again.
//
// So the identity is taken from the canonical form, and both spellings of one
// authorisation collapse to one key.
func TestOneAuthorisationHasOneMempoolIdentity(t *testing.T) {
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	addr := ethsig.AddressFromPubKey(priv.PubKey())

	tx := &token.Transaction{
		From:      addr[:],
		To:        "bob",
		Amount:    100,
		Nonce:     7,
		Timestamp: 1788769228123456789,
	}
	digest, err := tx.EthTransferDigest()
	if err != nil {
		t.Fatalf("EthTransferDigest: %v", err)
	}
	sig, err := ethsig.SignDigest(priv, digest)
	if err != nil {
		t.Fatalf("SignDigest: %v", err)
	}
	tx.Signature = sig
	if err := tx.Verify(); err != nil {
		t.Fatalf("the wallet's own signature was refused: %v", err)
	}

	// The same signature in the other convention. Both verify - that is the
	// point, and it is why the key cannot be the raw bytes.
	twin := &token.Transaction{}
	*twin = *tx
	twinSig := make([]byte, len(sig))
	copy(twinSig, sig)
	switch twinSig[64] {
	case 27:
		twinSig[64] = 0
	case 28:
		twinSig[64] = 1
	default:
		t.Fatalf("SignDigest produced v=%d, expected the {27,28} convention", twinSig[64])
	}
	twin.Signature = twinSig
	if string(twinSig) == string(sig) {
		t.Fatal("the twin is byte-identical; the test proves nothing")
	}
	if err := twin.Verify(); err != nil {
		t.Fatalf("the other recovery-byte convention was refused: %v", err)
	}

	if got, want := mempoolKey(twin), mempoolKey(tx); got != want {
		t.Fatalf("one authorisation produced two mempool identities:\n  %s\n  %s", want, got)
	}
}

// TestTwoDistinctTransfersDoNotShareAMempoolIdentity guards the other direction.
// Collapsing the recovery byte must not collapse anything else: two different
// authorisations from one sender at one nonce - which is exactly what a
// double-spend attempt looks like - have to stay distinguishable, or the second
// is silently dropped with a success response instead of being refused by the
// nonce rule.
func TestTwoDistinctTransfersDoNotShareAMempoolIdentity(t *testing.T) {
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	addr := ethsig.AddressFromPubKey(priv.PubKey())

	sign := func(tx *token.Transaction) {
		t.Helper()
		digest, err := tx.EthTransferDigest()
		if err != nil {
			t.Fatalf("EthTransferDigest: %v", err)
		}
		sig, err := ethsig.SignDigest(priv, digest)
		if err != nil {
			t.Fatalf("SignDigest: %v", err)
		}
		tx.Signature = sig
	}

	first := &token.Transaction{From: addr[:], To: "bob", Amount: 100, Nonce: 7, Timestamp: 1}
	second := &token.Transaction{From: addr[:], To: "carol", Amount: 100, Nonce: 7, Timestamp: 1}
	sign(first)
	sign(second)

	if mempoolKey(first) == mempoolKey(second) {
		t.Fatal("two different transfers at one nonce share a mempool identity; " +
			"the second would be dropped as a duplicate instead of refused")
	}
}
