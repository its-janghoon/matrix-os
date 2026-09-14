package consensus

import (
	"context"
	"errors"
	"fmt"
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
		Version:       ProtocolVersionGenesis,
		StateRoot:     engineStateRoot(t, nd.engine),
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

// TestReservedRecipientTransfersStillAdvanceTheNonce pins the split that made
// two bridge locks collide.
//
// CommittedTransfers deliberately OMITS reserved recipients - bonds, stake
// withdrawals, bridge locks are protocol state rather than user payments - and
// that is correct for a transaction list somebody reads. It is wrong as the
// basis for a nonce, which is what the CLI used it for: an account whose only
// activity is locking into the bridge saw a count that never advanced, signed
// everything at one nonce, and produced one lock id for every lock. The second
// overwrote the first's record and the native it had escrowed became unmintable.
//
// So: the history hides them, and the nonce counts them.
func TestReservedRecipientTransfersStillAdvanceTheNonce(t *testing.T) {
	nodes, stop := newCluster(t, 4, nil)
	defer stop()

	sender := nodes[0].acct
	senderID := sender.AccountID()
	// Enough for the bridge's anti-dust floor, which a lock below is refused for.
	mintAll(t, nodes, senderID, 500_000_000_000)

	before := nodes[0].engine.NextNonce(senderID, true)

	// A bridge lock: an ordinary signed transfer to a RESERVED recipient.
	var addr [20]byte
	addr[19] = 0x2a
	lock := signedTransfer(t, sender, BridgeLockRecipient(addr), 100_000_000_000, before)
	for _, nd := range nodes {
		if err := nd.engine.Submit(lock); err != nil {
			t.Fatalf("submit the lock: %v", err)
		}
	}
	waitForOrReport(t, 10*time.Second, "the lock to advance the sender's nonce",
		func() bool { return nodes[0].engine.NextNonce(senderID, true) > before },
		func() string {
			return fmt.Sprintf("nonce is still %d", nodes[0].engine.NextNonce(senderID, true))
		})

	after := nodes[0].engine.NextNonce(senderID, true)
	if after <= before {
		t.Fatalf("nonce did not advance across a reserved-recipient transfer: %d then %d.\n"+
			"Every lock would derive the same id and the second would overwrite the first.", before, after)
	}

	// And the same transfer stays out of the readable history, which is the
	// behaviour that made counting it the wrong way to get here.
	transfers, _, err := nodes[0].engine.CommittedTransfers(0, 0)
	if err != nil {
		t.Fatalf("committed transfers: %v", err)
	}
	for _, tr := range transfers {
		if IsReservedRecipient(tr.To) {
			t.Fatalf("a reserved recipient leaked into the transaction history: %+v", tr)
		}
	}
}
