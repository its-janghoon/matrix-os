package cli

import (
	"strings"
	"testing"
)

// TestCLI_Quickstart drives `matrix quickstart` against a live in-process
// MarketService whose FundAccount RPC is backed by a real Treasury with a
// genesis reward pool (the FEAT-002 funding path). It asserts the full loop
// runs: a wallet is created, the buyer is funded from the reward pool, a demo
// provider is registered, a job is submitted and completed, and native MATRIX
// actually moves buyer -> provider on the ledger.
func TestCLI_Quickstart(t *testing.T) {
	addr, mkt := startFundServer(t, 1_000_000_000)

	walletPath := t.TempDir() + "/wallet.json"

	// Small deterministic params so the arithmetic is easy to assert:
	// fund 1000, provider price 5, job of 10 units => settle 50.
	out, err := runFund(t, addr, "quickstart",
		"--wallet", walletPath,
		"--fund", "1000",
		"--provider", "qs-prov",
		"--price", "5",
		"--capacity", "100",
		"--units", "10",
	)
	if err != nil {
		t.Fatalf("quickstart: %v (%s)", err, out)
	}

	// Each numbered step should have printed.
	for _, want := range []string{
		"1. wallet created",
		"2. funded buyer with 1000",
		"3. registered provider \"qs-prov\"",
		"4. submitted job",
		"5. completed job",
		"done. native MATRIX moved buyer -> provider",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in quickstart output:\n%s", want, out)
		}
	}

	// The demo job is 10 units @ 5 = 50, settled buyer -> provider. Read the
	// buyer's account ID back from the created wallet to assert on-ledger balances.
	buyer, err := loadWallet(walletPath)
	if err != nil {
		t.Fatalf("loadWallet: %v", err)
	}
	buyerBal, _ := mkt.Ledger().Balance(buyer.AccountID())
	if buyerBal != 1000-50 {
		t.Fatalf("buyer ledger balance = %d, want %d", buyerBal, 1000-50)
	}
	provBal, _ := mkt.Ledger().Balance("qs-prov")
	if provBal != 50 {
		t.Fatalf("provider ledger balance = %d, want 50", provBal)
	}
}

// TestCLI_QuickstartReusesWallet asserts a second run reuses the existing wallet
// (idempotent) rather than failing to overwrite it, and still completes a job.
func TestCLI_QuickstartReusesWallet(t *testing.T) {
	addr, _ := startFundServer(t, 1_000_000_000)
	walletPath := t.TempDir() + "/wallet.json"

	if out, err := runFund(t, addr, "quickstart", "--wallet", walletPath, "--fund", "1000", "--provider", "p1"); err != nil {
		t.Fatalf("first quickstart: %v (%s)", err, out)
	}
	out, err := runFund(t, addr, "quickstart", "--wallet", walletPath, "--fund", "1000", "--provider", "p1")
	if err != nil {
		t.Fatalf("second quickstart: %v (%s)", err, out)
	}
	if !strings.Contains(out, "using existing wallet") {
		t.Fatalf("expected the second run to reuse the wallet:\n%s", out)
	}
}
