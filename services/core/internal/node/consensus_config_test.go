package node

import (
	"context"
	"crypto/ed25519"
	"path/filepath"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// TestInitialize_GeneratedConsensusMonetaryPolicy pins matrixd -init's launch
// economics: one-percent protocol fee, no provider emission, bonded-open stake,
// and no maintainer assignment or share unless a later launch config explicitly
// supplies a real account.
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

	if consensus.MaxFeeBasisPoints != 100 {
		t.Fatalf("consensus.MaxFeeBasisPoints = %d, want launch fee cap 100", consensus.MaxFeeBasisPoints)
	}
	if got := cfg.Consensus.FeeBasisPoints; got != consensus.MaxFeeBasisPoints {
		t.Errorf("generated fee_basis_points = %d, want %d", got, consensus.MaxFeeBasisPoints)
	}
	if got := cfg.Consensus.Rewards.PerBlock; got != 0 {
		t.Errorf("generated rewards.per_block = %d, want 0", got)
	}
	if got := cfg.Consensus.Rewards.HalfLife; got != consensus.DefaultProviderEmissionHalfLife {
		t.Errorf("generated rewards.half_life = %d, want %d", got, consensus.DefaultProviderEmissionHalfLife)
	}
	if cfg.Consensus.MembershipMode != string(consensus.MembershipBondedOpen) {
		t.Errorf("generated membership_mode = %q, want %q", cfg.Consensus.MembershipMode, consensus.MembershipBondedOpen)
	}
	if cfg.Consensus.ParticipateInOpenSet == nil || !*cfg.Consensus.ParticipateInOpenSet {
		t.Error("generated participate_in_open_set is not explicitly true")
	}
	if !cfg.Consensus.Stake.Enabled {
		t.Error("generated stake.enabled = false, want true for bonded-open membership")
	}
	if cfg.Consensus.Stake.MinBond == nil || *cfg.Consensus.Stake.MinBond != consensus.DefaultMinBond {
		t.Errorf("generated stake.min_bond = %v, want %d", cfg.Consensus.Stake.MinBond, consensus.DefaultMinBond)
	}
	if cfg.Consensus.Stake.Bond != consensus.DefaultMinBond {
		t.Errorf("generated stake.bond = %d, want %d", cfg.Consensus.Stake.Bond, consensus.DefaultMinBond)
	}
	if got := effectiveUnbonding(cfg.Consensus.Stake); got != consensus.DefaultUnbondingPeriod {
		t.Errorf("effective unbonding period = %d, want %d", got, consensus.DefaultUnbondingPeriod)
	}
	if cfg.Consensus.MaintainerAccount != "" {
		t.Errorf("generated maintainer_account = %q, want empty", cfg.Consensus.MaintainerAccount)
	}
	if cfg.Consensus.MaintainerFeeShareBasisPoints != 0 {
		t.Errorf("generated maintainer share = %d, want 0 without an explicit account",
			cfg.Consensus.MaintainerFeeShareBasisPoints)
	}
	if len(cfg.Consensus.Rewards.ApprovedProviders) != 0 {
		t.Errorf("generated rewards.approved_providers = %v, want empty", cfg.Consensus.Rewards.ApprovedProviders)
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

func TestParseConsensusRoundTimeout(t *testing.T) {
	t.Parallel()
	got, err := parseConsensusRoundTimeout("")
	if err != nil || got != 0 {
		t.Fatalf("empty: got %v err %v, want 0 nil", got, err)
	}
	got, err = parseConsensusRoundTimeout("3s")
	if err != nil || got != 3*time.Second {
		t.Fatalf("3s: got %v err %v, want 3s nil", got, err)
	}
	if _, err := parseConsensusRoundTimeout("nope"); err == nil {
		t.Fatal("nope: want error")
	}
	if _, err := parseConsensusRoundTimeout("0s"); err == nil {
		t.Fatal("0s: want error")
	}
}
