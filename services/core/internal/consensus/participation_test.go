package consensus

import (
	"testing"
	"time"
)

// The signal a seller consults before announcing or taking a reservation. What
// makes it the right one is that it survives an IDLE chain: the engine mints no
// empty blocks, so a height-watching check takes a healthy quiet node off the
// market for being unbusy.

// A validator set of one has nobody to hear from, so silence says nothing. A
// devnet and a single-validator chain would otherwise be permanently partitioned
// from themselves and could never sell.
func TestASetOfOneIsAlwaysParticipating(t *testing.T) {
	nodes, stop := newCluster(t, 1, nil)
	defer stop()
	e := nodes[0].engine

	participating, silence := e.ParticipatingInConsensus()
	if !participating {
		t.Fatalf("a lone validator considered itself partitioned after %s", silence)
	}
}

// Fail closed before the first message. A node that has just started has heard
// nothing, and that is indistinguishable from one whose peers are gone.
func TestANodeThatHasHeardNothingIsNotParticipating(t *testing.T) {
	nodes, stop := newCluster(t, 4, nil)
	defer stop()
	e := nodes[0].engine

	// Whatever a running cluster has already heard, reset to the just-started
	// state: the question is what the check says when the counter is zero.
	e.lastHeardUnixNano.Store(0)

	if participating, _ := e.ParticipatingInConsensus(); participating {
		t.Error("a node that has heard nothing from a four-member set called itself a participant")
	}
}

// Hearing a peer is what makes it a participant, and hearing ITSELF must not.
// A node alone in a partition still proposes to itself and votes for its own
// proposals; counting that would make the check unable to fail.
func TestOnlyAnotherValidatorCounts(t *testing.T) {
	nodes, stop := newCluster(t, 4, nil)
	defer stop()
	e := nodes[0].engine

	e.lastHeardUnixNano.Store(0)
	e.noteConsensusHeard(e.selfID)
	if participating, _ := e.ParticipatingInConsensus(); participating {
		t.Error("a node counted its own message as hearing the network")
	}

	peer := ""
	for _, id := range e.vset().IDs() {
		if id != e.selfID {
			peer = id
			break
		}
	}
	if peer == "" {
		t.Fatal("a four-member set produced no peer id")
	}
	e.noteConsensusHeard(peer)
	if participating, _ := e.ParticipatingInConsensus(); !participating {
		t.Error("a node that just heard a peer still called itself partitioned")
	}
}

// Silence past the bound is a partition. The bound is derived from the round
// timeout, because a round rotates the leader every timeout and so messages flow
// on an idle chain too.
func TestSilencePastTheBoundIsAPartition(t *testing.T) {
	nodes, stop := newCluster(t, 4, nil)
	defer stop()
	e := nodes[0].engine

	limit := e.participationSilenceLimit()
	if limit < minParticipationSilence {
		t.Fatalf("the bound (%s) fell below its own floor (%s)", limit, minParticipationSilence)
	}

	e.lastHeardUnixNano.Store(time.Now().Add(-limit - time.Second).UnixNano())
	participating, silence := e.ParticipatingInConsensus()
	if participating {
		t.Errorf("silence of %s did not read as a partition against a bound of %s", silence, limit)
	}

	// And just inside it is not, so an ordinary slow round does not suspend a
	// seller.
	e.lastHeardUnixNano.Store(time.Now().Add(-limit / 2).UnixNano())
	if participating, silence = e.ParticipatingInConsensus(); !participating {
		t.Errorf("silence of %s inside a bound of %s was called a partition", silence, limit)
	}
}
