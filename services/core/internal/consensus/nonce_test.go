package consensus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// TestTwoTransfersAtOneNonceCannotBothCommit is the regression guard for a
// double payment found by running two hosts.
//
// A client asks the node for its next nonce, signs, and submits. If it submits
// again before the first transfer commits, the node reports the SAME next nonce,
// so the client signs a second, different transfer at it. The dedup key includes
// the signature, so the two were different keys; block verification checked only
// that key; and commitAndApply checked only affordability. Both committed and
// both moved money.
//
// Observed on two hosts as history index 10 and 11 both carrying nonce 10, with
// both recipients credited 50 - a sender who meant to pay once paid twice.
func TestTwoTransfersAtOneNonceCannotBothCommit(t *testing.T) {
	nodes, stop := newCluster(t, 4, nil)
	defer stop()

	sender := nodes[0].acct
	senderID := sender.AccountID()
	mintAll(t, nodes, senderID, 1000)

	first := signedTransfer(t, sender, "paid-once", 50, 7)
	// A DIFFERENT transfer at the same nonce, which is exactly what a client
	// produces when it re-reads a next-nonce that has not moved yet.
	second := signedTransfer(t, sender, "paid-twice", 50, 7)

	for _, nd := range nodes {
		if err := nd.engine.Submit(first); err != nil {
			t.Fatalf("submit the first transfer: %v", err)
		}
	}

	// The second must be refused, and refused LOUDLY: a silent nil here is what
	// let the caller believe one payment had been made when two had.
	for i, nd := range nodes {
		err := nd.engine.Submit(second)
		if err == nil {
			t.Fatalf("node %d accepted a second transfer at nonce 7; it would pay twice", i)
		}
		if !errors.Is(err, ErrNonceAlreadyUsed) {
			t.Fatalf("node %d: error = %v, want ErrNonceAlreadyUsed", i, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	committed, applied, err := nodes[0].engine.WaitForSettlement(ctx, first)
	if err != nil {
		t.Fatalf("WaitForSettlement: %v", err)
	}
	if !committed || !applied {
		t.Fatalf("the first transfer should still settle: committed=%v applied=%v", committed, applied)
	}

	// One recipient paid, the other not, on every node.
	waitForBalances(t, nodes, "paid-once", 50, 5*time.Second)
	for i, nd := range nodes {
		bal, err := nd.ledger.Balance("paid-twice")
		if err != nil {
			t.Fatalf("balance: %v", err)
		}
		if bal != 0 {
			t.Fatalf("node %d credited the second recipient %d; one nonce authorized two payments", i, bal)
		}
	}
}

// TestASpentNonceIsRefusedAfterItCommits covers the other half: once a transfer
// has committed, a different transfer at that nonce must be refused rather than
// treated as a fresh payment.
func TestASpentNonceIsRefusedAfterItCommits(t *testing.T) {
	nodes, stop := newCluster(t, 4, nil)
	defer stop()

	sender := nodes[0].acct
	mintAll(t, nodes, sender.AccountID(), 1000)

	first := signedTransfer(t, sender, "recipient-one", 50, 0)
	for _, nd := range nodes {
		if err := nd.engine.Submit(first); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, applied, err := nodes[0].engine.WaitForSettlement(ctx, first); err != nil || !applied {
		t.Fatalf("first transfer did not settle: applied=%v err=%v", applied, err)
	}

	// Same signed transaction again: idempotent, still a no-op, still no error.
	// A caller re-submitting after a timeout must not be told it did something
	// wrong.
	if err := nodes[0].engine.Submit(first); err != nil {
		t.Fatalf("re-submitting the identical transaction should stay idempotent, got %v", err)
	}

	// A different transfer at the spent nonce is refused.
	second := signedTransfer(t, sender, "recipient-two", 50, 0)
	err := nodes[0].engine.Submit(second)
	if !errors.Is(err, ErrNonceAlreadyUsed) {
		t.Fatalf("error = %v, want ErrNonceAlreadyUsed", err)
	}
}

// TestAMaliciousLeaderCannotSmuggleTwoSameNonceTransfers checks the block-level
// guard directly. Submit refuses these at the door, so an honest leader cannot
// build such a block - but a leader that ignores its own mempool rules can, and
// honest validators must refuse to vote for it.
func TestAMaliciousLeaderCannotSmuggleTwoSameNonceTransfers(t *testing.T) {
	nodes, stop := newCluster(t, 4, nil)
	defer stop()

	nd := nodes[0]
	sender := nd.acct
	mintAll(t, nodes, sender.AccountID(), 1000)

	first := signedTransfer(t, sender, "block-one", 50, 3)
	second := signedTransfer(t, sender, "block-two", 50, 3)

	nd.engine.mu.Lock()
	height, prev := nd.engine.height, nd.engine.headHash
	round := nd.engine.round
	leader := nd.engine.vset().LeaderFor(height, round)
	nd.engine.mu.Unlock()
	if leader != nd.acct.AccountID() {
		// Find the node that does lead this height/round and use it, so the
		// block fails on the NONCE rule and not on the leader rule.
		for _, other := range nodes {
			if other.acct.AccountID() == leader {
				nd = other
				break
			}
		}
	}

	b := &Block{
		Height:        height,
		Round:         round,
		PrevBlockHash: prev,
		ProposerID:    nd.acct.AccountID(),
		Txs:           []token.Transaction{*first, *second},
		Timestamp:     time.Now().Unix(),
	}
	if err := b.Sign(nd.acct.PrivateKey); err != nil {
		t.Fatalf("sign block: %v", err)
	}

	nd.engine.mu.Lock()
	err := nd.engine.verifyBlockForHeightLocked(b)
	nd.engine.mu.Unlock()
	if err == nil {
		t.Fatal("a block carrying two transfers at one nonce was accepted")
	}
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("error = %v, want ErrInvalidMessage", err)
	}
}
