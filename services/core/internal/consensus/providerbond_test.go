package consensus

import (
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Bonding is what buys admission to the validator set. A node that has said it
// will not join has nothing to buy.
//
// The case is the GPU provider the runbook describes: it copies `consensus`
// verbatim off an existing node - which is the only way to get a state root that
// matches - and that brings the validator's `bond` with it. Before this, the
// engine read that target and tried to fill it on every interval forever, on a
// node that had declared participate_in_open_set: false in the same file.
//
// On an unfunded account that is log noise. On a funded one it is worse: the
// balance is locked into a set the operator explicitly declined to join, and
// getting it back means waiting out the unbonding period.

// newMembershipEngine builds a submit-only engine with a funded self account, so
// a bond attempt would actually succeed if one were made.
func newMembershipEngine(t *testing.T, mode MembershipMode, participate *bool, targetBond uint64) (*Engine, *StakeLedger, *token.Account) {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	self, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	vs, err := NewValidatorSet([]ed25519.PublicKey{self.PublicKey})
	if err != nil {
		t.Fatalf("NewValidatorSet: %v", err)
	}
	ledger := market.NewLedger(store)
	if err := ledger.Credit(self.AccountID(), 10*targetBond+1000); err != nil {
		t.Fatalf("credit self: %v", err)
	}
	stake := NewStakeLedger(ledger, store)
	eng, err := New(Config{
		Transport:            newMemBus().endpoint(peer.ID("membership-" + self.AccountID()[:8])),
		Validators:           vs,
		Chain:                NewBlockChain(store),
		Ledger:               ledger,
		Self:                 self,
		ProposeInterval:      time.Hour,
		RoundTimeout:         time.Hour,
		Evidence:             NewEvidenceStore(store),
		Sets:                 NewSetStore(store),
		SelfVotes:            NewSelfVoteStore(store),
		Stake:                stake,
		MembershipMode:       mode,
		MinBond:              100,
		TargetBond:           targetBond,
		ParticipateInOpenSet: participate,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return eng, stake, self
}

// bondSubmitted reports whether maybeTopUpBond put a stake transaction in the
// mempool. The mempool is where a self-submitted bond lands before any block.
func bondSubmitted(e *Engine, selfID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.mempool {
		if IsStakeRecipient(e.mempool[i].To) && e.mempool[i].SenderID() == selfID {
			return true
		}
	}
	return false
}

func TestANodeThatDeclinedTheOpenSetDoesNotBond(t *testing.T) {
	no := false
	eng, _, self := newMembershipEngine(t, MembershipBondedOpen, &no, 1000)
	eng.maybeTopUpBond()
	if bondSubmitted(eng, self.AccountID()) {
		t.Fatal("a node with participate_in_open_set: false submitted a bond; " +
			"it declined the set that a bond buys admission to")
	}
}

// The same engine with the same funded account and the same target DOES bond
// when it means to validate - so the guard is about intent, not about the bond
// path being broken.
func TestANodeThatWantsTheOpenSetStillBonds(t *testing.T) {
	yes := true
	eng, _, self := newMembershipEngine(t, MembershipBondedOpen, &yes, 1000)
	eng.maybeTopUpBond()
	if !bondSubmitted(eng, self.AccountID()) {
		t.Fatal("a participating node with a funded account and an unmet target did not bond")
	}
}

// Nil is the default and means participating, so a config that says nothing
// about membership keeps the behaviour it had.
func TestOmittedParticipationStillBonds(t *testing.T) {
	eng, _, self := newMembershipEngine(t, MembershipBondedOpen, nil, 1000)
	eng.maybeTopUpBond()
	if !bondSubmitted(eng, self.AccountID()) {
		t.Fatal("an omitted participate_in_open_set defaults to participating and must still bond")
	}
}

// participate_in_open_set has no meaning outside bonded-open: an operator-
// approved set is decided by the operators, not by this node's intent, so the
// guard must not reach into that mode.
func TestOperatorApprovedModeBondsRegardlessOfParticipation(t *testing.T) {
	no := false
	eng, _, self := newMembershipEngine(t, MembershipOperatorApproved, &no, 1000)
	eng.maybeTopUpBond()
	if !bondSubmitted(eng, self.AccountID()) {
		t.Fatal("operator-approved membership does not consult participate_in_open_set, so the bond must still be submitted")
	}
}
