package consensus

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sort"
)

// Protocol versioning and the state commitment.
//
// These live together because they answer the same question from two sides: are
// we all running the same rules, and did those rules produce the same result.
// The version says what a node MEANT to do; the state root says what its ledger
// actually holds.

// ProtocolUpgrade activates a protocol version at a height.
//
// This is the mechanism that makes a future rule change a release rather than a
// coordinated restart. Operators add an entry with a height far enough out for
// everyone to upgrade, roll the binary, and every node switches rules at the
// same height because the height is agreed rather than the moment. A node that
// has not upgraded by then stops voting, which is the loud failure; before the
// field existed the only alternative was everyone stopping and starting together
// at a time agreed out of band.
type ProtocolUpgrade struct {
	// Height is the first height at which Version applies.
	Height uint64
	// Version is the protocol version from that height on.
	Version uint32
}

// normalizeUpgrades sorts a schedule by height and puts the genesis version at
// its head, so lookup is a scan from the end and a config that omits the
// starting version still has one.
func normalizeUpgrades(in []ProtocolUpgrade) ([]ProtocolUpgrade, error) {
	out := make([]ProtocolUpgrade, 0, len(in)+1)
	out = append(out, ProtocolUpgrade{Height: 0, Version: ProtocolVersionGenesis})
	out = append(out, in...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Height < out[j].Height })

	for i := 1; i < len(out); i++ {
		if out[i].Height == out[i-1].Height && out[i].Version != out[i-1].Version {
			return nil, fmt.Errorf("%w: two protocol versions activate at height %d (%d and %d)",
				ErrInvalidMessage, out[i].Height, out[i-1].Version, out[i].Version)
		}
		// A schedule that goes backwards would have the chain downgrade its own
		// rules mid-run, which no node could apply consistently with what it has
		// already committed.
		if out[i].Version < out[i-1].Version {
			return nil, fmt.Errorf("%w: protocol version %d at height %d is lower than %d before it",
				ErrInvalidMessage, out[i].Version, out[i].Height, out[i-1].Version)
		}
	}
	return out, nil
}

// protocolVersionAt returns the version in force at a height.
func (e *Engine) protocolVersionAt(height uint64) uint32 {
	version := ProtocolVersionGenesis
	for _, u := range e.protocolUpgrades {
		if u.Height > height {
			break
		}
		version = u.Version
	}
	return version
}

// verifyBlockVersionLocked refuses a block built under rules this node does not
// expect at that height. Callers must hold e.mu.
//
// Both directions are a refusal, and both are correct. A LOWER version than
// expected is a node that has not upgraded still proposing; a HIGHER one is a
// node that upgraded early, or this node that has not upgraded at all. Either
// way the two are not running the same rules and a vote would be a claim they
// are.
func (e *Engine) verifyBlockVersionLocked(b *Block) error {
	want := e.protocolVersionAt(b.Height)
	if b.Version != want {
		return fmt.Errorf("%w: block %d is protocol version %d, this node expects %d at that height",
			ErrInvalidMessage, b.Height, b.Version, want)
	}
	return nil
}

// stateRootLocked returns the digest of this node's ledger as it stands, which
// is the state after everything up to height-1 has been applied. Callers must
// hold e.mu.
//
// It is cached because it is a fold over every balance, and both proposing and
// validating a block need it at the same height. The cache is invalidated by
// applying a block, which is the only thing that changes the answer.
func (e *Engine) stateRootLocked() []byte {
	// Keyed on the ledger's write epoch and not on the height. The ledger is
	// written from more than the block-apply path - genesis seeds it, and on a
	// single-node network so does funding - so a cache keyed on height alone
	// serves a digest taken BEFORE such a write to a comparison made after it,
	// and two nodes then disagree forever about a state they actually share.
	epoch := e.ledger.WriteEpoch()
	if e.stateRootEpoch == epoch && e.stateRoot != nil {
		return e.stateRoot
	}
	digest, err := e.ledger.StateDigest()
	if err != nil {
		// A ledger that cannot be read is not a state this node can commit to.
		// Returning nil makes the proposal carry no root, which every validator
		// then rejects - louder than proposing a root that is a guess.
		fmt.Printf("consensus: could not compute the ledger state digest: %v\n", err)
		return nil
	}
	e.stateRoot = digest
	e.stateRootEpoch = epoch
	return digest
}

// verifyBlockStateRootLocked refuses a block whose view of the ledger disagrees
// with this node's. Callers must hold e.mu.
//
// THIS IS THE CHECK THE CHAIN HAD NONE OF. Before it, two nodes applying the
// same ordered transactions differently produced identical block hashes and
// different balances, and no code path anywhere noticed. Refusing to vote halts
// this node rather than letting it keep signing blocks built on a ledger it does
// not share - which is the right trade: a halted node that says why is
// recoverable, and a quietly diverged one is not.
func (e *Engine) verifyBlockStateRootLocked(b *Block) error {
	mine := e.stateRootLocked()
	if mine == nil {
		return fmt.Errorf("%w: this node cannot compute its own state digest, so it cannot "+
			"check block %d's state root", ErrInvalidMessage, b.Height)
	}
	if len(b.StateRoot) == 0 {
		return fmt.Errorf("%w: block %d carries no state root", ErrInvalidMessage, b.Height)
	}
	if !bytes.Equal(b.StateRoot, mine) {
		return fmt.Errorf("%w: block %d says the ledger is %s but this node's is %s - "+
			"the two nodes have applied the same transactions and reached different balances, "+
			"which means they are running different rules; compare binaries and genesis before "+
			"restarting either",
			ErrInvalidMessage, b.Height, hex.EncodeToString(b.StateRoot[:8]), hex.EncodeToString(mine[:8]))
	}
	return nil
}

// headDisagreementLocked reports, as a ready-to-print line, whether a peer's
// announcement describes a different chain or a different ledger at a height
// this node also has. It returns "" when there is nothing to say. Callers must
// hold e.mu.
//
// It exists because height alone could not tell two chains apart: two nodes both
// at height 900 looked identical in an announcement even when they had committed
// different blocks or reached different balances. On an idle network the
// announcement is the only signal that arrives at all, so this is often the
// first chance anyone has to notice.
//
// Rate-limited to one line per distinct complaint per node, because the
// announcement repeats on a timer and a disagreement does not resolve itself -
// unbounded logging would bury the rest of the node's output.
func (e *Engine) headDisagreementLocked(ann *HeadAnnounce) string {
	if ann == nil || ann.NodeID == "" || ann.NodeID == e.selfID {
		return ""
	}
	// Only comparable at the same height: a peer one block ahead legitimately has
	// a different head and a different ledger.
	if ann.Height != e.height {
		return ""
	}

	var complaint string
	switch {
	case len(ann.HeadHash) > 0 && !bytes.Equal(ann.HeadHash, e.headHash):
		complaint = fmt.Sprintf("consensus: peer %s is at height %d on a DIFFERENT chain "+
			"(its head %s, ours %s); the two have committed different blocks\n",
			ann.NodeID, ann.Height, shortHex(ann.HeadHash), shortHex(e.headHash))
	case len(ann.StateRoot) > 0:
		mine := e.stateRootLocked()
		if mine != nil && !bytes.Equal(ann.StateRoot, mine) {
			complaint = fmt.Sprintf("consensus: peer %s is at height %d on the same chain but a "+
				"DIFFERENT ledger (its state %s, ours %s); the two applied the same blocks and "+
				"reached different balances\n",
				ann.NodeID, ann.Height, shortHex(ann.StateRoot), shortHex(mine))
		}
	}
	if complaint == "" {
		return ""
	}
	if e.headComplaints == nil {
		e.headComplaints = make(map[string]string)
	}
	if e.headComplaints[ann.NodeID] == complaint {
		return ""
	}
	e.headComplaints[ann.NodeID] = complaint
	return complaint
}

// shortHex renders the leading bytes of a hash, which is enough to tell two
// apart in a log line and short enough to read.
func shortHex(b []byte) string {
	if len(b) == 0 {
		return "none"
	}
	if len(b) > 6 {
		b = b[:6]
	}
	return hex.EncodeToString(b)
}
