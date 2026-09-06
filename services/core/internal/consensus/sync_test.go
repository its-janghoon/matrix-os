package consensus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// syncCluster builds a cluster tuned for the sync tests: short rounds so a
// stall is reached quickly, and a head announce interval short enough that a
// node which is behind on an idle network notices within the test's patience.
func syncCluster(t *testing.T, n int) ([]*testNode, context.CancelFunc) {
	t.Helper()
	return newCluster(t, n, func(c *Config) {
		c.ProposeInterval = 4 * time.Millisecond
		c.RoundTimeout = 40 * time.Millisecond
		c.HeadAnnounceInterval = 20 * time.Millisecond
	})
}

// dropTo returns a delivery filter that drops the named topics on their way to
// one node, leaving every other path intact.
func dropTo(victim peer.ID, topics ...string) func(from, to peer.ID, topic string) bool {
	blocked := make(map[string]struct{}, len(topics))
	for _, tp := range topics {
		blocked[tp] = struct{}{}
	}
	return func(_, to peer.ID, topic string) bool {
		if to != victim {
			return true
		}
		_, dropped := blocked[topic]
		return !dropped
	}
}

// waitForNodeHeight waits for one node to reach a height.
func waitForNodeHeight(t *testing.T, nd *testNode, height uint64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if nd.engine.Height() >= height {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for node to reach height %d (at %d)", height, nd.engine.Height())
}

// TestBlockSyncRecoversNodeThatMissedTheCommittingProposal covers the stall
// block sync exists for.
//
// Gossip is best effort, so a node can miss exactly one message: the proposal
// that the rest of the validator set then commits. It still receives the votes
// for that block - it can even tally a full quorum of them - but a quorum
// without the body cannot commit, and the body is never sent again: the network
// has moved to the next height and no leader re-proposes a committed block. The
// node is then stuck at that height permanently, stashing every later proposal
// as a future height it can never reach.
func TestBlockSyncRecoversNodeThatMissedTheCommittingProposal(t *testing.T) {
	nodes, stop := syncCluster(t, 4)
	defer stop()

	victim := nodes[3]
	nodes[0].bus.setDeliveryFilter(dropTo(victim.peerID, TopicProposal))

	alice := nodes[0].acct
	aliceID := alice.AccountID()
	mintAll(t, nodes, aliceID, 1000)

	// Submit to the connected nodes only. The victim then has an empty mempool,
	// so it never proposes a block of its own: the only body that exists for
	// height 0 is the one it cannot receive. (A node that proposes the committed
	// block already holds it and was never stalled - that is a different case.)
	tx := signedTransfer(t, alice, "recipient-0", 10, 0)
	for _, nd := range nodes[:3] {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}

	// The connected nodes commit the block.
	waitForHeight(t, nodes[:3], 1, 5*time.Second)

	// The victim never sees a proposal, so it can only get the body from a peer.
	// Block sync must carry it across even though the proposal topic stays cut.
	waitForNodeHeight(t, victim, 1, 5*time.Second)

	assertConverged(t, nodes, []string{aliceID, "recipient-0"})
}

// TestAMissedProposalStallsANodeWithoutBlockSync is the other half of the
// previous test: with the sync response topic cut as well, the victim is
// exactly as stuck as it was before block sync existed, and it recovers the
// moment that topic is restored. That is what pins the recovery on block sync
// rather than on some other retry in the engine.
func TestAMissedProposalStallsANodeWithoutBlockSync(t *testing.T) {
	nodes, stop := syncCluster(t, 4)
	defer stop()

	victim := nodes[3]
	nodes[0].bus.setDeliveryFilter(dropTo(victim.peerID, TopicProposal, TopicSyncResponse))

	alice := nodes[0].acct
	aliceID := alice.AccountID()
	mintAll(t, nodes, aliceID, 1000)

	// Only the connected nodes get the transaction, so the victim cannot propose
	// a block itself and the sole body for height 0 is one it cannot receive.
	tx := signedTransfer(t, alice, "recipient-0", 10, 0)
	for _, nd := range nodes[:3] {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
	waitForHeight(t, nodes[:3], 1, 5*time.Second)

	// Give the victim far longer than a round to make progress on its own. It
	// cannot: it holds the votes but not the block.
	time.Sleep(500 * time.Millisecond)
	if h := victim.engine.Height(); h != 0 {
		t.Fatalf("victim height = %d, want 0 while the body cannot reach it", h)
	}

	// Pin the shape of the stall: the victim has a quorum of precommits for the
	// block the network committed, and does not have that block.
	committed, err := nodes[0].chain.BlockAt(0)
	if err != nil {
		t.Fatalf("block at 0: %v", err)
	}
	committedKey := fmt.Sprintf("%x", committed.Hash())
	quorum := nodes[0].engine.validators.Quorum()

	victim.engine.mu.Lock()
	tallied := 0
	for _, byHash := range victim.engine.precommits {
		if n := len(byHash[committedKey]); n > tallied {
			tallied = n
		}
	}
	_, hasBody := victim.engine.proposals[committedKey]
	victim.engine.mu.Unlock()

	if tallied < quorum {
		t.Fatalf("victim tallied %d precommits for the committed block, want at least the quorum of %d",
			tallied, quorum)
	}
	if hasBody {
		t.Fatal("victim holds the committed block body, so this is not the stall under test")
	}

	// Restore only the sync response path. The proposal topic stays cut, so the
	// body can reach the victim by exactly one route.
	nodes[0].bus.setDeliveryFilter(dropTo(victim.peerID, TopicProposal))
	waitForNodeHeight(t, victim, 1, 5*time.Second)

	assertConverged(t, nodes, []string{aliceID, "recipient-0"})
}

// TestBlockSyncCatchesUpAPartitionedNodeOnAnIdleNetwork covers the node that
// was away while the chain moved on - a restart, a network outage - and comes
// back to a network with nothing left to say. No proposals, no votes and no
// transactions are in flight to reveal that it is behind, so recovery rests
// entirely on the head announcement and on the endorsing votes persisted with
// each committed block.
func TestBlockSyncCatchesUpAPartitionedNodeOnAnIdleNetwork(t *testing.T) {
	nodes, stop := syncCluster(t, 4)
	defer stop()

	victim := nodes[3]
	// Total blackout: nothing at all reaches the victim.
	nodes[0].bus.setDeliveryFilter(func(_, to peer.ID, _ string) bool { return to != victim.peerID })

	alice := nodes[0].acct
	aliceID := alice.AccountID()
	mintAll(t, nodes, aliceID, 100000)

	const transfers = 5
	for i := 0; i < transfers; i++ {
		tx := signedTransfer(t, alice, fmt.Sprintf("recipient-%d", i), 10, uint64(i))
		for _, nd := range nodes[:3] {
			if err := nd.engine.Submit(tx); err != nil {
				t.Fatalf("submit: %v", err)
			}
		}
		// Space the submissions so they land in separate blocks, giving the victim
		// several heights to catch up on rather than one.
		time.Sleep(25 * time.Millisecond)
	}

	waitForBalances(t, nodes[:3], aliceID, 100000-transfers*10, 8*time.Second)
	connected, err := nodes[0].chain.Len()
	if err != nil {
		t.Fatalf("len: %v", err)
	}
	if connected < 2 {
		t.Fatalf("connected nodes committed %d blocks, want at least 2 so catch-up spans heights", connected)
	}
	if h := victim.engine.Height(); h != 0 {
		t.Fatalf("victim height = %d, want 0 during the blackout", h)
	}

	// Heal the partition and submit nothing further. The victim has to work out
	// on its own that it is behind.
	nodes[0].bus.setDeliveryFilter(nil)

	waitForNodeHeight(t, victim, connected, 8*time.Second)
	accounts := []string{aliceID}
	for i := 0; i < transfers; i++ {
		accounts = append(accounts, fmt.Sprintf("recipient-%d", i))
	}
	waitForConvergedLength(t, nodes, 8*time.Second)
	assertConverged(t, nodes, accounts)
}

// TestCommitPersistsEndorsingVotes checks the evidence a node serves to a
// lagging peer is actually stored, and is a quorum of valid votes for the block
// it accompanies. Without this, catch-up on an idle network would have nothing
// to prove a commit with.
func TestCommitPersistsEndorsingVotes(t *testing.T) {
	nodes, stop := syncCluster(t, 4)
	defer stop()

	alice := nodes[0].acct
	aliceID := alice.AccountID()
	mintAll(t, nodes, aliceID, 1000)
	tx := signedTransfer(t, alice, "recipient-0", 10, 0)
	for _, nd := range nodes {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
	waitForHeight(t, nodes, 1, 5*time.Second)

	quorum := nodes[0].engine.validators.Quorum()
	block, err := nodes[0].chain.BlockAt(0)
	if err != nil {
		t.Fatalf("block at 0: %v", err)
	}
	votes, err := nodes[0].chain.CommitVotes(0)
	if err != nil {
		t.Fatalf("commit votes: %v", err)
	}
	voters := make(map[string]struct{}, len(votes))
	for i := range votes {
		v := &votes[i]
		if err := v.Verify(); err != nil {
			t.Fatalf("stored vote %d does not verify: %v", i, err)
		}
		if v.Type != VoteTypePrecommit {
			t.Fatalf("stored vote %d is a %s; the commit certificate must be precommits", i, v.Type)
		}
		if !nodes[0].engine.validators.Contains(v.VoterID) {
			t.Fatalf("stored vote %d is not from a validator", i)
		}
		if v.Height != 0 {
			t.Fatalf("stored vote %d is for height %d, want 0", i, v.Height)
		}
		if !bytesEqual(v.BlockHash, block.Hash()) {
			t.Fatalf("stored vote %d endorses a different block", i)
		}
		voters[v.VoterID] = struct{}{}
	}
	if len(voters) < quorum {
		t.Fatalf("stored %d distinct endorsements, want at least the quorum of %d", len(voters), quorum)
	}
}

// TestSyncedBlockNeedsAQuorumToCommit is the safety half of block sync. A
// served body is not authority: it commits only when the votes that come with
// it reach the same quorum a proposed block needs. A peer that serves a
// genuine, fully valid block with too few votes must not move the chain.
func TestSyncedBlockNeedsAQuorumToCommit(t *testing.T) {
	nodes, stop := syncCluster(t, 4)
	defer stop()

	victim := nodes[3]
	const attackerID = peer.ID("attacker")
	// The victim hears nothing on the paths that would legitimately deliver the
	// block - including sync responses from its real peers, which would serve it
	// a genuine quorum. The only sync responses that reach it are the ones this
	// test injects, so what commits (or does not) is exactly what was injected.
	nodes[0].bus.setDeliveryFilter(func(from, to peer.ID, topic string) bool {
		if to != victim.peerID {
			return true
		}
		switch topic {
		case TopicProposal, TopicVote:
			return false
		case TopicSyncResponse:
			return from == attackerID
		}
		return true
	})

	alice := nodes[0].acct
	aliceID := alice.AccountID()
	mintAll(t, nodes, aliceID, 1000)
	tx := signedTransfer(t, alice, "recipient-0", 10, 0)
	for _, nd := range nodes[:3] {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
	waitForHeight(t, nodes[:3], 1, 5*time.Second)

	block, err := nodes[0].chain.BlockAt(0)
	if err != nil {
		t.Fatalf("block at 0: %v", err)
	}
	votes, err := nodes[0].chain.CommitVotes(0)
	if err != nil {
		t.Fatalf("commit votes: %v", err)
	}
	quorum := nodes[0].engine.validators.Quorum()
	if len(votes) < quorum {
		t.Fatalf("need at least %d stored votes to build the cases, have %d", quorum, len(votes))
	}

	ctx := context.Background()
	attacker := victim.bus.endpoint(attackerID)
	publish := func(t *testing.T, cb CommittedBlock) {
		t.Helper()
		data, err := json.Marshal(&BlockSyncResponse{Blocks: []CommittedBlock{cb}})
		if err != nil {
			t.Fatalf("marshal response: %v", err)
		}
		if err := attacker.Publish(ctx, TopicSyncResponse, data); err != nil {
			t.Fatalf("publish: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Case 1: the real block, one vote short of a quorum.
	publish(t, CommittedBlock{Block: *block, Votes: votes[:quorum-1]})
	if h := victim.engine.Height(); h != 0 {
		t.Fatalf("victim committed at height %d on %d votes, want to stay at 0 below the quorum of %d",
			h, quorum-1, quorum)
	}

	// Case 2: a quorum of votes that endorse a different block hash. Each vote is
	// individually genuine, so this is the case a naive count would accept.
	forged := make([]Vote, 0, len(votes))
	for i := range votes {
		v := votes[i]
		v.BlockHash = append([]byte(nil), v.BlockHash...)
		v.BlockHash[0] ^= 0xff
		forged = append(forged, v)
	}
	publish(t, CommittedBlock{Block: *block, Votes: forged})
	if h := victim.engine.Height(); h != 0 {
		t.Fatalf("victim committed at height %d on votes for another block, want to stay at 0", h)
	}

	// Case 3: the real block with its real quorum. Now it commits.
	publish(t, CommittedBlock{Block: *block, Votes: votes})
	waitForNodeHeight(t, victim, 1, 5*time.Second)

	committed, err := victim.chain.BlockAt(0)
	if err != nil {
		t.Fatalf("victim block at 0: %v", err)
	}
	if !bytesEqual(committed.Hash(), block.Hash()) {
		t.Fatalf("victim committed a different block than the network")
	}
}

// TestAcceptSyncedBlockRejectsBadBodies checks a served body is held to the
// same standard as a proposal: it must be for the height we are on, link to our
// head, and be signed by the validator who led its round.
func TestAcceptSyncedBlockRejectsBadBodies(t *testing.T) {
	nodes, stop := syncCluster(t, 4)
	defer stop()

	alice := nodes[0].acct
	aliceID := alice.AccountID()
	mintAll(t, nodes, aliceID, 1000)
	tx := signedTransfer(t, alice, "recipient-0", 10, 0)
	for _, nd := range nodes {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
	waitForHeight(t, nodes, 1, 5*time.Second)

	block, err := nodes[0].chain.BlockAt(0)
	if err != nil {
		t.Fatalf("block at 0: %v", err)
	}
	// Every node is now at height 1, so the committed height-0 block is history.
	eng := nodes[0].engine

	if err := eng.acceptSyncedBlock(block); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("accepting a block for a height we passed: err = %v, want ErrInvalidMessage", err)
	}

	// A body for the current height that does not link to our head.
	unlinked := *block
	unlinked.Height = 1
	unlinked.PrevBlockHash = make([]byte, HashSize)
	if err := eng.acceptSyncedBlock(&unlinked); !errors.Is(err, ErrPrevHashMismatch) {
		t.Fatalf("accepting an unlinked block: err = %v, want ErrPrevHashMismatch", err)
	}

	// A body attributed to a validator who does not lead its round.
	head, _, err := nodes[0].chain.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	notLeader := ""
	for _, id := range eng.validators.IDs() {
		if !eng.validators.IsLeader(id, block.Round) {
			notLeader = id
			break
		}
	}
	if notLeader == "" {
		t.Fatal("expected at least one validator that does not lead round 0")
	}
	wrongLeader := *block
	wrongLeader.Height = 1
	wrongLeader.PrevBlockHash = head
	wrongLeader.ProposerID = notLeader
	if err := eng.acceptSyncedBlock(&wrongLeader); !errors.Is(err, ErrWrongLeader) {
		t.Fatalf("accepting a block from a non-leader: err = %v, want ErrWrongLeader", err)
	}

	// Nothing above may have left a body cached.
	eng.mu.Lock()
	cached := len(eng.proposals)
	eng.mu.Unlock()
	if cached != 0 {
		t.Fatalf("%d rejected bodies were cached, want 0", cached)
	}
}

// TestHeightSurvivesVoteLoss covers a height that has to be carried across a
// round because the vote gossip failed mid-flight.
//
// The validators all see the round-0 proposal and prevote it, but none of their
// votes reach each other, so no quorum forms and the round times out with every
// node having voted for a block and no evidence that anyone else did. When vote
// gossip comes back, nothing about the cluster has changed: the same
// transaction is pending, the same nodes are up, the leader keeps rotating. The
// height must still commit.
//
// This used to be fatal. A validator locked on the block it voted for, and the
// next leader re-proposed that block by rewriting its round and proposer and
// re-signing it - which changed its hash, so every locked validator saw a
// different block and refused it, forever. The proposal envelope is what fixed
// that: a re-proposal now forwards the original block untouched.
func TestHeightSurvivesVoteLoss(t *testing.T) {
	nodes, stop := syncCluster(t, 4)
	defer stop()

	// Every node hears proposals but no votes.
	nodes[0].bus.setDeliveryFilter(func(_, _ peer.ID, topic string) bool {
		return topic != TopicVote
	})

	alice := nodes[0].acct
	aliceID := alice.AccountID()
	mintAll(t, nodes, aliceID, 1000)

	tx := signedTransfer(t, alice, "recipient-0", 10, 0)
	for _, nd := range nodes {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}

	// Wait until a quorum of nodes has prevoted a block and the round has
	// rotated, so the height is being carried by a re-proposal rather than by the
	// original one.
	quorum := nodes[0].engine.validators.Quorum()
	deadline := time.Now().Add(3 * time.Second)
	for {
		voted, rotated := 0, 0
		for _, nd := range nodes {
			nd.engine.mu.Lock()
			if _, ok := selfPrevotedBlock(nd.engine); ok {
				voted++
			}
			if nd.engine.round > 0 {
				rotated++
			}
			nd.engine.mu.Unlock()
		}
		if voted >= quorum && rotated >= quorum {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d nodes prevoted and %d rotated, want a quorum of %d for both",
				voted, rotated, quorum)
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Restore vote gossip. Nothing else changes.
	nodes[0].bus.setDeliveryFilter(nil)

	waitForHeight(t, nodes, 1, 8*time.Second)
	assertConverged(t, nodes, []string{aliceID, "recipient-0"})
}

// TestCompetingValuesConverge covers the state that used to deadlock a height
// permanently: every validator holding a different block.
//
// With proposal gossip cut between nodes, each validator only ever sees the
// block it proposed itself, in the round it led, and votes for that one. Four
// validators, four different values, no quorum anywhere. When proposal gossip
// returns, the height must commit.
//
// Under one-phase voting this was unrecoverable. A validator locked on the block
// it voted for and would only release that lock for a quorum, so with the votes
// split four ways no quorum could form, no lock could be released, and the
// cluster spun through rounds forever - observed at round 265 with three nodes
// each locked on a block only they held. Locking on a PRECOMMIT, which is only
// cast after a quorum of prevotes has been seen, makes that state unreachable:
// a validator holding a value nobody else saw is not locked on anything, so it
// is free to vote for whatever the next leader proposes.
func TestCompetingValuesConverge(t *testing.T) {
	nodes, stop := syncCluster(t, 4)
	defer stop()

	// Nobody sees anyone else's proposals.
	nodes[0].bus.setDeliveryFilter(func(from, to peer.ID, topic string) bool {
		return topic != TopicProposal || from == to
	})

	alice := nodes[0].acct
	aliceID := alice.AccountID()
	mintAll(t, nodes, aliceID, 1000)

	tx := signedTransfer(t, alice, "recipient-0", 10, 0)
	for _, nd := range nodes {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}

	// Wait for the split: a quorum of nodes has voted, for at least two different
	// blocks. No block can reach a quorum from here.
	quorum := nodes[0].engine.validators.Quorum()
	deadline := time.Now().Add(5 * time.Second)
	for {
		voted := 0
		hashes := map[string]struct{}{}
		for _, nd := range nodes {
			nd.engine.mu.Lock()
			if hkey, ok := selfPrevotedBlock(nd.engine); ok {
				voted++
				hashes[hkey] = struct{}{}
			}
			nd.engine.mu.Unlock()
		}
		real := len(hashes)
		if voted >= quorum && real >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d nodes voted on %d distinct blocks; the split this test needs did not form",
				voted, real)
		}
		time.Sleep(2 * time.Millisecond)
	}

	// No validator may be locked in this state: a lock requires a polka, and no
	// block has one. This is the property that makes the old deadlock impossible.
	for i, nd := range nodes {
		nd.engine.mu.Lock()
		locked, hash := nd.engine.locked, nd.engine.lockedHash
		nd.engine.mu.Unlock()
		if locked {
			t.Fatalf("node %d is locked on %.8s with no quorum in existence", i, hash)
		}
	}

	// Restore proposal delivery. Nothing else changes.
	nodes[0].bus.setDeliveryFilter(nil)

	waitForHeight(t, nodes, 1, 10*time.Second)
	assertConverged(t, nodes, []string{aliceID, "recipient-0"})
}

// selfPrevotedBlock reports the block (never the nil marker) this engine itself
// prevoted at the current height, in any round. Callers must hold e.mu.
func selfPrevotedBlock(e *Engine) (string, bool) {
	for _, byHash := range e.prevotes {
		for hkey, byVoter := range byHash {
			if isNilVoteHashKey(hkey) {
				continue
			}
			if _, ok := byVoter[e.selfID]; ok {
				return hkey, true
			}
		}
	}
	return "", false
}
