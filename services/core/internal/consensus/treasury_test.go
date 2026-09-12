package consensus

import (
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

func treasuryAllocationTx(t *testing.T) *token.Transaction {
	t.Helper()
	return repairTx(t, treasuryAllocationAccount, treasuryAllocationRecipient, treasuryAllocationAmount)
}

func TestTreasuryAllocationIsExactAndReserved(t *testing.T) {
	if !IsReservedRecipient(treasuryAllocationRecipient) {
		t.Fatal("treasury allocation is not a reserved consensus operation")
	}
	if err := verifyLaunchRepair(
		treasuryAllocationAccount,
		treasuryAllocationRecipient,
		treasuryAllocationAmount,
		treasuryAllocationPoolBefore,
	); err != nil {
		t.Fatalf("exact allocation rejected: %v", err)
	}

	for name, mutate := range map[string]func(*token.Transaction, *uint64){
		"wrong signer":     func(tx *token.Transaction, _ *uint64) { tx.From[0] ^= 1 },
		"amount too high":  func(tx *token.Transaction, _ *uint64) { tx.Amount++ },
		"amount too low":   func(tx *token.Transaction, _ *uint64) { tx.Amount-- },
		"pool already low": func(_ *token.Transaction, pool *uint64) { *pool -= treasuryAllocationAmount },
		"pool higher":      func(_ *token.Transaction, pool *uint64) { *pool++ },
	} {
		t.Run(name, func(t *testing.T) {
			tx := treasuryAllocationTx(t)
			pool := uint64(treasuryAllocationPoolBefore)
			mutate(tx, &pool)
			if err := verifyLaunchRepair(tx.SenderID(), tx.To, tx.Amount, pool); err == nil {
				t.Fatal("invalid allocation accepted")
			}
		})
	}
}

// The allocation is guarded on the POOL, not on the destination, and this is the
// property that requires it. The whole purpose of the float is to be spent into
// the bridge, so the destination returns to its pre-allocation balance. A
// destination guard would re-arm there and the pool could be drained one
// allocation at a time.
func TestTreasuryAllocationCannotRepeatAfterRecipientSpendsIt(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()
	ledger := market.NewLedger(store)
	if err := ledger.Credit(token.RewardPoolAccount, treasuryAllocationPoolBefore); err != nil {
		t.Fatalf("credit pool: %v", err)
	}

	tx := treasuryAllocationTx(t)
	apply := func() bool {
		t.Helper()
		var applied bool
		if err := ledger.Atomically(func(ltx market.LedgerTx) error {
			var err error
			applied, err = applyLaunchRepair(ltx, tx)
			return err
		}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		return applied
	}

	if !apply() {
		t.Fatal("first allocation did not apply")
	}
	if got, _ := ledger.Balance(treasuryAllocationAccount); got != treasuryAllocationAmount {
		t.Fatalf("recipient balance = %d, want %d", got, treasuryAllocationAmount)
	}
	wantPool := uint64(treasuryAllocationPoolBefore) - treasuryAllocationAmount
	if got, _ := ledger.Balance(token.RewardPoolAccount); got != wantPool {
		t.Fatalf("pool balance = %d, want %d", got, wantPool)
	}

	if apply() {
		t.Fatal("allocation applied twice against an unchanged ledger")
	}

	// Spend the float exactly as the bridge will, returning the destination to
	// zero. A destination-guarded operation would become valid again here.
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return ltx.Transfer(treasuryAllocationAccount, defaultEscrowAccount, treasuryAllocationAmount)
	}); err != nil {
		t.Fatalf("spend float: %v", err)
	}
	if got, _ := ledger.Balance(treasuryAllocationAccount); got != 0 {
		t.Fatalf("recipient balance after spend = %d, want 0", got)
	}
	if apply() {
		t.Fatal("allocation re-armed after the recipient spent the float")
	}
	if got, _ := ledger.Balance(token.RewardPoolAccount); got != wantPool {
		t.Fatalf("pool moved on a refused allocation: %d", got)
	}
}

// The allocation moves EXISTING units. Issued supply must not change, or this
// would be a mint wearing a transfer's clothes.
func TestTreasuryAllocationConservesSupply(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()
	ledger := market.NewLedger(store)
	if err := ledger.Credit(token.RewardPoolAccount, treasuryAllocationPoolBefore); err != nil {
		t.Fatalf("credit pool: %v", err)
	}

	tx := treasuryAllocationTx(t)
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		applied, err := applyLaunchRepair(ltx, tx)
		if err != nil {
			return err
		}
		if !applied {
			t.Fatal("allocation did not apply")
		}
		return nil
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	pool, _ := ledger.Balance(token.RewardPoolAccount)
	recipient, _ := ledger.Balance(treasuryAllocationAccount)
	if pool+recipient != treasuryAllocationPoolBefore {
		t.Fatalf("pool %d + recipient %d != %d before",
			pool, recipient, uint64(treasuryAllocationPoolBefore))
	}
}

// The float has to fit under the wrapped mint cap headroom, or part of it could
// never reach Base and the published cap would be the real limit rather than the
// allocation. 50,000,000 of the 60,000,000 cap is already held by the vault.
func TestTreasuryAllocationFitsRemainingMintCap(t *testing.T) {
	const (
		mintCapMatrix      = uint64(60_000_000)
		founderVaultMatrix = uint64(50_000_000)
	)
	headroom := (mintCapMatrix - founderVaultMatrix) * token.NativeUnit
	if treasuryAllocationAmount > headroom {
		t.Fatalf("allocation %d exceeds remaining mint cap headroom %d",
			treasuryAllocationAmount, headroom)
	}
	if treasuryAllocationAmount < token.MinBridgeLockAmount {
		t.Fatalf("allocation %d is below the minimum bridge lock %d and could never be bridged",
			treasuryAllocationAmount, token.MinBridgeLockAmount)
	}
}

// Each pinned pool transfer must keep its own guard. Sharing one dispatch path
// is only safe while the two guard kinds stay distinct.
func TestPinnedPoolTransfersKeepDistinctGuards(t *testing.T) {
	for _, tc := range []struct {
		recipient string
		guard     repairGuard
		account   string
	}{
		{launchRepairRecipient, guardDestination, launchRepairFounder},
		{validatorRepairRecipient, guardDestination, validatorRepairAccount},
		{treasuryAllocationRecipient, guardRewardPool, token.RewardPoolAccount},
	} {
		t.Run(tc.recipient, func(t *testing.T) {
			if !IsPinnedPoolTransferRecipient(tc.recipient) {
				t.Fatal("not recognised as a pinned pool transfer")
			}
			spec, ok := repairSpec(tc.recipient)
			if !ok {
				t.Fatal("no spec")
			}
			if spec.guard != tc.guard {
				t.Fatalf("guard = %d, want %d", spec.guard, tc.guard)
			}
			if got := spec.guardAccount(); got != tc.account {
				t.Fatalf("guard account = %q, want %q", got, tc.account)
			}
		})
	}
}
