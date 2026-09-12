package market

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// A hash of every balance, so two nodes can discover they disagree.
//
// THE PROBLEM THIS EXISTS FOR. Consensus agrees on the ORDER of transactions and
// nothing else: each node then applies them itself, and the block carries no
// commitment to the result. So two nodes running different apply logic - one
// upgraded, one not, or one with a different fee constant - produce identical
// block hashes and different balances, and NOTHING reports it. The chain does
// not fork, no node errors, and the disagreement surfaces weeks later as numbers
// that do not add up.
//
// The digest is the missing commitment. It is cheap because the ledger is a
// single flat keyspace that pebble already iterates in sorted order, so there is
// no tree to maintain and no ordering to impose - the hash is a fold over keys
// and values in the order they are stored.
//
// WHAT IT IS NOT. It is not a Merkle root: there is no way to prove ONE balance
// against it without sending every balance, so it cannot serve a light client or
// an external verifier. It answers "do we agree", which is the question that is
// actually open, and it does so without the tree a proof system would need. A
// Merkle root is the upgrade when proofs are wanted; this is what makes silent
// divergence loud today.

// stateDigestDomain separates this hash from any other sha256 over ledger bytes.
const stateDigestDomain = "matrix/ledger/state-digest/v1"

// StateDigest returns a hash over every persisted balance.
//
// Determinism is the whole point, so every input is length-prefixed - without it
// an account named "ab" holding value X and one named "a" holding a value
// beginning with "b" could fold into the same bytes - and the iteration order is
// pebble's key order, which is a property of the store rather than of this
// node's memory.
func (l *Ledger) StateDigest() ([]byte, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	h := sha256.New()
	_, _ = h.Write([]byte(stateDigestDomain))

	var count uint64
	var scratch [8]byte
	err := l.store.Iterate([]byte(balanceKeyPrefix), func(key, value []byte) error {
		binary.BigEndian.PutUint64(scratch[:], uint64(len(key)))
		_, _ = h.Write(scratch[:])
		_, _ = h.Write(key)
		binary.BigEndian.PutUint64(scratch[:], uint64(len(value)))
		_, _ = h.Write(scratch[:])
		_, _ = h.Write(value)
		count++
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("market: compute the ledger state digest: %w", err)
	}
	// The count is folded in as well, so a truncated iteration cannot produce the
	// digest of a shorter but otherwise identical ledger.
	binary.BigEndian.PutUint64(scratch[:], count)
	_, _ = h.Write(scratch[:])
	return h.Sum(nil), nil
}

// ForEachBalance calls fn for every persisted account balance, in the store's
// key order.
//
// It is the enumeration the ledger never had, and several things want it: the
// state digest above, a supply or rich-list report, and anything that has to
// reconstruct a ledger elsewhere. Reading through the same iterator the digest
// uses means a caller enumerating balances and a caller hashing them see the
// same set in the same order.
//
// fn must not write to the ledger: the read lock is held for the whole scan, and
// a write from inside it deadlocks.
func (l *Ledger) ForEachBalance(fn func(account string, balance uint64) error) error {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.store.Iterate([]byte(balanceKeyPrefix), func(key, value []byte) error {
		account := string(key[len(balanceKeyPrefix):])
		if len(value) != 8 {
			return fmt.Errorf("market: corrupt balance for %q: expected 8 bytes, got %d", account, len(value))
		}
		return fn(account, binary.BigEndian.Uint64(value))
	})
}

// WriteEpoch returns a counter that changes whenever the ledger has been
// written, so a caller can cache something derived from the ledger and know
// when the cache is stale.
//
// It counts write-lock acquisitions rather than actual mutations, which makes
// it conservative in the safe direction: a section that took the lock and wrote
// nothing still bumps it, so a cache may recompute when it did not have to and
// can never serve a value from a ledger that has since changed.
//
// The distinction matters because the ledger is written from more than the block
// apply path - genesis seeds it, and on a single-node network so does funding -
// and a cache keyed on block height alone would go stale without noticing. That
// is not hypothetical: it is exactly how a state root cached per height came to
// be computed before a seed and compared after one.
func (l *Ledger) WriteEpoch() uint64 { return l.writeAcquires.Load() }
