package consensus

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Joining a running network.
//
// Every other test in this package starts its nodes together from the same
// genesis. That is not how a network grows, and the difference matters: a node
// that arrives late has an EMPTY store, so it has no idea what the validator set
// has become, and it has to work that out from the chain it downloads.
//
// The operational consequence is sharp enough to be worth a test rather than a
// paragraph. A joining node's consensus.validators has to be the GENESIS set,
// not the set in force when it joins. It replays the chain from height zero, and
// to accept the block at each height it checks the proposer was the leader for
// that height - against the set as it stood THEN. Handed the current set it
// would reject early history and never catch up.

// joinNode builds an engine on an existing cluster's bus with a fresh store, the
// way a new machine pointed at bootstrap peers arrives.
func joinNode(
	t *testing.T,
	existing []*testNode,
	self *token.Account,
	genesis *ValidatorSet,
	opts func(*Config),
	prepare func(*market.Ledger),
) *testNode {
	t.Helper()

	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ledger := market.NewLedger(store)
	chain := NewBlockChain(store)
	bus := existing[0].bus
	// A joining node starts from the cluster's genesis, not from nothing. Only
	// state that originated in the ordered log is replayed by block sync; balances
	// seeded outside it have to be present before the first block is applied, or
	// this node's ledger can never match its peers'.
	if prepare == nil {
		if err := bus.applySeed(ledger); err != nil {
			t.Fatalf("apply the cluster seed: %v", err)
		}
	}
	peerID := peer.ID("joiner-" + self.AccountID()[:8])

	cfg := Config{
		Transport:       bus.endpoint(peerID),
		Validators:      genesis,
		Chain:           chain,
		Ledger:          ledger,
		Self:            self,
		ProposeInterval: 4 * time.Millisecond,
		RoundTimeout:    40 * time.Millisecond,
		Evidence:        NewEvidenceStore(store),
		Sets:            NewSetStore(store),
		SelfVotes:       NewSelfVoteStore(store),
		Stake:           NewStakeLedger(ledger, store),
		Providers:       NewProviderRegistry(store),
		ZeroMinBond:     true,
	}
	if opts != nil {
		opts(&cfg)
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("new joining engine: %v", err)
	}
	// Genesis state BEFORE the engine starts, because that is when a real node
	// applies it: allocations and the reward pool come from the node's own
	// config, not from the chain it downloads. A joining node whose genesis
	// config differs from the network's ends up with the same blocks and
	// different balances - see the note in the catch-up test.
	if prepare != nil {
		prepare(ledger)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		eng.Wait()
	})
	if err := eng.Start(ctx); err != nil {
		t.Fatalf("start joining engine: %v", err)
	}
	return &testNode{
		acct: self, engine: eng, ledger: ledger, chain: chain,
		store: store, peerID: peerID, bus: bus,
	}
}

// genesisSetOf rebuilds the set a cluster started from, which is what a joining
// node has to be configured with.
func genesisSetOf(t *testing.T, nodes []*testNode) *ValidatorSet {
	t.Helper()
	keys := make([]ed25519.PublicKey, 0, len(nodes))
	for _, nd := range nodes {
		keys = append(keys, nd.acct.PublicKey)
	}
	vs, err := NewValidatorSet(keys)
	if err != nil {
		t.Fatalf("genesis set: %v", err)
	}
	return vs
}

// A node with an empty store catches up to a running network: same chain, same
// head, same balances. This is the plain case, with the set unchanged.
func TestAFreshNodeCatchesUpToARunningNetwork(t *testing.T) {
	nodes, stop := setChangeCluster(t, 4, 100)
	defer stop()

	payer := nodes[0].acct
	stopTraffic := keepTrafficFlowing(t, nodes, payer)
	defer stopTraffic()

	waitFor(t, 20*time.Second, "the network to build some history", func() bool {
		return nodes[0].engine.Height() >= 5
	})
	// Freeze the finite input stream before the newcomer starts. Otherwise it can
	// synchronize from a peer that is one block ahead of node 0 while the loop
	// below is comparing against node 0, making a correct catch-up look like a
	// height-out-of-range failure. Consensus drains the already-submitted work,
	// then all chains have a stable common tip for comparison.
	stopTraffic()

	// A machine arrives with nothing: no chain, no ledger, no idea of the set
	// beyond what its config says.
	newcomer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// The same genesis state the cluster has. mintAll credited the payer on each
	// existing node's ledger directly, which is what a genesis allocation does,
	// so the joining node has to start from the same place. This is the second
	// operational requirement: the chain tells a joining node the TRANSACTIONS,
	// never the state they started from.
	joiner := joinNode(t, nodes, newcomer, genesisSetOf(t, nodes), nil, func(l *market.Ledger) {
		if err := l.Credit(payer.AccountID(), 1000000); err != nil {
			t.Fatalf("genesis credit: %v", err)
		}
	})

	if got := joiner.engine.Height(); got != 0 {
		t.Fatalf("the joining node started at height %d, want 0 - its store is supposed to be empty", got)
	}

	waitFor(t, 30*time.Second, "the joining node to catch up", func() bool {
		want, err := nodes[0].chain.Len()
		if err != nil || want == 0 {
			return false
		}
		got, err := joiner.chain.Len()
		return err == nil && got >= want
	})

	// Same history, block for block, not merely the same length.
	shared, err := joiner.chain.Len()
	if err != nil {
		t.Fatalf("joiner chain length: %v", err)
	}
	for h := uint64(0); h < shared; h++ {
		mine, err := joiner.chain.BlockAt(h)
		if err != nil {
			t.Fatalf("joiner block %d: %v", h, err)
		}
		theirs, err := nodes[0].chain.BlockAt(h)
		if err != nil {
			t.Fatalf("node 0 block %d: %v", h, err)
		}
		if string(mine.Hash()) != string(theirs.Hash()) {
			t.Fatalf("the joining node's block %d differs from the network's", h)
		}
	}

	// And it applied them, so it agrees about money.
	waitFor(t, 20*time.Second, "the joining node to agree on a balance", func() bool {
		want, err := nodes[0].ledger.Balance(payer.AccountID())
		if err != nil {
			return false
		}
		got, err := joiner.ledger.Balance(payer.AccountID())
		return err == nil && got == want
	})
}

// The case the config note exists for: the set CHANGED before the node joined.
// It has to replay its way to the current set from the genesis one.
func TestAFreshNodeReplaysSetChangesItWasNotPresentFor(t *testing.T) {
	admitted, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	approval := "add:" + hex.EncodeToString(admitted.PublicKey)

	nodes, stop := setChangeCluster(t, 4, 2, approval)
	defer stop()

	genesis := genesisSetOf(t, nodes)
	if genesis.Len() != 4 {
		t.Fatalf("genesis set has %d members, want 4", genesis.Len())
	}

	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()

	// The network admits a fifth validator while the joiner is not even running.
	waitFor(t, 20*time.Second, "the network to admit a validator", func() bool {
		for _, nd := range nodes {
			if !nd.engine.vset().Contains(admitted.AccountID()) {
				return false
			}
		}
		return true
	})
	waitFor(t, 20*time.Second, "more history after the change", func() bool {
		return nodes[0].engine.Height() >= 6
	})

	// Now a node joins, configured with the GENESIS set - which no longer
	// describes the network.
	late, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	joiner := joinNode(t, nodes, late, genesis, func(c *Config) {
		c.EpochLength = 2
	}, nil)
	if joiner.engine.vset().Len() != 4 {
		t.Fatalf("the joining node started with %d validators, want the 4 its config named",
			joiner.engine.vset().Len())
	}

	waitFor(t, 30*time.Second, "the joining node to replay its way to the current set", func() bool {
		return joiner.engine.vset().Contains(admitted.AccountID())
	})

	if got := joiner.engine.vset().Len(); got != 5 {
		t.Fatalf("the joining node has %d validators, want the 5 the chain arrived at", got)
	}
	// Every member, and the same voting power, so it computes the same quorum.
	want := nodes[0].engine.vset()
	got := joiner.engine.vset()
	if got.QuorumPower() != want.QuorumPower() {
		t.Fatalf("the joining node's quorum is %d, the network's is %d", got.QuorumPower(), want.QuorumPower())
	}
	for _, id := range want.IDs() {
		if !got.Contains(id) {
			t.Fatalf("the joining node is missing validator %s", id[:8])
		}
		if got.Power(id) != want.Power(id) {
			t.Fatalf("the joining node gives %s power %d, the network gives %d", id[:8], got.Power(id), want.Power(id))
		}
	}

	// The set it arrived at is persisted, so a restart does not undo the replay.
	saved, _, err := joiner.sets2(t).LoadActive()
	if err != nil {
		t.Fatalf("LoadActive: %v", err)
	}
	if saved == nil || saved.Len() != 5 {
		t.Fatalf("the joining node did not persist the set it replayed to")
	}
}

// And the sharp edge itself: configured with the set in force NOW rather than
// the genesis set, a joining node cannot accept the early chain. Worth a test so
// the failure is a known, explained one rather than a mystery in the field.
func TestAJoiningNodeConfiguredWithTheCurrentSetCannotReplayHistory(t *testing.T) {
	admitted, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	approval := "add:" + hex.EncodeToString(admitted.PublicKey)

	nodes, stop := setChangeCluster(t, 4, 2, approval)
	defer stop()

	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()

	waitFor(t, 20*time.Second, "the network to admit a validator", func() bool {
		for _, nd := range nodes {
			if !nd.engine.vset().Contains(admitted.AccountID()) {
				return false
			}
		}
		return true
	})
	waitFor(t, 20*time.Second, "history after the change", func() bool {
		return nodes[0].engine.Height() >= 6
	})

	// The CURRENT set: five members, including one that was not there at genesis.
	current := nodes[0].engine.vset()
	currentKeys := current.PublicKeys()
	wrong, err := NewValidatorSet(currentKeys)
	if err != nil {
		t.Fatalf("current set: %v", err)
	}

	late, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	joiner := joinNode(t, nodes, late, wrong, func(c *Config) {
		c.EpochLength = 2
	}, nil)

	// Give it as long as the successful case gets, and it still cannot start:
	// the leader for height 0 under a five-member set is not the validator who
	// actually proposed it, so block 0 is refused and nothing after it can link.
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if l, _ := joiner.chain.Len(); l > 0 {
			t.Fatalf("a node configured with the current set committed %d blocks; "+
				"if this now works, the operational note about using the GENESIS set is obsolete "+
				"and the docs should say so", l)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// sets2 exposes a node's SetStore over its own store, which is what joinNode
// built the engine with. The testNode field of the same name is only populated
// by newCluster.
func (n *testNode) sets2(t *testing.T) *SetStore {
	t.Helper()
	return NewSetStore(n.store)
}
