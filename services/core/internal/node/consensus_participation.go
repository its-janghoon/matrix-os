package node

import (
	"errors"
	"fmt"
	"time"
)

// A seller that has fallen off consensus stops selling.
//
// THE FAULT THIS CLOSES. Settlement goes THROUGH consensus. A node that has
// stopped participating cannot complete a transaction, and it kept announcing and
// taking reservations anyway - so a buyer was routed to it, quoted, held capacity,
// funded a reservation, and then waited for a settlement that could not happen.
// From the buyer's side that is worse than the seller being absent, which is the
// same asymmetry inference_health.go already reasons about for a dead model
// server.
//
// IMMEDIATELY, ON THE FIRST MISSED SIGNAL, AND NOT AFTER A GRACE WINDOW. The two
// mistakes do not cost the same. Refusing to sell while partitioned loses a sale
// and nothing else; selling while partitioned takes a buyer's money for work that
// cannot settle. A grace window is the option that has to justify itself, and
// "partitions are usually transient" is a claim about how OFTEN the mistake
// happens, not about who pays when it does.
//
// The seller's own recovery IS the grace window. Announcing resumes on the next
// tick after a single message is heard, and the check is a read of one atomic, so
// there is no cost to being wrong for one interval.
//
// WHAT IT DOES NOT DO. It does not cancel work already reserved. A partition
// cannot be told apart from a node that was mid-settlement, and abandoning funded
// reservations on that evidence is the more expensive mistake - the same
// conclusion, for the same reason, that the backend health check reached about
// killing live jobs.

// ErrNotInConsensus is returned to a buyer whose reservation this node cannot
// honour because it is not participating in consensus.
//
// A named error and not a bare string: a caller has to be able to tell "this
// seller cannot settle right now" from "your request was wrong", because the first
// is worth retrying elsewhere and the second is not.
var ErrNotInConsensus = errors.New("node: not participating in consensus, so a settlement could not complete")

// consensusParticipation is the signal source. An interface rather than the
// engine, so a test can drive the seller side without standing up a cluster.
type consensusParticipation interface {
	ParticipatingInConsensus() (bool, time.Duration)
}

// participationSource is this node's signal, or nil when it has none.
//
// NO ENGINE MEANS NO GATE. A node with no consensus at all is not a validator
// that fell off one: it is a seller settling some other way, and refusing to let
// it announce would take a working configuration off the market.
func (n *Node) participationSource() consensusParticipation {
	if n.consensus == nil {
		return nil
	}
	return n.consensus
}

// participatingInConsensus answers for the whole node.
func (n *Node) participatingInConsensus() (bool, time.Duration) {
	src := n.participationSource()
	if src == nil {
		return true, 0
	}
	return src.ParticipatingInConsensus()
}

// refuseIfNotInConsensus is the reservation-path half of the gate. The announce
// gate keeps this node out of the directory; this one refuses a buyer who reached
// it directly, or who is holding a listing from before the partition.
func (n *Node) refuseIfNotInConsensus() error {
	return refuseIfNotParticipating(n.participationSource())
}

// refuseIfNotParticipating is the refusal itself, over the signal rather than the
// node, so there is one implementation and a test does not need a cluster.
//
// The error text is handed to the BUYER unchanged, so it says how long the silence
// has lasted. Without that it reads as a permanent refusal and they will not
// retry - which is the wrong conclusion for something that usually clears in
// seconds.
func refuseIfNotParticipating(src consensusParticipation) error {
	if src == nil {
		return nil
	}
	participating, silence := src.ParticipatingInConsensus()
	if participating {
		return nil
	}
	return fmt.Errorf("%w: no proposal or vote has been heard for %s", ErrNotInConsensus, silence.Round(time.Second))
}

// consensusSilenceReported makes the log a TRANSITION and not a line per tick.
// Announcing runs on an interval; a partition that lasts an hour would otherwise
// print sixty identical lines and bury the one that says it came back.
//
// Per NODE and not per package: `make devnet` runs three real nodes in one
// process, and a package-level flag would let one node's partition silence
// another's report.
func (n *Node) reportConsensusSilence(silence time.Duration) {
	if n.consensusSilenceReported.Swap(true) {
		return
	}
	fmt.Printf("Market: this node has heard no proposal or vote for %s, so it has stopped "+
		"announcing and is refusing reservations. It cannot settle while partitioned.\n",
		silence.Round(time.Second))
}

func (n *Node) clearConsensusSilence() {
	if !n.consensusSilenceReported.Swap(false) {
		return
	}
	fmt.Println("Market: consensus messages are arriving again; this node is back on the market.")
}
