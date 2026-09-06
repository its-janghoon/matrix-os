package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/marketapi"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// startFundServer stands up an in-process MarketService whose FundAccount RPC is
// backed by a real Treasury with a genesis reward pool, so the `matrix fund`
// command can be exercised end to end in-process. It returns the bound address
// and the backing market for balance assertions.
func startFundServer(t *testing.T, rewardPool uint64) (addr string, mkt *market.Market) {
	t.Helper()

	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mkt, err = market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}
	chain := token.NewChain(store)
	settled := token.NewSettledLedger(mkt.Ledger(), chain)
	treasury := token.NewTreasury(mkt.Ledger(), store)
	if err := treasury.ApplyGenesis(nil, rewardPool); err != nil {
		t.Fatalf("ApplyGenesis: %v", err)
	}

	srv, err := marketapi.NewServer(marketapi.Config{
		Addr:    "127.0.0.1:0",
		Market:  mkt,
		Settled: settled,
		Chain:   chain,
		Funder:  treasury,
	})
	if err != nil {
		t.Fatalf("marketapi.NewServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	addr = srv.Addr()
	if addr == "" {
		t.Fatal("server address empty after Start")
	}
	return addr, mkt
}

// runOut executes the root command with args against addr, returning combined
// output and error. It mirrors integration_test.go's run helper.
func runFund(t *testing.T, addr string, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"--addr", addr}, args...)
	root := NewRootCommand()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(full)
	err := root.Execute()
	return buf.String(), err
}

// TestCLI_Fund drives `matrix fund` against a live in-process server: it funds a
// buyer from the reward pool and asserts the printed balance and the on-ledger
// balance both reflect the funded amount.
func TestCLI_Fund(t *testing.T) {
	addr, mkt := startFundServer(t, 1_000_000)

	out, err := runFund(t, addr, "fund", "--account", "buyer-1", "--amount", "250000")
	if err != nil {
		t.Fatalf("fund: %v (%s)", err, out)
	}
	if !strings.Contains(out, "buyer-1") || !strings.Contains(out, "250000") {
		t.Fatalf("expected funded account + balance in output: %s", out)
	}

	bal, _ := mkt.Ledger().Balance("buyer-1")
	if bal != 250000 {
		t.Fatalf("ledger balance = %d, want 250000", bal)
	}

	// A second fund tops up and the printed balance reflects the cumulative total.
	out, err = runFund(t, addr, "fund", "--account", "buyer-1", "--amount", "50000")
	if err != nil {
		t.Fatalf("fund (top up): %v (%s)", err, out)
	}
	if !strings.Contains(out, "300000") {
		t.Fatalf("expected cumulative balance 300000: %s", out)
	}
}

// TestCLI_FundValidation asserts required-flag validation fires before any RPC.
func TestCLI_FundValidation(t *testing.T) {
	if _, err := executeArgs(t, "fund", "--amount", "1"); err == nil {
		t.Error("expected error when --account is missing")
	}
	if _, err := executeArgs(t, "fund", "--account", "x"); err == nil {
		t.Error("expected error when --amount is missing/zero")
	}
}
