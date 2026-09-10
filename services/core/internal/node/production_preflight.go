package node

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ecirlabs/matrix-core/internal/token"
	"gopkg.in/yaml.v3"
)

// ProductionPreflight summarizes consensus-critical launch inputs after they
// pass static validation. It contains no secrets.
type ProductionPreflight struct {
	GenesisSupply   uint64 `json:"genesis_supply"`
	AllocationCount int    `json:"allocation_count"`
	ValidatorCount  int    `json:"validator_count"`
	RoundTimeout    string `json:"round_timeout"`
}

// ValidateProductionConfig catches launch mistakes before Node.Start can apply
// genesis. Production intentionally requires explicit values where a dev node
// may rely on defaults.
func ValidateProductionConfig(configPath string) (ProductionPreflight, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ProductionPreflight{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return ProductionPreflight{}, fmt.Errorf("parse config: %w", err)
	}
	if strings.TrimSpace(cfg.Consensus.RoundTimeout) == "" {
		return ProductionPreflight{}, fmt.Errorf("consensus.round_timeout must be explicit for production")
	}
	if _, err := parseConsensusRoundTimeout(cfg.Consensus.RoundTimeout); err != nil {
		return ProductionPreflight{}, fmt.Errorf("consensus.round_timeout: %w", err)
	}
	if len(cfg.Consensus.Validators) < 3 {
		return ProductionPreflight{}, fmt.Errorf("consensus.validators has %d entries; production requires at least 3", len(cfg.Consensus.Validators))
	}
	if cfg.Consensus.EpochLength == 0 {
		return ProductionPreflight{}, fmt.Errorf("consensus.epoch_length must be explicit and positive for production")
	}
	if !cfg.Consensus.Stake.Enabled || cfg.Consensus.Stake.Bond == 0 || effectiveMinBond(cfg.Consensus.Stake) == 0 {
		return ProductionPreflight{}, fmt.Errorf("consensus.stake must be enabled with positive min_bond and bond for production")
	}

	validators := make(map[string]struct{}, len(cfg.Consensus.Validators))
	for i, id := range cfg.Consensus.Validators {
		if err := validateProductionAccountID(id); err != nil {
			return ProductionPreflight{}, fmt.Errorf("consensus.validators[%d]: %w", i, err)
		}
		if _, exists := validators[id]; exists {
			return ProductionPreflight{}, fmt.Errorf("consensus.validators contains duplicate %q", id)
		}
		validators[id] = struct{}{}
	}

	total := cfg.Genesis.RewardPool
	allocations := make(map[string]struct{}, len(cfg.Genesis.Allocations))
	for i, allocation := range cfg.Genesis.Allocations {
		if err := validateProductionAccountID(allocation.Account); err != nil {
			return ProductionPreflight{}, fmt.Errorf("genesis.allocations[%d].account: %w", i, err)
		}
		if allocation.Amount == 0 {
			return ProductionPreflight{}, fmt.Errorf("genesis.allocations[%d].amount must be positive", i)
		}
		if _, exists := allocations[allocation.Account]; exists {
			return ProductionPreflight{}, fmt.Errorf("genesis.allocations contains duplicate account %q", allocation.Account)
		}
		allocations[allocation.Account] = struct{}{}
		if allocation.Amount > token.NativeMaxSupply || total > token.NativeMaxSupply-allocation.Amount {
			return ProductionPreflight{}, fmt.Errorf("genesis supply exceeds native cap %d", token.NativeMaxSupply)
		}
		total += allocation.Amount
	}
	if total != token.NativeMaxSupply {
		return ProductionPreflight{}, fmt.Errorf("genesis supply is %d; production requires exactly %d", total, token.NativeMaxSupply)
	}

	return ProductionPreflight{
		GenesisSupply:   total,
		AllocationCount: len(cfg.Genesis.Allocations),
		ValidatorCount:  len(cfg.Consensus.Validators),
		RoundTimeout:    cfg.Consensus.RoundTimeout,
	}, nil
}

func validateProductionAccountID(id string) error {
	if id != strings.ToLower(id) {
		return fmt.Errorf("%q must be lowercase hex", id)
	}
	raw, err := hex.DecodeString(id)
	if err != nil || len(raw) != 32 {
		return fmt.Errorf("%q must be 32 bytes of hex", id)
	}
	return nil
}

func MarshalProductionPreflight(result ProductionPreflight) ([]byte, error) {
	return json.MarshalIndent(result, "", "  ")
}
