package consensus

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestSelfVoteStoreKeepsOnePerPositionAndPrunes(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()
	votes := NewSelfVoteStore(store)

	record := func(height, round uint64, typ VoteType, hash string) {
		t.Helper()
		if err := votes.Record(&Vote{
			Type: typ, Height: height, Round: round,
			BlockHash: []byte(hash), VoterID: "self",
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	record(7, 0, VoteTypePrevote, "a")
	record(7, 0, VoteTypePrecommit, "a")
	record(7, 1, VoteTypePrevote, "b")
	record(8, 0, VoteTypePrevote, "c")

	at7, err := votes.AtHeight(7)
	if err != nil {
		t.Fatalf("AtHeight: %v", err)
	}
	if len(at7) != 3 {
		t.Fatalf("height 7 has %d records, want 3", len(at7))
	}
	// Round then phase order, so a replay restores the lock from the highest
	// round last.
	if at7[0].Round != 0 || at7[0].Type != VoteTypePrevote ||
		at7[1].Round != 0 || at7[1].Type != VoteTypePrecommit ||
		at7[2].Round != 1 {
		t.Fatalf("records out of order: %+v", at7)
	}

	// One record per height, round and phase: re-recording a position is not a
	// second vote, it is the same vote.
	record(7, 0, VoteTypePrevote, "a")
	if at7, _ = votes.AtHeight(7); len(at7) != 3 {
		t.Fatalf("re-recording a position grew the height to %d records", len(at7))
	}

	if err := votes.PruneBelow(8); err != nil {
		t.Fatalf("PruneBelow: %v", err)
	}
	if at7, _ = votes.AtHeight(7); len(at7) != 0 {
		t.Fatalf("committed height kept %d records", len(at7))
	}
	at8, _ := votes.AtHeight(8)
	if len(at8) != 1 {
		t.Fatalf("uncommitted height lost its records: %d", len(at8))
	}
}

// The regression this whole store exists for. A validator that has precommitted
// a block and is then restarted must come back locked on it. Without that it is
// free to prevote whatever the next leader proposes, which is two signed votes
// for different blocks at one height and round - self-slashing evidence
// produced by nothing worse than a service restart.
func TestRestartRecoversOwnVotesAndLock(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()

	self, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	vs, err := NewValidatorSet([]ed25519.PublicKey{self.PublicKey})
	if err != nil {
		t.Fatalf("NewValidatorSet: %v", err)
	}
	ledger := market.NewLedger(store)
	votes := NewSelfVoteStore(store)
	bus := newMemBus()

	newEngine := func(name string) *Engine {
		t.Helper()
		eng, err := New(Config{
			Transport:       bus.endpoint(peer.ID(name)),
			Validators:      vs,
			Chain:           NewBlockChain(store),
			Ledger:          ledger,
			Self:            self,
			ProposeInterval: time.Hour,
			RoundTimeout:    time.Hour,
			Evidence:        NewEvidenceStore(store),
			Sets:            NewSetStore(store),
			SelfVotes:       votes,
			Stake:           NewStakeLedger(ledger, store),
			ZeroMinBond:     true,
			DisableTxGossip: true,
		})
		if err != nil {
			t.Fatalf("New %s: %v", name, err)
		}
		return eng
	}

	// A precommit for a real block at round 2 of the height this node is
	// deciding. Recording it through the store is what casting it does.
	hash := []byte{0xab, 0xcd}
	cast := &Vote{
		Type: VoteTypePrecommit, Height: 0, Round: 2,
		BlockHash: hash, VoterID: self.AccountID(), PublicKey: self.PublicKey,
	}
	if err := cast.Sign(self.PrivateKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := votes.Record(cast); err != nil {
		t.Fatalf("record own precommit: %v", err)
	}

	restarted := newEngine("selfvote-restart")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := restarted.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	restarted.Wait()

	restarted.mu.Lock()
	locked, round, lockedHash := restarted.locked, restarted.lockedRound, restarted.lockedHash
	prior, voted := restarted.selfVoteAtLocked(VoteTypePrecommit, 2)
	restarted.mu.Unlock()

	if !locked {
		t.Fatal("restarted validator came back unlocked after precommitting")
	}
	if round != 2 || lockedHash != "abcd" {
		t.Fatalf("restored lock = round %d on %s, want round 2 on abcd", round, lockedHash)
	}
	if !voted || prior != "abcd" {
		t.Fatalf("restarted validator forgot its own precommit: prior=%q voted=%v", prior, voted)
	}
}

// A persisted vote occupies its position for good, so resuming a height at
// round 0 while holding records for rounds 0..N leaves the node unable to vote
// on any proposal until it has burned N rounds again - and each of those rounds
// times out and writes two MORE nil votes, so the occupied range grows at least
// as fast as the node walks it. That is a permanent stall, and restarting makes
// it worse rather than better because the restart is what resets the round.
//
// A production chain died exactly this way: 1418 rounds of nil votes at one
// height, not one vote ever cast for a real block.
func TestRestartResumesPastRoundsAlreadyVotedIn(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()

	self, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	vs, err := NewValidatorSet([]ed25519.PublicKey{self.PublicKey})
	if err != nil {
		t.Fatalf("NewValidatorSet: %v", err)
	}
	ledger := market.NewLedger(store)
	votes := NewSelfVoteStore(store)

	// Three rounds of nil votes at the height this node is deciding, exactly as
	// three round timeouts would leave behind.
	const burned = 3
	for round := uint64(0); round < burned; round++ {
		for _, typ := range []VoteType{VoteTypePrevote, VoteTypePrecommit} {
			v := &Vote{
				Type: typ, Height: 0, Round: round,
				BlockHash: nilVoteHash(), VoterID: self.AccountID(),
				PublicKey: self.PublicKey,
			}
			if err := v.Sign(self.PrivateKey); err != nil {
				t.Fatalf("sign: %v", err)
			}
			if err := votes.Record(v); err != nil {
				t.Fatalf("record: %v", err)
			}
		}
	}

	eng, err := New(Config{
		Transport:       newMemBus().endpoint(peer.ID("selfvote-resume")),
		Validators:      vs,
		Chain:           NewBlockChain(store),
		Ledger:          ledger,
		Self:            self,
		ProposeInterval: time.Hour,
		RoundTimeout:    time.Hour,
		Evidence:        NewEvidenceStore(store),
		Sets:            NewSetStore(store),
		SelfVotes:       votes,
		Stake:           NewStakeLedger(ledger, store),
		ZeroMinBond:     true,
		DisableTxGossip: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := eng.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	eng.Wait()

	eng.mu.Lock()
	round := eng.round
	eng.mu.Unlock()

	if round != burned {
		t.Fatalf("resumed at round %d, want %d: the node must skip the rounds it has already voted in, not replay them",
			round, burned)
	}
	// Nil votes carry no lock, so skipping them cannot have invented one.
	eng.mu.Lock()
	locked := eng.locked
	eng.mu.Unlock()
	if locked {
		t.Fatal("nil votes produced a lock")
	}
}

// A store that cannot be written must stop the vote rather than let it out
// unrecorded. Bonded-open therefore refuses to build an engine without one at
// all, since there slashing is automatic.
func TestBondedOpenRequiresOwnVotePersistence(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()

	self, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	vs, err := NewValidatorSet([]ed25519.PublicKey{self.PublicKey})
	if err != nil {
		t.Fatalf("NewValidatorSet: %v", err)
	}
	ledger := market.NewLedger(store)
	cfg := Config{
		Transport:       newMemBus().endpoint(peer.ID("bonded-open-no-wal")),
		Validators:      vs,
		Chain:           NewBlockChain(store),
		Ledger:          ledger,
		Self:            self,
		ProposeInterval: time.Hour,
		RoundTimeout:    time.Hour,
		Evidence:        NewEvidenceStore(store),
		Sets:            NewSetStore(store),
		Stake:           NewStakeLedger(ledger, store),
		MembershipMode:  MembershipBondedOpen,
		MinBond:         100,
	}
	if _, err := New(cfg); err == nil {
		t.Fatal("bonded-open engine built without an own-vote store")
	}

	cfg.SelfVotes = NewSelfVoteStore(store)
	if _, err := New(cfg); err != nil {
		t.Fatalf("bonded-open engine refused a valid own-vote store: %v", err)
	}
}
