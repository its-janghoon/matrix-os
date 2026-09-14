package node

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ecirlabs/matrix-core/internal/bridge"
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

// validateProductionAccountID accepts the three things a genesis allocation may
// legitimately name.
//
// It used to accept only the 64-hex ed25519 form, which made two allocations a
// production network needs impossible to write:
//
//   - An account held in a WALLET. Those are addresses, and a chain whose users
//     hold wallets cannot allocate to any of them at genesis.
//   - The BRIDGE ESCROW. Wrapped supply must be backed one-for-one by native
//     coins sitting in escrow, and a network relaunching from a new genesis while
//     wrapped tokens already exist has to put that escrow back. Without this, the
//     only ways to restore the backing were to redeploy the token - abandoning
//     every holder and every pool - or to leave the mirror unbacked.
//
// Nothing else reserved is allowed. The reward pool has its own field, and the
// remaining reserved namespaces are consensus operations rather than places to
// put coins: an allocation to one would be value sent into an operation, which
// is not a thing a genesis file should be able to express.
func validateProductionAccountID(id string) error {
	if id != strings.ToLower(id) {
		return fmt.Errorf("%q must be lowercase", id)
	}
	if id == bridge.EscrowAccount {
		return nil
	}
	if token.IsEthAccountID(id) {
		if _, err := token.ParseEthAccountID(id); err != nil {
			return fmt.Errorf("%q is not a valid ethereum account id: %w", id, err)
		}
		return nil
	}
	raw, err := hex.DecodeString(id)
	if err != nil || len(raw) != 32 {
		return fmt.Errorf("%q must be 32 bytes of hex, an eth:0x... address, or %q",
			id, bridge.EscrowAccount)
	}
	return nil
}

func MarshalProductionPreflight(result ProductionPreflight) ([]byte, error) {
	return json.MarshalIndent(result, "", "  ")
}
