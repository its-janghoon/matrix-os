package consensus

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// testNode bundles an engine with its backing store/ledger/chain so a test can
// inspect per-node state and assert convergence.
type testNode struct {
	acct   *token.Account
	engine *Engine
	ledger *market.Ledger
	chain  *BlockChain
	store  *kv.Store
	// evidence is this node's equivocation record, so a test can assert what it
	// knows about misbehaviour.
	evidence *EvidenceStore
	// sets is this node's validator-set state.
	sets *SetStore
	// selfVotes is this node's own-vote record, so a restart test can assert
	// that the lock and votes survive.
	selfVotes *SelfVoteStore
	// peerID is this node's identity on the in-memory bus, which a test needs to
	// address it in a delivery filter.
	peerID peer.ID
	// bus is the shared gossip bus every node in the cluster is wired to.
	bus *memBus
	// seeds applies this node's share of the cluster's out-of-band credits, at
	// the log position each was filed against. See mintAll.
	seeds *seedApplier
}

// newCluster spins up n in-process consensus nodes wired to a shared in-memory
// gossip bus. All n accounts form the fixed validator set. It returns the nodes,
// the validator accounts, and a cancel func that stops every engine and closes
// its store.
func newCluster(t *testing.T, n int, opts func(*Config)) ([]*testNode, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	accts := make([]*token.Account, n)
	pubs := make([]ed25519.PublicKey, n)
	for i := 0; i < n; i++ {
		a, err := token.GenerateAccount()
		if err != nil {
			t.Fatalf("generate account: %v", err)
		}
		accts[i] = a
		pubs[i] = a.PublicKey
	}
	vs, err := NewValidatorSet(pubs)
	if err != nil {
		t.Fatalf("validator set: %v", err)
	}

	bus := newMemBus()
	nodes := make([]*testNode, n)
	for i := 0; i < n; i++ {
		peerID := peer.ID(fmt.Sprintf("node-%d", i))
		store, err := kv.New(kv.Config{Path: t.TempDir()})
		if err != nil {
			t.Fatalf("kv: %v", err)
		}
		ledger := market.NewLedger(store)
		chain := NewBlockChain(store)
		evidence := NewEvidenceStore(store)
		sets := NewSetStore(store)
		selfVotes := NewSelfVoteStore(store)
		// Every test cluster gets a stake ledger, matching how the node wires
		// one. With no bonds posted, every validator has power 1 and every
		// quorum is the same headcount it always was, so this changes nothing
		// for tests that do not use stake - and it means a test that does use it
		// never has to reach into a running engine to install one, which would
		// be a data race on the driver goroutine.
		stake := NewStakeLedger(ledger, store)
		// The provider reward registry, for the same reason: a test that uses it
		// must not reach into a running engine to install one.
		providers := NewProviderRegistry(store)
		// The settled-history tally, wired for every cluster for the same reason:
		// it must record what a real commit did, not what a test told it to.
		earnings := NewEarningsStore(store)
		cfg := Config{
			Transport:       bus.endpoint(peerID),
			Validators:      vs,
			Chain:           chain,
			Ledger:          ledger,
			Self:            accts[i],
			ProposeInterval: 5 * time.Millisecond,
			RoundTimeout:    60 * time.Millisecond,
			Evidence:        evidence,
			Sets:            sets,
			SelfVotes:       selfVotes,
			Stake:           stake,
			Providers:       providers,
			Earnings:        earnings,
			// Nothing is bonded in most tests, so an admission floor would refuse
			// every set change. Tests about the floor set one.
			ZeroMinBond:     true,
			DisableTxGossip: true,
		}
		if opts != nil {
			opts(&cfg)
		}
		// Set after opts, not before: a test that wired its own observer would
		// otherwise silently take the cluster's credits with it, and the symptom
		// - one node short of money, much later - names nothing.
		seeds := newSeedApplier(bus, ledger)
		prior := cfg.OnCommit
		cfg.OnCommit = func(b *Block) {
			// This node has committed b, so its next block is b.Height+1, and
			// every credit filed at or before that height is now due. Running it
			// here puts the credit inside the commit path, which is the only
			// place every node passes through in the same order - a block this
			// node voted for and a block it caught up on both arrive here.
			if err := seeds.beforeBlock(b.Height + 1); err != nil {
				panic(fmt.Sprintf("apply seeded credit after height %d: %v", b.Height, err))
			}
			if prior != nil {
				prior(b)
			}
		}
		eng, err := New(cfg)
		if err != nil {
			t.Fatalf("new engine: %v", err)
		}
		nodes[i] = &testNode{
			acct:      accts[i],
			engine:    eng,
			ledger:    ledger,
			chain:     chain,
			store:     store,
			peerID:    peerID,
			bus:       bus,
			seeds:     seeds,
			evidence:  evidence,
			sets:      sets,
			selfVotes: selfVotes,
		}
	}

	// Started only once EVERY node exists. Starting each engine as it was built
	// left node 0 proposing and committing while node 3 had no ledger at all, so
	// any balance seeded afterwards landed on some nodes after a block and on
	// others before it - a disagreement about state that no later credit undoes.
	for _, nd := range nodes {
		if err := nd.engine.Start(ctx); err != nil {
			t.Fatalf("start engine: %v", err)
		}
	}

	stop := func() {
		cancel()
		for _, nd := range nodes {
			nd.engine.Wait()
			_ = nd.store.Close()
		}
	}
	return nodes, stop
}

// mintAll credits an account on every node's ledger so a signed transfer from
// it can be afforded. In production genesis balances are seeded once, from a
// file every node holds; here each node's ledger starts empty, so the cluster
// seeds itself.
//
// It is the POSITION of the credit that this has to get right, not the amount.
// Writing to four ledgers in a loop leaves the four holding different balances
// until the loop ends, and a live cluster does not wait: the four disagree about
// the state root immediately, and any block landing in the window is applied
// against different balances on different nodes. A transfer affordable on one
// and unaffordable on another is skipped on that one, and a skipped transfer is
// still committed, so nothing ever retries it. The ledgers never come back
// together. That is not theory - it is what the weighted-cluster test hit, as
// two distinct state digests at height 0, before a single block existed.
//
// So this waits for a moment when the cluster is standing still - every node at
// the same height with no commit in flight - and credits them all inside it.
// Both conditions are needed and neither is enough alone:
//
//   - Equal heights, because a node that has applied one more block than another
//     would be taking the credit at a different point in the same log.
//   - No commit in flight, because e.mu does NOT cover the apply step:
//     commitAndApply deliberately runs outside it, and a node inside that
//     function is midway through changing the balances this is about to add to.
//     `committing` is the flag that serialises commits, it is set and cleared
//     under e.mu, and it spans exactly that window.
//
// The credit is also FILED against the height it landed before, so a node that
// joins later and replays the chain applies it in the same place rather than at
// genesis - see joinNode. Both routes run through one exactly-once applier, so a
// node reached by both is credited once.
func mintAll(t *testing.T, nodes []*testNode, account string, amount uint64) {
	t.Helper()
	if len(nodes) == 0 {
		return
	}
	bus := nodes[0].bus

	// A still moment is the ordinary state between two blocks, not a rare one,
	// so this normally succeeds on the first attempt.
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, nd := range nodes {
			nd.engine.mu.Lock()
		}
		height, still := nodes[0].engine.height, true
		for _, nd := range nodes {
			if nd.engine.committing || nd.engine.height != height {
				still = false
				break
			}
		}
		if still {
			bus.fileSeed(height, account, amount)
			var err error
			for _, nd := range nodes {
				if e := nd.seeds.beforeBlock(height); e != nil && err == nil {
					err = e
				}
			}
			for _, nd := range nodes {
				nd.engine.mu.Unlock()
			}
			if err != nil {
				t.Fatalf("credit: %v", err)
			}
			return
		}
		for _, nd := range nodes {
			nd.engine.mu.Unlock()
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}

	// The cluster never stood still - a test that deliberately impairs the
	// network, or one whose nodes are permanently at different heights. Filing
	// the credit one height ahead of the furthest node still gives it a single
	// position in the log: no node has committed that block yet, so every one of
	// them applies the credit on the way past, in its own commit path. The money
	// is not there until that block commits, which is the price of never finding
	// a still moment to use.
	for _, nd := range nodes {
		nd.engine.mu.Lock()
	}
	var ahead uint64
	for _, nd := range nodes {
		if nd.engine.height > ahead {
			ahead = nd.engine.height
		}
	}
	bus.fileSeed(ahead+1, account, amount)
	for _, nd := range nodes {
		nd.engine.mu.Unlock()
	}
}

// signedTransfer builds a signed token.Transaction from `from` to `to` with the
// given amount and nonce. Consensus does not enforce per-sender nonce ordering
// at apply time (it applies whatever the committed block contains), but a unique
// nonce keeps each transaction distinct and lets us model replay rejection.
func signedTransfer(t *testing.T, from *token.Account, to string, amount, nonce uint64) *token.Transaction {
	t.Helper()
	tx := &token.Transaction{
		From:      from.PublicKey,
		To:        to,
		Amount:    amount,
		Nonce:     nonce,
		Timestamp: time.Now().UnixNano(),
		PrevHash:  make([]byte, token.HashSize),
	}
	if err := tx.Sign(from.PrivateKey); err != nil {
		t.Fatalf("sign tx: %v", err)
	}
	return tx
}

// waitForHeight blocks until every node has committed at least `height` blocks
// or the timeout elapses.
func waitForHeight(t *testing.T, nodes []*testNode, height uint64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		done := true
		for _, nd := range nodes {
			if nd.engine.Height() < height {
				done = false
				break
			}
		}
		if done {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	for i, nd := range nodes {
		t.Logf("node %d height=%d", i, nd.engine.Height())
	}
	t.Fatalf("timeout waiting for all nodes to reach height %d", height)
}

// waitForConvergedLength waits until every node's committed chain has the same
// length (eventual consistency) or the timeout elapses.
func waitForConvergedLength(t *testing.T, nodes []*testNode, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		l0, err := nodes[0].chain.Len()
		if err != nil {
			t.Fatalf("len: %v", err)
		}
		converged := l0 > 0
		for _, nd := range nodes {
			l, err := nd.chain.Len()
			if err != nil {
				t.Fatalf("len: %v", err)
			}
			if l != l0 {
				converged = false
				break
			}
		}
		if converged {
			return
		}
		time.Sleep(3 * time.Millisecond)
	}
	for i, nd := range nodes {
		l, _ := nd.chain.Len()
		t.Logf("node %d chain length=%d height=%d", i, l, nd.engine.Height())
	}
	t.Fatalf("timeout waiting for chain length convergence")
}

// waitForBalances waits until every node's ledger reports `want` for `account`,
// which is how a test knows the cluster has quiesced: with no empty blocks
// proposed, a fully applied transaction set means nothing further will commit.
//
// assertConverged needs that. It reads node 0's balance as the expected value
// and then compares the other nodes against it, so on a cluster that is still
// committing, a block landing between those two reads fails the assertion with
// two legitimate balances from different moments in time.
func waitForBalances(t *testing.T, nodes []*testNode, account string, want uint64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		settled := true
		for _, nd := range nodes {
			got, err := nd.ledger.Balance(account)
			if err != nil {
				t.Fatalf("balance: %v", err)
			}
			if got != want {
				settled = false
				break
			}
		}
		if settled {
			return
		}
		time.Sleep(3 * time.Millisecond)
	}
	for i, nd := range nodes {
		got, _ := nd.ledger.Balance(account)
		l, _ := nd.chain.Len()
		t.Logf("node %d balance for %s = %d (chain length %d)", i, account, got, l)
	}
	t.Fatalf("timeout waiting for every node to report %d for %s", want, account)
}

// assertConverged asserts every node has the identical committed block sequence
// (same hashes, same ordered transactions) and identical balances for the given
// accounts.
func assertConverged(t *testing.T, nodes []*testNode, accounts []string) {
	t.Helper()

	// Same chain length.
	length, err := nodes[0].chain.Len()
	if err != nil {
		t.Fatalf("len: %v", err)
	}
	for i, nd := range nodes {
		l, err := nd.chain.Len()
		if err != nil {
			t.Fatalf("node %d len: %v", i, err)
		}
		if l != length {
			t.Fatalf("node %d chain length %d != node 0 length %d", i, l, length)
		}
	}

	// Same block hashes and same ordered transaction signatures at every height.
	for h := uint64(0); h < length; h++ {
		b0, err := nodes[0].chain.BlockAt(h)
		if err != nil {
			t.Fatalf("node 0 block %d: %v", h, err)
		}
		h0 := fmt.Sprintf("%x", b0.Hash())
		for i, nd := range nodes {
			bi, err := nd.chain.BlockAt(h)
			if err != nil {
				t.Fatalf("node %d block %d: %v", i, h, err)
			}
			if fmt.Sprintf("%x", bi.Hash()) != h0 {
				t.Fatalf("node %d block %d hash differs from node 0", i, h)
			}
			if len(bi.Txs) != len(b0.Txs) {
				t.Fatalf("node %d block %d tx count %d != %d", i, h, len(bi.Txs), len(b0.Txs))
			}
			for k := range b0.Txs {
				if fmt.Sprintf("%x", bi.Txs[k].Signature) != fmt.Sprintf("%x", b0.Txs[k].Signature) {
					t.Fatalf("node %d block %d tx %d differs", i, h, k)
				}
			}
		}
		// Each committed chain must independently validate.
		for i, nd := range nodes {
			if err := nd.chain.ValidateChain(); err != nil {
				t.Fatalf("node %d chain invalid: %v", i, err)
			}
		}
	}

	// Same balances.
	for _, acc := range accounts {
		want, err := nodes[0].ledger.Balance(acc)
		if err != nil {
			t.Fatalf("balance: %v", err)
		}
		for i, nd := range nodes {
			got, err := nd.ledger.Balance(acc)
			if err != nil {
				t.Fatalf("node %d balance: %v", i, err)
			}
			if got != want {
				t.Fatalf("node %d balance for %s = %d, want %d", i, acc, got, want)
			}
		}
	}
}

// TestMultiNodeConsensus is the required multi-node test: >=3 in-process engines
// over an in-memory gossip bus, transactions submitted to different nodes, and
// assertions that all nodes commit the same ordered log, converge to identical
// balances, make progress across leader rotation, and reject bad/replayed txs.
func TestMultiNodeConsensus(t *testing.T) {
	t.Run("agreement and balance convergence", func(t *testing.T) {
		nodes, stop := newCluster(t, 4, nil)
		defer stop()

		alice := nodes[0].acct
		bob := nodes[1].acct
		aliceID := alice.AccountID()
		bobID := bob.AccountID()
		carolID := "carol-recipient-fixed-id"

		// Seed Alice on every node identically.
		mintAll(t, nodes, aliceID, 1000)

		// Submit transactions to DIFFERENT nodes so no single node originates them
		// all. The leader among the validators batches whatever it has seen; here we
		// submit each tx to every node to model gossip fan-out to the leader.
		txs := []*token.Transaction{
			signedTransfer(t, alice, bobID, 100, 0),
			signedTransfer(t, alice, carolID, 50, 1),
			signedTransfer(t, alice, bobID, 25, 2),
		}
		for i, tx := range txs {
			target := nodes[i%len(nodes)]
			if err := target.engine.Submit(tx); err != nil {
				t.Fatalf("submit tx %d: %v", i, err)
			}
			// Also feed the current leader so it can include it promptly. In a real
			// deployment tx gossip does this; here we fan out to all nodes.
			for _, nd := range nodes {
				_ = nd.engine.Submit(tx)
			}
		}

		waitForHeight(t, nodes, 1, 5*time.Second)
		// Wait until the mempools drain on the proposing side (all txs committed).
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			total := uint64(0)
			for _, nd := range nodes {
				b, _ := nd.ledger.Balance(bobID)
				total = b
				_ = total
			}
			b0, _ := nodes[0].ledger.Balance(bobID)
			c0, _ := nodes[0].ledger.Balance(carolID)
			if b0 == 125 && c0 == 50 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}

		waitForConvergedLength(t, nodes, 5*time.Second)
		assertConverged(t, nodes, []string{aliceID, bobID, carolID})

		bal, _ := nodes[0].ledger.Balance(bobID)
		if bal != 125 {
			t.Fatalf("bob balance = %d, want 125", bal)
		}
		cbal, _ := nodes[0].ledger.Balance(carolID)
		if cbal != 50 {
			t.Fatalf("carol balance = %d, want 50", cbal)
		}
		abal, _ := nodes[0].ledger.Balance(aliceID)
		if abal != 1000-125-50 {
			t.Fatalf("alice balance = %d, want %d", abal, 1000-175)
		}
	})

	t.Run("progress across leader rotation", func(t *testing.T) {
		// With a longer round timeout disabled we just observe several sequential
		// commits, which requires leadership to rotate as rounds advance. We commit
		// many small blocks and confirm the height climbs past the number of
		// validators, so more than one distinct leader must have driven a commit.
		nodes, stop := newCluster(t, 4, func(c *Config) {
			c.ProposeInterval = 4 * time.Millisecond
			c.RoundTimeout = 40 * time.Millisecond
		})
		defer stop()

		alice := nodes[0].acct
		aliceID := alice.AccountID()
		mintAll(t, nodes, aliceID, 100000)

		// Submit a stream of transfers, one per block-ish, over time so multiple
		// heights (and thus multiple leaders across rotations) commit.
		for i := 0; i < 12; i++ {
			to := fmt.Sprintf("recipient-%d", i)
			tx := signedTransfer(t, alice, to, 10, uint64(i))
			for _, nd := range nodes {
				_ = nd.engine.Submit(tx)
			}
			time.Sleep(15 * time.Millisecond)
		}

		waitForHeight(t, nodes, 3, 8*time.Second)
		// Every transfer applied everywhere means the cluster has stopped
		// committing, without which the balance comparison below races the next
		// commit.
		waitForBalances(t, nodes, aliceID, 100000-12*10, 8*time.Second)
		waitForConvergedLength(t, nodes, 8*time.Second)

		// Collect the set of distinct proposers across committed blocks; leader
		// rotation should mean more than one proposer committed a block over time.
		length, _ := nodes[0].chain.Len()
		proposers := map[string]struct{}{}
		for h := uint64(0); h < length; h++ {
			b, err := nodes[0].chain.BlockAt(h)
			if err != nil {
				t.Fatalf("block %d: %v", h, err)
			}
			proposers[b.ProposerID] = struct{}{}
		}
		t.Logf("committed %d blocks from %d distinct proposers", length, len(proposers))

		accounts := []string{aliceID}
		for i := 0; i < 12; i++ {
			accounts = append(accounts, fmt.Sprintf("recipient-%d", i))
		}
		waitForConvergedLength(t, nodes, 8*time.Second)
		assertConverged(t, nodes, accounts)
	})

	t.Run("rejects badly-signed and replayed transactions", func(t *testing.T) {
		nodes, stop := newCluster(t, 4, nil)
		defer stop()

		alice := nodes[0].acct
		bob := nodes[1].acct
		aliceID := alice.AccountID()
		bobID := bob.AccountID()
		mintAll(t, nodes, aliceID, 1000)

		// (d1) Badly-signed tx: tamper with the amount after signing so the
		// signature no longer verifies. Submit must reject it, and consensus must
		// never commit it.
		bad := signedTransfer(t, alice, bobID, 100, 0)
		bad.Amount = 999 // invalidates the signature
		if err := nodes[0].engine.Submit(bad); err == nil {
			t.Fatalf("expected Submit to reject badly-signed tx")
		}

		// A forged tx claiming Alice's key but signed by Bob must also be rejected.
		forged := &token.Transaction{
			From:      alice.PublicKey,
			To:        bobID,
			Amount:    500,
			Nonce:     0,
			Timestamp: time.Now().UnixNano(),
			PrevHash:  make([]byte, token.HashSize),
		}
		forged.Signature = ed25519.Sign(bob.PrivateKey, forged.SigningBytes())
		if err := nodes[0].engine.Submit(forged); err == nil {
			t.Fatalf("expected Submit to reject forged tx")
		}

		// (d2) A valid tx commits once; re-submitting the identical signed tx
		// (replay) is deduped and must not double-apply.
		good := signedTransfer(t, alice, bobID, 100, 0)
		for _, nd := range nodes {
			if err := nd.engine.Submit(good); err != nil {
				t.Fatalf("submit good: %v", err)
			}
		}
		waitForHeight(t, nodes, 1, 5*time.Second)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			b, _ := nodes[0].ledger.Balance(bobID)
			if b == 100 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}

		// Replay the same signed tx everywhere.
		for _, nd := range nodes {
			_ = nd.engine.Submit(good)
		}
		time.Sleep(200 * time.Millisecond)

		waitForConvergedLength(t, nodes, 5*time.Second)
		assertConverged(t, nodes, []string{aliceID, bobID})
		b, _ := nodes[0].ledger.Balance(bobID)
		if b != 100 {
			t.Fatalf("bob balance after replay = %d, want 100 (replay must not double-apply)", b)
		}
	})
}

// TestValidatorSetLeaderRotation unit-tests the deterministic round-robin leader
// schedule and quorum math independent of the network.
func TestValidatorSetLeaderRotation(t *testing.T) {
	var pubs []ed25519.PublicKey
	for i := 0; i < 4; i++ {
		a, err := token.GenerateAccount()
		if err != nil {
			t.Fatalf("gen: %v", err)
		}
		pubs = append(pubs, a.PublicKey)
	}
	vs, err := NewValidatorSet(pubs)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if vs.QuorumPower() != 3 {
		t.Fatalf("quorum for N=4 = %d, want 3", vs.QuorumPower())
	}
	ids := vs.IDs()
	// (height, round) must select ids[(height+round) mod N] and wrap around. The
	// height is what makes the turn pass on every commit rather than only on a
	// round timeout.
	for h := uint64(0); h < 5; h++ {
		for r := uint64(0); r < 8; r++ {
			want := ids[(h+r)%uint64(len(ids))]
			if got := vs.LeaderFor(h, r); got != want {
				t.Fatalf("leader for height %d round %d = %s, want %s", h, r, got, want)
			}
		}
	}
	// Two consecutive heights at round 0 must have DIFFERENT leaders. This is the
	// regression that matters: with leadership keyed on the round alone, round 0
	// came round again at every commit and one validator proposed forever.
	if vs.LeaderFor(0, 0) == vs.LeaderFor(1, 0) {
		t.Fatal("the same validator leads two consecutive heights at round 0; leadership does not rotate")
	}
	// Ordering is deterministic regardless of input order.
	shuffled := []ed25519.PublicKey{pubs[3], pubs[0], pubs[2], pubs[1]}
	vs2, err := NewValidatorSet(shuffled)
	if err != nil {
		t.Fatalf("set2: %v", err)
	}
	ids2 := vs2.IDs()
	for i := range ids {
		if ids[i] != ids2[i] {
			t.Fatalf("validator ordering not deterministic at %d", i)
		}
	}
}

// TestTxGossipLetsALeaderProposeATransferItDidNotReceive submits a transfer to
// one node whose proposals never reach the others. The chain can only commit
// if TopicTx copies the transfer into a leader that can be heard.
func TestTxGossipLetsALeaderProposeATransferItDidNotReceive(t *testing.T) {
	nodes, stop := newCluster(t, 3, func(cfg *Config) {
		cfg.DisableTxGossip = false
	})
	defer stop()

	isolated := nodes[2]
	nodes[0].bus.setDeliveryFilter(func(from, to peer.ID, topic string) bool {
		if topic == TopicProposal && from == isolated.peerID {
			return false
		}
		return true
	})

	alice := nodes[0].acct
	mintAll(t, nodes, alice.AccountID(), 1000)
	const recipient = "tx-gossip-recipient"
	tx := signedTransfer(t, alice, recipient, 7, 0)
	if err := isolated.engine.Submit(tx); err != nil {
		t.Fatalf("submit: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ok := true
		for _, nd := range nodes {
			b, err := nd.ledger.Balance(recipient)
			if err != nil || b != 7 {
				ok = false
				break
			}
		}
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("transfer submitted to a silenced proposer never committed; TopicTx did not reach a heard leader")
}
