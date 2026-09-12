package consensus

import (
	"encoding/hex"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// Finding a transaction by the id Ethereum tooling uses.
//
// A wallet sends a transaction, is handed its keccak256 id, and then polls for a
// receipt under that id until one appears. Without a way to answer that lookup
// the send succeeds and the wallet shows it pending forever, which reads to the
// user as a chain that ate their transfer. An exchange crediting deposits has
// the same requirement for the same reason.
//
// The index is in memory and rebuilt from the committed chain at startup,
// alongside the replay and nonce bookkeeping that is already rebuilt there. It
// holds one map entry per enveloped transaction, which is the same order of
// magnitude as the dedup set living beside it.

// TxLocation is where a transaction sits in the committed chain.
type TxLocation struct {
	// Height is the block that carries it.
	Height uint64
	// Index is its position within that block's transactions.
	Index int
}

// recordEVMIndexLocked indexes a committed transaction under its Ethereum id, if
// it has one. Callers must hold e.mu.
func (e *Engine) recordEVMIndexLocked(tx *token.Transaction, at TxLocation) {
	if !tx.IsEVM() {
		return
	}
	hash, err := tx.EVMHash()
	if err != nil {
		return
	}
	e.evmIndex[hex.EncodeToString(hash)] = at
}

// EVMTransaction looks up a committed transaction by its Ethereum id, given as
// lowercase hex without the 0x prefix. It returns a copy, so a caller cannot
// reach into committed state.
func (e *Engine) EVMTransaction(hashHex string) (*token.Transaction, TxLocation, bool) {
	e.mu.Lock()
	at, ok := e.evmIndex[hashHex]
	e.mu.Unlock()
	if !ok {
		return nil, TxLocation{}, false
	}
	block, err := e.chain.BlockAt(at.Height)
	if err != nil || at.Index >= len(block.Txs) {
		return nil, TxLocation{}, false
	}
	tx := block.Txs[at.Index]
	return &tx, at, true
}

// PendingEVMTransaction looks up a transaction still in this node's mempool by
// its Ethereum id.
//
// A wallet polls for a receipt within a second of sending, well before the
// transaction commits. Answering "unknown" there is indistinguishable from
// answering "dropped", so a caller that can see the mempool can at least report
// the transaction as known and pending.
func (e *Engine) PendingEVMTransaction(hashHex string) (*token.Transaction, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.mempool {
		if !e.mempool[i].IsEVM() {
			continue
		}
		hash, err := e.mempool[i].EVMHash()
		if err != nil {
			continue
		}
		if hex.EncodeToString(hash) == hashHex {
			tx := e.mempool[i]
			return &tx, true
		}
	}
	return nil, false
}
