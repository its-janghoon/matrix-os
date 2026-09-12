package consensus

import "github.com/ecirlabs/matrix-core/internal/token"

// Reporting a sender's next nonce, which a wallet needs and this chain does not
// otherwise keep.
//
// On Ethereum a sender's nonces are consecutive and the chain stores the count,
// so eth_getTransactionCount is a lookup. Here a nonce is a uniquifier checked
// against a SET, which accepts 0 then 5 then 2 quite happily and therefore has
// no count to report. The high-water mark below is what makes the two views
// agree: a wallet handed high+1 produces consecutive nonces from there, each of
// which the set has never seen.

// recordNonceHighLocked notes a committed transaction's nonce against its
// sender. Callers must hold e.mu.
func (e *Engine) recordNonceHighLocked(tx *token.Transaction) {
	if _, checked := nonceKey(tx); !checked {
		return
	}
	sender := tx.SenderID()
	if sender == "" {
		return
	}
	if current, seen := e.nonceHigh[sender]; !seen || tx.Nonce > current {
		e.nonceHigh[sender] = tx.Nonce
	}
}

// NextNonce returns the nonce a sender should use for its next transaction.
//
// includePending also considers what is sitting in this node's mempool. A wallet
// asks for the pending count before it signs, and answering from committed state
// alone would hand it a nonce its own previous transaction is already using -
// the second transfer of a pair sent seconds apart would be refused as a
// duplicate. Committed state alone is the right answer for a caller asking what
// the chain has actually settled.
func (e *Engine) NextNonce(sender string, includePending bool) uint64 {
	if sender == "" {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	next := uint64(0)
	if high, seen := e.nonceHigh[sender]; seen {
		next = high + 1
	}
	if !includePending {
		return next
	}
	for i := range e.mempool {
		if _, checked := nonceKey(&e.mempool[i]); !checked {
			continue
		}
		if e.mempool[i].SenderID() != sender {
			continue
		}
		if candidate := e.mempool[i].Nonce + 1; candidate > next {
			next = candidate
		}
	}
	return next
}
