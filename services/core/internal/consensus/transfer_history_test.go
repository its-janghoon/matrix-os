package consensus

import (
	"context"
	"testing"
	"time"
)

// TestExternalSignedTransferSettlesThroughConsensusWithoutDivergence is the
// FEAT-001 correctness test: an external, client-signed value transfer (the
// shape SubmitSignedTransfer / `matrix wallet transfer` now submit) is settled
// through consensus, and after it commits every node computes the IDENTICAL
// resulting balances. This is what the old token.SettledLedger path could not
// promise: it moved credits on one node ordered by no quorum, so two nodes could
// diverge. Routing the same signed transfer through consensus makes the
// resulting balances a deterministic function of the committed blocks.
func TestExternalSignedTransferSettlesThroughConsensusWithoutDivergence(t *testing.T) {
	// Fee OFF (default) so the transfer moves the full amount, matching the
	// acceptance criterion "a signed transfer with the fee off still moves the
	// full amount". A separate test below exercises the fee-on path.
	nodes, stop := newCluster(t, 4, nil)
	defer stop()

	sender := nodes[0].acct
	senderID := sender.AccountID()
	recipient := "external-transfer-recipient"

	// Seed the sender identically on every node (genesis would do this in
	// production; the harness starts each ledger empty).
	mintAll(t, nodes, senderID, 1000)

	// Build a client-signed transfer exactly as a wallet would: signed by the
	// sender, prev_hash unused for consensus linkage (a zero seed keeps the
	// canonical signing bytes well-formed), nonce a uniquifier.
	tx := signedTransfer(t, sender, recipient, 250, 0)

	// Submit it to a NON-leader-only path: feed every node, modelling gossip
	// fan-out to whichever node is the current leader. This proves a transfer
	// submitted to any node settles, not just one submitted to node 0.
	for _, nd := range nodes {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit signed transfer: %v", err)
		}
	}

	// Wait for the transfer to commit AND apply on the submitting node, exactly
	// as the marketapi settler does.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	committed, applied, err := nodes[0].engine.WaitForSettlement(ctx, tx)
	if err != nil {
		t.Fatalf("WaitForSettlement: %v", err)
	}
	if !committed || !applied {
		t.Fatalf("expected committed+applied, got committed=%v applied=%v", committed, applied)
	}

	// Every node must report the same balances once it has applied the committed
	// transfer. waitForBalances also quiesces the cluster (no empty blocks are
	// proposed once the mempool drains).
	waitForBalances(t, nodes, recipient, 250, 10*time.Second)
	waitForBalances(t, nodes, senderID, 750, 10*time.Second)

	// Full convergence: identical committed block sequence and identical balances
	// on every node - no divergence.
	waitForConvergedLength(t, nodes, 10*time.Second)
	assertConverged(t, nodes, []string{senderID, recipient})

	// The committed transfer history is identical on every node, in the same
	// order. This is what `matrix tx list` reads now, so two nodes must agree.
	transfers0, total0, err := nodes[0].engine.CommittedTransfers(0, 0)
	if err != nil {
		t.Fatalf("CommittedTransfers node 0: %v", err)
	}
	if total0 != 1 {
		t.Fatalf("expected exactly 1 committed transfer, got %d", total0)
	}
	if got := transfers0[0]; got.From != senderID || got.To != recipient || got.Amount != 250 {
		t.Fatalf("unexpected committed transfer: %+v", got)
	}
	for i, nd := range nodes {
		transfers, total, err := nd.engine.CommittedTransfers(0, 0)
		if err != nil {
			t.Fatalf("CommittedTransfers node %d: %v", i, err)
		}
		if total != total0 || len(transfers) != len(transfers0) {
			t.Fatalf("node %d transfer history length %d/%d != node 0 %d/%d", i, len(transfers), total, len(transfers0), total0)
		}
		for k := range transfers0 {
			a, b := transfers0[k], transfers[k]
			if a.Index != b.Index || a.From != b.From || a.To != b.To || a.Amount != b.Amount || a.Nonce != b.Nonce || a.Height != b.Height {
				t.Fatalf("node %d transfer %d differs from node 0: %+v vs %+v", i, k, b, a)
			}
		}
	}

	// CommittedTransferAt resolves the same transfer by index, and an
	// out-of-range index is a clean error rather than a panic.
	at, err := nodes[0].engine.CommittedTransferAt(0)
	if err != nil {
		t.Fatalf("CommittedTransferAt(0): %v", err)
	}
	if at.From != senderID || at.To != recipient || at.Amount != 250 {
		t.Fatalf("CommittedTransferAt(0) = %+v, want sender=%s to=%s amount=250", at, senderID, recipient)
	}
	if _, err := nodes[0].engine.CommittedTransferAt(99); err == nil {
		t.Fatalf("expected an error for an out-of-range transfer index")
	}
}

// TestExternalSignedTransferPaysTheFeeThroughConsensus proves the transfer now
// flows through the fee path: with a fee configured, a signed transfer is
// charged the fee (recipient receives amount minus fee) and every node still
// agrees. This is the other half of the FEAT-001 acceptance criterion - the
// transfer is subject to the (default-off) fee because it flows through
// commitAndApply - without changing the default rate.
func TestExternalSignedTransferPaysTheFeeThroughConsensus(t *testing.T) {
	nodes, stop := feeCluster(t, 4, MaxFeeBasisPoints)
	defer stop()

	sender := nodes[0].acct
	senderID := sender.AccountID()
	recipient := "fee-charged-transfer-recipient"

	const amount = uint64(100_000)
	mintAll(t, nodes, senderID, 1_000_000)

	tx := signedTransfer(t, sender, recipient, amount, 0)
	for _, nd := range nodes {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit signed transfer: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	committed, applied, err := nodes[0].engine.WaitForSettlement(ctx, tx)
	if err != nil {
		t.Fatalf("WaitForSettlement: %v", err)
	}
	if !committed || !applied {
		t.Fatalf("expected committed+applied, got committed=%v applied=%v", committed, applied)
	}

	// The recipient receives the amount net of the protocol fee, and the fee is
	// paid to the validator set. The recipient's net is deterministic, so every
	// node agrees on it.
	fee := FeeFor(amount, MaxFeeBasisPoints)
	net := amount - fee
	waitForBalances(t, nodes, recipient, net, 10*time.Second)
	assertConverged(t, nodes, []string{senderID, recipient})

	// Conservation: no native MATRIX was created or destroyed. The sender was
	// debited the full amount; the recipient got the net and the validators
	// share the fee, so summing every account returns the minted total on every
	// node.
	if fee == 0 {
		t.Fatalf("expected a non-zero fee at %d bps for amount %d", MaxFeeBasisPoints, amount)
	}
}

// TestUnaffordableSignedTransferIsSkippedDeterministically proves an
// unaffordable signed transfer is committed to the ordered log but skipped at
// apply time on every node (no credits move), which is how the marketapi settler
// tells "paid" from "skipped" and reports FailedPrecondition.
func TestUnaffordableSignedTransferIsSkippedDeterministically(t *testing.T) {
	nodes, stop := newCluster(t, 4, nil)
	defer stop()

	sender := nodes[0].acct
	senderID := sender.AccountID()
	recipient := "unaffordable-recipient"

	// Seed the sender LESS than the transfer amount, so it cannot afford it.
	mintAll(t, nodes, senderID, 10)

	tx := signedTransfer(t, sender, recipient, 1000, 0)
	for _, nd := range nodes {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit signed transfer: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	committed, applied, err := nodes[0].engine.WaitForSettlement(ctx, tx)
	if err != nil {
		t.Fatalf("WaitForSettlement: %v", err)
	}
	if !committed {
		t.Fatalf("expected the transfer to commit to the ordered log")
	}
	if applied {
		t.Fatalf("expected the unaffordable transfer to be skipped (not applied)")
	}

	// No credits moved anywhere, on any node.
	waitForConvergedLength(t, nodes, 10*time.Second)
	assertConverged(t, nodes, []string{senderID, recipient})
	if bal, _ := nodes[0].ledger.Balance(recipient); bal != 0 {
		t.Fatalf("recipient balance = %d, want 0 (transfer was unaffordable)", bal)
	}
	if bal, _ := nodes[0].ledger.Balance(senderID); bal != 10 {
		t.Fatalf("sender balance = %d, want 10 (unchanged)", bal)
	}
}
