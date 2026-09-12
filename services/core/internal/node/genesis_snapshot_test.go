package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

func snapshotLedgerWith(t *testing.T, balances map[string]uint64) *market.Ledger {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ledger := market.NewLedger(store)
	for account, amount := range balances {
		if err := ledger.Credit(account, amount); err != nil {
			t.Fatalf("Credit(%s): %v", account, err)
		}
	}
	return ledger
}

const (
	holderA  = "eth:0x1111111111111111111111111111111111111111"
	holderB  = "eth:0x2222222222222222222222222222222222222222"
	edHolder = "aa" + "00000000000000000000000000000000000000000000000000000000000000"
)

// TestTheSnapshotCarriesHoldersAndSeparatesTheRewardPool is the ordinary case.
// The reward pool is a FIELD, not an allocation: emitted as one it would be
// credited to the account and counted again as the pool, and the supply identity
// a production genesis has to close would not.
func TestTheSnapshotCarriesHoldersAndSeparatesTheRewardPool(t *testing.T) {
	ledger := snapshotLedgerWith(t, map[string]uint64{
		holderA:                 500,
		holderB:                 300,
		token.RewardPoolAccount: 9000,
		"bridge/escrow":         200,
	})

	snap, err := snapshotLedger(ledger)
	if err != nil {
		t.Fatalf("snapshotLedger: %v", err)
	}

	if snap.RewardPool != 9000 {
		t.Fatalf("RewardPool = %d, want 9000", snap.RewardPool)
	}
	for _, a := range snap.Allocations {
		if a.Account == token.RewardPoolAccount {
			t.Fatal("the reward pool was emitted as an allocation; it would be counted twice")
		}
	}
	// The escrow comes first: it is an accounting fact rather than a policy
	// choice, and the reader has to confirm it against wrapped supply.
	if len(snap.Allocations) == 0 || snap.Allocations[0].Account != "bridge/escrow" {
		t.Fatalf("first allocation is %+v, want the bridge escrow", snap.Allocations[0])
	}
	if snap.Total != 500+300+9000+200 {
		t.Fatalf("Total = %d, want %d", snap.Total, 500+300+9000+200)
	}
	if len(snap.Unclassified) != 0 {
		t.Fatalf("unexpected unclassified accounts: %+v", snap.Unclassified)
	}
}

// TestABondIsReturnedToTheAccountThatPostedIt is the case that would quietly
// take a validator's money.
//
// A bond lives in a reserved account genesis does not accept, so it cannot be
// carried as itself. Dropped, every validator loses its stake in a file that
// still looks complete and still passes preflight. Bonds do not survive a
// relaunch - they were balances on a chain that no longer exists - so the value
// goes back to whoever posted it and is re-bonded on the new chain.
func TestABondIsReturnedToTheAccountThatPostedIt(t *testing.T) {
	ledger := snapshotLedgerWith(t, map[string]uint64{
		holderA:                        100,
		consensus.BondAccount(holderA): 700,
		consensus.BondAccount(holderB): 400,
	})

	snap, err := snapshotLedger(ledger)
	if err != nil {
		t.Fatalf("snapshotLedger: %v", err)
	}

	got := map[string]uint64{}
	for _, a := range snap.Allocations {
		got[a.Account] = a.Amount
		if strings.HasPrefix(a.Account, consensus.BondAccount("")) {
			t.Fatalf("a bond account was emitted as an allocation: %s", a.Account)
		}
	}
	// A holder with both a balance and a bond gets one allocation for the sum,
	// not two entries - genesis refuses a duplicate account.
	if got[holderA] != 800 {
		t.Fatalf("%s = %d, want 800 (100 held + 700 bonded)", holderA, got[holderA])
	}
	// A validator whose whole balance was bonded still gets its money back.
	if got[holderB] != 400 {
		t.Fatalf("%s = %d, want 400 (bonded only)", holderB, got[holderB])
	}
	if snap.Total != 1200 {
		t.Fatalf("Total = %d, want 1200; value was lost or double counted", snap.Total)
	}
	if len(snap.ReturnedBonds) != 2 {
		t.Fatalf("ReturnedBonds = %+v, want both bonds reported", snap.ReturnedBonds)
	}
}

// TestAnUnknownReservedAccountStopsTheSnapshot. Guessing an allocation for a
// reserved account would invent a balance; dropping it would lose one. Neither
// is a decision this can make, so it reports and refuses to look finished.
func TestAnUnknownReservedAccountStopsTheSnapshot(t *testing.T) {
	ledger := snapshotLedgerWith(t, map[string]uint64{
		holderA:             100,
		"some/future/thing": 50,
	})

	snap, err := snapshotLedger(ledger)
	if err != nil {
		t.Fatalf("snapshotLedger: %v", err)
	}
	if len(snap.Unclassified) != 1 || snap.Unclassified[0].Account != "some/future/thing" {
		t.Fatalf("Unclassified = %+v, want the unknown reserved account", snap.Unclassified)
	}
	for _, a := range snap.Allocations {
		if a.Account == "some/future/thing" {
			t.Fatal("an unrecognised reserved account was carried into allocations")
		}
	}
	// It is excluded from the total too, so the supply line reads as short rather
	// than as closing over a balance nobody has placed.
	if snap.Total != 100 {
		t.Fatalf("Total = %d, want 100", snap.Total)
	}

	out := MarshalGenesisSnapshot(snap)
	if !strings.Contains(out, "NOT READY") {
		t.Fatal("the rendered snapshot does not warn that it is incomplete")
	}
}

// TestAnEd25519HolderIsCarried. The older account kind was never removed and
// still verifies, so a relaunch must not quietly drop everyone who has not moved
// to a wallet.
func TestAnEd25519HolderIsCarried(t *testing.T) {
	ledger := snapshotLedgerWith(t, map[string]uint64{edHolder: 42})

	snap, err := snapshotLedger(ledger)
	if err != nil {
		t.Fatalf("snapshotLedger: %v", err)
	}
	if len(snap.Allocations) != 1 || snap.Allocations[0].Account != edHolder {
		t.Fatalf("Allocations = %+v, want the ed25519 holder carried", snap.Allocations)
	}
	if len(snap.Unclassified) != 0 {
		t.Fatalf("an ed25519 id was treated as unrecognised: %+v", snap.Unclassified)
	}
}

// TestAZeroBalanceIsNotAnAllocation. Genesis refuses a zero amount, so emitting
// one produces a file that fails preflight for a reason that says nothing about
// what is wrong.
func TestAZeroBalanceIsNotAnAllocation(t *testing.T) {
	ledger := snapshotLedgerWith(t, map[string]uint64{holderA: 100, holderB: 0})

	snap, err := snapshotLedger(ledger)
	if err != nil {
		t.Fatalf("snapshotLedger: %v", err)
	}
	for _, a := range snap.Allocations {
		if a.Amount == 0 {
			t.Fatalf("a zero allocation was emitted for %s", a.Account)
		}
	}
}

// TestTheRenderedSnapshotIsAGenesisFileThatPreflightAccepts closes the loop. The
// output is not a report ABOUT a genesis, it is one: pasted under a consensus
// block it has to parse and pass the same validation a production launch runs.
// Anything less and the operator is back to editing by hand, which is the step
// this exists to remove.
func TestTheRenderedSnapshotIsAGenesisFileThatPreflightAccepts(t *testing.T) {
	// A ledger whose supply closes exactly at the cap, as a real one does.
	const held = 1_000_000
	ledger := snapshotLedgerWith(t, map[string]uint64{
		holderA:                 held,
		"bridge/escrow":         held,
		token.RewardPoolAccount: token.NativeMaxSupply - 2*held,
	})

	snap, err := snapshotLedger(ledger)
	if err != nil {
		t.Fatalf("snapshotLedger: %v", err)
	}
	if snap.Total != token.NativeMaxSupply {
		t.Fatalf("Total = %d, want the cap %d", snap.Total, token.NativeMaxSupply)
	}

	rendered := MarshalGenesisSnapshot(snap)
	if !strings.Contains(rendered, "Supply closes exactly at the cap") {
		t.Fatalf("a snapshot that closes at the cap does not say so:\n%s", rendered)
	}

	// Paste the rendered text verbatim under a consensus block, exactly as the
	// runbook tells an operator to, and run the real preflight over the file.
	config := fmt.Sprintf(`consensus:
  validators:
    - %s
    - %s
    - %s
  epoch_length: 100
  round_timeout: "3s"
  stake:
    enabled: true
    min_bond: 1000
    bond: 1000
%s`, strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64), rendered)

	path := filepath.Join(t.TempDir(), "genesis.yaml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := ValidateProductionConfig(path)
	if err != nil {
		t.Fatalf("production preflight refused the snapshot's own output: %v\n\n%s", err, config)
	}
	if result.GenesisSupply != token.NativeMaxSupply {
		t.Fatalf("preflight read supply %d, want %d", result.GenesisSupply, token.NativeMaxSupply)
	}
	if result.AllocationCount != len(snap.Allocations) {
		t.Fatalf("preflight read %d allocations, want %d", result.AllocationCount, len(snap.Allocations))
	}
}

// TestUndistributedFeesGoBackToThePoolAndNotToAnyone
//
// The protocol fee accrual account holds fee taken but not yet paid out: the
// remainder of an uneven split, which the next block picks up. A relaunch never
// gets that next block, so the value has to go somewhere.
//
// It belongs to the validator set collectively and to no member in particular -
// that is precisely why no block has distributed it - so carrying it to an
// account would hand the network's money to whoever the snapshot named first.
// It goes back to the reward pool: supply is conserved and nobody is favoured,
// which is what the value already was. Dropping it instead would leave the
// supply short and a production genesis refuses one that does not close.
func TestUndistributedFeesGoBackToThePoolAndNotToAnyone(t *testing.T) {
	ledger := snapshotLedgerWith(t, map[string]uint64{
		holderA:                       100,
		token.RewardPoolAccount:       900,
		consensus.FeeAccrualAccount(): 7,
	})

	snap, err := snapshotLedger(ledger)
	if err != nil {
		t.Fatalf("snapshotLedger: %v", err)
	}

	if snap.AccruedFees != 7 {
		t.Fatalf("AccruedFees = %d, want 7", snap.AccruedFees)
	}
	if snap.RewardPool != 907 {
		t.Fatalf("RewardPool = %d, want 907 (900 pooled + 7 accrued)", snap.RewardPool)
	}
	for _, a := range snap.Allocations {
		if a.Account == consensus.FeeAccrualAccount() {
			t.Fatal("the fee accrual account was emitted as an allocation")
		}
		if a.Account == holderA && a.Amount != 100 {
			t.Fatalf("%s = %d, want 100; the fee was given to a holder", a.Account, a.Amount)
		}
	}
	if len(snap.Unclassified) != 0 {
		t.Fatalf("the fee account was left unclassified: %+v", snap.Unclassified)
	}
	// Supply is conserved: nothing dropped, nothing counted twice.
	if snap.Total != 1007 {
		t.Fatalf("Total = %d, want 1007", snap.Total)
	}

	if out := MarshalGenesisSnapshot(snap); !strings.Contains(out, "UNDISTRIBUTED FEES RETURNED TO THE POOL") {
		t.Fatalf("the report does not say where the fee went:\n%s", out)
	}
}
