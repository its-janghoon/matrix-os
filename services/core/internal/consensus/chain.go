package consensus

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/cockroachdb/pebble"
	"github.com/ecirlabs/matrix-core/internal/kv"
)

// KV key prefixes/keys for the persisted committed-block ledger. Blocks live
// under consensus/block/<height> (height big-endian for lexicographic ordering)
// and the chain head metadata under consensus/head. These namespaces are
// DISJOINT from token/* and market/* so the consensus ledger never collides with
// the per-node token chain or the balances in the shared store.
const (
	blockKeyPrefix = "consensus/block/"
	headKey        = "consensus/head"
)

// BlockChain errors.
var (
	// ErrHeightMismatch is returned when a committed block's Height is not the
	// chain's next expected height.
	ErrHeightMismatch = errors.New("consensus: block height mismatch")
	// ErrHeightOutOfRange is returned when a requested height does not exist.
	ErrHeightOutOfRange = errors.New("consensus: height out of range")
	// ErrCorruptChain is returned by ValidateChain when a persisted link hash does
	// not verify.
	ErrCorruptChain = errors.New("consensus: corrupt block chain")
)

// chainHead is the persisted committed-chain head metadata.
type chainHead struct {
	Height uint64 `json:"height"` // height of the last committed block
	Hash   []byte `json:"hash"`   // link hash of the last committed block
	Length uint64 `json:"length"` // number of committed blocks
}

// BlockChain is an append-only, SHA-256 hash-linked log of committed consensus
// Blocks persisted in a Pebble kv.Store under the consensus/* prefix. It mirrors
// token.Chain's persistence style: each Commit writes the block and the advanced
// head atomically in one batch, so the head can never drift from the stored
// blocks. All access is guarded by a mutex so concurrent readers observe a
// consistent head.
type BlockChain struct {
	store *kv.Store
	mu    sync.Mutex
}

// NewBlockChain creates a BlockChain backed by store. Existing state (if any) is
// read lazily, so a BlockChain over a store that already holds committed blocks
// resumes from the persisted head.
func NewBlockChain(store *kv.Store) *BlockChain {
	return &BlockChain{store: store}
}

// blockKey returns the KV key for a block at the given height.
func blockKey(height uint64) []byte {
	buf := make([]byte, len(blockKeyPrefix)+8)
	copy(buf, blockKeyPrefix)
	binary.BigEndian.PutUint64(buf[len(blockKeyPrefix):], height)
	return buf
}

// loadHead reads the persisted head metadata. A missing head means an empty
// chain: height 0, genesis prev-hash, length 0. Callers must hold c.mu.
func (c *BlockChain) loadHead() (chainHead, error) {
	data, err := c.store.Get([]byte(headKey))
	if err != nil {
		return chainHead{}, fmt.Errorf("consensus: failed to read head: %w", err)
	}
	if data == nil {
		return chainHead{Height: 0, Hash: genesisPrevHash, Length: 0}, nil
	}
	var h chainHead
	if err := json.Unmarshal(data, &h); err != nil {
		return chainHead{}, fmt.Errorf("consensus: failed to decode head: %w", err)
	}
	return h, nil
}

// Head returns the current committed head hash and the chain length. For an
// empty chain it returns the genesis seed hash (all zeros) and length 0. The
// returned hash is what the next block must place in its PrevBlockHash.
func (c *BlockChain) Head() (hash []byte, length uint64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, err := c.loadHead()
	if err != nil {
		return nil, 0, err
	}
	out := make([]byte, len(h.Hash))
	copy(out, h.Hash)
	return out, h.Length, nil
}

// Len returns the number of committed blocks.
func (c *BlockChain) Len() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, err := c.loadHead()
	if err != nil {
		return 0, err
	}
	return h.Length, nil
}

// Commit appends an already-agreed block to the committed chain. It enforces
// that (a) the block's Height equals the chain's next expected height and (b)
// its PrevBlockHash links to the current head hash, then persists the block and
// the advanced head atomically in a single batch. It returns the block's link
// hash. Commit is idempotent-safe against the caller: the engine guarantees a
// block is only committed once, and the height/prev-hash gate here rejects any
// out-of-order or forked commit so the persisted chain is always a single linear
// history.
//
// Commit does NOT re-verify signatures or leadership; those are enforced by the
// engine before a block reaches quorum. It is the durability + linkage step,
// exactly analogous to token.Chain.Append's persistence half.
func (c *BlockChain) Commit(b *Block) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	h, err := c.loadHead()
	if err != nil {
		return nil, err
	}

	if b.Height != h.Length {
		return nil, fmt.Errorf("%w: expected height %d, got %d", ErrHeightMismatch, h.Length, b.Height)
	}
	if !bytes.Equal(b.PrevBlockHash, h.Hash) {
		return nil, fmt.Errorf("%w: expected %x, got %x", ErrPrevHashMismatch, h.Hash, b.PrevBlockHash)
	}

	linkHash := b.Hash()
	blockBytes, err := json.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("consensus: failed to encode block: %w", err)
	}
	newHead := chainHead{Height: b.Height, Hash: linkHash, Length: h.Length + 1}
	headBytes, err := json.Marshal(newHead)
	if err != nil {
		return nil, fmt.Errorf("consensus: failed to encode head: %w", err)
	}

	batch := c.store.NewBatch()
	defer batch.Close()
	if err := batch.Set(blockKey(b.Height), blockBytes, nil); err != nil {
		return nil, fmt.Errorf("consensus: failed to stage block: %w", err)
	}
	if err := batch.Set([]byte(headKey), headBytes, nil); err != nil {
		return nil, fmt.Errorf("consensus: failed to stage head: %w", err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return nil, fmt.Errorf("consensus: failed to commit block: %w", err)
	}

	out := make([]byte, len(linkHash))
	copy(out, linkHash)
	return out, nil
}

// BlockAt returns the committed block at the given height.
func (c *BlockChain) BlockAt(height uint64) (*Block, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readBlock(height)
}

// readBlock loads and decodes the block at height. Callers must hold c.mu.
func (c *BlockChain) readBlock(height uint64) (*Block, error) {
	data, err := c.store.Get(blockKey(height))
	if err != nil {
		return nil, fmt.Errorf("consensus: failed to read block %d: %w", height, err)
	}
	if data == nil {
		return nil, fmt.Errorf("%w: %d", ErrHeightOutOfRange, height)
	}
	var b Block
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("consensus: failed to decode block %d: %w", height, err)
	}
	return &b, nil
}

// ValidateChain walks the entire committed chain from genesis and confirms, for
// every block, that its stored link hash equals sha256(prevHash || signingBytes
// || signature) with prevHash threaded from the previous block (genesis seed for
// height 0). It returns an error wrapping ErrCorruptChain identifying the first
// broken height, or nil when the chain is intact.
func (c *BlockChain) ValidateChain() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	h, err := c.loadHead()
	if err != nil {
		return err
	}
	prevHash := genesisPrevHash
	for height := uint64(0); height < h.Length; height++ {
		b, err := c.readBlock(height)
		if err != nil {
			return err
		}
		if !bytes.Equal(b.PrevBlockHash, prevHash) {
			return fmt.Errorf("%w: prev-hash mismatch at height %d", ErrCorruptChain, height)
		}
		prevHash = b.Hash()
	}
	if !bytes.Equal(prevHash, h.Hash) {
		return fmt.Errorf("%w: head hash does not match final link", ErrCorruptChain)
	}
	return nil
}
