// Command bridge-watch is the runnable burn->unlock relayer: it connects to an
// Ethereum JSON-RPC endpoint, scans a WrappedMatrix contract for `Burned` events
// in a block range, and applies each one to the native bridge (releasing
// escrowed native MATRIX to the burn's named L1 recipient) exactly once via the
// replay-safe internal/bridge.ProcessBurn primitive.
//
// It is the always-on counterpart to cmd/bridge-attest (the mint half). Where
// bridge-attest proves a Go attestation mints on-chain, bridge-watch proves a Go
// watcher ingests a real on-chain Burned event and drives the native unlock. It
// is exercised end to end against a local hardhat node by the contracts
// BridgeWatch test.
//
// OPERATOR MODEL (honest): this command runs a single-node relayer that applies
// burns to a LOCAL ledger it is given. It seeds a throwaway escrow so the demo
// unlock has backing; a production deployment points --escrow-fund at the real
// outstanding escrow. The internal/bridge.Watcher this command drives is a
// reusable library type, but matrixd does NOT currently construct or run a
// Watcher against the node's own ledger: there is no in-node bridge-watch
// subsystem wired into internal/node yet, so today an operator runs the watcher
// out of process via this command (or a script). Wiring an optional
// config-driven Watcher into matrixd is a remaining item (see STILL-REMAINING.md).
// The watcher does not itself reach multi-validator consensus on the unlock; see
// internal/bridge/watcher.go for the automation boundary.
//
// Usage:
//
//	go run ./cmd/bridge-watch \
//	  -rpc http://127.0.0.1:8545 \
//	  -contract 0x...       # deployed WrappedMatrix address
//	  -from 0 -to latest    # block range to scan
//	  -escrow-fund 100000000000  # native base units to seed escrow so unlock has backing
//
// It prints a JSON object with the applied burns (id, native recipient, native
// amount) and the recipient balances after unlock.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/ecirlabs/matrix-core/internal/bridge"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

func main() {
	rpc := flag.String("rpc", "http://127.0.0.1:8545", "ethereum JSON-RPC endpoint")
	contractHex := flag.String("contract", "", "deployed WrappedMatrix contract address")
	from := flag.Uint64("from", 0, "first block to scan")
	toFlag := flag.String("to", "latest", "last block to scan (a number or \"latest\")")
	escrowFund := flag.Uint64("escrow-fund", 0, "native base units to seed the local escrow so unlocks have backing")
	chainID := flag.Int64("chain-id", 31337, "EVM chain id (for attestation params binding)")
	timeout := flag.Duration("timeout", 30*time.Second, "overall timeout")
	flag.Parse()

	if *contractHex == "" {
		fmt.Fprintln(os.Stderr, "contract is required")
		flag.Usage()
		os.Exit(2)
	}
	contract, err := bridge.ParseAddress(*contractHex)
	must(err)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client := bridge.NewHTTPEthClient(*rpc, nil)

	// Determine the target block.
	var to uint64
	if *toFlag == "latest" {
		to, err = client.BlockNumber(ctx)
		must(err)
	} else {
		to, err = parseUint(*toFlag)
		must(err)
	}

	// Build a throwaway ledger + bridge for the relay, seeding escrow so the
	// unlock has native backing to release (mirrors the outstanding escrow a real
	// deployment holds against the wrapped supply).
	dir, err := os.MkdirTemp("", "bridge-watch")
	must(err)
	defer os.RemoveAll(dir)
	store, err := kv.New(kv.Config{Path: filepath.Join(dir, "db")})
	must(err)
	defer store.Close()

	ledger := market.NewLedger(store)
	if *escrowFund > 0 {
		must(ledger.Credit(bridge.EscrowAccount, *escrowFund))
	}
	params := bridge.AttestationParams{ChainID: big.NewInt(*chainID), BridgeContract: contract}
	b := bridge.New(ledger, store, params)

	type appliedBurn struct {
		ID              string `json:"id"`
		NativeRecipient string `json:"native_recipient"`
		Native          uint64 `json:"native_amount"`
		Balance         uint64 `json:"recipient_balance"`
	}
	var applied []appliedBurn

	w, err := bridge.NewWatcher(bridge.WatcherConfig{
		Client:     client,
		Bridge:     b,
		Contract:   contract,
		StartBlock: *from,
		OnBurn: func(d bridge.DecodedBurn) {
			bal, _ := ledger.Balance(d.NativeRecipient)
			native, _ := token.ERC20ToNative(d.ERC20Amount)
			applied = append(applied, appliedBurn{
				ID:              d.ID,
				NativeRecipient: d.NativeRecipient,
				Native:          native,
				Balance:         bal,
			})
		},
		OnError: func(e error) { fmt.Fprintln(os.Stderr, "watch warning:", e) },
	})
	must(err)

	// Drive a single deterministic scan pass over [from, to]. We set the head to
	// `to` implicitly by asking Poll, which reads BlockNumber; to bound the scan
	// exactly we override the watcher's target by scanning until the cursor passes
	// `to`.
	for w.Cursor() <= to {
		before := w.Cursor()
		if _, err := w.Poll(ctx); err != nil {
			must(err)
		}
		if w.Cursor() == before {
			// No progress (e.g. waiting on confirmations); stop to avoid a spin.
			break
		}
	}

	// Sum the unlocked native across the applied burns. We deliberately do NOT
	// call Bridge.Reconcile here: this standalone relay does not hold the source
	// node's LOCK accounting (locks happened on the native chain, not here), so
	// the locked==escrow invariant Reconcile enforces does not apply to a
	// watch-only escrow we seeded for the demo. Reconcile is the right check
	// INSIDE matrixd, where the same bridge holds both the locks and the unlocks.
	var unlocked uint64
	for _, a := range applied {
		unlocked += a.Native
	}

	out := map[string]any{
		"contract":       contract.Hex(),
		"scannedFrom":    *from,
		"scannedTo":      to,
		"appliedBurns":   applied,
		"unlockedNative": unlocked,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	must(enc.Encode(out))
}

func parseUint(s string) (uint64, error) {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || !v.IsUint64() {
		return 0, fmt.Errorf("invalid block number %q", s)
	}
	return v.Uint64(), nil
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
