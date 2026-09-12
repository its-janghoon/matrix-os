package market

import (
	"encoding/binary"
	"fmt"
)

// Reading the whole ledger out, and knowing when it has changed.
//
// Both exist for things built on top of the ledger rather than inside it: the
// state root in merkle.go folds every balance, and a cache of anything derived
// from the ledger needs to know when it is stale.

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
