package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/bridge"
	"github.com/ecirlabs/matrix-core/internal/token"
)

func writeProductionConfig(t *testing.T, roundTimeout string, rewardPool uint64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	config := fmt.Sprintf(`consensus:
  validators:
    - %s
    - %s
    - %s
  epoch_length: 100
  round_timeout: %q
  stake:
    enabled: true
    min_bond: 1000
    bond: 1000
genesis:
  allocations:
    - account: %s
      amount: 1000
  reward_pool: %d
`, strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64),
		roundTimeout, strings.Repeat("4", 64), rewardPool)
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateProductionConfigAcceptsExactSupply(t *testing.T) {
	path := writeProductionConfig(t, "3s", token.NativeMaxSupply-1000)
	result, err := ValidateProductionConfig(path)
	if err != nil {
		t.Fatalf("ValidateProductionConfig: %v", err)
	}
	if result.GenesisSupply != token.NativeMaxSupply || result.ValidatorCount != 3 ||
		result.AllocationCount != 1 || result.RoundTimeout != "3s" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestValidateProductionConfigRejectsUnsafeLaunchValues(t *testing.T) {
	tests := []struct {
		name    string
		timeout string
		pool    uint64
		want    string
	}{
		{name: "implicit LAN timeout", timeout: "", pool: token.NativeMaxSupply - 1000, want: "round_timeout"},
		{name: "invalid timeout", timeout: "soon", pool: token.NativeMaxSupply - 1000, want: "consensus.round_timeout"},
		{name: "underissued supply", timeout: "3s", pool: token.NativeMaxSupply - 1001, want: "requires exactly"},
		{name: "supply overflow", timeout: "3s", pool: token.NativeMaxSupply, want: "exceeds native cap"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateProductionConfig(writeProductionConfig(t, tt.timeout, tt.pool))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

// TestGenesisMayAllocateToAWalletAndToTheEscrow covers the two allocations a
// production network needs that the 64-hex-only rule made impossible to write.
//
// A chain whose users hold wallets could allocate to none of them, and a network
// relaunching from a new genesis while wrapped tokens already exist had no way
// to put the escrow back - leaving redeploying the token, which abandons every
// holder and every pool, or leaving the mirror unbacked.
func TestGenesisMayAllocateToAWalletAndToTheEscrow(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   string
		ok   bool
	}{
		{"an ed25519 account", strings.Repeat("ab", 32), true},
		{"a wallet address", "eth:0x00000000000000000000000000000000000000aa", true},
		{"the bridge escrow", bridge.EscrowAccount, true},
		{"an uppercase account", strings.Repeat("AB", 32), false},
		{"a short account", strings.Repeat("ab", 20), false},
		{"a malformed wallet address", "eth:0xnothex", false},
		// Every other reserved namespace is a consensus OPERATION, not a place to
		// put coins, and an allocation to one would be value sent into an
		// operation. A genesis file must not be able to express that.
		{"a bond account", "consensus/stake/bond/" + strings.Repeat("ab", 32), false},
		{"the reward pool", "native/reward-pool", false},
		{"a validator admission", "consensus/validator/add/" + strings.Repeat("ab", 32), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProductionAccountID(tc.id)
			if tc.ok && err != nil {
				t.Fatalf("%q should be allowed: %v", tc.id, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("%q should be refused", tc.id)
			}
		})
	}
}
