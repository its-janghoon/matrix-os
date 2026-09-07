package consensus

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// --- voting power ------------------------------------------------------

// The point of weighting: a quorum costs two thirds of the STAKE, not two
// thirds of the identities. Identities are free; bonds are not.
func TestQuorumIsMeasuredInStakeNotHeads(t *testing.T) {
	accts, pubs := testKeys(t, 4)
	vs, err := NewValidatorSet(pubs)
	if err != nil {
		t.Fatalf("set: %v", err)
	}

	// Unweighted, this is the classic headcount quorum.
	if vs.TotalPower() != 4 || vs.QuorumPower() != 3 {
		t.Fatalf("unstaked set: total %d quorum %d, want 4 and 3", vs.TotalPower(), vs.QuorumPower())
	}

	// One validator holds 97 of 100 bonded. Three of the four heads - every
	// small holder together - is no longer a quorum, and it should not be: they
	// have 3% of what is at risk.
	weighted, err := vs.WithPower(map[string]uint64{
		accts[0].AccountID(): 97,
		accts[1].AccountID(): 1,
		accts[2].AccountID(): 1,
		accts[3].AccountID(): 1,
	})
	if err != nil {
		t.Fatalf("WithPower: %v", err)
	}
	if weighted.TotalPower() != 100 {
		t.Fatalf("total power = %d, want 100", weighted.TotalPower())
	}
	if got, want := weighted.QuorumPower(), uint64(67); got != want {
		t.Fatalf("quorum = %d, want %d", got, want)
	}

	small := map[string]struct{}{
		accts[1].AccountID(): {},
		accts[2].AccountID(): {},
		accts[3].AccountID(): {},
	}
	if p := weighted.PowerOfSet(small); p >= weighted.QuorumPower() {
		t.Fatalf("three of four heads carry %d power, which reached the %d quorum; a headcount majority must not be a stake quorum",
			p, weighted.QuorumPower())
	}
	// And the large holder plus any one other clears it.
	pair := map[string]struct{}{accts[0].AccountID(): {}, accts[1].AccountID(): {}}
	if p := weighted.PowerOfSet(pair); p < weighted.QuorumPower() {
		t.Fatalf("98 of 100 power carried %d, below the %d quorum", p, weighted.QuorumPower())
	}

	// The receiver is untouched: a set already deciding a height must not change.
	if vs.TotalPower() != 4 {
		t.Fatal("WithPower mutated the set it was called on")
	}
}

// A validator bonded zero keeps power 1 rather than dropping to zero: a
// zero-power member would take its turn as leader while counting for nothing.
func TestAnUnbondedMemberKeepsPowerOne(t *testing.T) {
	accts, pubs := testKeys(t, 3)
	vs, _ := NewValidatorSet(pubs)
	weighted, err := vs.WithPower(map[string]uint64{accts[0].AccountID(): 10})
	if err != nil {
		t.Fatalf("WithPower: %v", err)
	}
	if got := weighted.Power(accts[1].AccountID()); got != 1 {
		t.Fatalf("unbonded member has power %d, want 1", got)
	}
	if got := weighted.TotalPower(); got != 12 {
		t.Fatalf("total power = %d, want 12 (10 + 1 + 1)", got)
	}
}

func TestWithPowerRefusesAnOverflowingTotal(t *testing.T) {
	accts, pubs := testKeys(t, 3)
	vs, _ := NewValidatorSet(pubs)
	huge := ^uint64(0) / 2
	if _, err := vs.WithPower(map[string]uint64{
		accts[0].AccountID(): huge,
		accts[1].AccountID(): huge,
		accts[2].AccountID(): huge,
	}); err == nil {
		t.Fatal("WithPower accepted a total that overflows; a wrapped total would make a tiny vote set look like a quorum")
	}
}

// Power has to survive a restart. A node that reloaded the set at equal power
// while its peers held it at stake weights would compute a different quorum
// from the same votes.
func TestPersistedSetKeepsItsVotingPower(t *testing.T) {
	accts, pubs := testKeys(t, 3)
	vs, _ := NewValidatorSet(pubs)
	weighted, err := vs.WithPower(map[string]uint64{
		accts[0].AccountID(): 500,
		accts[1].AccountID(): 300,
		accts[2].AccountID(): 200,
	})
	if err != nil {
		t.Fatalf("WithPower: %v", err)
	}

	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	defer store.Close()
	sets := NewSetStore(store)

	if err := sets.SaveActive(weighted, 100); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	loaded, at, err := sets.LoadActive()
	if err != nil {
		t.Fatalf("LoadActive: %v", err)
	}
	if at != 100 {
		t.Fatalf("height = %d, want 100", at)
	}
	if loaded.TotalPower() != 1000 {
		t.Fatalf("reloaded total power = %d, want 1000", loaded.TotalPower())
	}
	for _, a := range accts {
		if loaded.Power(a.AccountID()) != weighted.Power(a.AccountID()) {
			t.Fatalf("power of %s changed across a restart: %d -> %d",
				a.AccountID()[:8], weighted.Power(a.AccountID()), loaded.Power(a.AccountID()))
		}
	}
	if loaded.QuorumPower() != weighted.QuorumPower() {
		t.Fatalf("quorum changed across a restart: %d -> %d", weighted.QuorumPower(), loaded.QuorumPower())
	}
}

// --- the bond ledger ---------------------------------------------------

func newStakeLedger(t *testing.T) (*StakeLedger, *market.Ledger) {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ledger := market.NewLedger(store)
	return NewStakeLedger(ledger, store), ledger
}

// A bond is a balance in a reserved account, so bonding conserves supply and
// takes the coins out of the spendable balance.
func TestABondIsAReservedBalance(t *testing.T) {
	stake, ledger := newStakeLedger(t)
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	id := acct.AccountID()

	if err := ledger.Credit(id, 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := ledger.Transfer(id, BondAccount(id), 400); err != nil {
		t.Fatalf("bond: %v", err)
	}

	spendable, _ := ledger.Balance(id)
	if spendable != 600 {
		t.Fatalf("spendable balance = %d, want 600: bonded coins must leave it", spendable)
	}
	bonded, err := stake.Bonded(id)
	if err != nil {
		t.Fatalf("Bonded: %v", err)
	}
	if bonded != 400 {
		t.Fatalf("bonded = %d, want 400", bonded)
	}
	if spendable+bonded != 1000 {
		t.Fatalf("bonding did not conserve the balance: %d + %d != 1000", spendable, bonded)
	}
}

// Slashing moves the bond to the reward pool rather than destroying it, so the
// circulating supply still matches what the treasury says was issued.
func TestSlashingMovesTheBondToTheRewardPool(t *testing.T) {
	stake, ledger := newStakeLedger(t)
	acct, _ := token.GenerateAccount()
	id := acct.AccountID()

	if err := ledger.Credit(id, 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := ledger.Transfer(id, BondAccount(id), 750); err != nil {
		t.Fatalf("bond: %v", err)
	}

	taken, err := stake.Slash(id)
	if err != nil {
		t.Fatalf("Slash: %v", err)
	}
	if taken != 750 {
		t.Fatalf("slashed %d, want 750", taken)
	}
	if bonded, _ := stake.Bonded(id); bonded != 0 {
		t.Fatalf("bond is %d after slashing, want 0", bonded)
	}
	pool, _ := ledger.Balance(token.RewardPoolAccount)
	if pool != 750 {
		t.Fatalf("reward pool = %d, want the 750 that was slashed", pool)
	}
	// And the offender keeps only what it never bonded.
	if spendable, _ := ledger.Balance(id); spendable != 250 {
		t.Fatalf("offender's spendable balance = %d, want 250", spendable)
	}

	// Slashing an account with no bond takes nothing and is not an error.
	other, _ := token.GenerateAccount()
	if taken, err := stake.Slash(other.AccountID()); err != nil || taken != 0 {
		t.Fatalf("slashing an unbonded account = (%d, %v), want (0, nil)", taken, err)
	}
}

// The account name this package uses for the reward pool must be the one
// internal/token defines, or a slash would credit an account nothing else
// knows about.
func TestRewardPoolAccountNameAgreesWithToken(t *testing.T) {
	if rewardPoolAccount != token.RewardPoolAccount {
		t.Fatalf("consensus says %q, token says %q", rewardPoolAccount, token.RewardPoolAccount)
	}
}

// A validator may not withdraw, and after leaving it must wait. That delay is
// the whole reason a bond deters anything: without it a validator equivocates,
// is ejected, and withdraws before the evidence is committed.
func TestWithdrawalIsGatedOnLeavingAndWaiting(t *testing.T) {
	stake, ledger := newStakeLedger(t)
	accts, pubs := testKeys(t, 2)
	vs, _ := NewValidatorSet(pubs)
	id := accts[0].AccountID()
	const unbonding = 100

	if err := ledger.Credit(id, 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := ledger.Transfer(id, BondAccount(id), 1000); err != nil {
		t.Fatalf("bond: %v", err)
	}

	// While a validator: refused at any height.
	if _, err := stake.Withdraw(id, vs, unbonding, 1_000_000); !errors.Is(err, ErrBondLocked) {
		t.Fatalf("withdrawing while a validator = %v, want ErrBondLocked", err)
	}

	// Left the set at height 500, so withdrawable from 600.
	if err := stake.RecordLeftSet(id, 500); err != nil {
		t.Fatalf("RecordLeftSet: %v", err)
	}
	out, err := vs.WithChanges([]SetChange{{Kind: SetChangeRemove, ValidatorID: id}})
	if err != nil {
		t.Fatalf("WithChanges: %v", err)
	}
	if _, err := stake.Withdraw(id, out, unbonding, 599); !errors.Is(err, ErrBondLocked) {
		t.Fatalf("withdrawing one block early = %v, want ErrBondLocked", err)
	}
	if bonded, _ := stake.Bonded(id); bonded != 1000 {
		t.Fatalf("a refused withdrawal moved %d out of the bond", 1000-bonded)
	}

	returned, err := stake.Withdraw(id, out, unbonding, 600)
	if err != nil {
		t.Fatalf("Withdraw at the unlock height: %v", err)
	}
	if returned != 1000 {
		t.Fatalf("returned %d, want the whole 1000 bond", returned)
	}
	if spendable, _ := ledger.Balance(id); spendable != 1000 {
		t.Fatalf("spendable balance = %d after withdrawal, want 1000", spendable)
	}
}

// Rejoining and leaving again must restart the clock from the LATER departure,
// or a validator could serve one delay and then misbehave indefinitely.
func TestRejoiningRestartsTheUnbondingClock(t *testing.T) {
	stake, _ := newStakeLedger(t)
	acct, _ := token.GenerateAccount()
	id := acct.AccountID()

	if err := stake.RecordLeftSet(id, 100); err != nil {
		t.Fatalf("RecordLeftSet: %v", err)
	}
	if err := stake.ClearLeftSet(id); err != nil {
		t.Fatalf("ClearLeftSet: %v", err)
	}
	if _, ok, _ := stake.LeftSetAt(id); ok {
		t.Fatal("rejoining left the old departure height in place")
	}
	if err := stake.RecordLeftSet(id, 900); err != nil {
		t.Fatalf("RecordLeftSet: %v", err)
	}
	at, allowed, err := stake.WithdrawableAt(id, nil, 100)
	if err != nil || !allowed {
		t.Fatalf("WithdrawableAt = (%d, %v, %v)", at, allowed, err)
	}
	if at != 1000 {
		t.Fatalf("withdrawable at %d, want 1000 (left at 900 + 100)", at)
	}
}

// An account that was never a validator has nothing to wait out.
func TestANonValidatorCanWithdrawImmediately(t *testing.T) {
	stake, ledger := newStakeLedger(t)
	acct, _ := token.GenerateAccount()
	id := acct.AccountID()
	if err := ledger.Credit(id, 50); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := ledger.Transfer(id, BondAccount(id), 50); err != nil {
		t.Fatalf("bond: %v", err)
	}
	returned, err := stake.Withdraw(id, nil, 1000, 0)
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if returned != 50 {
		t.Fatalf("returned %d, want 50", returned)
	}
}

// --- recipient encoding ------------------------------------------------

func TestStakeRecipientRoundTrip(t *testing.T) {
	acct, _ := token.GenerateAccount()
	id := acct.AccountID()

	for _, c := range []struct {
		to   string
		op   StakeOp
		kind string
	}{
		{BondAccount(id), StakeOpBond, "bond"},
		{WithdrawRecipient(id), StakeOpWithdraw, "withdraw"},
	} {
		if !IsStakeRecipient(c.to) {
			t.Fatalf("%q not recognised as a stake recipient", c.to)
		}
		req, err := ParseStakeRecipient(c.to)
		if err != nil {
			t.Fatalf("ParseStakeRecipient(%s): %v", c.kind, err)
		}
		if req.Op != c.op || req.Account != id {
			t.Fatalf("parsed %s = %+v", c.kind, req)
		}
	}

	for _, bad := range []string{
		"consensus/stake/",
		"consensus/stake/bond/",
		"consensus/stake/bond/short",
		"consensus/stake/bond/" + id + "extra",
		"consensus/stake/promote/" + id,
		"consensus/stake/bond/" + hex.EncodeToString(make([]byte, 32))[:63] + "Z",
		"native/reward-pool",
		BondAccount(id) + "/nested",
	} {
		if _, err := ParseStakeRecipient(bad); err == nil {
			t.Fatalf("ParseStakeRecipient(%q) succeeded; a lookalike must not be read as a stake operation", bad)
		}
	}
	if IsStakeRecipient("native/reward-pool") {
		t.Fatal("the reward pool was read as a stake recipient")
	}
}

func TestSlashChangeRoundTrip(t *testing.T) {
	acct, _ := token.GenerateAccount()
	id := acct.AccountID()

	to := SlashValidatorRecipient(id)
	if !IsSetChangeRecipient(to) {
		t.Fatalf("%q not recognised as a set change", to)
	}
	change, err := ParseSetChange(to, 7)
	if err != nil {
		t.Fatalf("ParseSetChange: %v", err)
	}
	if change.Kind != SetChangeSlash || change.ValidatorID != id {
		t.Fatalf("parsed = %+v", change)
	}
	if change.String() != "slash:"+id {
		t.Fatalf("String() = %q", change.String())
	}
	if change.Recipient() != to {
		t.Fatalf("Recipient() = %q, want %q", change.Recipient(), to)
	}
	spec, err := ParseChangeSpec("slash:" + id)
	if err != nil {
		t.Fatalf("ParseChangeSpec: %v", err)
	}
	if spec.Kind != SetChangeSlash || spec.ValidatorID != id {
		t.Fatalf("spec = %+v", spec)
	}

	// A slash removes the member, like a removal does.
	_, pubs := testKeys(t, 3)
	vs, _ := NewValidatorSet(pubs)
	victim := vs.IDs()[0]
	next, err := vs.WithChanges([]SetChange{{Kind: SetChangeSlash, ValidatorID: victim}})
	if err != nil {
		t.Fatalf("WithChanges: %v", err)
	}
	if next.Contains(victim) {
		t.Fatal("a slash did not remove the validator")
	}
}

// --- the engine --------------------------------------------------------

// stakeCluster builds a cluster whose engines use bonded stake.
func stakeCluster(t *testing.T, n int, epoch, minBond uint64, approved ...string) ([]*testNode, func()) {
	t.Helper()
	return newCluster(t, n, func(c *Config) {
		c.ProposeInterval = 4 * time.Millisecond
		c.RoundTimeout = 40 * time.Millisecond
		c.HeadAnnounceInterval = 20 * time.Millisecond
		c.EpochLength = epoch
		c.ApprovedSetChanges = approved
		c.MinBond = minBond
		c.ZeroMinBond = minBond == 0
		c.UnbondingPeriod = 4
	})
}

// bondAll has every validator in the cluster bond the given amount through a
// committed transaction, and waits until every node sees every bond. A set is
// only weighted once every member has bonded, so this is the precondition for
// any test about voting power.
func bondAll(t *testing.T, nodes []*testNode, amounts []uint64) {
	t.Helper()
	for i, nd := range nodes {
		mintAll(t, nodes, nd.acct.AccountID(), amounts[i])
		tx, err := nodes[0].engine.SubmitBond(nd.acct, amounts[i], 1)
		if err != nil {
			t.Fatalf("SubmitBond for node %d: %v", i, err)
		}
		for _, peerNode := range nodes[1:] {
			if err := peerNode.engine.Submit(tx); err != nil {
				t.Fatalf("submit bond: %v", err)
			}
		}
	}
	waitFor(t, 20*time.Second, "every bond to commit on every node", func() bool {
		for i, owner := range nodes {
			for _, nd := range nodes {
				got, err := nd.engine.BondedStake(owner.acct.AccountID())
				if err != nil || got != amounts[i] {
					return false
				}
			}
		}
		return true
	})
}

// Admission requires a bond. Without this rule an account could be voted in
// with nothing at risk, which is what stake exists to prevent.
func TestAdmissionRequiresTheMinimumBond(t *testing.T) {
	newcomer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Deliberately NOT approved: this test inspects the validity rule directly,
	// and an approved change would be offered and committed by the cluster
	// itself, so the second check below would race with it becoming in force.
	nodes, stop := stakeCluster(t, 4, 2, 1000)
	defer stop()

	proposer := nodes[0].acct
	mintAll(t, nodes, proposer.AccountID(), 10000)

	// Unbonded: the block carrying the admission is invalid, not merely unpopular.
	tx := signedTransfer(t, proposer, AddValidatorRecipient(newcomer.PublicKey), 0, 1)
	nodes[0].engine.mu.Lock()
	err = nodes[0].engine.verifySetChangeLocked(tx, nodes[0].engine.height)
	nodes[0].engine.mu.Unlock()
	if !errors.Is(err, ErrInsufficientBond) {
		t.Fatalf("admitting an unbonded account = %v, want ErrInsufficientBond", err)
	}

	// Bond enough on every node's ledger and the same change becomes valid.
	for _, nd := range nodes {
		if err := nd.ledger.Credit(newcomer.AccountID(), 1000); err != nil {
			t.Fatalf("credit: %v", err)
		}
		if err := nd.ledger.Transfer(newcomer.AccountID(), BondAccount(newcomer.AccountID()), 1000); err != nil {
			t.Fatalf("bond: %v", err)
		}
	}
	nodes[0].engine.mu.Lock()
	err = nodes[0].engine.verifySetChangeLocked(tx, nodes[0].engine.height)
	nodes[0].engine.mu.Unlock()
	if err != nil {
		t.Fatalf("admitting a bonded account = %v, want nil", err)
	}
}

// A bond submitted as a transaction must commit and then show up as voting
// power at the next epoch boundary - not before, because power has to change at
// the same height everywhere.
func TestABondBecomesVotingPowerAtTheEpochBoundary(t *testing.T) {
	nodes, stop := stakeCluster(t, 4, 4, 0)
	defer stop()

	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()

	amounts := []uint64{4_000, 3_000, 2_000, 1_000}
	bondAll(t, nodes, amounts)

	waitFor(t, 20*time.Second, "the bonds to become voting power everywhere", func() bool {
		for _, nd := range nodes {
			if nd.engine.vset().TotalPower() != 10_000 {
				return false
			}
		}
		return true
	})

	for i, nd := range nodes {
		vs := nd.engine.vset()
		for j, owner := range nodes {
			if got := vs.Power(owner.acct.AccountID()); got != amounts[j] {
				t.Fatalf("node %d gives node %d power %d, want its %d bond", i, j, got, amounts[j])
			}
		}
		// (2 * 10000) / 3 + 1
		if got, want := vs.QuorumPower(), uint64(6_667); got != want {
			t.Fatalf("node %d quorum = %d, want %d", i, got, want)
		}
		if vs.TotalPower() != nodes[0].engine.vset().TotalPower() {
			t.Fatalf("node %d disagrees with node 0 about total power", i)
		}
	}

	// The bond left the spendable balance.
	bonded, err := nodes[0].engine.BondedStake(nodes[0].acct.AccountID())
	if err != nil {
		t.Fatalf("BondedStake: %v", err)
	}
	if bonded != amounts[0] {
		t.Fatalf("bonded = %d, want %d", bonded, amounts[0])
	}
}

// Weighting a PARTLY bonded set is the dangerous case: an unbonded member
// counts 1, so the first validator to bond anything would hold essentially all
// the voting power - and could not even be slashed, because a slash needs a
// quorum it now controls. The set has to stay at headcount until everyone has
// bonded.
func TestAPartlyBondedSetStaysAtEqualPower(t *testing.T) {
	nodes, stop := stakeCluster(t, 4, 2, 0)
	defer stop()

	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()

	// One validator bonds a fortune; the other three bond nothing.
	rich := nodes[0].acct
	const huge = 500_000
	mintAll(t, nodes, rich.AccountID(), huge)
	tx, err := nodes[0].engine.SubmitBond(rich, huge, 1)
	if err != nil {
		t.Fatalf("SubmitBond: %v", err)
	}
	for _, nd := range nodes[1:] {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit bond: %v", err)
		}
	}
	waitFor(t, 20*time.Second, "the bond to commit", func() bool {
		got, err := nodes[0].engine.BondedStake(rich.AccountID())
		return err == nil && got == huge
	})

	// Give the chain several epoch boundaries to get it wrong.
	before := nodes[0].engine.Height()
	waitFor(t, 20*time.Second, "several epoch boundaries to pass", func() bool {
		return nodes[0].engine.Height() > before+6
	})

	for i, nd := range nodes {
		vs := nd.engine.vset()
		if vs.TotalPower() != 4 {
			t.Fatalf("node %d total power = %d, want 4: a partly-bonded set must stay at equal power", i, vs.TotalPower())
		}
		if got := vs.Power(rich.AccountID()); got != 1 {
			t.Fatalf("node %d gave the only bonded validator power %d; it would control every quorum", i, got)
		}
		if vs.QuorumPower() != 3 {
			t.Fatalf("node %d quorum = %d, want 3", i, vs.QuorumPower())
		}
	}
}

// A withdrawal from a sitting validator must be refused as a validity rule, so
// no honest leader proposes it and no block carrying it can commit.
func TestAValidatorCannotWithdrawWhileServing(t *testing.T) {
	nodes, stop := stakeCluster(t, 4, 4, 0)
	defer stop()

	eng := nodes[0].engine
	validator := nodes[0].acct
	mintAll(t, nodes, validator.AccountID(), 10000)

	tx := signedTransfer(t, validator, WithdrawRecipient(validator.AccountID()), 0, 1)
	eng.mu.Lock()
	err := eng.verifyStakeTxLocked(tx, eng.height)
	eng.mu.Unlock()
	if !errors.Is(err, ErrBondLocked) {
		t.Fatalf("withdrawing while a validator = %v, want ErrBondLocked", err)
	}
}

// Staking is for your own account only. Bonding into someone else's would gift
// them voting power; withdrawing from theirs would be theft.
func TestStakeTransactionsMustNameTheirOwnSender(t *testing.T) {
	nodes, stop := stakeCluster(t, 4, 4, 0)
	defer stop()

	eng := nodes[0].engine
	alice := nodes[0].acct
	bob := nodes[1].acct
	mintAll(t, nodes, alice.AccountID(), 10000)

	for _, to := range []string{
		BondAccount(bob.AccountID()),
		WithdrawRecipient(bob.AccountID()),
	} {
		tx := signedTransfer(t, alice, to, 100, 1)
		eng.mu.Lock()
		err := eng.verifyStakeTxLocked(tx, eng.height)
		eng.mu.Unlock()
		if err == nil {
			t.Fatalf("a stake transaction to %q from a different sender was accepted", to)
		}
		if !errors.Is(err, ErrInvalidMessage) {
			t.Fatalf("error = %v, want ErrInvalidMessage", err)
		}
	}

	// A bond must also carry an amount, and a withdrawal must not.
	zeroBond := signedTransfer(t, alice, BondAccount(alice.AccountID()), 0, 2)
	eng.mu.Lock()
	err := eng.verifyStakeTxLocked(zeroBond, eng.height)
	eng.mu.Unlock()
	if err == nil {
		t.Fatal("a zero-amount bond was accepted")
	}

	valuedWithdraw := signedTransfer(t, alice, WithdrawRecipient(alice.AccountID()), 5, 3)
	eng.mu.Lock()
	err = eng.verifyStakeTxLocked(valuedWithdraw, eng.height)
	eng.mu.Unlock()
	if err == nil {
		t.Fatal("a withdrawal carrying an amount was accepted")
	}
}

// The whole point, end to end: an equivocating validator loses its bond, and
// the coins land in the reward pool rather than vanishing.
func TestEquivocationCostsTheOffenderItsBond(t *testing.T) {
	nodes, stop := stakeCluster(t, 4, 2, 0)
	defer stop()

	offender := nodes[1]
	offenderID := offender.acct.AccountID()

	// Everyone bonds, which is what a weighted set requires - and it has to be
	// a distribution the fault threshold allows. An offender holding more than a
	// third of the stake cannot be slashed by anyone, because a slash needs a
	// quorum it would control; that is the BFT assumption, not a defect, and a
	// test that violated it would be testing the impossible.
	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()
	const bond = 7000
	bondAll(t, nodes, []uint64{bond, bond, bond, bond})
	waitFor(t, 20*time.Second, "the set to be weighted by stake", func() bool {
		for _, nd := range nodes {
			if nd.engine.vset().TotalPower() != 4*bond {
				return false
			}
		}
		return true
	})

	// Publish the two conflicting votes for the height the chain is actually AT.
	// A vote for a height already passed is ignored, so with traffic flowing the
	// pair has to be aimed at a live height - and re-aimed if the chain moves on
	// before they are tallied.
	relay := nodes[0].bus.endpoint(peer.ID("slash-relay"))
	waitFor(t, 20*time.Second, "the equivocation to be detected", func() bool {
		h := nodes[0].engine.Height()
		publishVote(t, relay, buildVote(t, offender.acct, h, 0, hashOf(0xB1)))
		publishVote(t, relay, buildVote(t, offender.acct, h, 0, hashOf(0xB2)))
		for _, nd := range nodes {
			records, err := nd.evidence.ByValidator(offenderID)
			if err != nil || len(records) == 0 {
				return false
			}
		}
		return true
	})

	waitFor(t, 20*time.Second, "the offender to be slashed everywhere", func() bool {
		for _, nd := range nodes {
			if nd.engine.vset().Contains(offenderID) {
				return false
			}
			bonded, err := nd.engine.BondedStake(offenderID)
			if err != nil || bonded != 0 {
				return false
			}
		}
		return true
	})

	for i, nd := range nodes {
		pool, err := nd.ledger.Balance(token.RewardPoolAccount)
		if err != nil {
			t.Fatalf("node %d balance: %v", i, err)
		}
		if pool < bond {
			t.Fatalf("node %d reward pool = %d, want at least the slashed %d", i, pool, bond)
		}
		// The honest validators keep theirs. Only the offender pays.
		for j, other := range nodes {
			if j == 1 {
				continue
			}
			if got, _ := nd.engine.BondedStake(other.acct.AccountID()); got != bond {
				t.Fatalf("node %d shows node %d bonded %d, want its untouched %d", i, j, got, bond)
			}
		}
		// The offender must not get the bond back by withdrawing: there is
		// nothing left to withdraw.
		if bonded, _ := nd.engine.BondedStake(offenderID); bonded != 0 {
			t.Fatalf("node %d still shows a bond of %d for the offender", i, bonded)
		}
	}
}

// Leaving the set must start the unbonding clock, so a validator cannot leave
// and immediately pull its bond out from under a pending slash.
func TestLeavingTheSetStartsTheUnbondingClock(t *testing.T) {
	// No approvals at construction: the id to remove is only known once the
	// cluster exists, and a malformed approval fails startup by design.
	nodes, stop := stakeCluster(t, 4, 2, 0)
	defer stop()

	leaver := nodes[3].acct.AccountID()
	approval := "remove:" + leaver
	for _, nd := range nodes {
		nd.engine.mu.Lock()
		spec, err := ParseChangeSpec(approval)
		if err != nil {
			nd.engine.mu.Unlock()
			t.Fatalf("ParseChangeSpec: %v", err)
		}
		nd.engine.approvedChanges[approval] = struct{}{}
		nd.engine.approvedSpecs = append(nd.engine.approvedSpecs, spec)
		nd.engine.mu.Unlock()
	}

	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()

	waitFor(t, 20*time.Second, "the validator to leave the set", func() bool {
		for _, nd := range nodes {
			if nd.engine.vset().Contains(leaver) {
				return false
			}
		}
		return true
	})

	for i, nd := range nodes {
		at, ok, err := nd.engine.stake.LeftSetAt(leaver)
		if err != nil {
			t.Fatalf("node %d LeftSetAt: %v", i, err)
		}
		if !ok {
			t.Fatalf("node %d recorded no departure height, so nothing gates the withdrawal", i)
		}
		if at == 0 {
			t.Fatalf("node %d recorded a departure at height 0", i)
		}
	}
}

// publishVote puts a raw vote on the wire, the way an attacker relaying an
// offender's two conflicting votes would.
func publishVote(t *testing.T, ep Transport, v Vote) {
	t.Helper()
	data, err := json.Marshal(&v)
	if err != nil {
		t.Fatalf("marshal vote: %v", err)
	}
	if err := ep.Publish(context.Background(), TopicVote, data); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// A weighted cluster has to keep committing and keep agreeing. Weighting the
// quorum touches the safety-critical counting path, so this is the property
// that matters: unequal power, real blocks, identical balances.
func TestAWeightedClusterCommitsAndAgrees(t *testing.T) {
	nodes, stop := stakeCluster(t, 4, 2, 0)
	defer stop()

	// Deliberately lopsided: one validator holds most of the stake, so a
	// headcount majority and a stake majority are different sets.
	//
	// Bonded through committed transactions rather than written to each ledger
	// by hand, because that is what makes the boundary re-weight: a bond takes
	// effect because a bond transaction committed.
	bonds := []uint64{60_000, 20_000, 15_000, 5_000}
	for i, nd := range nodes {
		id := nd.acct.AccountID()
		mintAll(t, nodes, id, bonds[i])
		tx, err := nodes[0].engine.SubmitBond(nd.acct, bonds[i], 1)
		if err != nil {
			t.Fatalf("SubmitBond for node %d: %v", i, err)
		}
		for _, peerNode := range nodes[1:] {
			if err := peerNode.engine.Submit(tx); err != nil {
				t.Fatalf("submit bond: %v", err)
			}
		}
	}

	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()

	waitFor(t, 20*time.Second, "every node to weight the set the same way", func() bool {
		want := nodes[0].engine.vset()
		if want.TotalPower() != 100_000 {
			return false
		}
		for _, nd := range nodes {
			got := nd.engine.vset()
			if got.TotalPower() != want.TotalPower() || got.QuorumPower() != want.QuorumPower() {
				return false
			}
			for _, id := range want.IDs() {
				if got.Power(id) != want.Power(id) {
					return false
				}
			}
		}
		return true
	})

	// (2 * 100000) / 3 + 1
	if got, want := nodes[0].engine.vset().QuorumPower(), uint64(66_667); got != want {
		t.Fatalf("quorum = %d, want %d", got, want)
	}

	// The chain keeps committing with weighted votes.
	before := nodes[0].engine.Height()
	waitFor(t, 20*time.Second, "progress under weighted voting", func() bool {
		return nodes[0].engine.Height() > before+3
	})

	// And no divergence: every pair of nodes agrees on the committed prefix.
	waitFor(t, 20*time.Second, "the nodes to agree on the committed chain", func() bool {
		l0, err := nodes[0].chain.Len()
		if err != nil || l0 == 0 {
			return false
		}
		for _, nd := range nodes[1:] {
			l, err := nd.chain.Len()
			if err != nil || l == 0 {
				return false
			}
			shared := l0
			if l < shared {
				shared = l
			}
			for h := uint64(0); h < shared; h++ {
				a, err := nodes[0].chain.BlockAt(h)
				if err != nil {
					return false
				}
				b, err := nd.chain.BlockAt(h)
				if err != nil {
					return false
				}
				if string(a.Hash()) != string(b.Hash()) {
					t.Fatalf("nodes 0 and %s diverged at height %d under weighted voting", nd.peerID, h)
				}
			}
		}
		return true
	})
}

// The full round trip through committed transactions, with nothing written to a
// ledger by hand: bond, leave the set, wait out the unbonding period, and get
// the coins back.
func TestBondLeaveWaitWithdrawRoundTrip(t *testing.T) {
	// FIVE validators, not four, because one of them leaves. A four-member set
	// that loses a member is a three-member set, and a three-member set has
	// quorum three: it tolerates no slow node at all, so every round times out
	// before all three precommit and the chain crawls. Five leaves four, quorum
	// three, which tolerates one lagging node. That is a property of the fault
	// threshold, not something to tune the timeout around.
	nodes, stop := stakeCluster(t, 5, 2, 0)
	defer stop()

	leaver := nodes[4]
	leaverID := leaver.acct.AccountID()
	const funded = 9_000
	const bond = 6_000
	mintAll(t, nodes, leaverID, funded)

	// 1. Bond, as a committed transaction.
	bondTx, err := nodes[0].engine.SubmitBond(leaver.acct, bond, 1)
	if err != nil {
		t.Fatalf("SubmitBond: %v", err)
	}
	for _, nd := range nodes[1:] {
		if err := nd.engine.Submit(bondTx); err != nil {
			t.Fatalf("submit bond: %v", err)
		}
	}

	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()

	waitFor(t, 20*time.Second, "the bond to commit everywhere", func() bool {
		for _, nd := range nodes {
			bonded, err := nd.engine.BondedStake(leaverID)
			if err != nil || bonded != bond {
				return false
			}
		}
		return true
	})
	if spendable, _ := nodes[0].ledger.Balance(leaverID); spendable != funded-bond {
		t.Fatalf("spendable balance = %d, want %d: the bond must leave it", spendable, funded-bond)
	}

	// 2. A withdrawal now is refused: it is still a validator.
	withdrawTx, err := nodes[0].engine.SubmitWithdrawBond(leaver.acct, 2)
	if err != nil {
		t.Fatalf("SubmitWithdrawBond: %v", err)
	}
	for _, nd := range nodes[1:] {
		if err := nd.engine.Submit(withdrawTx); err != nil {
			t.Fatalf("submit withdrawal: %v", err)
		}
	}
	time.Sleep(400 * time.Millisecond)
	if bonded, _ := nodes[0].engine.BondedStake(leaverID); bonded != bond {
		t.Fatalf("the bond was returned while its owner was still a validator (now %d)", bonded)
	}

	// 3. Have the network remove it. Every operator approves, so it carries.
	spec, err := ParseChangeSpec("remove:" + leaverID)
	if err != nil {
		t.Fatalf("ParseChangeSpec: %v", err)
	}
	for _, nd := range nodes {
		nd.engine.mu.Lock()
		nd.engine.approvedChanges[spec.String()] = struct{}{}
		nd.engine.approvedSpecs = append(nd.engine.approvedSpecs, spec)
		nd.engine.mu.Unlock()
	}
	waitFor(t, 20*time.Second, "the validator to leave the set", func() bool {
		for _, nd := range nodes {
			if nd.engine.vset().Contains(leaverID) {
				return false
			}
		}
		return true
	})

	// 4. The withdrawal is still in the mempool and lands by itself once the
	//    unbonding period (4 blocks, per stakeCluster) has elapsed. Nothing is
	//    resubmitted: waiting is the mechanism.
	waitFor(t, 25*time.Second, "the bond to be returned after the unbonding period", func() bool {
		bonded, err := nodes[0].engine.BondedStake(leaverID)
		return err == nil && bonded == 0
	})
	waitFor(t, 20*time.Second, "the coins to be spendable again", func() bool {
		bal, err := nodes[0].ledger.Balance(leaverID)
		return err == nil && bal == funded
	})

	// The departure height was recorded, which is what gated the wait.
	if at, ok, err := nodes[0].engine.stake.LeftSetAt(leaverID); err != nil || !ok || at == 0 {
		t.Fatalf("LeftSetAt = (%d, %v, %v), want a recorded height", at, ok, err)
	}
}
