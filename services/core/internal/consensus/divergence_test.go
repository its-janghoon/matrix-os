package consensus

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/ecirlabs/matrix-core/internal/transport"
)

// buildSignedBlock constructs and signs a block at (height, round) for the given
// leader over txs, linking to prevHash. It mirrors what a leader's
// buildBlockLocked produces, but is driven directly by the test so we can force
// two conflicting blocks at the SAME height across a round rotation.
//
// Unlock evidence belongs to the proposal envelope, not the block, so it is
// passed to deliverProposal rather than here.
func buildSignedBlock(t *testing.T, leader *token.Account, height, round uint64, prevHash []byte, txs []token.Transaction) *Block {
	t.Helper()
	b := &Block{
		Height:        height,
		Round:         round,
		PrevBlockHash: append([]byte(nil), prevHash...),
		Txs:           txs,
		ProposerID:    leader.AccountID(),
		Timestamp:     time.Now().Unix(),
	}
	if err := b.Sign(leader.PrivateKey); err != nil {
		t.Fatalf("sign block: %v", err)
	}
	return b
}

// buildVote constructs and signs a PREVOTE by voter for a block hash at
// (height, round). Prevotes are what polka certificates are made of.
func buildVote(t *testing.T, voter *token.Account, height, round uint64, blockHash []byte) Vote {
	t.Helper()
	return buildTypedVote(t, voter, VoteTypePrevote, height, round, blockHash)
}

// buildPrecommit constructs and signs a PRECOMMIT, the phase a quorum of which
// commits a block.
func buildPrecommit(t *testing.T, voter *token.Account, height, round uint64, blockHash []byte) Vote {
	t.Helper()
	return buildTypedVote(t, voter, VoteTypePrecommit, height, round, blockHash)
}

func buildTypedVote(t *testing.T, voter *token.Account, typ VoteType, height, round uint64, blockHash []byte) Vote {
	t.Helper()
	v := Vote{
		Type:      typ,
		Height:    height,
		Round:     round,
		BlockHash: append([]byte(nil), blockHash...),
		VoterID:   voter.AccountID(),
		PublicKey: voter.PublicKey,
	}
	if err := v.Sign(voter.PrivateKey); err != nil {
		t.Fatalf("sign vote: %v", err)
	}
	return v
}

// otherValidators returns up to count accounts from accts that are not `exclude`.
func otherValidators(accts []*token.Account, exclude *token.Account, count int) []*token.Account {
	out := make([]*token.Account, 0, count)
	for _, a := range accts {
		if a.AccountID() == exclude.AccountID() {
			continue
		}
		out = append(out, a)
		if len(out) == count {
			break
		}
	}
	return out
}

// leaderForRound returns the validator account whose ID is the round-robin
// leader for (height, round) among accts, given the deterministic ValidatorSet
// ordering.
func leaderForRound(t *testing.T, vs *ValidatorSet, accts []*token.Account, height, round uint64) *token.Account {
	t.Helper()
	id := vs.LeaderFor(height, round)
	for _, a := range accts {
		if a.AccountID() == id {
			return a
		}
	}
	t.Fatalf("no account for leader id %s", id)
	return nil
}

// TestConsensusNoDivergenceAcrossRotation directly exercises the safety property
// the review flagged: two conflicting blocks proposed for the SAME height across
// a leader rotation must NEVER both reach quorum, and a validator that voted for
// the round-0 block must refuse to vote for a conflicting round-1 block that
// lacks a valid polka certificate. This is the scenario a WAN deployment hits
// when commit latency approaches the round timeout.
func TestConsensusNoDivergenceAcrossRotation(t *testing.T) {
	// A 4-validator set (quorum 3). We drive one engine directly through its
	// message handlers so we control exactly which proposals and votes it sees and
	// in what order, modelling delayed gossip + round rotation deterministically.
	const n = 4
	accts := make([]*token.Account, n)
	pubs := make([]ed25519.PublicKey, n)
	for i := 0; i < n; i++ {
		a, err := token.GenerateAccount()
		if err != nil {
			t.Fatalf("gen account: %v", err)
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
	ledger := market.NewLedger(store)
	chain := NewBlockChain(store)

	// The subject node is a passive follower (Self == the round-0 leader so it can
	// vote), driven only through handleProposal/handleVote. Using a no-op transport
	// keeps its own publishes from feeding anything back.
	subject := leaderForRound(t, vs, accts, 0, 0)
	eng, err := New(Config{
		Transport:  noopTransport{},
		Validators: vs,
		Chain:      chain,
		Ledger:     ledger,
		Self:       subject,
		// Large timeout so the driver never bumps the round on its own; the test
		// drives rounds explicitly.
		RoundTimeout: time.Hour,
	})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); eng.Wait() }()
	if err := eng.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Fund a shared sender so transfers are affordable and both blocks are equally
	// valid apart from their contents.
	sender := accts[0]
	senderID := sender.AccountID()
	if err := ledger.Credit(senderID, 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}

	head, _, err := chain.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}

	// Two CONFLICTING blocks at height 0: block A (round 0) and block B (round 1),
	// with different transaction contents so their hashes differ.
	txA := signedTransfer(t, sender, "recipient-A", 100, 0)
	txB := signedTransfer(t, sender, "recipient-B", 200, 1)
	leader0 := leaderForRound(t, vs, accts, 0, 0)
	leader1 := leaderForRound(t, vs, accts, 0, 1)
	blockA := buildSignedBlock(t, leader0, 0, 0, head, []token.Transaction{*txA})
	blockB := buildSignedBlock(t, leader1, 0, 1, head, []token.Transaction{*txB})
	if string(blockA.Hash()) == string(blockB.Hash()) {
		t.Fatal("test setup: blocks A and B must differ")
	}

	// The subject sees block A (round 0) and prevotes it.
	deliverProposal(t, eng, leader0, 0, blockA, nil)
	if !engHasPrevotedFor(eng, blockA.Hash()) {
		t.Fatal("subject should have prevoted block A at round 0")
	}

	// Two other validators prevote A, so A reaches a prevote quorum (3 of 4) -
	// a polka. The subject answers a polka with a precommit, and THAT is what
	// locks it on A. A lock is never taken on a node's own single vote.
	others := otherValidators(accts, subject, 2)
	deliverVote(t, eng, buildVote(t, others[0], 0, 0, blockA.Hash()))
	deliverVote(t, eng, buildVote(t, others[1], 0, 0, blockA.Hash()))
	if !engHasPrecommittedFor(eng, blockA.Hash()) {
		t.Fatal("subject should have precommitted block A after the polka")
	}

	// Now a conflicting block B arrives for round 1 WITHOUT a polka certificate.
	// The locked subject must REFUSE to prevote it.
	deliverProposal(t, eng, leader1, 1, blockB, nil)
	if engHasPrevotedFor(eng, blockB.Hash()) {
		t.Fatal("SAFETY VIOLATION: subject prevoted conflicting block B without a polka certificate")
	}

	// Feed the subject the other validators' PRECOMMITS for A so A reaches a
	// precommit quorum and commits here.
	deliverVote(t, eng, buildPrecommit(t, others[0], 0, 0, blockA.Hash()))
	deliverVote(t, eng, buildPrecommit(t, others[1], 0, 0, blockA.Hash()))

	// A must commit at height 0; B must never commit.
	deadline := time.Now().Add(2 * time.Second)
	for eng.Height() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if eng.Height() != 1 {
		t.Fatalf("expected height 1 after committing A, got %d", eng.Height())
	}
	committed, err := chain.BlockAt(0)
	if err != nil {
		t.Fatalf("block at 0: %v", err)
	}
	if string(committed.Hash()) != string(blockA.Hash()) {
		t.Fatal("committed block at height 0 is not block A")
	}

	// Balances reflect ONLY block A's transfer, never block B's.
	if bal, _ := ledger.Balance("recipient-A"); bal != 100 {
		t.Fatalf("recipient-A balance = %d, want 100", bal)
	}
	if bal, _ := ledger.Balance("recipient-B"); bal != 0 {
		t.Fatalf("SAFETY VIOLATION: recipient-B balance = %d, want 0 (block B must never apply)", bal)
	}
}

// TestConsensusUnlockRequiresValidCertificate confirms the safety valve is also
// LIVE: a locked validator WILL vote for a conflicting higher-round block when
// (and only when) it is accompanied by a valid polka certificate proving that
// block reached a quorum at a round >= the lock. This is what keeps the protocol
// making progress after a legitimate leader rotation.
func TestConsensusUnlockRequiresValidCertificate(t *testing.T) {
	const n = 4
	accts := make([]*token.Account, n)
	pubs := make([]ed25519.PublicKey, n)
	for i := 0; i < n; i++ {
		a, err := token.GenerateAccount()
		if err != nil {
			t.Fatalf("gen: %v", err)
		}
		accts[i] = a
		pubs[i] = a.PublicKey
	}
	vs, err := NewValidatorSet(pubs)
	if err != nil {
		t.Fatalf("set: %v", err)
	}

	leader0 := leaderForRound(t, vs, accts, 0, 0)
	leader1 := leaderForRound(t, vs, accts, 0, 1)
	subject := leader0

	// Each case gets its own engine so one case's votes cannot influence another.
	newSubject := func(t *testing.T) (*Engine, *BlockChain, []byte) {
		t.Helper()
		store, err := kv.New(kv.Config{Path: t.TempDir()})
		if err != nil {
			t.Fatalf("kv: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		ledger := market.NewLedger(store)
		chain := NewBlockChain(store)
		eng, err := New(Config{
			Transport:    noopTransport{},
			Validators:   vs,
			Chain:        chain,
			Ledger:       ledger,
			Self:         subject,
			RoundTimeout: time.Hour,
		})
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(func() { cancel(); eng.Wait() })
		if err := eng.Start(ctx); err != nil {
			t.Fatalf("start: %v", err)
		}
		if err := ledger.Credit(subject.AccountID(), 1000); err != nil {
			t.Fatalf("credit: %v", err)
		}
		head, _, _ := chain.Head()
		return eng, chain, head
	}

	t.Run("a valid certificate releases the lock", func(t *testing.T) {
		eng, _, head := newSubject(t)

		txA := signedTransfer(t, subject, "recipient-A", 100, 0)
		blockA := buildSignedBlock(t, leader0, 0, 0, head, []token.Transaction{*txA})

		// Subject prevotes A, then a polka for A makes it precommit and lock.
		deliverProposal(t, eng, leader0, 0, blockA, nil)
		others := otherValidators(accts, subject, 2)
		deliverVote(t, eng, buildVote(t, others[0], 0, 0, blockA.Hash()))
		deliverVote(t, eng, buildVote(t, others[1], 0, 0, blockA.Hash()))
		if !engHasPrecommittedFor(eng, blockA.Hash()) {
			t.Fatal("subject should be locked on A (precommitted) before the unlock is tested")
		}

		// A VALID polka certificate for block B at round 1: a quorum (3) of
		// validators voted for B at round 1. In reality this can only arise if the
		// network genuinely converged on B; here we synthesise it to prove the
		// unlock path honors a valid certificate.
		txB := signedTransfer(t, subject, "recipient-B", 200, 1)
		blockB := buildSignedBlock(t, leader1, 0, 1, head, []token.Transaction{*txB})
		cert := &PolkaCertificate{
			Height:    0,
			Round:     1,
			BlockHash: blockB.Hash(),
			Votes: []Vote{
				buildVote(t, accts[0], 0, 1, blockB.Hash()),
				buildVote(t, accts[1], 0, 1, blockB.Hash()),
				buildVote(t, accts[2], 0, 1, blockB.Hash()),
			},
		}
		if err := cert.Verify(vs); err != nil {
			t.Fatalf("synthesised certificate should verify: %v", err)
		}

		// With a valid polka at a round >= its lock, the locked subject releases
		// the lock and prevotes B.
		deliverProposal(t, eng, leader1, 1, blockB, cert)
		if !engHasPrevotedFor(eng, blockB.Hash()) {
			t.Fatal("subject should prevote B once shown a valid polka certificate at a round >= its lock")
		}
	})

	t.Run("a tampered certificate does not", func(t *testing.T) {
		eng, chain, head := newSubject(t)

		txA := signedTransfer(t, subject, "recipient-A", 100, 0)
		blockA := buildSignedBlock(t, leader0, 0, 0, head, []token.Transaction{*txA})
		deliverProposal(t, eng, leader0, 0, blockA, nil)
		others := otherValidators(accts, subject, 2)
		deliverVote(t, eng, buildVote(t, others[0], 0, 0, blockA.Hash()))
		deliverVote(t, eng, buildVote(t, others[1], 0, 0, blockA.Hash()))
		if !engHasPrecommittedFor(eng, blockA.Hash()) {
			t.Fatal("subject should be locked on A (precommitted) before the unlock is tested")
		}

		// A certificate with one vote's signature broken must not unlock anything.
		txC := signedTransfer(t, subject, "recipient-C", 300, 2)
		blockC := buildSignedBlock(t, leader1, 0, 1, head, []token.Transaction{*txC})
		badVote := buildVote(t, accts[2], 0, 1, blockC.Hash())
		badVote.Signature[0] ^= 0xFF
		badCert := &PolkaCertificate{
			Height:    0,
			Round:     1,
			BlockHash: blockC.Hash(),
			Votes: []Vote{
				buildVote(t, accts[0], 0, 1, blockC.Hash()),
				buildVote(t, accts[1], 0, 1, blockC.Hash()),
				badVote,
			},
		}
		if err := badCert.Verify(vs); err == nil {
			t.Fatal("tampered certificate should not verify")
		}

		deliverProposal(t, eng, leader1, 1, blockC, badCert)
		if engHasPrevotedFor(eng, blockC.Hash()) {
			t.Fatal("SAFETY VIOLATION: subject prevoted block C waving an invalid certificate")
		}
		if l, err := chain.Len(); err != nil || l != 0 {
			t.Fatalf("chain length = %d (err %v), want 0: nothing may commit on an invalid certificate", l, err)
		}
	})
}

// TestMultiNodeNoDivergenceUnderTightTimeout runs a real N-node cluster over the
// in-memory bus with a round timeout close to commit latency, so rounds actually
// rotate mid-height under contention. It submits work and asserts that every
// node that commits a given height commits the IDENTICAL block hash (no
// divergence), across many heights. It is designed to run under -race.
func TestMultiNodeNoDivergenceUnderTightTimeout(t *testing.T) {
	nodes, stop := newCluster(t, 4, func(c *Config) {
		// Tight timings so rounds rotate aggressively while commits are still in
		// flight, maximising the chance two proposers compete at one height. The
		// round timeout is deliberately close to in-memory commit latency, which is
		// exactly the WAN condition the review said the original test never
		// reproduced.
		c.ProposeInterval = 2 * time.Millisecond
		c.RoundTimeout = 6 * time.Millisecond
	})
	defer stop()

	alice := nodes[0].acct
	aliceID := alice.AccountID()
	mintAll(t, nodes, aliceID, 1_000_000)

	for i := 0; i < 40; i++ {
		to := fmt.Sprintf("recipient-%d", i)
		tx := signedTransfer(t, alice, to, 1, uint64(i))
		// Fan the tx out to every node (modelling tx gossip) so whichever node is
		// leader in the current round can include it; rounds rotate meanwhile,
		// creating competing proposals at the same height.
		for _, nd := range nodes {
			_ = nd.engine.Submit(tx)
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Let the cluster churn until the WORK is done, not until some block count is
	// reached. How many blocks 40 transfers become is a timing artefact - a loaded
	// machine batches more of them per block - so a height target here measures
	// the machine rather than the engine. What has to happen is that every
	// transfer commits, on at least a quorum of nodes; the safety property (no
	// divergence) is then asserted over every pair of nodes below, regardless of
	// how far each one got.
	const wantBalance = 1_000_000 - 40
	quorum := int(nodes[0].engine.ValidatorSet().QuorumPower()) // equal power: heads == power
	deadline := time.Now().Add(20 * time.Second)
	for {
		settled := 0
		for _, nd := range nodes {
			bal, err := nd.ledger.Balance(aliceID)
			if err != nil {
				t.Fatalf("balance: %v", err)
			}
			if bal == wantBalance {
				settled++
			}
		}
		if settled >= quorum {
			break
		}
		if time.Now().After(deadline) {
			for i, nd := range nodes {
				bal, _ := nd.ledger.Balance(aliceID)
				l, _ := nd.chain.Len()
				t.Logf("node %d height=%d chain length=%d alice=%d (want %d)",
					i, nd.engine.Height(), l, bal, wantBalance)
			}
			t.Fatalf("fewer than a quorum (%d) of nodes committed all 40 transfers", quorum)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// SAFETY ASSERTION: for every height, any two nodes that have BOTH committed
	// that height must have committed the IDENTICAL block. Divergence (two nodes
	// committing different blocks at the same height) fails here. This holds
	// pairwise regardless of how far any individual node has progressed, so a
	// legitimately-lagging node never masks a divergence.
	maxLen := uint64(0)
	lens := make([]uint64, len(nodes))
	for i, nd := range nodes {
		l, err := nd.chain.Len()
		if err != nil {
			t.Fatalf("len: %v", err)
		}
		lens[i] = l
		if l > maxLen {
			maxLen = l
		}
	}
	for h := uint64(0); h < maxLen; h++ {
		var refHash string
		var haveRef bool
		for i, nd := range nodes {
			if lens[i] <= h {
				continue
			}
			b, err := nd.chain.BlockAt(h)
			if err != nil {
				t.Fatalf("node %d block %d: %v", i, h, err)
			}
			hh := fmt.Sprintf("%x", b.Hash())
			if !haveRef {
				refHash = hh
				haveRef = true
				continue
			}
			if hh != refHash {
				t.Fatalf("DIVERGENCE at height %d: node %d committed a different block", h, i)
			}
		}
	}
	minLen := maxLen
	for _, l := range lens {
		if l < minLen {
			minLen = l
		}
	}
	for h := uint64(0); h < minLen; h++ {
		want, err := nodes[0].chain.BlockAt(h)
		if err != nil {
			t.Fatalf("node 0 block %d: %v", h, err)
		}
		wantHash := fmt.Sprintf("%x", want.Hash())
		for i, nd := range nodes {
			got, err := nd.chain.BlockAt(h)
			if err != nil {
				t.Fatalf("node %d block %d: %v", i, h, err)
			}
			if fmt.Sprintf("%x", got.Hash()) != wantHash {
				t.Fatalf("DIVERGENCE at height %d: node %d committed a different block than node 0", h, i)
			}
		}
	}
	if minLen < 2 {
		t.Fatalf("every node committed %d block(s); the test needs at least 2 heights to compare", minLen)
	}
	t.Logf("no divergence across %d committed heights under tight round rotation", minLen)
}

// --- test helpers wired to the engine internals (same package) ---

// noopTransport is a Transport that accepts subscriptions (returning a channel
// closed on ctx cancel) and drops all publishes. It lets a test drive an engine
// purely through deliverProposal/deliverVote without gossip feedback.
type noopTransport struct{}

func (noopTransport) Subscribe(ctx context.Context, _ string) (<-chan transport.Message, error) {
	ch := make(chan transport.Message)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

func (noopTransport) Publish(_ context.Context, _ string, _ []byte) error { return nil }

// deliverProposal wraps a block in an envelope signed by proposer for round and
// feeds it to the engine's proposal handler, as gossip would.
func deliverProposal(t *testing.T, e *Engine, proposer *token.Account, round uint64, b *Block, justify *PolkaCertificate) {
	t.Helper()
	p := &Proposal{Block: *b, Round: round, ProposerID: proposer.AccountID(), Justify: justify}
	if err := p.Sign(proposer.PrivateKey); err != nil {
		t.Fatalf("sign proposal: %v", err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal proposal: %v", err)
	}
	e.handleProposal(context.Background(), transport.Message{From: peer.ID("test"), Topic: TopicProposal, Payload: data})
	time.Sleep(10 * time.Millisecond)
}

func deliverVote(t *testing.T, e *Engine, v Vote) {
	t.Helper()
	data, err := json.Marshal(&v)
	if err != nil {
		t.Fatalf("marshal vote: %v", err)
	}
	e.handleVote(context.Background(), transport.Message{From: peer.ID("test"), Topic: TopicVote, Payload: data})
	time.Sleep(10 * time.Millisecond)
}

// engHasVotedFor reports whether the engine has tallied its own vote for the
// given block hash at the current height.
// engHasPrevotedFor reports whether the engine itself prevoted the given block
// at the current height, in any round.
func engHasPrevotedFor(e *Engine, blockHash []byte) bool {
	return engHasVotedForType(e, VoteTypePrevote, blockHash)
}

// engHasPrecommittedFor reports whether the engine itself precommitted the
// given block at the current height, in any round.
func engHasPrecommittedFor(e *Engine, blockHash []byte) bool {
	return engHasVotedForType(e, VoteTypePrecommit, blockHash)
}

func engHasVotedForType(e *Engine, typ VoteType, blockHash []byte) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	byRound := e.prevotes
	if typ == VoteTypePrecommit {
		byRound = e.precommits
	}
	hkey := fmt.Sprintf("%x", blockHash)
	for _, byHash := range byRound {
		if _, ok := byHash[hkey][e.selfID]; ok {
			return true
		}
	}
	return false
}
