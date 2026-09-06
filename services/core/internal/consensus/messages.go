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
//     broadcasts it inside a signed Proposal envelope over a gossip topic. The
//     envelope is separate from the block so the same value can be re-proposed
//     at a later round without changing its identity.
//   - Validators verify the proposal (correct leader for the round, every tx
//     signature, correct prev-block-hash link) and PREVOTE it - or prevote nil,
//     if a lock forbids supporting it.
//   - A quorum of prevotes for one block at one round is a POLKA. On seeing one,
//     a validator PRECOMMITS that block, which also LOCKS it: from then on it
//     will not prevote a different block at that height unless shown a polka for
//     that other block at a round >= its lock.
//   - A quorum of precommits for one block at one round COMMITS it: the node
//     extends a hash-linked committed chain persisted in the Pebble kv.Store
//     under the consensus/* prefix, deterministically applies the block's
//     ordered transactions to the market ledger, and pipelines the next height
//     immediately.
//
// Two voting phases rather than one, because one cannot be both safe and live
// here. A validator that locks on a block the moment it votes can end up locked
// on a block no other node ever saw, and only a quorum for that block could
// release it - so validators locked on different blocks deadlock the height
// forever, spinning through rounds at full speed. Locking on a precommit, which
// is only cast after a polka has been observed, means every lock in the network
// is backed by evidence that some leader can collect and show to the others,
// which is what lets them converge. A validator that is not locked prevotes
// whatever the current leader proposes, so a value nobody else saw costs a round
// rather than the height.
//
// A node that misses a committed block obtains it from a peer over the block
// sync topics, with the precommit quorum that committed it as proof (see
// BlockSyncRequest). Without that path, a single dropped proposal stalls a node
// at that height permanently: the votes for the block keep arriving but the body
// never does, because the network has moved on and no leader re-proposes a
// committed block.
//
// Wire messages (Proposal, Vote) are ed25519-signed using a canonical,
// length-prefixed encoding in the exact style of token.Transaction.SigningBytes,
// so no two distinct field combinations can collide into the same signed bytes.
// Messages travel over the transport.Transport gossip topics; a small Transport
// interface is kept so a multi-node test can wire N engines to an in-memory bus.
//
// This is a REAL global consensus that provides an authoritative, agreed,
// hash-linked ordered ledger of settlements. It is the settlement path new
// inference marketplace flows use (see internal/inference). NOTE: it does NOT
// yet replace the pre-existing per-node pairwise settlement in
// internal/marketexchange nor the token-chain SubmitSignedTransfer path; those
// still write to the same market ledger. Until they are routed through
// consensus, multiple settlement paths coexist on one ledger and consensus is
// the authoritative path only for the flows that submit through it. See the
// node wiring notes in internal/node/node.go for the current authority model.
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
//
// v2 introduced the two voting phases and the proposal envelope, both of which
// change the wire format incompatibly: a v1 node's vote carries no type and a
// v1 proposal carries no envelope signature, so neither can be acted on by a
// v2 node (nor the reverse). Sharing a topic across that boundary would look
// like a network fault - messages arriving and being silently discarded - so
// the topics move with the protocol and the two versions simply do not meet.
const (
	// TopicProposal carries Proposal messages (a leader's proposed block).
	TopicProposal = "matrix.consensus.v2/proposal"
	// TopicVote carries Vote messages (a validator's vote for a block).
	TopicVote = "matrix.consensus.v2/vote"
	// TopicSyncRequest carries BlockSyncRequest messages (a lagging node asking
	// for committed block bodies it never received).
	TopicSyncRequest = "matrix.consensus.v2/sync-request"
	// TopicSyncResponse carries BlockSyncResponse messages (a caught-up node
	// serving committed blocks and the votes that endorsed them).
	TopicSyncResponse = "matrix.consensus.v2/sync-response"
	// TopicHead carries HeadAnnounce messages (a node's committed chain length).
	TopicHead = "matrix.consensus.v2/head"
)

// Block-sync bounds. A response is capped both by block count and by encoded
// size so one catch-up message cannot become unboundedly large on a gossip
// topic; a node further behind than one batch simply asks again after it
// applies what it got.
const (
	// MaxSyncBatch is the greatest number of committed blocks one response
	// carries.
	MaxSyncBatch = 8
	// MaxSyncResponseBytes caps the encoded size of one response. Blocks are
	// added to a batch until the next one would cross it.
	MaxSyncResponseBytes = 1 << 20
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
//
// A Block is IMMUTABLE once built, and that is load-bearing. Height, Round,
// ProposerID and Signature are all part of its identity (see Hash), so a
// validator that re-proposes a block at a later round must forward these exact
// bytes rather than re-sign them: re-signing produces a different hash, which
// means a different block, which every validator locked on the original will
// correctly refuse to vote for - and the height can then never commit. The
// round a proposal is being made FOR therefore lives on the Proposal envelope,
// not here; Round records only the round at which this value first appeared.
type Block struct {
	Height        uint64              `json:"height"`
	Round         uint64              `json:"round"`
	PrevBlockHash []byte              `json:"prev_block_hash"`
	Txs           []token.Transaction `json:"txs"`
	ProposerID    string              `json:"proposer_id"`
	Signature     []byte              `json:"signature"`
}

// PolkaCertificate is a quorum of PREVOTES for one block at one (height,
// round),
// proving that block gathered (or could gather) enough support to commit at that
// round. A leader proposing at round r > 0 attaches the certificate for the
// highest earlier round it knows a block was polka'd at, so a validator that
// locked a block in a prior round can verify the network moved on and safely
// release its lock. The engine validates every vote in the certificate against
// the validator set and confirms the set forms a quorum before honoring it.
type PolkaCertificate struct {
	// Height is the height all votes in the certificate are for.
	Height uint64 `json:"height"`
	// Round is the round all votes in the certificate are for (strictly less than
	// the round of the proposal carrying it).
	Round uint64 `json:"round"`
	// BlockHash is the block hash all votes in the certificate endorse.
	BlockHash []byte `json:"block_hash"`
	// Votes are the individual signed votes; each is verified independently and
	// distinct voters are counted toward the quorum.
	Votes []Vote `json:"votes"`
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

// Proposal is the envelope a round's leader broadcasts to put a block to the
// vote at that round. It is separate from the Block because the same value has
// to be proposable more than once.
//
// When a round times out after validators have locked on a block but before a
// quorum of votes reached anyone, the next leader is REQUIRED to re-propose
// that same locked value - a locked validator will not vote for anything else
// without a certificate proving the network moved on. If re-proposing meant
// rewriting the block's round and proposer and re-signing it, the block's hash
// would change, the locked validators would see a different block, and they
// would all refuse it: the height would be dead with a valid value that a
// quorum wanted. The envelope is what makes re-proposal possible - the leader
// forwards the original block bytes untouched and signs the envelope around
// them, so the block's identity, and the votes already cast for it, survive
// rotation.
//
// Round is the round this proposal is for and must be >= Block.Round.
// ProposerID is the validator making it (the leader for Round, which is not
// necessarily the block's original proposer). Signature is by ProposerID over
// the block hash, the round and the proposer id.
//
// Justify carries the lock/polka certificate that makes a round > 0 proposal
// safe under leader rotation (see the voting discipline in engine.go). It is
// nil for a round-0 proposal (no prior round to justify) and MUST be present
// and valid for a proposal that asks locked validators to switch to a
// different block. The certificate is a quorum of votes for a block at an
// earlier round of the SAME height; it proves the network reached (or could
// have reached) a quorum on that block, which is what lets a locked validator
// safely release its lock. It rides on the envelope rather than in the block
// precisely because it must not affect the block's identity.
type Proposal struct {
	Block      Block             `json:"block"`
	Round      uint64            `json:"round"`
	ProposerID string            `json:"proposer_id"`
	Signature  []byte            `json:"signature"`
	Justify    *PolkaCertificate `json:"justify,omitempty"`
}

// signingBytes returns the canonical length-prefixed payload the envelope
// proposer signs: the hash of the block being proposed, the round it is
// proposed for, and the proposer's id.
func (p *Proposal) signingBytes() []byte {
	hash := p.Block.Hash()
	buf := make([]byte, 0, 32+len(hash)+len(p.ProposerID))
	buf = appendLenPrefixed(buf, hash)
	buf = appendUint64(buf, p.Round)
	buf = appendLenPrefixed(buf, []byte(p.ProposerID))
	return buf
}

// Sign signs the envelope with priv, which must correspond to the public key of
// ProposerID.
func (p *Proposal) Sign(priv ed25519.PrivateKey) error {
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: private key must be %d bytes", ErrInvalidMessage, ed25519.PrivateKeySize)
	}
	p.Signature = ed25519.Sign(priv, p.signingBytes())
	return nil
}

// VerifySignature validates the envelope signature against pub. It does not
// check leadership or the block itself; the engine does that around it.
func (p *Proposal) VerifySignature(pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key must be %d bytes", ErrInvalidMessage, ed25519.PublicKeySize)
	}
	if p.ProposerID == "" {
		return fmt.Errorf("%w: proposal proposer must not be empty", ErrInvalidMessage)
	}
	if len(p.Signature) == 0 {
		return ErrUnsignedMessage
	}
	if !ed25519.Verify(pub, p.signingBytes(), p.Signature) {
		return ErrInvalidSignature
	}
	return nil
}

// HeadAnnounce is a node's periodic statement of how much chain it has
// committed.
//
// It is what makes a lagging node's recovery independent of traffic. The other
// signals that a node is behind - votes and proposals for later heights, a
// quorum whose body never arrived - all require the network to be busy. A node
// that was down while blocks were committed, and comes back to an idle network,
// would see none of them and would sit at its old height indefinitely. Hearing
// a peer announce a greater height is direct evidence to ask for the
// difference.
type HeadAnnounce struct {
	// Height is the announcing node's committed chain length, i.e. the next
	// height it expects to commit.
	Height uint64 `json:"height"`
	// NodeID identifies the announcing node (informational).
	NodeID string `json:"node_id,omitempty"`
}

// BlockSyncRequest asks peers for the committed blocks starting at Height.
//
// It exists because gossip is best-effort and a node that misses the ONE
// proposal that committed at its current height has no other way to obtain that
// block body. Votes for it keep arriving and can even reach a quorum, but a
// quorum without the body cannot commit: the rest of the network has moved on
// to later heights and will never re-propose the block. Before block sync such
// a node was stalled permanently - it could stash every later proposal as a
// future height and never apply any of them. The request is broadcast on
// TopicSyncRequest; every node that has the height answers on
// TopicSyncResponse.
type BlockSyncRequest struct {
	// Height is the first height the requester is missing (its current height).
	Height uint64 `json:"height"`
	// RequesterID identifies the asking node. It is informational - responses are
	// broadcast to the topic, not addressed - and lets an operator see which node
	// is behind.
	RequesterID string `json:"requester_id,omitempty"`
}

// CommittedBlock is a committed block body together with the votes that
// endorsed it at that height.
//
// The votes are what make a synced block safe to act on without trusting the
// sender: the receiver verifies each one independently (signature, validator
// membership, matching height and block hash) and feeds them through the same
// tally that ordinary vote gossip goes through, so a synced block commits under
// exactly the same quorum rule as one that arrived by proposal. A response with
// too few valid votes leaves the receiver where it was rather than committing
// anything.
type CommittedBlock struct {
	Block Block  `json:"block"`
	Votes []Vote `json:"votes"`
}

// BlockSyncResponse carries committed blocks in ascending height order,
// starting at the requested height.
type BlockSyncResponse struct {
	Blocks []CommittedBlock `json:"blocks"`
}

// VoteType distinguishes the two rounds of voting a block goes through.
type VoteType uint8

const (
	// VoteTypePrevote is the first phase: "this is the block I see for this
	// round". A quorum of prevotes for one block is a POLKA - it proves the
	// network can see that block, and it is what a validator must be shown before
	// it will abandon a block it has locked.
	VoteTypePrevote VoteType = 1
	// VoteTypePrecommit is the second phase, cast only after seeing a polka:
	// "this block has quorum support, I am committing to it". A quorum of
	// precommits for one block COMMITS it, and casting one locks the validator.
	VoteTypePrecommit VoteType = 2
)

// String renders the vote type for errors and logs.
func (t VoteType) String() string {
	switch t {
	case VoteTypePrevote:
		return "prevote"
	case VoteTypePrecommit:
		return "precommit"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(t))
	}
}

// Vote is a validator's signed statement about a specific block at a specific
// height/round. VoterID is the validator's account ID (hex of PublicKey).
// BlockHash is the hash of the block being voted for (which binds the vote to
// the exact block contents), or the nil marker to vote for no block at all.
// Signature is by PublicKey over the canonical vote bytes.
//
// Type is what makes the protocol safe under leader rotation. A single voting
// phase cannot be both safe and live here: a validator that locks on a block
// the moment it votes can end up locked on a block no one else ever saw, and
// nothing short of a quorum for that block can release it - so a set of
// validators locked on different blocks deadlocks the height forever. Locking
// on a PRECOMMIT, which is only cast after a quorum of prevotes has been seen,
// means every lock is backed by evidence a proposer can gather and show to the
// others, which is exactly what lets them converge.
type Vote struct {
	Type      VoteType          `json:"type"`
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
	buf = appendUint64(buf, uint64(v.Type))
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
	if v.Type != VoteTypePrevote && v.Type != VoteTypePrecommit {
		return fmt.Errorf("%w: vote type %s", ErrInvalidMessage, v.Type)
	}
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

// nilVoteHash is the BlockHash a validator votes with to say "at this round I
// could not vote for any block". It is HashSize zero bytes, which no real block
// hash can be: a block hash is a SHA-256 digest over a non-empty preimage.
//
// Nil votes exist for liveness. Without them a round in which nothing could be
// agreed leaves no trace, and a validator locked on a block has no way to learn
// that the network did not agree on anything - so it holds its lock forever
// (see RoundCertificate).
func nilVoteHash() []byte { return make([]byte, HashSize) }

// isNilVoteHash reports whether a block hash is the nil marker.
func isNilVoteHash(hash []byte) bool {
	if len(hash) != HashSize {
		return false
	}
	for _, b := range hash {
		if b != 0 {
			return false
		}
	}
	return true
}

// Verify validates the certificate against a validator set: it confirms the
// certificate is structurally sound, every vote individually verifies, is cast
// by a distinct member of vs for the certificate's exact (height, round,
// blockHash), and that the number of distinct valid voters reaches quorum. It
// returns nil when the certificate proves a quorum polka'd BlockHash at
// (Height, Round). A nil certificate is not valid.
func (c *PolkaCertificate) Verify(vs *ValidatorSet) error {
	if c == nil {
		return fmt.Errorf("%w: nil certificate", ErrInvalidMessage)
	}
	if len(c.BlockHash) == 0 {
		return fmt.Errorf("%w: certificate block hash must not be empty", ErrInvalidMessage)
	}
	if isNilVoteHash(c.BlockHash) {
		return fmt.Errorf("%w: certificate cannot endorse the nil block", ErrInvalidMessage)
	}
	seen := make(map[string]struct{}, len(c.Votes))
	for i := range c.Votes {
		v := &c.Votes[i]
		// Only prevotes form a polka. A precommit quorum is a commit, not a
		// permission to abandon a lock, and must never be read as one.
		if v.Type != VoteTypePrevote {
			return fmt.Errorf("%w: certificate vote %d is a %s, not a prevote", ErrInvalidMessage, i, v.Type)
		}
		if v.Height != c.Height || v.Round != c.Round {
			return fmt.Errorf("%w: certificate vote %d height/round mismatch", ErrInvalidMessage, i)
		}
		if !bytesEqual(v.BlockHash, c.BlockHash) {
			return fmt.Errorf("%w: certificate vote %d block hash mismatch", ErrInvalidMessage, i)
		}
		if !vs.Contains(v.VoterID) {
			return fmt.Errorf("%w: certificate vote %d not a validator", ErrNotValidator, i)
		}
		if err := v.Verify(); err != nil {
			return fmt.Errorf("%w: certificate vote %d: %v", ErrInvalidSignature, i, err)
		}
		seen[v.VoterID] = struct{}{}
	}
	if len(seen) < vs.Quorum() {
		return fmt.Errorf("%w: certificate has %d distinct voters, need quorum %d", ErrInvalidMessage, len(seen), vs.Quorum())
	}
	return nil
}

// bytesEqual reports whether two byte slices are equal.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
