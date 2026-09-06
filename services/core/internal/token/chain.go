package token

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/cockroachdb/pebble"
	"github.com/ecirlabs/matrix-core/internal/kv"
)

// KV key prefixes and keys for the persisted transaction log. Records live
// under token/tx/<height> (height big-endian for lexicographic ordering) and
// the chain head metadata under token/head. These namespaces are distinct from
// the market/* prefixes so the two subsystems never collide in the shared store.
const (
	txKeyPrefix = "token/tx/"
	headKey     = "token/head"
)

// HashSize is the length in bytes of a chain link hash (SHA-256).
const HashSize = sha256.Size

// genesisPrevHash is the fixed seed PrevHash for the first record in the chain.
// It is HashSize zero bytes.
var genesisPrevHash = make([]byte, HashSize)

// Chain errors.
var (
	// ErrNonceMismatch is returned when a transaction's Nonce is not the sender's
	// expected next value, which rejects both replays (nonce too low) and gaps
	// (nonce too high).
	ErrNonceMismatch = errors.New("token: sender nonce mismatch")
	// ErrPrevHashMismatch is returned when a transaction's PrevHash does not link
	// to the current chain head.
	ErrPrevHashMismatch = errors.New("token: prev-hash does not match chain head")
	// ErrHeightOutOfRange is returned when a requested height does not exist.
	ErrHeightOutOfRange = errors.New("token: height out of range")
	// ErrCorruptChain is returned by ValidateChain when a persisted link hash or
	// signature does not verify. The error message identifies the broken height.
	ErrCorruptChain = errors.New("token: corrupt chain")
)

// Record is one entry in the hash-chained transaction log: the transaction plus
// its position (Height) and the link Hash = sha256(PrevHash || canonical(tx)).
type Record struct {
	Height uint64      `json:"height"`
	Tx     Transaction `json:"tx"`
	Hash   []byte      `json:"hash"`
}

// head is the persisted chain-head metadata.
type head struct {
	Height uint64 `json:"height"` // height of the last appended record
	Hash   []byte `json:"hash"`   // link hash of the last appended record
	Length uint64 `json:"length"` // number of records in the chain
}

// Chain is an append-only, SHA-256 hash-chained transaction log persisted in a
// Pebble kv.Store. It enforces signature validity, per-sender monotonic nonces
// (replay protection) and correct hash linkage on every Append. All access is
// guarded by a mutex so concurrent callers observe a consistent head.
type Chain struct {
	store *kv.Store
	mu    sync.Mutex
}

// NewChain creates a Chain backed by store. Existing state (if any) is read
// lazily from the store, so a Chain constructed over a store that already holds
// a chain resumes from the persisted head.
func NewChain(store *kv.Store) *Chain {
	return &Chain{store: store}
}

// txKey returns the KV key for a record at the given height.
func txKey(height uint64) []byte {
	buf := make([]byte, len(txKeyPrefix)+8)
	copy(buf, txKeyPrefix)
	binary.BigEndian.PutUint64(buf[len(txKeyPrefix):], height)
	return buf
}

// hashRecord computes the link hash sha256(prevHash || canonical(tx)).
func hashRecord(prevHash []byte, tx *Transaction) []byte {
	h := sha256.New()
	h.Write(prevHash)
	h.Write(tx.SigningBytes())
	// The signature is bound into the link too so a re-signed transaction
	// produces a distinct hash and cannot silently replace a record.
	h.Write(tx.Signature)
	return h.Sum(nil)
}

// loadHead reads the persisted head metadata. A missing head means an empty
// chain: height 0, genesis prev-hash, length 0. Callers must hold c.mu.
func (c *Chain) loadHead() (head, error) {
	data, err := c.store.Get([]byte(headKey))
	if err != nil {
		return head{}, fmt.Errorf("token: failed to read head: %w", err)
	}
	if data == nil {
		return head{Height: 0, Hash: genesisPrevHash, Length: 0}, nil
	}
	var h head
	if err := json.Unmarshal(data, &h); err != nil {
		return head{}, fmt.Errorf("token: failed to decode head: %w", err)
	}
	return h, nil
}

// nextNonce returns the expected next nonce for sender: the number of
// transactions already accepted from that sender. The first transaction from a
// sender must therefore carry Nonce 0. Callers must hold c.mu.
func (c *Chain) nextNonce(sender string) (uint64, error) {
	var count uint64
	err := c.store.Iterate([]byte(txKeyPrefix), func(_, value []byte) error {
		var rec Record
		if err := json.Unmarshal(value, &rec); err != nil {
			return fmt.Errorf("token: failed to decode record: %w", err)
		}
		if rec.Tx.SenderID() == sender {
			count++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// Append validates and appends a transaction to the chain. It (a) verifies the
// signature, (b) checks the sender nonce equals the expected next value
// (rejecting replays and gaps), (c) links PrevHash to the current head hash,
// (d) persists the new record and advanced head atomically in a single batch,
// and (e) returns the persisted Record. On any validation failure nothing is
// written. The caller-provided tx.PrevHash must equal the current head hash;
// use HeadHash to obtain it before signing.
func (c *Chain) Append(tx *Transaction) (*Record, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := tx.Verify(); err != nil {
		return nil, err
	}

	h, err := c.loadHead()
	if err != nil {
		return nil, err
	}

	expectedNonce, err := c.nextNonce(tx.SenderID())
	if err != nil {
		return nil, err
	}
	if tx.Nonce != expectedNonce {
		return nil, fmt.Errorf("%w: sender %s expected nonce %d, got %d", ErrNonceMismatch, tx.SenderID(), expectedNonce, tx.Nonce)
	}

	if !bytes.Equal(tx.PrevHash, h.Hash) {
		return nil, fmt.Errorf("%w: expected %x, got %x", ErrPrevHashMismatch, h.Hash, tx.PrevHash)
	}

	newHeight := h.Length // heights are 0-based and equal to length-before-append
	rec := Record{
		Height: newHeight,
		Tx:     *tx,
		Hash:   hashRecord(tx.PrevHash, tx),
	}
	recBytes, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("token: failed to encode record: %w", err)
	}

	newHead := head{Height: newHeight, Hash: rec.Hash, Length: h.Length + 1}
	headBytes, err := json.Marshal(newHead)
	if err != nil {
		return nil, fmt.Errorf("token: failed to encode head: %w", err)
	}

	batch := c.store.NewBatch()
	defer batch.Close()
	if err := batch.Set(txKey(newHeight), recBytes, nil); err != nil {
		return nil, fmt.Errorf("token: failed to stage record: %w", err)
	}
	if err := batch.Set([]byte(headKey), headBytes, nil); err != nil {
		return nil, fmt.Errorf("token: failed to stage head: %w", err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return nil, fmt.Errorf("token: failed to commit append: %w", err)
	}

	out := rec
	return &out, nil
}

// Len returns the number of records in the chain.
func (c *Chain) Len() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, err := c.loadHead()
	if err != nil {
		return 0, err
	}
	return h.Length, nil
}

// HeadHash returns the current head link hash, which is the value a new
// transaction must place in its PrevHash. For an empty chain it returns the
// genesis seed hash (all zeros).
func (c *Chain) HeadHash() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, err := c.loadHead()
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(h.Hash))
	copy(out, h.Hash)
	return out, nil
}

// Head returns the current head metadata as (height, length). When the chain is
// empty length is 0 and height is 0.
func (c *Chain) Head() (height uint64, length uint64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, err := c.loadHead()
	if err != nil {
		return 0, 0, err
	}
	return h.Height, h.Length, nil
}

// TransactionAt returns the record at the given height.
func (c *Chain) TransactionAt(height uint64) (*Record, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readRecord(height)
}

// readRecord loads and decodes the record at height. Callers must hold c.mu.
func (c *Chain) readRecord(height uint64) (*Record, error) {
	data, err := c.store.Get(txKey(height))
	if err != nil {
		return nil, fmt.Errorf("token: failed to read record %d: %w", height, err)
	}
	if data == nil {
		return nil, fmt.Errorf("%w: %d", ErrHeightOutOfRange, height)
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("token: failed to decode record %d: %w", height, err)
	}
	return &rec, nil
}

// ValidateChain walks the entire chain from genesis and confirms, for every
// record, that (1) the transaction signature verifies and (2) the stored link
// hash equals sha256(prevHash || canonical(tx) || signature) with prevHash
// threaded from the previous record (genesis seed for height 0). It returns an
// error wrapping ErrCorruptChain that identifies the first broken height, or nil
// when the whole chain is intact.
func (c *Chain) ValidateChain() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	h, err := c.loadHead()
	if err != nil {
		return err
	}

	prevHash := genesisPrevHash
	for height := uint64(0); height < h.Length; height++ {
		rec, err := c.readRecord(height)
		if err != nil {
			return err
		}
		if err := rec.Tx.Verify(); err != nil {
			return fmt.Errorf("%w: signature invalid at height %d: %v", ErrCorruptChain, height, err)
		}
		if !bytes.Equal(rec.Tx.PrevHash, prevHash) {
			return fmt.Errorf("%w: prev-hash mismatch at height %d", ErrCorruptChain, height)
		}
		want := hashRecord(prevHash, &rec.Tx)
		if !bytes.Equal(rec.Hash, want) {
			return fmt.Errorf("%w: link hash mismatch at height %d", ErrCorruptChain, height)
		}
		prevHash = rec.Hash
	}

	if !bytes.Equal(prevHash, h.Hash) {
		return fmt.Errorf("%w: head hash does not match final link", ErrCorruptChain)
	}
	return nil
}
