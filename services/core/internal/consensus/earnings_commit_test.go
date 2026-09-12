package consensus

import (
	"testing"
	"time"
)

// TestOnlyAnAppliedTransferCountsAsEarnings is the property the whole tally
// rests on, and the one that is easy to get wrong by reading the block.
//
// A transaction in a committed block has NOT necessarily moved anything. Every
// node commits the ordered log and then applies it, and a transfer whose sender
// cannot afford it at that height is deterministically SKIPPED - it stays in the
// block forever, and no coins move. A tally built by walking blocks would credit
// a seller for money it was never paid, and a seller could manufacture a track
// record out of transfers it knew would bounce: sign a thousand payments from an
// empty account, and every one appears in a block.
//
// So the tally is written from the apply path, where whether it moved is known.
func TestOnlyAnAppliedTransferCountsAsEarnings(t *testing.T) {
	nodes, cancel := newCluster(t, 3, nil)
	defer cancel()
	for _, nd := range nodes {
		nd.engine.Start(t.Context())
	}

	payer := nodes[0].acct
	const seller = "eth:0x00000000000000000000000000000000000000aa"

	// A payer who can afford exactly one of the two transfers below.
	mintAll(t, nodes, payer.AccountID(), 1_000)

	afford := signedTransfer(t, payer, seller, 600, 1)
	// More than the whole balance, so it commits and is skipped at apply.
	bounce := signedTransfer(t, payer, seller, 5_000, 2)
	for _, nd := range nodes {
		if err := nd.engine.Submit(afford); err != nil {
			t.Fatalf("submit affordable: %v", err)
		}
		if err := nd.engine.Submit(bounce); err != nil {
			t.Fatalf("submit unaffordable: %v", err)
		}
	}

	waitFor(t, 10*time.Second, "the affordable transfer to be tallied", func() bool {
		got, _, err := nodes[0].engine.Earnings(seller)
		return err == nil && got.Payments > 0
	})

	got, _, err := nodes[0].engine.Earnings(seller)
	if err != nil {
		t.Fatalf("Earnings: %v", err)
	}
	if got.Payments != 1 {
		t.Fatalf("Payments = %d, want exactly 1 - the unaffordable transfer was counted", got.Payments)
	}
	// Net of the protocol fee: what the seller actually got, which on a cluster
	// with no fee configured is the whole amount.
	if got.Received == 0 || got.Received > 600 {
		t.Fatalf("Received = %d, want at most the 600 that moved", got.Received)
	}
	if got.Payers != 1 {
		t.Fatalf("Payers = %d, want 1", got.Payers)
	}
}

// TestEveryNodeTalliesTheSameEarnings is why a buyer can ask its OWN node.
//
// The figures are only worth showing because the seller has no say in them, and
// that holds only if every node computes the same answer from the same blocks. If
// they diverged, a seller could shop for the node that flattered it and the
// number would be a claim again, just laundered through someone else's RPC.
func TestEveryNodeTalliesTheSameEarnings(t *testing.T) {
	nodes, cancel := newCluster(t, 3, nil)
	defer cancel()
	for _, nd := range nodes {
		nd.engine.Start(t.Context())
	}

	const seller = "eth:0x00000000000000000000000000000000000000bb"
	payer := nodes[0].acct
	mintAll(t, nodes, payer.AccountID(), 10_000)

	for i := uint64(1); i <= 3; i++ {
		tx := signedTransfer(t, payer, seller, 100, i)
		for _, nd := range nodes {
			if err := nd.engine.Submit(tx); err != nil {
				t.Fatalf("submit: %v", err)
			}
		}
	}

	waitFor(t, 10*time.Second, "all three nodes to tally all three payments", func() bool {
		for _, nd := range nodes {
			got, _, err := nd.engine.Earnings(seller)
			if err != nil || got.Payments != 3 {
				return false
			}
		}
		return true
	})

	want, _, err := nodes[0].engine.Earnings(seller)
	if err != nil {
		t.Fatalf("Earnings: %v", err)
	}
	for i, nd := range nodes[1:] {
		got, _, err := nd.engine.Earnings(seller)
		if err != nil {
			t.Fatalf("Earnings on node %d: %v", i+1, err)
		}
		if got != want {
			t.Fatalf("node %d tallied %+v, node 0 tallied %+v - a seller could shop for the kinder node",
				i+1, got, want)
		}
	}
}

// TestBondingIsNotEarning. A bond reaches the ordinary-transfer path on purpose,
// so it gets the same affordability check as any transfer. It is an account
// moving its own coins into its own bond, and counting it would let a validator
// manufacture a sales record by bonding - the one payment that costs nothing
// because the money stays theirs.
func TestBondingIsNotEarning(t *testing.T) {
	nodes, cancel := newCluster(t, 3, nil)
	defer cancel()
	for _, nd := range nodes {
		nd.engine.Start(t.Context())
	}

	bonder := nodes[0].acct
	mintAll(t, nodes, bonder.AccountID(), 10_000)

	tx, err := nodes[0].engine.SubmitBond(bonder, 5_000, 1)
	if err != nil {
		t.Fatalf("SubmitBond: %v", err)
	}
	for _, nd := range nodes[1:] {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit bond: %v", err)
		}
	}

	waitFor(t, 10*time.Second, "the bond to be applied", func() bool {
		bonded, err := nodes[0].engine.BondedStake(bonder.AccountID())
		return err == nil && bonded > 0
	})

	// The bond account itself, and the bonder, must both be untouched by the
	// tally: neither was paid by anyone.
	for _, account := range []string{BondAccount(bonder.AccountID()), bonder.AccountID()} {
		got, _, err := nodes[0].engine.Earnings(account)
		if err != nil {
			t.Fatalf("Earnings(%s): %v", account, err)
		}
		if got.Payments != 0 {
			t.Fatalf("%s was tallied %d payments for a bond; bonding is not a sale", account, got.Payments)
		}
	}
}
