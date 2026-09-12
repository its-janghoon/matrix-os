package node

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ecirlabs/matrix-core/internal/bridge"
	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Reading a running chain's balances back out as a genesis file.
//
// A relaunch starts from a new genesis, and a new genesis is a new ledger.
// Nothing carries over on its own: a balance survives only because somebody
// wrote it into genesis.allocations, and an account left out is simply gone.
// That made the one irreversible step of a relaunch "copy every account by
// hand", which is a step nobody can review and everybody can get wrong.
//
// So this reads the ledger and writes the file. The interesting part is not the
// copying, which is mechanical, but the CLASSIFICATION - because not every
// balance in the ledger is an allocation, and the three that are not would each
// be a different kind of quiet loss:
//
//   - The reward pool is genesis.reward_pool, a field of its own. Emitted as an
//     allocation it would be credited to the account AND counted again as the
//     pool, and the supply identity would not close.
//   - A BOND is locked value belonging to a validator, held in a reserved
//     account that genesis does not accept. Dropped silently, every validator
//     loses its stake in a file that still looks complete. This returns it to
//     the bonder it belongs to, which is also what a relaunch means: bonds do
//     not carry over, so the money goes back and is re-bonded on the new chain.
//   - The bridge escrow is an accounting fact rather than a policy choice, and
//     it has to match wrapped supply on the other side of the bridge. It is
//     emitted first and flagged for confirmation against the contract.
//
// What it does NOT do is decide anything. It refuses to invent an allocation
// for a reserved account it does not recognise, and says so, because the one
// thing worse than stopping here is guessing.

// GenesisSnapshot is the ledger, sorted into what a genesis file needs.
type GenesisSnapshot struct {
	// Allocations are the balances to carry over, escrow first and then by
	// account id so two runs of this produce the same file.
	Allocations []GenesisAllocationConfig
	// RewardPool is what goes in genesis.reward_pool, including any fee dust
	// folded back into it.
	RewardPool uint64
	// AccruedFees is the undistributed protocol fee folded into RewardPool,
	// reported separately so the report can say the pool is not just what the
	// pool account held.
	AccruedFees uint64
	// Escrow is the bridge escrow figure, repeated here because it is the one
	// number that must be confirmed against the wrapped supply.
	Escrow uint64
	// ReturnedBonds records bonds folded back into their owner's allocation, so
	// the report can say whose stake moved and why the numbers differ from a
	// naive balance listing.
	ReturnedBonds []BondReturn
	// Unclassified are reserved accounts holding value that this does not know
	// how to carry. Non-empty means the snapshot is NOT ready to use.
	Unclassified []GenesisAllocationConfig
	// Total is allocations plus the reward pool, which production requires to
	// equal the native supply cap exactly.
	Total uint64
}

// BondReturn is one validator's bond, returned to the account that posted it.
type BondReturn struct {
	Account string
	Amount  uint64
}

// bondPrefix is the reserved prefix a bond account carries. It is derived from
// consensus.BondAccount rather than written out again, so a change there cannot
// leave this silently matching nothing and dropping every bond.
var bondPrefix = consensus.BondAccount("")

// SnapshotGenesis reads the ledger at the configured storage path and returns it
// as the genesis a relaunch would carry. The node must not be running: the store
// is opened directly, the same way -init-identities opens it.
func SnapshotGenesis(configPath string) (GenesisSnapshot, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return GenesisSnapshot{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return GenesisSnapshot{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Storage.Path == "" {
		cfg.Storage.Path = "./data"
	}

	store, err := kv.New(kv.Config{Path: cfg.Storage.Path})
	if err != nil {
		return GenesisSnapshot{}, fmt.Errorf("open store at %s: %w", cfg.Storage.Path, err)
	}
	defer func() { _ = store.Close() }()

	return snapshotLedger(market.NewLedger(store))
}

// snapshotLedger is the classification itself, over any ledger, so a test can
// drive it without a config file or a store on disk.
func snapshotLedger(ledger *market.Ledger) (GenesisSnapshot, error) {
	var snap GenesisSnapshot
	holders := make(map[string]uint64)
	bonds := make(map[string]uint64)

	err := ledger.ForEachBalance(func(account string, balance uint64) error {
		if balance == 0 {
			// A zero balance is not an allocation: genesis refuses a zero amount,
			// and carrying one over would say something about an account that
			// holds nothing.
			return nil
		}
		switch {
		case account == token.RewardPoolAccount:
			// Added, not assigned: the fee-dust case below also contributes here
			// and iteration order over the store is not something to depend on.
			snap.RewardPool += balance
		case account == bridge.EscrowAccount:
			snap.Escrow = balance
		case account == consensus.FeeAccrualAccount():
			// Protocol fee taken but not yet distributed: the remainder of an
			// uneven split, left for the next block to pick up. It belongs to the
			// validator set collectively and to no member in particular, which is
			// why no block has paid it out yet - so there is no account to carry it
			// to, and inventing one would hand the network's money to whoever the
			// snapshot happened to name first.
			//
			// It goes back to the reward pool, which is the network's own
			// undistributed balance. Supply is conserved and nobody is favoured,
			// which is exactly what this value already is.
			snap.AccruedFees = balance
			snap.RewardPool += balance
		case strings.HasPrefix(account, bondPrefix):
			owner := strings.TrimPrefix(account, bondPrefix)
			if owner == "" {
				snap.Unclassified = append(snap.Unclassified,
					GenesisAllocationConfig{Account: account, Amount: balance})
				return nil
			}
			bonds[owner] += balance
		case isCarryableAccount(account):
			holders[account] += balance
		default:
			// A reserved account this does not recognise. Guessing an allocation
			// for it would be inventing a balance; dropping it would be losing
			// one. Neither is this function's decision.
			snap.Unclassified = append(snap.Unclassified,
				GenesisAllocationConfig{Account: account, Amount: balance})
		}
		return nil
	})
	if err != nil {
		return GenesisSnapshot{}, fmt.Errorf("read balances: %w", err)
	}

	// Fold each bond back into its owner's allocation. Bonds do not carry over -
	// they were balances on a chain that no longer exists - so the value returns
	// to the account that posted it and is re-bonded on the new chain.
	for owner, amount := range bonds {
		if !isCarryableAccount(owner) {
			snap.Unclassified = append(snap.Unclassified,
				GenesisAllocationConfig{Account: consensus.BondAccount(owner), Amount: amount})
			continue
		}
		holders[owner] += amount
		snap.ReturnedBonds = append(snap.ReturnedBonds, BondReturn{Account: owner, Amount: amount})
	}
	sort.Slice(snap.ReturnedBonds, func(i, j int) bool {
		return snap.ReturnedBonds[i].Account < snap.ReturnedBonds[j].Account
	})
	sort.Slice(snap.Unclassified, func(i, j int) bool {
		return snap.Unclassified[i].Account < snap.Unclassified[j].Account
	})

	// The escrow FIRST, because it is the one allocation that is an accounting
	// fact rather than a policy choice, then the holders in a stable order.
	if snap.Escrow > 0 {
		snap.Allocations = append(snap.Allocations,
			GenesisAllocationConfig{Account: bridge.EscrowAccount, Amount: snap.Escrow})
	}
	ids := make([]string, 0, len(holders))
	for id := range holders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		snap.Allocations = append(snap.Allocations,
			GenesisAllocationConfig{Account: id, Amount: holders[id]})
	}

	snap.Total = snap.RewardPool
	for _, a := range snap.Allocations {
		snap.Total += a.Amount
	}
	return snap, nil
}

// isCarryableAccount reports whether genesis.allocations will accept this id.
// It is the same rule validateProductionAccountID enforces, asked in advance:
// an ed25519 id, a wallet address, or the escrow.
func isCarryableAccount(id string) bool {
	return validateProductionAccountID(id) == nil
}

// MarshalGenesisSnapshot renders the snapshot as the genesis block to paste,
// with a report above it of what was decided and what still needs a decision.
//
// It is one document rather than a file plus a summary printed elsewhere,
// because the reader who needs the warnings is the one holding the file.
func MarshalGenesisSnapshot(snap GenesisSnapshot) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Genesis snapshot: %d allocations, %d total base units\n",
		len(snap.Allocations), snap.Total)
	fmt.Fprintf(&b, "#\n")

	if snap.Escrow > 0 {
		fmt.Fprintf(&b, "# CONFIRM THE ESCROW before using this. %d base units is what this\n", snap.Escrow)
		fmt.Fprintf(&b, "# ledger holds; wrapped supply on the contract must equal it. Run\n")
		fmt.Fprintf(&b, "# `matrix bridge reconcile` and check totalSupply() against the same block.\n#\n")
	} else {
		fmt.Fprintf(&b, "# NO BRIDGE ESCROW in this ledger. If wrapped tokens exist, this snapshot\n")
		fmt.Fprintf(&b, "# is not complete and the mirror would be unbacked on the new chain.\n#\n")
	}

	if len(snap.ReturnedBonds) > 0 {
		fmt.Fprintf(&b, "# BONDS RETURNED TO THEIR OWNERS. Bonds do not carry over - they were\n")
		fmt.Fprintf(&b, "# balances on a chain that no longer exists - so each is folded back into\n")
		fmt.Fprintf(&b, "# the account that posted it, to be re-bonded on the new chain:\n")
		for _, r := range snap.ReturnedBonds {
			fmt.Fprintf(&b, "#   %s  +%d\n", r.Account, r.Amount)
		}
		fmt.Fprintf(&b, "#\n")
	}

	if snap.AccruedFees > 0 {
		fmt.Fprintf(&b, "# UNDISTRIBUTED FEES RETURNED TO THE POOL. %d base units of protocol fee\n", snap.AccruedFees)
		fmt.Fprintf(&b, "# had been taken and not yet paid out - the remainder of an uneven split,\n")
		fmt.Fprintf(&b, "# owed to the validator set collectively and to no member in particular.\n")
		fmt.Fprintf(&b, "# It is folded into reward_pool below rather than given to anyone.\n#\n")
	}

	if len(snap.Unclassified) > 0 {
		fmt.Fprintf(&b, "# NOT READY. These reserved accounts hold value and this does not know how\n")
		fmt.Fprintf(&b, "# to carry them. Decide where each goes and add it by hand; leaving them\n")
		fmt.Fprintf(&b, "# out loses the value, and the supply total below does NOT include them:\n")
		for _, u := range snap.Unclassified {
			fmt.Fprintf(&b, "#   %s  %d\n", u.Account, u.Amount)
		}
		fmt.Fprintf(&b, "#\n")
	}

	switch {
	case snap.Total == token.NativeMaxSupply:
		fmt.Fprintf(&b, "# Supply closes exactly at the cap (%d). Production requires this.\n",
			token.NativeMaxSupply)
	case snap.Total < token.NativeMaxSupply:
		fmt.Fprintf(&b, "# SUPPLY IS %d SHORT of the cap (%d vs %d). Production preflight refuses a\n",
			token.NativeMaxSupply-snap.Total, snap.Total, token.NativeMaxSupply)
		fmt.Fprintf(&b, "# genesis that does not close exactly; the difference belongs in reward_pool\n")
		fmt.Fprintf(&b, "# unless it belongs to an account listed above as unclassified.\n")
	default:
		fmt.Fprintf(&b, "# SUPPLY IS %d OVER the cap (%d vs %d). Something is counted twice; do not\n",
			snap.Total-token.NativeMaxSupply, snap.Total, token.NativeMaxSupply)
		fmt.Fprintf(&b, "# reconcile this by trimming an allocation until it fits.\n")
	}
	fmt.Fprintf(&b, "\ngenesis:\n  allocations:\n")
	if len(snap.Allocations) == 0 {
		fmt.Fprintf(&b, "    [] # the ledger holds no carryable balance\n")
	}
	for _, a := range snap.Allocations {
		fmt.Fprintf(&b, "    - account: %q\n      amount: %d\n", a.Account, a.Amount)
	}
	fmt.Fprintf(&b, "  reward_pool: %d\n", snap.RewardPool)
	return b.String()
}
