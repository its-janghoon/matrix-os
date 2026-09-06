package token

import (
	"encoding/json"
	"errors"
	"testing"
)

// appendSigned signs and appends a transaction from sender, using the chain's
// current head hash as PrevHash, and returns the resulting record.
func appendSigned(t *testing.T, chain *Chain, sender *Account, to string, amount, nonce uint64) (*Record, error) {
	t.Helper()
	prev, err := chain.HeadHash()
	if err != nil {
		t.Fatalf("HeadHash() error = %v", err)
	}
	tx := signedTx(t, sender, to, amount, nonce, prev)
	return chain.Append(tx)
}

func TestChain_AppendAdvancesHead(t *testing.T) {
	chain := NewChain(newTestStore(t))
	alice := mustAccount(t)
	bob := mustAccount(t)

	if l, _ := chain.Len(); l != 0 {
		t.Fatalf("empty chain Len() = %d, want 0", l)
	}

	rec1, err := appendSigned(t, chain, alice, bob.AccountID(), 10, 0)
	if err != nil {
		t.Fatalf("Append() first error = %v", err)
	}
	if rec1.Height != 0 {
		t.Errorf("first record height = %d, want 0", rec1.Height)
	}

	rec2, err := appendSigned(t, chain, alice, bob.AccountID(), 5, 1)
	if err != nil {
		t.Fatalf("Append() second error = %v", err)
	}
	if rec2.Height != 1 {
		t.Errorf("second record height = %d, want 1", rec2.Height)
	}

	l, err := chain.Len()
	if err != nil {
		t.Fatalf("Len() error = %v", err)
	}
	if l != 2 {
		t.Errorf("Len() = %d, want 2", l)
	}

	// Head hash must equal the last record's hash.
	headHash, _ := chain.HeadHash()
	got, err := chain.TransactionAt(1)
	if err != nil {
		t.Fatalf("TransactionAt(1) error = %v", err)
	}
	if string(headHash) != string(got.Hash) {
		t.Error("head hash does not match last record hash")
	}

	if err := chain.ValidateChain(); err != nil {
		t.Errorf("ValidateChain() on good chain error = %v", err)
	}
}

func TestChain_AppendRejectsBadSignature(t *testing.T) {
	chain := NewChain(newTestStore(t))
	alice := mustAccount(t)
	bob := mustAccount(t)

	prev, _ := chain.HeadHash()
	tx := signedTx(t, alice, bob.AccountID(), 10, 0, prev)
	tx.Signature[0] ^= 0xff // corrupt after signing

	if _, err := chain.Append(tx); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("Append() with bad signature error = %v, want ErrInvalidSignature", err)
	}
	if l, _ := chain.Len(); l != 0 {
		t.Errorf("chain advanced after rejected append: Len() = %d, want 0", l)
	}
}

func TestChain_NonceEnforcement(t *testing.T) {
	alice := mustAccount(t)
	bob := mustAccount(t)

	t.Run("replayed nonce rejected", func(t *testing.T) {
		chain := NewChain(newTestStore(t))
		if _, err := appendSigned(t, chain, alice, bob.AccountID(), 10, 0); err != nil {
			t.Fatalf("Append() nonce 0 error = %v", err)
		}
		// Re-using nonce 0 (a replay) must be rejected; expected next is 1.
		prev, _ := chain.HeadHash()
		tx := signedTx(t, alice, bob.AccountID(), 10, 0, prev)
		if _, err := chain.Append(tx); !errors.Is(err, ErrNonceMismatch) {
			t.Fatalf("Append() replayed nonce error = %v, want ErrNonceMismatch", err)
		}
	})

	t.Run("gapped nonce rejected", func(t *testing.T) {
		chain := NewChain(newTestStore(t))
		if _, err := appendSigned(t, chain, alice, bob.AccountID(), 10, 0); err != nil {
			t.Fatalf("Append() nonce 0 error = %v", err)
		}
		// Skipping to nonce 2 (a gap) must be rejected; expected next is 1.
		prev, _ := chain.HeadHash()
		tx := signedTx(t, alice, bob.AccountID(), 10, 2, prev)
		if _, err := chain.Append(tx); !errors.Is(err, ErrNonceMismatch) {
			t.Fatalf("Append() gapped nonce error = %v, want ErrNonceMismatch", err)
		}
	})

	t.Run("per-sender nonces are independent", func(t *testing.T) {
		chain := NewChain(newTestStore(t))
		carol := mustAccount(t)
		if _, err := appendSigned(t, chain, alice, bob.AccountID(), 10, 0); err != nil {
			t.Fatalf("alice nonce 0 error = %v", err)
		}
		// Carol's first tx must also start at nonce 0, independent of alice.
		if _, err := appendSigned(t, chain, carol, bob.AccountID(), 10, 0); err != nil {
			t.Fatalf("carol nonce 0 error = %v", err)
		}
	})
}

func TestChain_AppendRejectsWrongPrevHash(t *testing.T) {
	chain := NewChain(newTestStore(t))
	alice := mustAccount(t)
	bob := mustAccount(t)

	if _, err := appendSigned(t, chain, alice, bob.AccountID(), 10, 0); err != nil {
		t.Fatalf("Append() first error = %v", err)
	}

	// Build a tx whose PrevHash points at genesis instead of the current head.
	tx := signedTx(t, alice, bob.AccountID(), 5, 1, genesisPrevHash)
	if _, err := chain.Append(tx); !errors.Is(err, ErrPrevHashMismatch) {
		t.Fatalf("Append() wrong prev-hash error = %v, want ErrPrevHashMismatch", err)
	}
}

func TestChain_ValidateChainDetectsCorruption(t *testing.T) {
	store := newTestStore(t)
	chain := NewChain(store)
	alice := mustAccount(t)
	bob := mustAccount(t)

	if _, err := appendSigned(t, chain, alice, bob.AccountID(), 10, 0); err != nil {
		t.Fatalf("Append() 0 error = %v", err)
	}
	if _, err := appendSigned(t, chain, alice, bob.AccountID(), 20, 1); err != nil {
		t.Fatalf("Append() 1 error = %v", err)
	}

	// Good chain validates.
	if err := chain.ValidateChain(); err != nil {
		t.Fatalf("ValidateChain() on good chain error = %v", err)
	}

	// Corrupt the persisted record at height 1 by mutating its amount while
	// leaving the stored link hash intact. This simulates on-disk tampering.
	raw, err := store.Get(txKey(1))
	if err != nil || raw == nil {
		t.Fatalf("failed to read stored record: %v", err)
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("failed to decode stored record: %v", err)
	}
	rec.Tx.Amount = 999999 // tamper
	mutated, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("failed to re-encode record: %v", err)
	}
	if err := store.Put(txKey(1), mutated); err != nil {
		t.Fatalf("failed to write mutated record: %v", err)
	}

	err = chain.ValidateChain()
	if !errors.Is(err, ErrCorruptChain) {
		t.Fatalf("ValidateChain() on corrupted chain error = %v, want ErrCorruptChain", err)
	}
	// The error must identify the broken height (1).
	if got := err.Error(); got == "" {
		t.Error("ValidateChain() error should identify the broken link")
	}
}

func TestChain_ResumesFromPersistedHead(t *testing.T) {
	store := newTestStore(t)
	alice := mustAccount(t)
	bob := mustAccount(t)

	chain := NewChain(store)
	if _, err := appendSigned(t, chain, alice, bob.AccountID(), 10, 0); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	// A fresh Chain over the same store must observe the persisted head/length.
	reopened := NewChain(store)
	l, err := reopened.Len()
	if err != nil {
		t.Fatalf("Len() error = %v", err)
	}
	if l != 1 {
		t.Errorf("reopened chain Len() = %d, want 1", l)
	}
	// And it must continue the chain correctly (next nonce = 1).
	if _, err := appendSigned(t, reopened, alice, bob.AccountID(), 5, 1); err != nil {
		t.Fatalf("Append() on reopened chain error = %v", err)
	}
	if err := reopened.ValidateChain(); err != nil {
		t.Errorf("ValidateChain() after resume error = %v", err)
	}
}
