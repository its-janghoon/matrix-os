package node

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/inference"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// TestWalletAccounts_ResolvesFromMemoryAndDisk asserts the node's inference
// Accounts resolver finds signing keys both from in-memory registrations (Add)
// and from wallet files on disk, and reports missing accounts honestly.
func TestWalletAccounts_ResolvesFromMemoryAndDisk(t *testing.T) {
	dir := t.TempDir()

	// An account registered in memory resolves.
	mem, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	wa := newWalletAccountsDir(dir)
	wa.Add(mem)
	got, ok := wa.Account(mem.AccountID())
	if !ok || got.AccountID() != mem.AccountID() {
		t.Fatalf("expected in-memory account to resolve")
	}

	// An account written as a wallet file on disk resolves.
	disk, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	writeWalletFile(t, filepath.Join(dir, "wallet.json"), disk)
	gotDisk, ok := wa.Account(disk.AccountID())
	if !ok || gotDisk.AccountID() != disk.AccountID() {
		t.Fatalf("expected on-disk wallet account to resolve")
	}

	// A resolvable account signs verifiably (proves key material round-tripped).
	tx := &token.Transaction{From: gotDisk.PublicKey, To: "someone", Amount: 1, Nonce: 0}
	if err := tx.Sign(gotDisk.PrivateKey); err != nil {
		t.Fatalf("sign with resolved account: %v", err)
	}
	if err := tx.Verify(); err != nil {
		t.Fatalf("verify signature from resolved account: %v", err)
	}

	// An unknown account does not resolve.
	if _, ok := wa.Account("deadbeef"); ok {
		t.Fatalf("expected unknown account to not resolve")
	}
}

// TestNodeInferenceWiring_EndToEnd proves the exact wiring the node builds in
// Start (inference.NewService over the market, a consensus-backed settler, the
// GPU-free echo backend registered for the demo provider, and the node's
// walletAccounts resolver resolving the buyer from an on-disk wallet) settles an
// inference job end to end: reserve -> fulfill -> settle -> completed, with the
// balances moving on the shared ledger by the deterministic echo cost.
func TestNodeInferenceWiring_EndToEnd(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}

	// Single-validator consensus, exactly as the node runs a solo/dev node.
	validator, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount(validator): %v", err)
	}
	vs, err := consensus.NewValidatorSet([]ed25519.PublicKey{validator.PublicKey})
	if err != nil {
		t.Fatalf("NewValidatorSet: %v", err)
	}
	engine, err := consensus.New(consensus.Config{
		Transport:  newMemBus(),
		Validators: vs,
		Chain:      consensus.NewBlockChain(store),
		Ledger:     mkt.Ledger(),
		Self:       validator,
	})
	if err != nil {
		t.Fatalf("consensus.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("engine.Start: %v", err)
	}

	// The buyer's wallet lives on disk; the node's walletAccounts resolver must
	// find it there (the honest dev-custody model documented in
	// inference_accounts.go).
	walletDir := t.TempDir()
	buyer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount(buyer): %v", err)
	}
	writeWalletFile(t, filepath.Join(walletDir, "wallet.json"), buyer)
	buyerID := buyer.AccountID()

	const demoProvider = "demo-inference-provider"
	const initialCredits = 1000
	if err := mkt.Ledger().Credit(buyerID, initialCredits); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if err := mkt.RegisterProvider(market.Provider{ID: demoProvider, Capacity: 10000, PricePerUnit: 1}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	// Build the SAME pieces node.Start builds: registry with the echo backend for
	// the demo provider, the walletAccounts resolver, and inference.NewService
	// over the consensus engine.
	registry := inference.NewRegistry()
	if err := registry.Register(demoProvider, inference.NewEchoBackend()); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	accts := newWalletAccountsDir(walletDir)
	svc, err := inference.NewService(inference.Config{
		Market:   mkt,
		Registry: registry,
		Settler:  engine,
		Accounts: accts,
	})
	if err != nil {
		t.Fatalf("inference.NewService: %v", err)
	}

	job, err := svc.SubmitInferenceJob(buyerID, demoProvider, inference.InferenceRequest{
		Model:  "stub",
		Prompt: "hello world",
	}, 8)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	if job.Status != inference.InferenceJobPending {
		t.Fatalf("expected PENDING, got %q", job.Status)
	}

	fulfilled, err := svc.FulfillJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("FulfillJob: %v", err)
	}
	if fulfilled.Status != inference.InferenceJobCompleted {
		t.Fatalf("expected COMPLETED, got %q", fulfilled.Status)
	}
	if fulfilled.Completion != "echo: user: hello world" {
		t.Fatalf("unexpected completion: %q", fulfilled.Completion)
	}
	const wantUnits = 7
	if fulfilled.Units != wantUnits {
		t.Fatalf("settled units = %d, want %d", fulfilled.Units, wantUnits)
	}

	buyerBal, _ := mkt.Ledger().Balance(buyerID)
	provBal, _ := mkt.Ledger().Balance(demoProvider)
	if buyerBal != initialCredits-wantUnits || provBal != wantUnits {
		t.Fatalf("balances: buyer=%d provider=%d, want buyer=%d provider=%d",
			buyerBal, provBal, initialCredits-wantUnits, wantUnits)
	}
}

// writeWalletFile writes acct to path in the same hex-field JSON layout the CLI
// wallet uses, so the node's walletAccounts resolver can read it back.
func writeWalletFile(t *testing.T, path string, acct *token.Account) {
	t.Helper()
	wf := walletDiskFile{
		PublicKey:  hex.EncodeToString(acct.PublicKey),
		PrivateKey: hex.EncodeToString(acct.PrivateKey),
	}
	data, err := json.Marshal(wf)
	if err != nil {
		t.Fatalf("marshal wallet: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write wallet: %v", err)
	}
}
