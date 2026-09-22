package consensus

import (
	"time"
)

// Whether this node is still part of consensus, as a question a SELLER can ask.
//
// THE GAP THIS CLOSES. A provider kept announcing and taking reservations while
// its node had stopped participating in consensus. Settlement goes THROUGH
// consensus, so a buyer was routed to a seller that could not complete the
// transaction: quoted, reserved, funded, and then nothing. That is worse than the
// seller being absent, because the buyer waited and their capacity was held while
// they did.
//
// Distinct from node/inference_health.go, which watches the MODEL SERVER and is
// already correct. That one answers "can I do the work". This one answers "can I
// be paid for it", and nothing was asking it.
//
// WHY HEIGHT IS THE WRONG SIGNAL, AND WHY THIS ONE IS NOT IT EITHER. The obvious
// check is "has the height moved". It is wrong for the reason an idle chain is
// wrong for every other check: the engine returns nil rather than minting an
// empty block, so a healthy quiet network produces no blocks and a
// height-watching seller takes itself off the market for being unbusy.
//
// The signal that survives an idle chain is ROUND-LEVEL. A round times out every
// round_timeout and rotates the leader, so proposals and votes keep flowing
// whether or not anything is being committed. "I have not heard a proposal or a
// vote from another validator in a long time" therefore means partitioned, on a
// busy chain and a silent one alike.
//
// WHAT COUNTS AS HEARING. A proposal or a vote from ANOTHER validator, after it
// has been verified and its author confirmed to be in the set. Before
// verification it would be a signal any peer could forge, and this node's own
// messages would make a partitioned node vote for itself and call that
// participation.

const (
	// participationRoundsMissed is how many worst-case rounds of silence are
	// read as a partition.
	//
	// Worst case, because the round timeout BACKS OFF up to
	// maxRoundTimeoutFactor as rounds fail, so a network that is merely slow
	// legitimately goes quiet for longer than the base timeout. Four of those
	// is long enough that ordinary gossip loss cannot produce it and short
	// enough that a partitioned seller leaves the market in seconds.
	participationRoundsMissed = 4

	// minParticipationSilence floors the derived bound, because round_timeout is
	// configurable down to milliseconds and a bound derived from a small one
	// would suspend a seller over a GC pause.
	minParticipationSilence = 10 * time.Second
)

// noteConsensusHeard records that a verified consensus message from another
// validator has arrived. Lock-free: it is on the receive path of every proposal
// and every vote, which is the hottest path the engine has.
func (e *Engine) noteConsensusHeard(from string) {
	if from == e.selfID {
		// A node alone in a partition still proposes to itself and votes for its
		// own proposals. Counting that would make the check unable to fail.
		return
	}
	e.lastHeardUnixNano.Store(time.Now().UnixNano())
}

// participationSilenceLimit is how long this node may hear nothing before it
// considers itself out of consensus.
func (e *Engine) participationSilenceLimit() time.Duration {
	limit := time.Duration(participationRoundsMissed) * e.roundTimeout * maxRoundTimeoutFactor
	if limit < minParticipationSilence {
		return minParticipationSilence
	}
	return limit
}

// ParticipatingInConsensus reports whether this node is still hearing the rest of
// the validator set, and how long it has been since it last did.
//
// A ZERO DURATION MEANS "NEVER", NOT "JUST NOW". Nothing has been heard since this
// process started, so there is no interval to measure. Callers that put the
// duration in front of a person must say so in words: "no vote for 0s" reads as a
// stopped clock, and on a restart - which is when it prints - it reads as a broken
// node rather than one that has not finished joining.
//
// A SET OF ONE IS ALWAYS PARTICIPATING. There is nobody to hear from, so silence
// carries no information - a devnet, a single-validator chain and a node that is
// the whole set would otherwise be permanently partitioned from themselves and
// could never sell anything.
//
// BEFORE THE FIRST MESSAGE IS HEARD, IT IS NOT PARTICIPATING. A node that has
// just started has heard nothing, and that is indistinguishable from a node whose
// peers are gone. Fail closed: it sells once it has heard the network, which on a
// healthy chain is within a round.
func (e *Engine) ParticipatingInConsensus() (bool, time.Duration) {
	if e.vset().Len() <= 1 {
		return true, 0
	}
	last := e.lastHeardUnixNano.Load()
	if last == 0 {
		return false, 0
	}
	silence := time.Since(time.Unix(0, last))
	return silence < e.participationSilenceLimit(), silence
}
