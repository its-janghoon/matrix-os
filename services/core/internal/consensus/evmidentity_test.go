package consensus

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/evmtx"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// TestTwoDifferentEnvelopesAreTwoTransactions is the bug a three-node devnet run
// found, and it was mine.
//
// The dedup key was sender + nonce + the Signature FIELD, which is the whole
// identity of a transaction. An Ethereum-enveloped transaction keeps its
// signature inside the envelope and leaves that field empty, so every
// transaction from one sender at one nonce shared a key whatever it paid or to
// whom. Everything downstream is keyed the same way, and all of it pointed one
// direction:
//
//   - the second was accepted as an idempotent resubmission and silently
//     dropped, so a transfer a user signed simply vanished;
//   - the nonce-reuse rule never fired, because the idempotent return is ahead
//     of it;
//   - committedTxs is the replay set, so once one committed the other could
//     never be included in any block by anyone;
//   - appliedTxs is keyed the same way, so a receipt for the second would have
//     reported the first's outcome - a success for a transfer that never
//     applied.
func TestTwoDifferentEnvelopesAreTwoTransactions(t *testing.T) {
	const chainID = 61_337
	eng := blockTimeEngine(t, nowFuncForTest())
	eng.mu.Lock()
	eng.chainID = chainID
	eng.mu.Unlock()

	priv, _, accountID := walletFor(t)
	if err := eng.ledger.Credit(accountID, 100*token.NativeUnit); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	_, recipient, _ := walletFor(t)

	build := func(value uint64) *token.Transaction {
		t.Helper()
		env := &evmtx.Transaction{
			Type:      evmtx.TxLegacy,
			Nonce:     3,
			GasFeeCap: big.NewInt(0),
			Gas:       21000,
			To:        &recipient,
			Value:     token.NativeToERC20(value),
		}
		if err := env.Sign(priv, chainID); err != nil {
			t.Fatalf("Sign: %v", err)
		}
		raw, err := env.MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary: %v", err)
		}
		tx, err := token.NewTransactionFromEVM(raw, chainID, ReservedRecipientFor)
		if err != nil {
			t.Fatalf("NewTransactionFromEVM: %v", err)
		}
		return tx
	}

	cheap := build(1 * token.NativeUnit)
	dear := build(9 * token.NativeUnit)

	// Same sender, same nonce, different money. These must be two identities.
	if mempoolKey(cheap) == mempoolKey(dear) {
		t.Fatal("two different envelopes share one dedup key: the second would be dropped as a " +
			"duplicate of the first, and a receipt for it would report the first's outcome")
	}

	if err := eng.Submit(cheap); err != nil {
		t.Fatalf("the first transfer was refused: %v", err)
	}
	// And with two identities, the nonce rule is reached and refuses the second
	// LOUDLY, rather than the chain silently keeping one of the two.
	err := eng.Submit(dear)
	if !errors.Is(err, ErrNonceAlreadyUsed) {
		t.Fatalf("a second transfer at a spent nonce = %v, want ErrNonceAlreadyUsed", err)
	}
}

// TestResubmittingTheSameEnvelopeIsStillIdempotent keeps the property the dedup
// key exists for. Gossip re-delivers, and a client retries; neither is an error
// and neither should produce a second transaction.
func TestResubmittingTheSameEnvelopeIsStillIdempotent(t *testing.T) {
	const chainID = 61_337
	eng := blockTimeEngine(t, nowFuncForTest())
	eng.mu.Lock()
	eng.chainID = chainID
	eng.mu.Unlock()

	priv, _, accountID := walletFor(t)
	if err := eng.ledger.Credit(accountID, 100*token.NativeUnit); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	_, recipient, _ := walletFor(t)

	env := &evmtx.Transaction{
		Type: evmtx.TxLegacy, Nonce: 1, GasFeeCap: big.NewInt(0), Gas: 21000,
		To: &recipient, Value: token.NativeToERC20(token.NativeUnit),
	}
	if err := env.Sign(priv, chainID); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, err := env.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	first, err := token.NewTransactionFromEVM(raw, chainID, ReservedRecipientFor)
	if err != nil {
		t.Fatalf("NewTransactionFromEVM: %v", err)
	}
	again, err := token.NewTransactionFromEVM(raw, chainID, ReservedRecipientFor)
	if err != nil {
		t.Fatalf("NewTransactionFromEVM: %v", err)
	}

	if mempoolKey(first) != mempoolKey(again) {
		t.Fatal("the same envelope produced two dedup keys, so gossip would fill the mempool with copies")
	}
	if err := eng.Submit(first); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if err := eng.Submit(again); err != nil {
		t.Fatalf("resubmitting the identical envelope should be a no-op, got %v", err)
	}

	eng.mu.Lock()
	size := len(eng.mempool)
	eng.mu.Unlock()
	if size != 1 {
		t.Fatalf("the mempool holds %d copies of one transaction, want 1", size)
	}
}
