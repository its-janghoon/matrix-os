package consensus

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/ecirlabs/matrix-core/internal/transport"
)

// evidenceHarness is one engine driven directly, with an evidence store.
type evidenceHarness struct {
	eng      *Engine
	vs       *ValidatorSet
	accts    []*token.Account
	evidence *EvidenceStore
	seen     chan Equivocation
}

func newEvidenceHarness(t *testing.T) *evidenceHarness {
	t.Helper()
	const n = 4
	accts := make([]*token.Account, n)
	pubs := make([]ed25519.PublicKey, n)
	for i := 0; i < n; i++ {
		a, err := token.GenerateAccount()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		accts[i] = a
		pubs[i] = a.PublicKey
	}
	vs, err := NewValidatorSet(pubs)
	if err != nil {
		t.Fatalf("validator set: %v", err)
	}
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	evidence := NewEvidenceStore(store)
	seen := make(chan Equivocation, 8)
	eng, err := New(Config{
		Transport:      noopTransport{},
		Validators:     vs,
		Chain:          NewBlockChain(store),
		Ledger:         market.NewLedger(store),
		Self:           accts[0],
		RoundTimeout:   time.Hour,
		Evidence:       evidence,
		OnEquivocation: func(eq *Equivocation) { seen <- *eq },
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); eng.Wait() })
	if err := eng.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	return &evidenceHarness{eng: eng, vs: vs, accts: accts, evidence: evidence, seen: seen}
}

func hashOf(b byte) []byte {
	out := make([]byte, HashSize)
	for i := range out {
		out[i] = b
	}
	return out
}

// TestDoubleVoteIsDetectedAndRecorded is the case that used to pass unnoticed:
// one validator, one round, two different blocks, both signed.
func TestDoubleVoteIsDetectedAndRecorded(t *testing.T) {
	h := newEvidenceHarness(t)
	offender := h.accts[1]

	deliverVote(t, h.eng, buildVote(t, offender, 0, 0, hashOf(0xAA)))
	deliverVote(t, h.eng, buildVote(t, offender, 0, 0, hashOf(0xBB)))

	select {
	case eq := <-h.seen:
		if eq.VoterID != offender.AccountID() {
			t.Fatalf("evidence names %s, want the offender %s", eq.VoterID, offender.AccountID())
		}
		if eq.Type != VoteTypePrevote || eq.Height != 0 || eq.Round != 0 {
			t.Fatalf("evidence header = height %d round %d type %s", eq.Height, eq.Round, eq.Type)
		}
		if err := eq.Verify(h.vs); err != nil {
			t.Fatalf("recorded evidence does not verify: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a double vote produced no evidence")
	}

	stored, err := h.evidence.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored %d records, want 1", len(stored))
	}
}

func TestPrecommitEquivocationIsDetectedToo(t *testing.T) {
	h := newEvidenceHarness(t)
	offender := h.accts[2]

	deliverVote(t, h.eng, buildPrecommit(t, offender, 0, 0, hashOf(0x11)))
	deliverVote(t, h.eng, buildPrecommit(t, offender, 0, 0, hashOf(0x22)))

	select {
	case eq := <-h.seen:
		if eq.Type != VoteTypePrecommit {
			t.Fatalf("evidence type = %s, want precommit", eq.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a double precommit produced no evidence")
	}
}

// TestNilVoteThenBlockVoteIsEquivocation covers the case a reader might not
// expect: saying "no block this round" and then voting for one is still two
// contradictory statements in one round.
func TestNilVoteThenBlockVoteIsEquivocation(t *testing.T) {
	h := newEvidenceHarness(t)
	offender := h.accts[3]

	deliverVote(t, h.eng, buildVote(t, offender, 0, 0, nilVoteHash()))
	deliverVote(t, h.eng, buildVote(t, offender, 0, 0, hashOf(0x33)))

	select {
	case <-h.seen:
	case <-time.After(2 * time.Second):
		t.Fatal("a nil vote followed by a block vote produced no evidence")
	}
}

// TestHonestVotingProducesNoEvidence is the false-positive guard. Duplicate
// gossip, votes in different rounds and votes in different phases are all
// normal.
func TestHonestVotingProducesNoEvidence(t *testing.T) {
	h := newEvidenceHarness(t)
	v := h.accts[1]

	// The same vote three times: gossip echoes.
	same := buildVote(t, v, 0, 0, hashOf(0x44))
	deliverVote(t, h.eng, same)
	deliverVote(t, h.eng, same)
	deliverVote(t, h.eng, same)

	// Different rounds: rotation, not misbehaviour.
	deliverVote(t, h.eng, buildVote(t, v, 0, 1, hashOf(0x55)))
	deliverVote(t, h.eng, buildVote(t, v, 0, 2, hashOf(0x66)))

	// A prevote and a precommit for different blocks in the same round is
	// allowed: they are different phases, and a validator prevotes what it sees
	// before it knows what the network chose.
	deliverVote(t, h.eng, buildPrecommit(t, v, 0, 1, hashOf(0x77)))

	select {
	case eq := <-h.seen:
		t.Fatalf("honest voting was reported as equivocation: %+v", eq)
	case <-time.After(300 * time.Millisecond):
	}

	stored, err := h.evidence.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(stored) != 0 {
		t.Fatalf("stored %d records for honest voting, want 0", len(stored))
	}
}

func TestEvidenceIsReportedOncePerOffence(t *testing.T) {
	h := newEvidenceHarness(t)
	offender := h.accts[1]

	a := buildVote(t, offender, 0, 0, hashOf(0xA1))
	b := buildVote(t, offender, 0, 0, hashOf(0xB2))
	deliverVote(t, h.eng, a)
	deliverVote(t, h.eng, b)
	// The offence, re-delivered: a real network gossips both votes repeatedly.
	deliverVote(t, h.eng, a)
	deliverVote(t, h.eng, b)

	<-h.seen
	select {
	case eq := <-h.seen:
		t.Fatalf("the same offence was reported twice: %+v", eq)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestEvidenceFromAPeerIsCheckedNotTrusted feeds the engine a report over the
// gossip topic, which is how a node learns about an offence it did not witness.
func TestEvidenceFromAPeerIsCheckedNotTrusted(t *testing.T) {
	h := newEvidenceHarness(t)
	offender := h.accts[1]
	honest := h.accts[2]

	good := NewEquivocation(
		buildVote(t, offender, 7, 3, hashOf(0xC1)),
		buildVote(t, offender, 7, 3, hashOf(0xD2)),
	)

	cases := []struct {
		name   string
		eq     Equivocation
		accept bool
	}{
		{name: "genuine proof", eq: good, accept: true},
		{
			name: "both votes for the same block",
			eq: NewEquivocation(
				buildVote(t, offender, 8, 0, hashOf(0xE1)),
				buildVote(t, offender, 8, 0, hashOf(0xE1)),
			),
		},
		{
			name: "votes from two different validators",
			eq: Equivocation{
				Height: 9, Round: 0, Type: VoteTypePrevote, VoterID: offender.AccountID(),
				A: buildVote(t, offender, 9, 0, hashOf(0xF1)),
				B: buildVote(t, honest, 9, 0, hashOf(0xF2)),
			},
		},
		{
			name: "different rounds",
			eq: Equivocation{
				Height: 10, Round: 0, Type: VoteTypePrevote, VoterID: offender.AccountID(),
				A: buildVote(t, offender, 10, 0, hashOf(0x01)),
				B: buildVote(t, offender, 10, 1, hashOf(0x02)),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A tampered signature must also be refused; break one on the accepted
			// case to prove the check is real rather than structural.
			eq := tc.eq
			data, err := json.Marshal(&eq)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			h.eng.handleEvidence(context.Background(), transport.Message{
				From: peer.ID("reporter"), Topic: TopicEvidence, Payload: data,
			})

			stored, err := h.evidence.ByValidator(offender.AccountID())
			if err != nil {
				t.Fatalf("ByValidator: %v", err)
			}
			found := false
			for _, s := range stored {
				if s.Height == eq.Height && s.Round == eq.Round {
					found = true
				}
			}
			if found != tc.accept {
				t.Fatalf("stored = %v, want %v for %s", found, tc.accept, tc.name)
			}
		})
	}

	t.Run("forged signature", func(t *testing.T) {
		eq := NewEquivocation(
			buildVote(t, offender, 20, 0, hashOf(0x21)),
			buildVote(t, offender, 20, 0, hashOf(0x22)),
		)
		eq.B.Signature[0] ^= 0xFF
		if err := eq.Verify(h.vs); !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("Verify on a forged vote = %v, want ErrInvalidSignature", err)
		}
		data, _ := json.Marshal(&eq)
		h.eng.handleEvidence(context.Background(), transport.Message{Payload: data})

		stored, _ := h.evidence.ByValidator(offender.AccountID())
		for _, s := range stored {
			if s.Height == 20 {
				t.Fatal("evidence with a forged signature was stored")
			}
		}
	})

	t.Run("not a validator", func(t *testing.T) {
		outsider, err := token.GenerateAccount()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		eq := NewEquivocation(
			buildVote(t, outsider, 30, 0, hashOf(0x31)),
			buildVote(t, outsider, 30, 0, hashOf(0x32)),
		)
		if err := eq.Verify(h.vs); !errors.Is(err, ErrNotValidator) {
			t.Fatalf("Verify for a non-validator = %v, want ErrNotValidator", err)
		}
	})
}

// TestEvidenceSurvivesRestart matters because the record is the whole point: an
// offence a node forgets when it reboots is not a record of anything.
func TestEvidenceSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := kv.New(kv.Config{Path: dir})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	offender, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	eq := NewEquivocation(
		buildVote(t, offender, 5, 1, hashOf(0x51)),
		buildVote(t, offender, 5, 1, hashOf(0x52)),
	)

	first := NewEvidenceStore(store)
	recorded, err := first.Record(&eq)
	if err != nil || !recorded {
		t.Fatalf("Record = %v, %v", recorded, err)
	}
	// Recording the same offence again is a no-op success, not a duplicate.
	again, err := first.Record(&eq)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if again {
		t.Fatal("the same offence was recorded twice")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := kv.New(kv.Config{Path: dir})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	after, err := NewEvidenceStore(reopened).All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("after restart: %d records, want 1", len(after))
	}
	if after[0].VoterID != offender.AccountID() {
		t.Fatalf("after restart: record names %s", after[0].VoterID)
	}
}

// TestEvidenceReachesNodesThatDidNotWitnessIt is the reason evidence is
// gossiped rather than logged.
//
// The offender's two votes are delivered only to one node. Every other node
// learns about the offence anyway, and each one verifies the two signatures for
// itself before recording anything - so the report travels without the
// reporter having to be trusted.
func TestEvidenceReachesNodesThatDidNotWitnessIt(t *testing.T) {
	nodes, stop := syncCluster(t, 4)
	defer stop()

	witness := nodes[0]
	offender := nodes[1]

	// Only the witness hears votes. Note the evidence topic is NOT filtered:
	// that is the path under test.
	nodes[0].bus.setDeliveryFilter(func(_, to peer.ID, topic string) bool {
		if topic != TopicVote {
			return true
		}
		return to == witness.peerID
	})

	a := buildVote(t, offender.acct, 0, 0, hashOf(0x8A))
	b := buildVote(t, offender.acct, 0, 0, hashOf(0x8B))
	attacker := witness.bus.endpoint(peer.ID("offender-relay"))
	for _, v := range []Vote{a, b} {
		data, err := json.Marshal(&v)
		if err != nil {
			t.Fatalf("marshal vote: %v", err)
		}
		if err := attacker.Publish(context.Background(), TopicVote, data); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		informed := 0
		for _, nd := range nodes {
			records, err := nd.evidence.ByValidator(offender.acct.AccountID())
			if err != nil {
				t.Fatalf("ByValidator: %v", err)
			}
			if len(records) > 0 {
				informed++
			}
		}
		if informed == len(nodes) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d nodes learned about the equivocation", informed, len(nodes))
		}
		time.Sleep(5 * time.Millisecond)
	}

	// What every node holds must be checkable proof, not hearsay.
	for i, nd := range nodes {
		records, _ := nd.evidence.ByValidator(offender.acct.AccountID())
		for _, eq := range records {
			if err := eq.Verify(nd.engine.vset()); err != nil {
				t.Fatalf("node %d stored evidence that does not verify: %v", i, err)
			}
		}
	}
}
