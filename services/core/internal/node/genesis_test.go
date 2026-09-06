package node

import (
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// applyConfiguredGenesis reproduces the exact genesis-application logic that
// Node.Start runs: it translates the node's GenesisConfig into token
// allocations and calls Treasury.ApplyGenesis. Testing it against a persistent
// store (reopened between calls to simulate a node restart) proves genesis is
// applied exactly once and never double-credits, without standing up the full
// libp2p node.
func applyConfiguredGenesis(t *testing.T, treasury *token.Treasury, cfg GenesisConfig) {
	t.Helper()
	allocations := make([]token.GenesisAllocation, 0, len(cfg.Allocations))
	for _, a := range cfg.Allocations {
		allocations = append(allocations, token.GenesisAllocation{Account: a.Account, Amount: a.Amount})
	}
	if err := treasury.ApplyGenesis(allocations, cfg.RewardPool); err != nil {
		t.Fatalf("ApplyGenesis: %v", err)
	}
}

// TestGenesis_AppliedOnceIdempotentAcrossRestart proves the node's genesis path
// credits allocations and the reward pool exactly once and is a no-op on a
// simulated restart (the store is reopened and genesis re-applied), so balances
// are never double-credited and issued supply is stable.
func TestGenesis_AppliedOnceIdempotentAcrossRestart(t *testing.T) {
	dir := t.TempDir()

	const (
		allocAmount = uint64(500_000)
		rewardPool  = uint64(10_000_000)
	)
	cfg := GenesisConfig{
		Allocations: []GenesisAllocationConfig{{Account: "genesis/treasury", Amount: allocAmount}},
		RewardPool:  rewardPool,
	}

	// First boot: open the store, apply genesis, assert balances + supply, close.
	func() {
		store, err := kv.New(kv.Config{Path: dir})
		if err != nil {
			t.Fatalf("kv.New (boot 1): %v", err)
		}
		defer func() { _ = store.Close() }()

		mkt, err := market.NewMarket(store)
		if err != nil {
			t.Fatalf("market.NewMarket: %v", err)
		}
		treasury := token.NewTreasury(mkt.Ledger(), store)

		applied, err := treasury.GenesisApplied()
		if err != nil {
			t.Fatalf("GenesisApplied: %v", err)
		}
		if applied {
			t.Fatal("genesis unexpectedly already applied on a fresh store")
		}

		applyConfiguredGenesis(t, treasury, cfg)

		assertBalance(t, mkt, "genesis/treasury", allocAmount)
		assertBalance(t, mkt, token.RewardPoolAccount, rewardPool)
		assertIssuedSupply(t, treasury, allocAmount+rewardPool)
	}()

	// Second boot: reopen the SAME store and re-apply genesis (as Start does
	// unconditionally). It must be a no-op: balances and supply are unchanged.
	func() {
		store, err := kv.New(kv.Config{Path: dir})
		if err != nil {
			t.Fatalf("kv.New (boot 2): %v", err)
		}
		defer func() { _ = store.Close() }()

		mkt, err := market.NewMarket(store)
		if err != nil {
			t.Fatalf("market.NewMarket: %v", err)
		}
		treasury := token.NewTreasury(mkt.Ledger(), store)

		applied, err := treasury.GenesisApplied()
		if err != nil {
			t.Fatalf("GenesisApplied (boot 2): %v", err)
		}
		if !applied {
			t.Fatal("genesis marker did not persist across restart")
		}

		// Re-apply: idempotent no-op.
		applyConfiguredGenesis(t, treasury, cfg)

		assertBalance(t, mkt, "genesis/treasury", allocAmount)
		assertBalance(t, mkt, token.RewardPoolAccount, rewardPool)
		assertIssuedSupply(t, treasury, allocAmount+rewardPool)
	}()
}

// TestGenesis_DefaultConfigRewardPoolIsFullCapUnderLimit asserts the default
// config that `matrixd --init` writes yields a reward pool exactly at the native
// cap and applies cleanly (nothing exceeds NativeMaxSupply).
func TestGenesis_DefaultConfigRewardPoolIsFullCap(t *testing.T) {
	// Mirror node.Initialize's default genesis.
	cfg := GenesisConfig{RewardPool: token.NativeMaxSupply}

	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}
	treasury := token.NewTreasury(mkt.Ledger(), store)

	applyConfiguredGenesis(t, treasury, cfg)

	assertBalance(t, mkt, token.RewardPoolAccount, token.NativeMaxSupply)
	assertIssuedSupply(t, treasury, token.NativeMaxSupply)
}

func assertBalance(t *testing.T, mkt *market.Market, account string, want uint64) {
	t.Helper()
	got, err := mkt.Ledger().Balance(account)
	if err != nil {
		t.Fatalf("Balance(%q): %v", account, err)
	}
	if got != want {
		t.Fatalf("Balance(%q) = %d, want %d", account, got, want)
	}
}

func assertIssuedSupply(t *testing.T, treasury *token.Treasury, want uint64) {
	t.Helper()
	got, err := treasury.IssuedSupply()
	if err != nil {
		t.Fatalf("IssuedSupply: %v", err)
	}
	if got != want {
		t.Fatalf("IssuedSupply = %d, want %d", got, want)
	}
}
