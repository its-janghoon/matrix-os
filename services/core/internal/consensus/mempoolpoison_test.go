package consensus

import (
	"context"
	"testing"
	"time"
)

// TestAMempoolPoisonTransactionDoesNotHaltTheChain
//
// THE HYPOTHESIS. Engine.Submit accepts a transaction after checking only the
// signature and a non-empty recipient. Block validation is much stricter: a
// transfer to a reserved recipient must carry no value, its sender must be a
// validator, and so on. Anything in that gap gets into the mempool and can
// never be in a valid block.
//
// If a leader includes it, every honest validator refuses the block. The
// mempool only drops transactions that COMMIT (or are explicitly pruned), so
// the poison stays, every subsequent leader draws it again, and every block is
// refused. One signed transaction from any account with no privileges would
// halt the chain for good.
//
// This test decides whether that is real. It submits such a transaction to
// every node and then asks whether an ordinary transfer can still commit.
func TestAMempoolPoisonTransactionDoesNotHaltTheChain(t *testing.T) {
	poisons := map[string]func(nd *testNode) string{
		// Value to a provider-registry recipient: verifyProviderChangeLocked
		// rejects any amount != 0.
		"value to a provider change recipient": func(*testNode) string {
			return ProviderChange{Kind: ProviderChangeAdd, ProviderID: "poison"}.Recipient()
		},
		// Value to a burn-unlock recipient: verifyBurnUnlockLocked rejects any
		// amount != 0.
		"value to a burn unlock recipient": func(nd *testNode) string {
			return BurnUnlock{
				BurnIDHash:   "1e0c5f1a3d4b6e7c8a9b0c1d2e3f40516273849506172839405162738495a6b7",
				ToAccount:    nd.acct.AccountID(),
				NativeAmount: 1,
			}.Recipient()
		},
		// A malformed burn unlock: ParseBurnUnlock rejects it outright.
		"malformed burn unlock recipient": func(*testNode) string {
			return burnUnlockPrefix + "not-a-burn"
		},
	}

	// And the case the first version of the fix got wrong: a BOND legitimately
	// carries value, and refusing value to every reserved recipient broke
	// bonding outright (six stake tests). Kept here so the distinction cannot be
	// lost again while tightening the rule.
	t.Run("a bond still carries value", func(t *testing.T) {
		nodes, stop := newCluster(t, 4, nil)
		defer stop()
		sender := nodes[0].acct
		mintAll(t, nodes, sender.AccountID(), 10_000)

		bond := signedTransfer(t, sender, BondAccount(sender.AccountID()), 500, 0)
		if err := nodes[0].engine.Submit(bond); err != nil {
			t.Fatalf("a bond carrying value was refused: %v", err)
		}
	})

	for name, recipient := range poisons {
		t.Run(name, func(t *testing.T) {
			nodes, stop := newCluster(t, 4, nil)
			defer stop()

			sender := nodes[0].acct
			mintAll(t, nodes, sender.AccountID(), 10_000)

			// The poison. A signed, well-formed transaction that no valid block
			// can contain. Submitted to every node, as gossip would spread it.
			poison := signedTransfer(t, sender, recipient(nodes[0]), 500, 0)
			for _, nd := range nodes {
				// Submit must refuse it: it can never be in a valid block.
				if err := nd.engine.Submit(poison); err == nil {
					t.Fatal("Submit accepted a transaction no valid block could contain")
				}
			}

			// Now the layer that actually stops the halt. Submit is a door, not
			// the defense: a node could acquire this some other way - a future
			// gossip path, a peer running an older build, a malicious peer - so
			// put it straight into the mempool the way such a node would hold
			// it, and require the chain to keep working anyway.
			for _, nd := range nodes {
				nd.engine.mu.Lock()
				nd.engine.mempool = append(nd.engine.mempool, *poison)
				nd.engine.mempoolSet[mempoolKey(poison)] = struct{}{}
				nd.engine.mu.Unlock()
			}

			// Now an ordinary transfer that must commit. If the poison halted
			// the chain, this never settles.
			good := signedTransfer(t, sender, "still-working", 100, 1)
			for _, nd := range nodes {
				if err := nd.engine.Submit(good); err != nil {
					t.Fatalf("submit the good transfer: %v", err)
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			committed, applied, err := nodes[0].engine.WaitForSettlement(ctx, good)
			if err != nil {
				t.Fatalf("the chain stopped committing after one poison transaction "+
					"submitted by an account with no privileges: %v", err)
			}
			if !committed || !applied {
				t.Fatalf("an ordinary transfer did not settle after the poison: committed=%v applied=%v",
					committed, applied)
			}
			waitForBalances(t, nodes, "still-working", 100, 5*time.Second)
		})
	}
}
