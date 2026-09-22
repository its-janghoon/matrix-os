package node

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type stubParticipation struct {
	participating bool
	silence       time.Duration
}

func (s stubParticipation) ParticipatingInConsensus() (bool, time.Duration) {
	return s.participating, s.silence
}

// Settlement goes through consensus, so a node that has fallen off it cannot
// complete a transaction. The buyer must be refused BEFORE capacity is held,
// because the failure the gate exists to prevent is a buyer who waited, funded a
// reservation, and got nothing.
func TestAPartitionedSellerRefusesAReservation(t *testing.T) {
	err := refuseIfNotParticipating(stubParticipation{participating: false, silence: 42 * time.Second})
	if err == nil {
		t.Fatal("a node that cannot settle accepted a reservation")
	}
	if !errors.Is(err, ErrNotInConsensus) {
		t.Errorf("refused with an error a caller cannot classify: %v", err)
	}
	// The buyer reads this. It has to say how long, or it reads as a permanent
	// refusal and they will not retry.
	if got := err.Error(); !strings.Contains(got, "42s") {
		t.Errorf("the refusal does not say how long the silence has lasted: %q", got)
	}
}

// Immediately, on the first missed signal, and not after a grace window. The two
// mistakes do not cost the same: refusing while partitioned loses a sale, selling
// while partitioned takes money for work that cannot settle.
func TestThereIsNoGraceWindow(t *testing.T) {
	// One interval's worth of silence is already a refusal - there is no
	// consecutive-failure threshold to cross first.
	if err := refuseIfNotParticipating(stubParticipation{participating: false, silence: time.Millisecond}); err == nil {
		t.Error("a just-detected partition was allowed to keep selling")
	}
}

// And a participating node is not impeded. The gate must be invisible when the
// network is healthy, which is almost always.
func TestAParticipatingSellerIsNotImpeded(t *testing.T) {
	if err := refuseIfNotParticipating(stubParticipation{participating: true}); err != nil {
		t.Errorf("a healthy node was refused: %v", err)
	}
}

// A node with no consensus at all is not a validator that fell off one: it is a
// seller settling some other way. Refusing it would take a working configuration
// off the market.
func TestANodeWithNoConsensusIsNotGated(t *testing.T) {
	n := &Node{}
	if err := n.refuseIfNotInConsensus(); err != nil {
		t.Errorf("a node with no consensus engine was gated: %v", err)
	}
	if participating, _ := n.participatingInConsensus(); !participating {
		t.Error("a node with no consensus engine reported itself partitioned")
	}
}

// A just-started node has heard nothing, so there is no interval to report. It
// printed "heard no proposal or vote for 0s" on the GPU box during the v0.5.6
// rollout: correct behaviour - fail closed until the network is heard, back 40
// seconds later - reported as a stopped clock. During an upgrade that reads as a
// broken node rather than one still joining.
func TestAJustStartedNodeDoesNotReportZeroSeconds(t *testing.T) {
	err := refuseIfNotParticipating(stubParticipation{participating: false, silence: 0})
	if err == nil {
		t.Fatal("a node that has heard nothing accepted a reservation")
	}
	got := err.Error()
	if strings.Contains(got, "0s") {
		t.Errorf("reported a zero interval as if it were a measurement: %q", got)
	}
	if !strings.Contains(got, "since starting") {
		t.Errorf("does not say that nothing has been heard since the process started: %q", got)
	}
}

// And a real measured silence still reads as one, so the fix above did not replace
// the duration with a phrase everywhere.
func TestAMeasuredSilenceIsStillReportedAsADuration(t *testing.T) {
	err := refuseIfNotParticipating(stubParticipation{participating: false, silence: 42 * time.Second})
	if err == nil {
		t.Fatal("a partitioned node accepted a reservation")
	}
	if got := err.Error(); !strings.Contains(got, "for 42s") {
		t.Errorf("a measured silence lost its duration: %q", got)
	}
}
