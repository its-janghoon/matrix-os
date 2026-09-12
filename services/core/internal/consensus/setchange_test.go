package consensus

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// --- encoding -----------------------------------------------------------

func TestSetChangeRecipientRoundTrip(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	add := AddValidatorRecipient(acct.PublicKey)
	if !IsSetChangeRecipient(add) {
		t.Fatalf("%q not recognised as a set change", add)
	}
	parsed, err := ParseSetChange(add, 7)
	if err != nil {
		t.Fatalf("ParseSetChange: %v", err)
	}
	if parsed.Kind != SetChangeAdd || parsed.ValidatorID != acct.AccountID() || parsed.Height != 7 {
		t.Fatalf("parsed = %+v", parsed)
	}
	if parsed.Recipient() != add {
		t.Fatalf("Recipient() = %q, want %q", parsed.Recipient(), add)
	}
	if want := "add:" + hex.EncodeToString(acct.PublicKey); parsed.String() != want {
		t.Fatalf("String() = %q, want %q", parsed.String(), want)
	}

	remove := RemoveValidatorRecipient(acct.AccountID())
	parsedRemove, err := ParseSetChange(remove, 9)
	if err != nil {
		t.Fatalf("ParseSetChange(remove): %v", err)
	}
	if parsedRemove.Kind != SetChangeRemove || parsedRemove.ValidatorID != acct.AccountID() {
		t.Fatalf("parsed remove = %+v", parsedRemove)
	}
	if parsedRemove.String() != "remove:"+acct.AccountID() {
		t.Fatalf("String() = %q", parsedRemove.String())
	}
}

func TestParseSetChangeRejectsMalformed(t *testing.T) {
	cases := []string{
		"alice",
		"consensus/validator/",
		"consensus/validator/add/",
		"consensus/validator/add/nothex",
		"consensus/validator/add/" + hex.EncodeToString(make([]byte, 16)), // too short
		"consensus/validator/remove/short",
		"consensus/validator/promote/" + hex.EncodeToString(make([]byte, 32)),
		"native/reward-pool",
	}
	for _, to := range cases {
		if _, err := ParseSetChange(to, 0); err == nil {
			t.Fatalf("ParseSetChange(%q) succeeded; a lookalike recipient must not be read as a change", to)
		}
	}
	// A plain transfer must not be mistaken for one either.
	if IsSetChangeRecipient("native/reward-pool") {
		t.Fatal("the reward pool was read as a set change")
	}
}

// --- WithChanges --------------------------------------------------------

func testKeys(t *testing.T, n int) ([]*token.Account, []ed25519.PublicKey) {
	t.Helper()
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
	return accts, pubs
}

func TestWithChangesAddsAndRemoves(t *testing.T) {
	accts, pubs := testKeys(t, 4)
	vs, err := NewValidatorSet(pubs)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	newcomer, newPubs := testKeys(t, 1)

	next, err := vs.WithChanges([]SetChange{
		{Kind: SetChangeAdd, PublicKey: newPubs[0], ValidatorID: newcomer[0].AccountID()},
		{Kind: SetChangeRemove, ValidatorID: accts[0].AccountID()},
	})
	if err != nil {
		t.Fatalf("WithChanges: %v", err)
	}
	if next.Len() != 4 {
		t.Fatalf("new set has %d members, want 4 (one in, one out)", next.Len())
	}
	if next.Contains(accts[0].AccountID()) {
		t.Fatal("removed validator is still in the set")
	}
	if !next.Contains(newcomer[0].AccountID()) {
		t.Fatal("admitted validator is not in the set")
	}
	// The receiver is untouched: a set already validating a height must not
	// change under it.
	if vs.Len() != 4 || !vs.Contains(accts[0].AccountID()) {
		t.Fatal("WithChanges mutated the original set")
	}
}

// TestWithChangesAppliesRemovalsFirst pins the order, because a block carrying
// both "remove X" and "add X" must resolve the same way on every node. Two
// nodes deriving different sets from one committed block is a fork.
func TestWithChangesAppliesRemovalsFirst(t *testing.T) {
	accts, pubs := testKeys(t, 4)
	vs, _ := NewValidatorSet(pubs)
	target := accts[1]

	next, err := vs.WithChanges([]SetChange{
		{Kind: SetChangeAdd, PublicKey: target.PublicKey, ValidatorID: target.AccountID()},
		{Kind: SetChangeRemove, ValidatorID: target.AccountID()},
	})
	if err != nil {
		t.Fatalf("WithChanges: %v", err)
	}
	if !next.Contains(target.AccountID()) {
		t.Fatal("with both a remove and an add for one key, the add must win (removals apply first)")
	}

	// And in the other input order, the same answer.
	other, err := vs.WithChanges([]SetChange{
		{Kind: SetChangeRemove, ValidatorID: target.AccountID()},
		{Kind: SetChangeAdd, PublicKey: target.PublicKey, ValidatorID: target.AccountID()},
	})
	if err != nil {
		t.Fatalf("WithChanges: %v", err)
	}
	if other.Len() != next.Len() {
		t.Fatal("the result depends on the order the changes were listed in")
	}
}

func TestWithChangesRefusesAnUnusableSet(t *testing.T) {
	accts, pubs := testKeys(t, 3)
	vs, _ := NewValidatorSet(pubs)

	var removeAll []SetChange
	for _, a := range accts {
		removeAll = append(removeAll, SetChange{Kind: SetChangeRemove, ValidatorID: a.AccountID()})
	}
	if _, err := vs.WithChanges(removeAll); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("emptying the set = %v, want a refusal: there would be nobody left to admit anyone", err)
	}

	tooMany := make([]SetChange, 0, MaxValidators+1)
	for i := 0; i <= MaxValidators; i++ {
		a, err := token.GenerateAccount()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		tooMany = append(tooMany, SetChange{Kind: SetChangeAdd, PublicKey: a.PublicKey, ValidatorID: a.AccountID()})
	}
	if _, err := vs.WithChanges(tooMany); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("overflowing the set = %v, want a refusal", err)
	}
}

// --- persistence --------------------------------------------------------

func TestSetStoreRoundTrip(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	_, pubs := testKeys(t, 4)
	vs, _ := NewValidatorSet(pubs)
	sets := NewSetStore(store)

	loaded, at, err := sets.LoadActive()
	if err != nil {
		t.Fatalf("LoadActive on an empty store: %v", err)
	}
	if loaded != nil {
		t.Fatal("an empty store reported a set; config supplies the genesis set")
	}

	if err := sets.SaveActive(vs, 42); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	loaded, at, err = sets.LoadActive()
	if err != nil {
		t.Fatalf("LoadActive: %v", err)
	}
	if loaded == nil || loaded.Len() != 4 || at != 42 {
		t.Fatalf("loaded = %v at %d", loaded, at)
	}
	for _, id := range vs.IDs() {
		if !loaded.Contains(id) {
			t.Fatalf("loaded set is missing %s", id)
		}
	}

	pending := []SetChange{{Kind: SetChangeRemove, ValidatorID: vs.IDs()[0], Height: 7}}
	if err := sets.SavePending(pending); err != nil {
		t.Fatalf("SavePending: %v", err)
	}
	back, err := sets.LoadPending()
	if err != nil {
		t.Fatalf("LoadPending: %v", err)
	}
	if len(back) != 1 || back[0].ValidatorID != pending[0].ValidatorID {
		t.Fatalf("pending round trip = %+v", back)
	}
}

// --- cluster behaviour --------------------------------------------------

// setChangeCluster is a cluster with a short epoch so a boundary arrives inside
// a test, and with every node approving the given changes.
func setChangeCluster(t *testing.T, n int, epoch uint64, approved ...string) ([]*testNode, func()) {
	t.Helper()
	return newCluster(t, n, func(c *Config) {
		c.ProposeInterval = 4 * time.Millisecond
		c.RoundTimeout = 40 * time.Millisecond
		c.HeadAnnounceInterval = 20 * time.Millisecond
		c.EpochLength = epoch
		c.ApprovedSetChanges = approved
	})
}

// submitSetChange has `from` submit a change to every node's mempool, the way a
// client fanning out would.
func submitSetChange(t *testing.T, nodes []*testNode, from *token.Account, recipient string, nonce uint64) {
	t.Helper()
	tx := signedTransfer(t, from, recipient, 0, nonce)
	for _, nd := range nodes {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
}

// waitForOrReport is waitFor with a diagnostic: on timeout it prints what the
// cluster actually looked like, so a failure says which of several causes it was
// rather than only that it did not happen.
func waitForOrReport(t *testing.T, timeout time.Duration, what string, cond func() bool, report func() string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s; state was:%s", what, report())
		}
		time.Sleep(3 * time.Millisecond)
	}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(3 * time.Millisecond)
	}
}

// TestApprovedSetChangeAdmitsAValidatorAtTheEpochBoundary is the feature: a
// validator joins a running network, with no restart and no coordinated config
// edit, because the change rides in a committed block.
func TestApprovedSetChangeAdmitsAValidatorAtTheEpochBoundary(t *testing.T) {
	newcomer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	approval := "add:" + hex.EncodeToString(newcomer.PublicKey)

	nodes, stop := setChangeCluster(t, 4, 2, approval)
	defer stop()

	alice := nodes[0].acct
	mintAll(t, nodes, alice.AccountID(), 1000)
	submitSetChange(t, nodes, alice, AddValidatorRecipient(newcomer.PublicKey), 0)

	// Keep ordinary traffic flowing so heights keep coming and an epoch boundary
	// is reached.
	go func() {
		for i := uint64(1); i < 12; i++ {
			tx := signedTransfer(t, alice, fmt.Sprintf("recipient-%d", i), 1, i)
			for _, nd := range nodes {
				_ = nd.engine.Submit(tx)
			}
			time.Sleep(12 * time.Millisecond)
		}
	}()

	waitFor(t, 10*time.Second, "every node to admit the newcomer", func() bool {
		for _, nd := range nodes {
			if !nd.engine.vset().Contains(newcomer.AccountID()) {
				return false
			}
		}
		return true
	})

	for i, nd := range nodes {
		vs := nd.engine.vset()
		if vs.Len() != 5 {
			t.Fatalf("node %d has %d validators, want 5", i, vs.Len())
		}
		if vs.QuorumPower() != 4 {
			t.Fatalf("node %d quorum = %d, want 4 for a set of 5", i, vs.QuorumPower())
		}
		// The set must have taken effect at an epoch boundary, not at whatever
		// height each node happened to apply the block.
		saved, at, err := nd.sets.LoadActive()
		if err != nil {
			t.Fatalf("node %d LoadActive: %v", i, err)
		}
		if saved == nil {
			t.Fatalf("node %d did not persist the new set, so a restart would forget it", i)
		}
		if at%2 != 0 {
			t.Fatalf("node %d applied the set at height %d, which is not an epoch boundary", i, at)
		}
	}

	// And the chain keeps committing after the change.
	before := nodes[0].engine.Height()
	waitFor(t, 8*time.Second, "progress after the set change", func() bool {
		return nodes[0].engine.Height() > before
	})
}

// TestEveryNodeAppliesTheChangeAtTheSameHeight is the safety property. Two
// nodes switching sets at different heights disagree about who leads which
// round, which is a fork.
func TestEveryNodeAppliesTheChangeAtTheSameHeight(t *testing.T) {
	newcomer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	nodes, stop := setChangeCluster(t, 4, 2, "add:"+hex.EncodeToString(newcomer.PublicKey))
	defer stop()

	alice := nodes[0].acct
	mintAll(t, nodes, alice.AccountID(), 1000)
	submitSetChange(t, nodes, alice, AddValidatorRecipient(newcomer.PublicKey), 0)
	go func() {
		for i := uint64(1); i < 12; i++ {
			tx := signedTransfer(t, alice, fmt.Sprintf("r-%d", i), 1, i)
			for _, nd := range nodes {
				_ = nd.engine.Submit(tx)
			}
			time.Sleep(12 * time.Millisecond)
		}
	}()

	waitFor(t, 10*time.Second, "all nodes to admit the newcomer", func() bool {
		for _, nd := range nodes {
			if !nd.engine.vset().Contains(newcomer.AccountID()) {
				return false
			}
		}
		return true
	})

	heights := map[uint64]int{}
	for _, nd := range nodes {
		_, at, err := nd.sets.LoadActive()
		if err != nil {
			t.Fatalf("LoadActive: %v", err)
		}
		heights[at]++
	}
	if len(heights) != 1 {
		t.Fatalf("nodes applied the set at different heights: %v", heights)
	}
}

// TestUnapprovedSetChangeDoesNotPass is the veto. One validator proposes a
// change nobody else listed; the set must not move, and the chain must keep
// working.
func TestUnapprovedSetChangeDoesNotPass(t *testing.T) {
	newcomer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// No approvals anywhere: the default is to vote against.
	nodes, stop := setChangeCluster(t, 4, 2)
	defer stop()

	alice := nodes[0].acct
	mintAll(t, nodes, alice.AccountID(), 1000)
	// Only the proposer holds it, which is what a single validator acting alone
	// looks like.
	tx := signedTransfer(t, alice, AddValidatorRecipient(newcomer.PublicKey), 0, 0)
	if err := nodes[0].engine.Submit(tx); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// Ordinary traffic, so the chain has reason to advance past several epochs.
	// Open-ended rather than a fixed batch: one block can carry any number of
	// transfers, so a fixed batch is not a reliable way to produce a fixed
	// number of HEIGHTS - it produced enough until the machine was busy enough
	// to batch them together.
	stopTraffic := keepTrafficFlowing(t, nodes, alice)
	defer stopTraffic()

	waitFor(t, 15*time.Second, "the chain to pass several epochs", func() bool {
		return nodes[0].engine.Height() >= 4
	})

	for i, nd := range nodes {
		if nd.engine.vset().Contains(newcomer.AccountID()) {
			t.Fatalf("node %d admitted a validator nobody approved", i)
		}
		if nd.engine.vset().Len() != 4 {
			t.Fatalf("node %d set size = %d, want 4", i, nd.engine.vset().Len())
		}
	}
}

// TestRemovedValidatorStopsVotingAndTheRestCarryOn covers ejection, which is
// what makes the equivocation evidence actionable.
func TestRemovedValidatorStopsVotingAndTheRestCarryOn(t *testing.T) {
	nodes, stop := setChangeCluster(t, 4, 2)
	defer stop()

	victim := nodes[3]
	approval := "remove:" + victim.acct.AccountID()
	// Re-create the cluster with the approval in place; the id is only known once
	// the accounts exist, so this test approves after construction by reaching
	// into each engine the way an operator's config would have.
	for _, nd := range nodes {
		nd.engine.mu.Lock()
		nd.engine.approvedChanges[approval] = struct{}{}
		nd.engine.mu.Unlock()
	}

	alice := nodes[0].acct
	mintAll(t, nodes, alice.AccountID(), 1000)
	submitSetChange(t, nodes, alice, RemoveValidatorRecipient(victim.acct.AccountID()), 0)
	// Open-ended traffic: the assertions below run AFTER the removal has landed,
	// and a fixed batch of transfers is long spent by then - leaving the chain
	// idle exactly where the test wants to see it still committing.
	stopTraffic := keepTrafficFlowing(t, nodes, alice)
	defer stopTraffic()

	waitFor(t, 10*time.Second, "the validator to be removed everywhere", func() bool {
		for _, nd := range nodes {
			if nd.engine.vset().Contains(victim.acct.AccountID()) {
				return false
			}
		}
		return true
	})

	for i, nd := range nodes {
		if got := nd.engine.vset().Len(); got != 3 {
			t.Fatalf("node %d set size = %d, want 3", i, got)
		}
		if got := nd.engine.vset().QuorumPower(); got != 3 {
			t.Fatalf("node %d quorum = %d, want 3 for a set of 3", i, got)
		}
	}

	// The ejected node knows it: it follows and applies but no longer votes.
	victim.engine.mu.Lock()
	stillVoting := victim.engine.isValidator.Load()
	victim.engine.mu.Unlock()
	if stillVoting {
		t.Fatal("the removed node still considers itself a validator")
	}

	// And the remaining three keep committing. Quorum is 3 of 3 here, which
	// tolerates no lag at all, so this is given a generous window rather than a
	// tight one: it is asserting that progress happens, not how fast.
	before := nodes[0].engine.Height()
	waitFor(t, 20*time.Second, "progress after the removal", func() bool {
		return nodes[0].engine.Height() > before
	})
	waitForConvergedLength(t, nodes, 8*time.Second)
}

// TestSetChangeFromANonValidatorMakesTheBlockInvalid is a consensus rule, not a
// policy: any node reaches the same verdict, so a leader cannot smuggle a set
// change in on an outsider's signature.
func TestSetChangeFromANonValidatorMakesTheBlockInvalid(t *testing.T) {
	nodes, stop := setChangeCluster(t, 4, 4)
	defer stop()

	outsider, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	newcomer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	mintAll(t, nodes, outsider.AccountID(), 1000)

	eng := nodes[0].engine
	head, _, err := nodes[0].chain.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	leader := leaderForRound(t, eng.vset(), []*token.Account{
		nodes[0].acct, nodes[1].acct, nodes[2].acct, nodes[3].acct,
	}, 0, 0)

	tx := signedTransfer(t, outsider, AddValidatorRecipient(newcomer.PublicKey), 0, 0)
	block := buildSignedBlock(t, leader, 0, 0, head, []token.Transaction{*tx})

	if _, err := eng.acceptProposal(block, 0, nil); !errors.Is(err, ErrNotValidator) {
		t.Fatalf("accepting a set change from a non-validator = %v, want ErrNotValidator", err)
	}
}

// TestEngineResumesTheSetTheChainArrivedAt covers restart: a node that admitted
// a validator must not forget it and start rejecting that validator's votes.
func TestEngineResumesTheSetTheChainArrivedAt(t *testing.T) {
	dir := t.TempDir()
	store, err := kv.New(kv.Config{Path: dir})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	accts, pubs := testKeys(t, 4)
	vs, _ := NewValidatorSet(pubs)
	newcomer, newPubs := testKeys(t, 1)

	// A set the chain arrived at, five members, saved at an epoch boundary.
	grown, err := vs.WithChanges([]SetChange{
		{Kind: SetChangeAdd, PublicKey: newPubs[0], ValidatorID: newcomer[0].AccountID()},
	})
	if err != nil {
		t.Fatalf("WithChanges: %v", err)
	}
	sets := NewSetStore(store)
	if err := sets.SaveActive(grown, 100); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}

	// A fresh engine constructed with the ORIGINAL config set must come up with
	// the persisted one.
	eng, err := New(Config{
		Transport:  noopTransport{},
		Validators: vs,
		Chain:      NewBlockChain(store),
		Ledger:     market.NewLedger(store),
		Self:       accts[0],
		Sets:       sets,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); eng.Wait(); _ = store.Close() }()
	if err := eng.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	if got := eng.vset().Len(); got != 5 {
		t.Fatalf("resumed set has %d members, want the 5 the chain arrived at", got)
	}
	if !eng.vset().Contains(newcomer[0].AccountID()) {
		t.Fatal("resumed set forgot the admitted validator")
	}
}

// TestPendingChangesSurviveRestart: a change committed just before a restart
// must still take effect at the boundary.
func TestPendingChangesSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := kv.New(kv.Config{Path: dir})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	accts, pubs := testKeys(t, 4)
	vs, _ := NewValidatorSet(pubs)
	newcomer, newPubs := testKeys(t, 1)

	sets := NewSetStore(store)
	pending := []SetChange{{
		Kind:        SetChangeAdd,
		PublicKey:   newPubs[0],
		ValidatorID: newcomer[0].AccountID(),
		Height:      3,
	}}
	if err := sets.SavePending(pending); err != nil {
		t.Fatalf("SavePending: %v", err)
	}

	eng, err := New(Config{
		Transport:  noopTransport{},
		Validators: vs,
		Chain:      NewBlockChain(store),
		Ledger:     market.NewLedger(store),
		Self:       accts[0],
		Sets:       sets,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); eng.Wait(); _ = store.Close() }()
	if err := eng.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	got := eng.PendingSetChanges()
	if len(got) != 1 || got[0].ValidatorID != newcomer[0].AccountID() {
		t.Fatalf("pending after restart = %+v, want the change that was committed before it", got)
	}
}

func TestEpochLengthDefaults(t *testing.T) {
	_, pubs := testKeys(t, 1)
	vs, _ := NewValidatorSet(pubs)
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	eng, err := New(Config{
		Transport:  noopTransport{},
		Validators: vs,
		Chain:      NewBlockChain(store),
		Ledger:     market.NewLedger(store),
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if eng.EpochLength() != DefaultEpochLength {
		t.Fatalf("epoch length = %d, want the default %d", eng.EpochLength(), DefaultEpochLength)
	}
}

// --- the operator flow --------------------------------------------------

func TestParseChangeSpecRoundTrip(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	add, err := ParseChangeSpec("add:" + hex.EncodeToString(acct.PublicKey))
	if err != nil {
		t.Fatalf("ParseChangeSpec(add): %v", err)
	}
	if add.Kind != SetChangeAdd || add.ValidatorID != acct.AccountID() {
		t.Fatalf("add = %+v", add)
	}
	// What the engine prints must parse back, so an operator can paste a log line
	// into a config.
	if _, err := ParseChangeSpec(add.String()); err != nil {
		t.Fatalf("a change's own String() did not parse back: %v", err)
	}

	remove, err := ParseChangeSpec("  REMOVE:" + acct.AccountID() + "  ")
	if err != nil {
		t.Fatalf("ParseChangeSpec(remove): %v", err)
	}
	if remove.Kind != SetChangeRemove || remove.ValidatorID != acct.AccountID() {
		t.Fatalf("remove = %+v", remove)
	}

	for _, bad := range []string{
		"",
		"add",
		"add:",
		"add:nothex",
		"promote:" + acct.AccountID(),
		"remove:short",
		acct.AccountID(),
	} {
		if _, err := ParseChangeSpec(bad); err == nil {
			t.Fatalf("ParseChangeSpec(%q) succeeded", bad)
		}
	}
}

// A mistyped approval must stop the node. Ignoring it would leave an operator
// believing they had approved a change their node will in fact vote against.
func TestAMalformedApprovalRefusesToStart(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	vs, err := NewValidatorSet([]ed25519.PublicKey{acct.PublicKey})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	defer store.Close()

	bus := newMemBus()
	_, err = New(Config{
		Transport:          bus.endpoint("solo"),
		Validators:         vs,
		Chain:              NewBlockChain(store),
		Ledger:             market.NewLedger(store),
		Self:               acct,
		Sets:               NewSetStore(store),
		ApprovedSetChanges: []string{"add:oops"},
	})
	if err == nil {
		t.Fatal("New accepted a malformed approved change")
	}
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("New error = %v, want ErrInvalidMessage", err)
	}
}

// A set change must not double as a transfer to an address no key can spend
// from.
func TestASetChangeCarryingValueIsInvalid(t *testing.T) {
	newcomer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	approval := "add:" + hex.EncodeToString(newcomer.PublicKey)

	nodes, stop := setChangeCluster(t, 4, 2, approval)
	defer stop()

	validator := nodes[0].acct
	mintAll(t, nodes, validator.AccountID(), 1000)

	tx := signedTransfer(t, validator, AddValidatorRecipient(newcomer.PublicKey), 5, 99)
	block := &Block{Height: nodes[0].engine.Height(), Txs: []token.Transaction{*tx}}

	nodes[0].engine.mu.Lock()
	err = nodes[0].engine.verifySetChangeLocked(&block.Txs[0], block.Height)
	nodes[0].engine.mu.Unlock()
	if err == nil {
		t.Fatal("a set change carrying 5 credits was accepted")
	}
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("error = %v, want ErrInvalidMessage", err)
	}
}

// setChangeClusterApprovals builds a cluster where each node gets its OWN
// approval list, which is what an operator rollout actually looks like: configs
// are edited one at a time, not all at once.
func setChangeClusterApprovals(t *testing.T, n int, epoch uint64, approvals [][]string) ([]*testNode, func()) {
	t.Helper()
	next := 0
	return newCluster(t, n, func(c *Config) {
		c.ProposeInterval = 4 * time.Millisecond
		c.RoundTimeout = 40 * time.Millisecond
		c.HeadAnnounceInterval = 20 * time.Millisecond
		c.EpochLength = epoch
		if next < len(approvals) {
			c.ApprovedSetChanges = approvals[next]
		}
		next++
	})
}

// keepTrafficFlowing feeds ordinary transfers into every node until the
// returned stop is called. Heights only advance when there is something to
// propose, so a test that waits on an epoch boundary needs a steady trickle.
func keepTrafficFlowing(t *testing.T, nodes []*testNode, from *token.Account) func() {
	t.Helper()
	mintAll(t, nodes, from.AccountID(), 1000000)
	stop := keepTrafficFlowingAmount(t, nodes, from, 1)
	return func() { stop() }
}

// recipientName is the deterministic name traffic pays to, so a test can sum
// every recipient's balance and check the ledger conserved value.
func recipientName(n int) string { return fmt.Sprintf("recipient-%d", n) }

// keepTrafficFlowingAmount is keepTrafficFlowing with a chosen transfer size,
// and without minting: a caller that cares how much was funded does that itself.
//
// The returned stop is idempotent and reports how many transfers were
// submitted, so a test that adds up every recipient's balance knows exactly how
// many recipients there are. Idempotent because it is natural to both defer it
// and call it, and closing a closed channel panics.
func keepTrafficFlowingAmount(t *testing.T, nodes []*testNode, from *token.Account, amount uint64) func() int {
	t.Helper()

	done := make(chan struct{})
	stopped := make(chan struct{})
	var submitted atomic.Uint64
	go func() {
		defer close(stopped)
		// Signed here rather than via signedTransfer: that helper takes a
		// *testing.T, and a background goroutine must not touch one after the test
		// has finished. The traffic has to be open-ended, because a test that waits
		// on an epoch boundary cannot know in advance how many blocks that takes.
		for nonce := uint64(1); ; nonce++ {
			select {
			case <-done:
				return
			case <-time.After(10 * time.Millisecond):
			}
			tx := &token.Transaction{
				From:      from.PublicKey,
				To:        recipientName(int(nonce)),
				Amount:    amount,
				Nonce:     nonce,
				Timestamp: time.Now().UnixNano(),
				PrevHash:  make([]byte, token.HashSize),
			}
			if err := tx.Sign(from.PrivateKey); err != nil {
				return
			}
			for _, nd := range nodes {
				_ = nd.engine.Submit(tx)
			}
			submitted.Store(nonce)
		}
	}()
	var once sync.Once
	return func() int {
		once.Do(func() {
			close(done)
			<-stopped
		})
		return int(submitted.Load())
	}
}

// TestApprovingOnAQuorumIsEnoughToAdmitAValidator is the operator interface:
// nobody submits a transaction. A quorum of operators lists the change under
// consensus.approved_changes and their nodes offer it until it commits.
func TestApprovingOnAQuorumIsEnoughToAdmitAValidator(t *testing.T) {
	newcomer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	approval := "add:" + hex.EncodeToString(newcomer.PublicKey)

	// Three of four approve. Quorum for a set of four is three, so this is the
	// thinnest majority that can carry the change - and the fourth node voting
	// nil must not be able to stop it.
	nodes, stop := setChangeClusterApprovals(t, 4, 2, [][]string{
		{approval}, {approval}, {approval}, nil,
	})
	defer stop()

	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()

	waitFor(t, 15*time.Second, "the approved newcomer to be admitted everywhere", func() bool {
		for _, nd := range nodes {
			if !nd.engine.vset().Contains(newcomer.AccountID()) {
				return false
			}
		}
		return true
	})

	// Including on the node that never approved it: it lost the vote, and a
	// committed change is not optional.
	if !nodes[3].engine.vset().Contains(newcomer.AccountID()) {
		t.Fatal("the node that voted nil did not apply the committed change")
	}
	for i, nd := range nodes {
		if got := nd.engine.vset().Len(); got != 5 {
			t.Fatalf("node %d has %d validators, want 5", i, got)
		}
	}

	// The chain keeps committing with the wider set in force.
	before := nodes[0].engine.Height()
	waitFor(t, 10*time.Second, "progress after the set change", func() bool {
		return nodes[0].engine.Height() > before
	})
}

// TestApprovingOnLessThanAQuorumAdmitsNobody is the safety half: one operator
// cannot let a stranger in, however persistently their node offers the change.
func TestApprovingOnLessThanAQuorumAdmitsNobody(t *testing.T) {
	newcomer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	approval := "add:" + hex.EncodeToString(newcomer.PublicKey)

	// Two of four: one short of quorum.
	nodes, stop := setChangeClusterApprovals(t, 4, 2, [][]string{
		{approval}, {approval}, nil, nil,
	})
	defer stop()

	// Give the two approving nodes many rounds to lead and re-offer the change.
	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()

	waitFor(t, 15*time.Second, "the chain to cross several epoch boundaries", func() bool {
		return nodes[0].engine.Height() >= 6
	})

	for i, nd := range nodes {
		if pending := nd.engine.PendingSetChanges(); len(pending) != 0 {
			t.Fatalf("node %d has %d set changes pending; a sub-quorum approval committed", i, len(pending))
		}
		if nd.engine.vset().Contains(newcomer.AccountID()) {
			t.Fatalf("node %d admitted a validator that only two of four operators approved", i)
		}
		if got := nd.engine.vset().Len(); got != 4 {
			t.Fatalf("node %d has %d validators, want 4", i, got)
		}
	}
}

// An approval left in the config after the change has taken effect must be a
// no-op, not a source of blocks that change nothing.
func TestAnAppliedApprovalStopsBeingOffered(t *testing.T) {
	accts, pubs := testKeys(t, 3)
	vs, err := NewValidatorSet(pubs)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	defer store.Close()

	bus := newMemBus()
	// The approval names a validator that is ALREADY in the set, which is exactly
	// the state a config left untouched after a successful admission is in.
	eng, err := New(Config{
		Transport:          bus.endpoint("solo"),
		Validators:         vs,
		Chain:              NewBlockChain(store),
		Ledger:             market.NewLedger(store),
		Self:               accts[0],
		Sets:               NewSetStore(store),
		ApprovedSetChanges: []string{"add:" + hex.EncodeToString(pubs[1])},
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	eng.maybeProposeApprovedChanges()
	if got := eng.MempoolLen(); got != 0 {
		t.Fatalf("mempool holds %d transactions, want 0: an approval already in force was offered again", got)
	}
}

// TestASetChangeReachesItsEpochBoundaryOnAnIdleChain is a liveness property
// that a real single-node run exposed: the change committed and then sat
// there, because an epoch boundary is a HEIGHT and an idle chain produces no
// heights. A quiet network must not be able to strand an admission - or, worse,
// an ejection of a validator caught equivocating.
func TestASetChangeReachesItsEpochBoundaryOnAnIdleChain(t *testing.T) {
	newcomer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	approval := "add:" + hex.EncodeToString(newcomer.PublicKey)

	// Epoch 4, so the change cannot possibly land on the block that carries it.
	// No transfers are submitted anywhere in this test.
	nodes, stop := setChangeCluster(t, 4, 4, approval)
	defer stop()

	waitFor(t, 15*time.Second, "an idle chain to carry the change to its boundary", func() bool {
		for _, nd := range nodes {
			if !nd.engine.vset().Contains(newcomer.AccountID()) {
				return false
			}
		}
		return true
	})

	for i, nd := range nodes {
		_, at, err := nd.sets.LoadActive()
		if err != nil {
			t.Fatalf("node %d LoadActive: %v", i, err)
		}
		if at%4 != 0 {
			t.Fatalf("node %d applied the change at height %d, which is not a multiple of the epoch length", i, at)
		}
	}

	// And the empty blocks stop once there is nothing pending: the chain must go
	// quiet again rather than churn out blocks forever.
	for _, nd := range nodes {
		if pending := nd.engine.PendingSetChanges(); len(pending) != 0 {
			t.Fatalf("changes still pending after the boundary: %v", pending)
		}
	}
	waitFor(t, 10*time.Second, "the chain to go quiet again", func() bool {
		before := nodes[0].engine.Height()
		time.Sleep(300 * time.Millisecond)
		return nodes[0].engine.Height() == before
	})
}

// An epoch boundary must not be able to switch block production off for good.
//
// The boundary clears pendingChanges and stakeDirty, which is what
// mustAdvanceToEpochBoundaryLocked reads. On a chain whose recent history is
// validator-set churn rather than user traffic, that flag is the ONLY reason
// blocks are being produced - so entering the boundary stops production with
// nothing left to restart it, and the height stalls forever with an empty
// mempool. A production chain stopped dead at an exact epoch boundary this way.
//
// The round is what distinguishes the two cases. Round 0 with nothing to say is
// an idle chain and must stay silent. A height that has already burned a round
// is a stalled one and needs a block to get out.
func TestStalledHeightProposesAnEmptyBlockToRecover(t *testing.T) {
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
	eng, err := New(Config{
		Transport:       newMemBus().endpoint(peer.ID("epoch-stall")),
		Validators:      vs,
		Chain:           NewBlockChain(store),
		Ledger:          ledger,
		Self:            self,
		ProposeInterval: time.Hour,
		RoundTimeout:    time.Hour,
		Evidence:        NewEvidenceStore(store),
		Sets:            NewSetStore(store),
		SelfVotes:       NewSelfVoteStore(store),
		Stake:           NewStakeLedger(ledger, store),
		ZeroMinBond:     true,
		DisableTxGossip: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Exactly the stalled state: sitting on an epoch boundary, nothing pending,
	// nothing to weight, empty mempool.
	eng.mu.Lock()
	eng.height = eng.epochLength * 4
	eng.pendingChanges = nil
	eng.stakeDirty = false
	eng.stakeNeverWeighted = false
	eng.mempool = nil
	eng.precommits = make(map[uint64]map[string]map[string]Vote)

	if eng.mustAdvanceToEpochBoundaryLocked() {
		eng.mu.Unlock()
		t.Fatal("test did not reproduce the stall: something is still pending")
	}

	// Freshly entered: an ordinary height that has simply not committed yet, and
	// it must stay quiet. A high round number does NOT make it a stall - rounds
	// rotate on a short timeout and a loaded network runs them up routinely, so
	// treating that as a stall would mint empty blocks forever.
	eng.heightEnteredAt = time.Now()
	eng.round = 12
	slow, _ := eng.buildProposalLocked()

	// Stuck far longer than any backed-off round timeout can account for.
	eng.heightEnteredAt = time.Now().Add(-2 * eng.stallRecoveryAfter)
	stalled, _ := eng.buildProposalLocked()
	height := eng.height
	eng.mu.Unlock()

	if slow != nil {
		t.Fatal("a merely slow height proposed an empty block; that mints blocks forever on any slow network")
	}
	if stalled == nil {
		t.Fatal("a stalled height proposed nothing, so it can never commit and the chain is dead")
	}
	if len(stalled.Txs) != 0 {
		t.Fatalf("recovery block carries %d transactions, want an empty one", len(stalled.Txs))
	}
	if stalled.Height != height {
		t.Fatalf("recovery block height = %d, want %d", stalled.Height, height)
	}

	// But a node that is only MISSING A BODY must stay quiet and let block sync
	// work. It holds a quorum of precommits for a block the network already
	// agreed on, so that height is settled and this node is behind, not stalled.
	// Proposing a competing empty block would have it vote for its own block
	// instead of the one that already won.
	eng.mu.Lock()
	eng.heightEnteredAt = time.Now().Add(-2 * eng.stallRecoveryAfter)
	eng.precommits = map[uint64]map[string]map[string]Vote{
		0: {"deadbeef": {self.AccountID(): Vote{
			Type: VoteTypePrecommit, Height: eng.height, Round: 0,
			BlockHash: []byte{0xde, 0xad, 0xbe, 0xef}, VoterID: self.AccountID(),
		}}},
	}
	if !eng.quorumWithoutBodyLocked() {
		eng.mu.Unlock()
		t.Fatal("test did not reproduce a quorum without a body")
	}
	missingBody, _ := eng.buildProposalLocked()
	eng.mu.Unlock()

	if missingBody != nil {
		t.Fatal("a node missing only the body proposed a competing block; it must wait for block sync")
	}
}

// --- ejecting a validator caught equivocating ---------------------------

// TestProvenEquivocationEjectsTheOffender is what the evidence store was
// missing a use for: proof of a double vote used to be recorded and printed,
// and nothing could act on it because the set was fixed at startup. Now the
// network removes the offender itself.
func TestProvenEquivocationEjectsTheOffender(t *testing.T) {
	// FIVE, because one gets ejected. Four minus one is a three-member set with
	// quorum three, which tolerates no lag at all - so under load every round
	// times out before all three precommit and the chain crawls. Five leaves
	// four, quorum three, which tolerates one slow node. That is the fault
	// threshold, not a timeout to tune.
	nodes, stop := setChangeCluster(t, 5, 2)
	defer stop()

	offender := nodes[1]
	offenderID := offender.acct.AccountID()

	// Two votes from the offender's key for different blocks at one height,
	// round and phase. No honest validator produces this pair.
	a := buildVote(t, offender.acct, 0, 0, hashOf(0xE1))
	b := buildVote(t, offender.acct, 0, 0, hashOf(0xE2))
	relay := nodes[0].bus.endpoint(peer.ID("offender-relay"))
	for _, v := range []Vote{a, b} {
		data, err := json.Marshal(&v)
		if err != nil {
			t.Fatalf("marshal vote: %v", err)
		}
		if err := relay.Publish(context.Background(), TopicVote, data); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	waitFor(t, 20*time.Second, "the equivocating validator to be ejected everywhere", func() bool {
		for _, nd := range nodes {
			if nd.engine.vset().Contains(offenderID) {
				return false
			}
		}
		return true
	})

	for i, nd := range nodes {
		vs := nd.engine.vset()
		if vs.Len() != 4 {
			t.Fatalf("node %d has %d validators, want 4 after the ejection", i, vs.Len())
		}
		// The removal is chain state, so a restart must not bring the offender
		// back.
		saved, _, err := nd.sets.LoadActive()
		if err != nil {
			t.Fatalf("node %d LoadActive: %v", i, err)
		}
		if saved == nil || saved.Contains(offenderID) {
			t.Fatalf("node %d did not persist the ejection", i)
		}
	}

	// The remaining four keep committing.
	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()
	before := nodes[0].engine.Height()
	waitFor(t, 15*time.Second, "the chain to keep committing without the offender", func() bool {
		return nodes[0].engine.Height() > before
	})
}

// And an operator who would rather investigate than have the network act can
// turn it off.
func TestEjectionCanBeTurnedOff(t *testing.T) {
	off := false
	nodes, stop := newCluster(t, 4, func(c *Config) {
		c.ProposeInterval = 4 * time.Millisecond
		c.RoundTimeout = 40 * time.Millisecond
		c.EpochLength = 2
		c.EjectEquivocators = &off
	})
	defer stop()

	offender := nodes[1]
	offenderID := offender.acct.AccountID()

	a := buildVote(t, offender.acct, 0, 0, hashOf(0xE3))
	b := buildVote(t, offender.acct, 0, 0, hashOf(0xE4))
	relay := nodes[0].bus.endpoint(peer.ID("offender-relay-off"))
	for _, v := range []Vote{a, b} {
		data, err := json.Marshal(&v)
		if err != nil {
			t.Fatalf("marshal vote: %v", err)
		}
		if err := relay.Publish(context.Background(), TopicVote, data); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	// The offence is still recorded - turning ejection off must not turn
	// detection off, or an operator would have nothing to investigate.
	waitFor(t, 10*time.Second, "the offence to be recorded", func() bool {
		records, err := nodes[0].evidence.ByValidator(offenderID)
		return err == nil && len(records) > 0
	})

	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()
	waitFor(t, 15*time.Second, "the chain to keep committing", func() bool {
		return nodes[0].engine.Height() >= 6
	})

	for i, nd := range nodes {
		if !nd.engine.vset().Contains(offenderID) {
			t.Fatalf("node %d ejected the offender with eject_equivocators off", i)
		}
	}
}

// TestLeadershipRotatesOnEveryCommit is the regression for a flaw the set-change
// work exposed rather than caused: leadership was ids[round mod N], and the
// round resets to zero on every commit, so on a chain that keeps committing
// round 0 came round again at every height and ONE validator proposed every
// block for the life of the network. Rotation only ever happened on a timeout,
// and a leader that keeps committing never times out.
//
// The cost was not cosmetic. That validator had a permanent veto over what got
// into a block, and a transaction submitted to any other node was never
// proposed at all, because a node can only propose from its own mempool.
func TestLeadershipRotatesOnEveryCommit(t *testing.T) {
	nodes, stop := setChangeCluster(t, 4, 100)
	defer stop()

	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()

	waitFor(t, 15*time.Second, "a run of committed blocks", func() bool {
		return nodes[0].engine.Height() >= 8
	})

	proposers := make(map[string]int)
	for h := uint64(0); h < 8; h++ {
		block, err := nodes[0].chain.BlockAt(h)
		if err != nil {
			t.Fatalf("block at %d: %v", h, err)
		}
		proposers[block.ProposerID]++
	}
	if len(proposers) < 2 {
		t.Fatalf("8 blocks were proposed by %d validator(s): %v - leadership is not rotating", len(proposers), proposers)
	}

	// And every node must attribute a height to the same leader, or they would
	// reject each other's blocks.
	for h := uint64(0); h < 8; h++ {
		var want string
		for i, nd := range nodes {
			got := nd.engine.vset().LeaderFor(h, 0)
			if i == 0 {
				want = got
				continue
			}
			if got != want {
				t.Fatalf("nodes disagree about the leader of height %d: %s vs %s", h, want, got)
			}
		}
	}
}
