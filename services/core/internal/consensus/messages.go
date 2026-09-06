// Package consensus implements a fast, leader-based BFT-style consensus over a
// fixed validator set for Matrix OS. It gives every participating node an
// agreed-upon, hash-linked, ordered ledger of signed token transfers.
//
// Design (speed first, no proof-of-work):
//
//   - A FIXED validator set of ed25519 keys (reusing token.Account identities).
//     The leader for a round is chosen round-robin by round number.
//   - Each round the leader batches pending signed token.Transaction values into
//     a Block{Height, Round, PrevBlockHash, Txs, ProposerID, Signature} and
//     broadcasts a signed Proposal over a gossip topic.
//   - Validators verify the proposal (correct leader for the round, every tx
//     signature, correct prev-block-hash link) and broadcast a signed Vote.
//   - Once a node collects a quorum of votes (> 2/3 of the fixed set, i.e.
//     2f+1 of 3f+1) it commits the block: it extends a hash-linked committed
//     chain persisted in the Pebble kv.Store under the consensus/* prefix and
//     deterministically applies the block's ordered transactions to the market
//     ledger. A single voting round + immediate commit keeps latency low; the
//     next round is pipelined immediately.
//
// Wire messages (Proposal, Vote) are ed25519-signed using a canonical,
// length-prefixed encoding in the exact style of token.Transaction.SigningBytes,
// so no two distinct field combinations can collide into the same signed bytes.
// Messages travel over the transport.Transport gossip topics; a small Transport
// interface is kept so a multi-node test can wire N engines to an in-memory bus.
//
// This is a REAL global consensus that REPLACES the deliberately per-node
// pairwise settlement in internal/marketexchange: the authoritative, agreed
// ordered ledger is the committed consensus chain, and marketplace settlement
// now flows through it.
package consensus

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// Gossip topic names, versioned so nodes on compatible protocol versions
// rendezvous on the same pubsub topics.
const (
	// TopicProposal carries Proposal messages (a leader's proposed block).
	TopicProposal = "matrix.consensus.v1/proposal"
	// TopicVote carries Vote messages (a validator's vote for a block).
	TopicVote = "matrix.consensus.v1/vote"
)

// HashSize is the length in bytes of a block link hash (SHA-256).
const HashSize = sha256.Size

// genesisPrevHash is the fixed seed PrevBlockHash for the first block. It is
// HashSize zero bytes, mirroring token.Chain's genesis seed.
var genesisPrevHash = make([]byte, HashSize)

// Consensus wire / validation errors. Callers and tests may errors.Is against
// them.
var (
	// ErrUnsignedMessage is returned when a proposal or vote carries no signature.
	ErrUnsignedMessage = errors.New("consensus: message is not signed")
	// ErrInvalidSignature is returned when a signature does not verify against the
	// declared public key and canonical payload.
	ErrInvalidSignature = errors.New("consensus: invalid message signature")
	// ErrInvalidMessage is returned when a message is structurally invalid (bad
	// key length, empty proposer, etc.).
	ErrInvalidMessage = errors.New("consensus: invalid message")
	// ErrWrongLeader is returned when a proposal's proposer is not the correct
	// round-robin leader for its round.
	ErrWrongLeader = errors.New("consensus: proposer is not the leader for this round")
	// ErrNotValidator is returned when a proposal/vote is signed by a key that is
	// not in the fixed validator set.
	ErrNotValidator = errors.New("consensus: signer is not in the validator set")
	// ErrPrevHashMismatch is returned when a block does not link to the expected
	// previous committed block hash.
	ErrPrevHashMismatch = errors.New("consensus: prev-block-hash does not link to head")
)

// Block is an ordered batch of signed transactions proposed for one consensus
// round. Height is the block's 0-based position in the committed chain. Round is
// the consensus round that produced it (used to derive the leader). PrevBlockHash
// links the block to the committed head at proposal time. Txs are the ordered,
// individually-signed token transactions the block commits. ProposerID is the
// leader's account ID (hex of its public key). Signature is the leader's ed25519
// signature over the canonical block bytes.
type Block struct {
	Height        uint64              `json:"height"`
	Round         uint64              `json:"round"`
	PrevBlockHash []byte              `json:"prev_block_hash"`
	Txs           []token.Transaction `json:"txs"`
	ProposerID    string              `json:"proposer_id"`
	Signature     []byte              `json:"signature"`
}

// signingBytes returns the canonical, deterministic, length-prefixed
// serialization of the block's signable fields (everything except Signature),
// mirroring token.Transaction.SigningBytes. Layout (all integers big-endian):
//
//	uint64(Height) | uint64(Round) |
//	uint32(len(PrevBlockHash)) | PrevBlockHash |
//	uint32(len(ProposerID)) | ProposerID |
//	uint64(len(Txs)) | for each tx: uint32(len(txSig)) | txSig
//
// Each transaction is bound by its own signature bytes, which uniquely commit to
// all of that transaction's fields (see token.Transaction.SigningBytes/Sign), so
// the block signature transitively commits to the exact ordered transaction set.
func (b *Block) signingBytes() []byte {
	buf := make([]byte, 0, 64+len(b.PrevBlockHash)+len(b.ProposerID)+len(b.Txs)*72)
	buf = appendUint64(buf, b.Height)
	buf = appendUint64(buf, b.Round)
	buf = appendLenPrefixed(buf, b.PrevBlockHash)
	buf = appendLenPrefixed(buf, []byte(b.ProposerID))
	buf = appendUint64(buf, uint64(len(b.Txs)))
	for i := range b.Txs {
		// Bind each tx by its signature; a tx with no signature contributes an
		// empty length-prefixed slice. Block verification independently rejects
		// unsigned transactions, so this is only about collision-resistance.
		buf = appendLenPrefixed(buf, b.Txs[i].Signature)
	}
	return buf
}

// Hash returns the block's link hash sha256(PrevBlockHash || signingBytes ||
// Signature). Binding the proposer signature into the link means a re-signed
// block produces a distinct hash and cannot silently replace a committed block,
// mirroring token.Chain.hashRecord.
func (b *Block) Hash() []byte {
	h := sha256.New()
	h.Write(b.PrevBlockHash)
	h.Write(b.signingBytes())
	h.Write(b.Signature)
	return h.Sum(nil)
}

// Sign signs the block with priv (which must correspond to a validator public
// key) and stores the signature.
func (b *Block) Sign(priv ed25519.PrivateKey) error {
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: private key must be %d bytes", ErrInvalidMessage, ed25519.PrivateKeySize)
	}
	b.Signature = ed25519.Sign(priv, b.signingBytes())
	return nil
}

// VerifySignature validates the block's proposer signature against pub. It does
// not check leadership, tx validity, or linkage; the engine does that around it.
func (b *Block) VerifySignature(pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key must be %d bytes", ErrInvalidMessage, ed25519.PublicKeySize)
	}
	if len(b.Signature) == 0 {
		return ErrUnsignedMessage
	}
	if !ed25519.Verify(pub, b.signingBytes(), b.Signature) {
		return ErrInvalidSignature
	}
	return nil
}

// Proposal wraps a Block broadcast by the round leader. The block itself carries
// the proposer's identity and signature, so no separate envelope signature is
// required.
type Proposal struct {
	Block Block `json:"block"`
}

// Vote is a validator's signed endorsement of a specific block at a specific
// height/round. VoterID is the validator's account ID (hex of PublicKey).
// BlockHash is the hash of the block being voted for (which binds the vote to
// the exact block contents). Signature is by PublicKey over the canonical vote
// bytes.
type Vote struct {
	Height    uint64            `json:"height"`
	Round     uint64            `json:"round"`
	BlockHash []byte            `json:"block_hash"`
	VoterID   string            `json:"voter_id"`
	PublicKey ed25519.PublicKey `json:"public_key"`
	Signature []byte            `json:"signature"`
}

// signingBytes returns the canonical length-prefixed payload signed by the
// voter, covering every field except Signature.
func (v *Vote) signingBytes() []byte {
	buf := make([]byte, 0, 64+len(v.BlockHash)+len(v.VoterID)+len(v.PublicKey))
	buf = appendUint64(buf, v.Height)
	buf = appendUint64(buf, v.Round)
	buf = appendLenPrefixed(buf, v.BlockHash)
	buf = appendLenPrefixed(buf, []byte(v.VoterID))
	buf = appendLenPrefixed(buf, v.PublicKey)
	return buf
}

// Sign signs the vote with priv, which must correspond to PublicKey.
func (v *Vote) Sign(priv ed25519.PrivateKey) error {
	if err := checkKeyPair(v.PublicKey, priv); err != nil {
		return err
	}
	v.Signature = ed25519.Sign(priv, v.signingBytes())
	return nil
}

// Verify validates the vote's signature and structure, confirming VoterID is
// derived from PublicKey.
func (v *Vote) Verify() error {
	if len(v.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key must be %d bytes", ErrInvalidMessage, ed25519.PublicKeySize)
	}
	if v.VoterID == "" {
		return fmt.Errorf("%w: voter id must not be empty", ErrInvalidMessage)
	}
	if v.VoterID != token.AccountIDFromPublicKey(v.PublicKey) {
		return fmt.Errorf("%w: voter id does not match public key", ErrInvalidMessage)
	}
	if len(v.BlockHash) == 0 {
		return fmt.Errorf("%w: block hash must not be empty", ErrInvalidMessage)
	}
	if len(v.Signature) == 0 {
		return ErrUnsignedMessage
	}
	if !ed25519.Verify(v.PublicKey, v.signingBytes(), v.Signature) {
		return ErrInvalidSignature
	}
	return nil
}

// checkKeyPair confirms priv is a well-formed ed25519 private key whose public
// half equals pub.
func checkKeyPair(pub ed25519.PublicKey, priv ed25519.PrivateKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key must be %d bytes", ErrInvalidMessage, ed25519.PublicKeySize)
	}
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: private key must be %d bytes", ErrInvalidMessage, ed25519.PrivateKeySize)
	}
	if !priv.Public().(ed25519.PublicKey).Equal(pub) {
		return fmt.Errorf("%w: private key does not match public key", ErrInvalidMessage)
	}
	return nil
}

// appendLenPrefixed appends a 4-byte big-endian length followed by the bytes,
// mirroring token.Transaction's canonical encoding.
func appendLenPrefixed(dst, b []byte) []byte {
	var lp [4]byte
	binary.BigEndian.PutUint32(lp[:], uint32(len(b)))
	dst = append(dst, lp[:]...)
	return append(dst, b...)
}

// appendUint64 appends v as 8 big-endian bytes.
func appendUint64(dst []byte, v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return append(dst, b[:]...)
}
