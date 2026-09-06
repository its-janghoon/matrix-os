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
func buildSignedBlock(t *testing.T, leader *token.Account, height, round uint64, prevHash []byte, txs []token.Transaction, justify *PolkaCertificate) *Block {
	t.Helper()
	b := &Block{
		Height:        height,
		Round:         round,
		PrevBlockHash: append([]byte(nil), prevHash...),
		Txs:           txs,
		ProposerID:    leader.AccountID(),
		Justify:       justify,
	}
	if err := b.Sign(leader.PrivateKey); err != nil {
		t.Fatalf("sign block: %v", err)
	}
	return b
}

// buildVote constructs and signs a vote by voter for a block hash at
// (height, round).
func buildVote(t *testing.T, voter *token.Account, height, round uint64, blockHash []byte) Vote {
	t.Helper()
	v := Vote{
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
// leader for round r among accts, given the deterministic ValidatorSet ordering.
func leaderForRound(t *testing.T, vs *ValidatorSet, accts []*token.Account, round uint64) *token.Account {
	t.Helper()
	id := vs.LeaderForRound(round)
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
	subject := leaderForRound(t, vs, accts, 0)
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
	leader0 := leaderForRound(t, vs, accts, 0)
	leader1 := leaderForRound(t, vs, accts, 1)
	blockA := buildSignedBlock(t, leader0, 0, 0, head, []token.Transaction{*txA}, nil)
	blockB := buildSignedBlock(t, leader1, 0, 1, head, []token.Transaction{*txB}, nil)
	if string(blockA.Hash()) == string(blockB.Hash()) {
		t.Fatal("test setup: blocks A and B must differ")
	}

	// The subject sees block A (round 0) and votes for it -> it locks on A.
	deliverProposal(t, eng, blockA)
	if !engHasVotedFor(eng, blockA.Hash()) {
		t.Fatal("subject should have voted for block A at round 0")
	}

	// Now a conflicting block B arrives for round 1 WITHOUT a justification. The
	// locked subject must REFUSE it: it must not cache it and must not vote for it.
	deliverProposal(t, eng, blockB)
	if engHasVotedFor(eng, blockB.Hash()) {
		t.Fatal("SAFETY VIOLATION: subject voted for conflicting block B without a polka certificate")
	}

	// Feed the subject the OTHER validators' votes for A so A reaches quorum (3)
	// and commits on the subject. We pick voters that are not the subject itself
	// (whose vote is already tallied) so we add exactly two distinct votes.
	others := otherValidators(accts, subject, 2)
	deliverVote(t, eng, buildVote(t, others[0], 0, 0, blockA.Hash()))
	deliverVote(t, eng, buildVote(t, others[1], 0, 0, blockA.Hash()))

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
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ledger := market.NewLedger(store)
	chain := NewBlockChain(store)

	subject := leaderForRound(t, vs, accts, 0)
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
	defer func() { cancel(); eng.Wait() }()
	if err := eng.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	sender := accts[0]
	if err := ledger.Credit(sender.AccountID(), 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	head, _, _ := chain.Head()

	txA := signedTransfer(t, sender, "recipient-A", 100, 0)
	txB := signedTransfer(t, sender, "recipient-B", 200, 1)
	leader0 := leaderForRound(t, vs, accts, 0)
	leader1 := leaderForRound(t, vs, accts, 1)
	blockA := buildSignedBlock(t, leader0, 0, 0, head, []token.Transaction{*txA}, nil)

	// Subject sees A at round 0 and locks on it.
	deliverProposal(t, eng, blockA)
	if !engHasVotedFor(eng, blockA.Hash()) {
		t.Fatal("subject should vote for A")
	}

	// Build a VALID polka certificate for block B at round 1: a quorum (3) of
	// validators voted for B at round 1. In reality this can only arise if the
	// network genuinely converged on B; here we synthesise it to prove the unlock
	// path honors a valid certificate.
	blockB := buildSignedBlock(t, leader1, 0, 1, head, []token.Transaction{*txB}, nil)
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
	blockBJustified := buildSignedBlock(t, leader1, 0, 1, head, []token.Transaction{*txB}, cert)

	// With a valid certificate, the locked subject releases its lock and votes B.
	deliverProposal(t, eng, blockBJustified)
	if !engHasVotedFor(eng, blockB.Hash()) {
		t.Fatal("subject should vote for B once shown a valid polka certificate at a round >= its lock")
	}

	// A tampered certificate (one vote's signature broken) must be rejected: the
	// subject must NOT vote for a third conflicting block waving an invalid cert.
	txC := signedTransfer(t, sender, "recipient-C", 300, 2)
	blockC := buildSignedBlock(t, leader1, 0, 1, head, []token.Transaction{*txC}, nil)
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
	blockCBad := buildSignedBlock(t, leader1, 0, 1, head, []token.Transaction{*txC}, badCert)
	deliverProposal(t, eng, blockCBad)
	if engHasVotedFor(eng, blockC.Hash()) {
		t.Fatal("SAFETY VIOLATION: subject voted for block C waving an invalid certificate")
	}
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
	quorum := nodes[0].engine.ValidatorSet().Quorum()
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

func deliverProposal(t *testing.T, e *Engine, b *Block) {
	t.Helper()
	data, err := json.Marshal(&Proposal{Block: *b})
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
func engHasVotedFor(e *Engine, blockHash []byte) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	set := e.votes[fmt.Sprintf("%x", blockHash)]
	if set == nil {
		return false
	}
	_, ok := set[e.selfID]
	return ok
}
