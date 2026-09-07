package node

import (
	"context"
	"crypto/ed25519"
	"path/filepath"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// TestInitialize_GeneratedConsensusMonetaryPolicy pins the monetary policy a
// generated config (matrixd -init) ships with to the operator's (CTO's) explicit
// decisions: a 100 basis-point protocol fee, the recommended provider-emission
// schedule (280,000,000,000 base units per block, halving every 1,000,000
// blocks), and bonded stake OFF (permissioned for the first release).
//
// These live in the generated config, not in the engine defaults: the point of
// TestConsensusNew_ZeroValueIsFeeAndEmissionFree below is that consensus.New with
// an all-zero monetary config still runs fee-free and emission-free, so this test
// is about what Initialize writes for a fresh node, not what the protocol forces.
func TestInitialize_GeneratedConsensusMonetaryPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Initialize(path); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	n, err := New(context.Background(), path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cfg := n.config

	if got := cfg.Consensus.FeeBasisPoints; got != 100 {
		t.Errorf("generated fee_basis_points = %d, want 100", got)
	}
	if got := cfg.Consensus.FeeBasisPoints; got != consensus.MaxFeeBasisPoints {
		t.Errorf("generated fee_basis_points = %d, want the code cap consensus.MaxFeeBasisPoints (%d)",
			got, consensus.MaxFeeBasisPoints)
	}
	if got := cfg.Consensus.Rewards.PerBlock; got != 280_000_000_000 {
		t.Errorf("generated rewards.per_block = %d, want 280000000000", got)
	}
	if got := cfg.Consensus.Rewards.HalfLife; got != 1_000_000 {
		t.Errorf("generated rewards.half_life = %d, want 1000000", got)
	}
	if cfg.Consensus.Stake.Enabled {
		t.Error("generated stake.enabled = true, want false (permissioned first release)")
	}
	// The registry is deliberately empty: a registration needs a quorum of
	// operators, so the armed emission pays nothing until this operator adds a
	// provider. A generated config that pre-listed providers would be deciding
	// who earns on the operator's behalf.
	if len(cfg.Consensus.Rewards.ApprovedProviders) != 0 {
		t.Errorf("generated rewards.approved_providers = %v, want empty", cfg.Consensus.Rewards.ApprovedProviders)
	}
}

// TestGeneratedProviderEmissionMatchesProposalAllocation checks the recommended
// per-block figure really does target ~40% of the native supply cap over its
// half-life, so the documented arithmetic in node.go stays honest if someone
// edits the constant.
//
// The total ever paid by a halving (right-shift) schedule is about
// 1.44 * PerBlock * HalfLife. We assert that lands within a couple percent of
// 40% of token.NativeMaxSupply.
func TestGeneratedProviderEmissionMatchesProposalAllocation(t *testing.T) {
	const (
		perBlock = uint64(280_000_000_000)
		halfLife = consensus.DefaultProviderEmissionHalfLife // 1,000,000
	)
	if recommendedProviderEmissionPerBlock != perBlock {
		t.Fatalf("recommendedProviderEmissionPerBlock = %d, want %d", recommendedProviderEmissionPerBlock, perBlock)
	}
	// 1.44 ~= 1 / ln(2), the sum of a halving series relative to its first term.
	totalPaid := 1.44 * float64(perBlock) * float64(halfLife)
	target := 0.40 * float64(token.NativeMaxSupply)

	ratio := totalPaid / target
	if ratio < 0.98 || ratio > 1.02 {
		t.Fatalf("total emission ~= %.3e base units is %.1f%% of the 40%%-of-cap target (%.3e); "+
			"expected within 2%%", totalPaid, ratio*100, target)
	}
}

// TestConsensusNew_ZeroValueIsFeeAndEmissionFree guards the hard constraint that
// the ENGINE's zero-value monetary defaults are unchanged: an engine built with
// the required plumbing but an all-zero monetary config (no FeeBasisPoints, no
// ProviderEmissionPerBlock) must charge no fee and emit nothing. Only the
// generated config reflects the operator's chosen policy; the protocol itself
// stays neutral.
func TestConsensusNew_ZeroValueIsFeeAndEmissionFree(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}
	validator, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	vs, err := consensus.NewValidatorSet([]ed25519.PublicKey{validator.PublicKey})
	if err != nil {
		t.Fatalf("NewValidatorSet: %v", err)
	}
	// Only the plumbing consensus.New requires; every monetary field left zero.
	eng, err := consensus.New(consensus.Config{
		Transport:  newMemBus(),
		Validators: vs,
		Chain:      consensus.NewBlockChain(store),
		Ledger:     mkt.Ledger(),
		Self:       validator,
	})
	if err != nil {
		t.Fatalf("consensus.New(zero monetary config): %v", err)
	}
	if got := eng.FeeBasisPoints(); got != 0 {
		t.Errorf("zero-value engine FeeBasisPoints() = %d, want 0 (fee-free by default)", got)
	}
	// Emission at any height must be zero when no per-block budget is configured.
	for _, h := range []uint64{1, 100, 1_000_000} {
		if got := eng.ProviderEmissionAt(h); got != 0 {
			t.Errorf("zero-value engine ProviderEmissionAt(%d) = %d, want 0 (emission-free by default)", h, got)
		}
	}
}
