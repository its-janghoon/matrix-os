// Command bridge-attest is a LOCAL, TEST-ONLY helper that demonstrates the Go
// side of the native<->wrapped bridge and emits a real, on-chain-verifiable
// attestation as JSON so the Solidity/hardhat end-to-end flow can mint against
// it.
//
// It does NOT read or write any real key material: attestor secp256k1 keys are
// derived deterministically from a plain-text seed via sha256 so the same
// invocation always yields the same local test addresses. NEVER use this key
// derivation for anything holding real funds; it exists purely to make the
// lock -> attest -> mint flow reproducible on a local hardhat node.
//
// Usage:
//
//	go run ./cmd/bridge-attest \
//	  -recipient 0xabc...   # 20-byte ethereum address to receive wMATRIX
//	  -native 4000000000    # native base units to lock (9 decimals)
//	  -chain-id 31337       # hardhat chain id
//	  -contract 0x...       # deployed WrappedMatrix address
//	  -threshold 2 -validators 3
//
// It prints a JSON object with the attestor addresses (register these in the
// contract), the lockId, the wrapped amount, and the ordered signatures ready
// for WrappedMatrix.mint.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"

	"github.com/ecirlabs/matrix-core/internal/bridge"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
)

func main() {
	recipientHex := flag.String("recipient", "", "ethereum recipient address (0x + 40 hex)")
	native := flag.Uint64("native", 0, "native base units to lock (9 decimals)")
	chainID := flag.Int64("chain-id", 31337, "EVM chain id the WrappedMatrix is deployed on")
	contractHex := flag.String("contract", "", "deployed WrappedMatrix contract address")
	threshold := flag.Int("threshold", 2, "signatures required to mint")
	validators := flag.Int("validators", 3, "number of validator attestors")
	seed := flag.String("seed", "matrix-local-test", "deterministic local test-key seed (NOT a secret)")
	flag.Parse()

	if *recipientHex == "" || *contractHex == "" || *native == 0 {
		fmt.Fprintln(os.Stderr, "recipient, contract, and native are required")
		flag.Usage()
		os.Exit(2)
	}
	if *threshold < 1 || *threshold > *validators {
		fmt.Fprintln(os.Stderr, "require 1 <= threshold <= validators")
		os.Exit(2)
	}

	recipient, err := bridge.ParseAddress(*recipientHex)
	must(err)
	contract, err := bridge.ParseAddress(*contractHex)
	must(err)

	// Derive local test attestors deterministically from the seed.
	signers := make([]bridge.ValidatorSigner, 0, *validators)
	attestorAddrs := make([]string, 0, *validators)
	for i := 0; i < *validators; i++ {
		key := sha256.Sum256([]byte(fmt.Sprintf("%s/attestor/%d", *seed, i)))
		att, err := bridge.NewAttestorFromBytes(key[:])
		must(err)
		signers = append(signers, bridge.ValidatorSigner{Label: fmt.Sprintf("v%d", i), Attestor: att})
		attestorAddrs = append(attestorAddrs, att.Address().Hex())
	}

	// Lock on a throwaway in-memory-ish ledger to produce a real LockEvent and
	// then attest with the first `threshold` validators.
	dir, err := os.MkdirTemp("", "bridge-attest")
	must(err)
	defer os.RemoveAll(dir)
	store, err := kv.New(kv.Config{Path: filepath.Join(dir, "db")})
	must(err)
	defer store.Close()

	ledger := market.NewLedger(store)
	must(ledger.Credit("local-user", *native))

	params := bridge.AttestationParams{ChainID: big.NewInt(*chainID), BridgeContract: contract}
	b := bridge.New(ledger, store, params)

	ev, err := b.Lock("local-user", recipient, *native)
	must(err)

	att, err := b.Attest(ev, signers[:*threshold])
	must(err)
	must(att.SortSignaturesForChain(params))

	rec, err := b.Reconcile()
	must(err)

	out := map[string]any{
		"attestors":          attestorAddrs,
		"threshold":          *threshold,
		"recipient":          recipient.Hex(),
		"lockId":             "0x" + hex.EncodeToString(att.LockID[:]),
		"nativeAmount":       *native,
		"wrappedAmount":      att.Amount.String(),
		"signatures":         hexSignatures(att.Signatures),
		"outstandingNative":  rec.OutstandingNative,
		"outstandingWrapped": rec.OutstandingERC20.String(),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	must(enc.Encode(out))
}

func hexSignatures(sigs [][]byte) []string {
	out := make([]string, len(sigs))
	for i, s := range sigs {
		out[i] = "0x" + hex.EncodeToString(s)
	}
	return out
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
