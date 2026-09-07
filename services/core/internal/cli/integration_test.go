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

// startServer stands up an in-process MarketService on 127.0.0.1:0, reusing the
// exact harness pattern from internal/marketapi/integration_test.go (kv.New temp
// dir, market.NewMarket, token chain + settled ledger, NewServer). It returns
// the bound address and the backing market/chain for direct assertions.
func startServer(t *testing.T) (addr string, mkt *market.Market, chain *token.Chain) {
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
	chain = token.NewChain(store)
	settled := token.NewSettledLedger(mkt.Ledger(), chain)

	srv, err := marketapi.NewServer(marketapi.Config{
		Addr:    "127.0.0.1:0",
		Market:  mkt,
		Settled: settled,
		Chain:   chain,
		// Exercise the CLI against the consensus settlement path it now targets:
		// the transfer settler moves credits ignoring prev_hash (a consensus
		// transfer uses nonce only as a uniquifier) and records committed history
		// for `tx list`/`tx get`. This mirrors the node's real wiring closely
		// enough to test the CLI's request/response handling without standing up
		// a full consensus engine in a CLI unit test.
		TransferSettler: newFakeConsensusSettler(mkt),
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
	return addr, mkt, chain
}

// run executes the root command with args against addr, returning combined
// output and error. Each call constructs a fresh command tree so flag state
// never leaks between invocations.
func run(t *testing.T, addr string, args ...string) (string, error) {
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

// TestCLI_EndToEnd drives the CLI command functions against a live in-process
// MarketService and asserts the RPC round-trips: health, RegisterProvider,
// ListProviders, a signed wallet transfer (SubmitSignedTransfer) funding a
// buyer, GetBalance, and SubmitJob.
func TestCLI_EndToEnd(t *testing.T) {
	addr, mkt, _ := startServer(t)

	// health -> SERVING
	out, err := run(t, addr, "health")
	if err != nil {
		t.Fatalf("health: %v (%s)", err, out)
	}
	if !strings.Contains(out, "SERVING") {
		t.Fatalf("expected SERVING, got: %s", out)
	}

	// provider register
	out, err = run(t, addr, "provider", "register", "--id", "provider-1", "--capacity", "100", "--price", "5")
	if err != nil {
		t.Fatalf("provider register: %v (%s)", err, out)
	}
	if !strings.Contains(out, "provider-1") {
		t.Fatalf("expected provider-1 in output: %s", out)
	}

	// provider list
	out, err = run(t, addr, "provider", "list")
	if err != nil {
		t.Fatalf("provider list: %v (%s)", err, out)
	}
	if !strings.Contains(out, "provider-1") {
		t.Fatalf("expected provider-1 listed: %s", out)
	}

	// Create a treasury wallet on disk and fund it directly on the ledger so it
	// can be the signed sender of a transfer.
	walletDir := t.TempDir()
	treasuryPath := walletDir + "/treasury.json"
	treasury, err := createWallet(treasuryPath)
	if err != nil {
		t.Fatalf("createWallet(treasury): %v", err)
	}
	if err := mkt.Ledger().Credit(treasury.AccountID(), 1000); err != nil {
		t.Fatalf("Credit treasury: %v", err)
	}

	// A buyer wallet that will receive credits via a signed transfer.
	buyer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	buyerID := buyer.AccountID()

	// wallet transfer: treasury -> buyer, 200 (derives prev_hash + nonce over gRPC).
	out, err = run(t, addr, "wallet", "transfer", "--wallet", treasuryPath, "--to", buyerID, "--amount", "200")
	if err != nil {
		t.Fatalf("wallet transfer: %v (%s)", err, out)
	}
	if !strings.Contains(out, buyerID) {
		t.Fatalf("expected transfer to %s in output: %s", buyerID, out)
	}

	// wallet balance (treasury) via --wallet -> 800
	out, err = run(t, addr, "wallet", "balance", "--wallet", treasuryPath)
	if err != nil {
		t.Fatalf("wallet balance: %v (%s)", err, out)
	}
	if !strings.Contains(out, "800") {
		t.Fatalf("expected treasury balance 800: %s", out)
	}

	// balance --account (buyer) -> 200
	out, err = run(t, addr, "balance", "--account", buyerID)
	if err != nil {
		t.Fatalf("balance: %v (%s)", err, out)
	}
	if !strings.Contains(out, "200") {
		t.Fatalf("expected buyer balance 200: %s", out)
	}

	// job submit: buyer buys 10 units @ 5 = 50
	out, err = run(t, addr, "job", "submit", "--buyer", buyerID, "--provider", "provider-1", "--units", "10")
	if err != nil {
		t.Fatalf("job submit: %v (%s)", err, out)
	}
	if !strings.Contains(out, "pending") {
		t.Fatalf("expected pending job: %s", out)
	}

	// tx list should show the one settled transfer with JSON output.
	out, err = run(t, addr, "--json", "tx", "list")
	if err != nil {
		t.Fatalf("tx list: %v (%s)", err, out)
	}
	if !strings.Contains(out, "\"total\": 1") {
		t.Fatalf("expected total 1 in json: %s", out)
	}
}

// TestCLI_SecondTransferNonce asserts the client-derived nonce advances so a
// second transfer from the same sender is accepted (nonce=1) rather than
// rejected as a replay.
func TestCLI_SecondTransferNonce(t *testing.T) {
	addr, mkt, _ := startServer(t)

	dir := t.TempDir()
	senderPath := dir + "/sender.json"
	sender, err := createWallet(senderPath)
	if err != nil {
		t.Fatalf("createWallet: %v", err)
	}
	if err := mkt.Ledger().Credit(sender.AccountID(), 1000); err != nil {
		t.Fatalf("Credit: %v", err)
	}

	recip, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	recipID := recip.AccountID()

	if out, err := run(t, addr, "wallet", "transfer", "--wallet", senderPath, "--to", recipID, "--amount", "10"); err != nil {
		t.Fatalf("first transfer: %v (%s)", err, out)
	}
	if out, err := run(t, addr, "wallet", "transfer", "--wallet", senderPath, "--to", recipID, "--amount", "20"); err != nil {
		t.Fatalf("second transfer (nonce should advance): %v (%s)", err, out)
	}

	// Recipient should have 30 across the two transfers.
	out, err := run(t, addr, "balance", "--account", recipID)
	if err != nil {
		t.Fatalf("balance: %v (%s)", err, out)
	}
	if !strings.Contains(out, "30") {
		t.Fatalf("expected recipient balance 30: %s", out)
	}
}

// fakeConsensusSettler is a marketapi.TransferSettler that stands in for the
// consensus engine in CLI tests: it applies a verified signed transfer directly
// to the market ledger (moving credits and skipping an unaffordable one, as the
// consensus apply path does), ignores prev_hash (a consensus transfer uses nonce
// only as a uniquifier), and records committed transfers so the history RPCs the
// CLI reads return the same ordered sequence. It preserves the guards the RPC
// relies on (signature/authZ, non-empty recipient, no self-transfer).
type fakeConsensusSettler struct {
	mkt     *market.Market
	history []marketapi.TransferView
}

func newFakeConsensusSettler(mkt *market.Market) *fakeConsensusSettler {
	return &fakeConsensusSettler{mkt: mkt}
}

func (f *fakeConsensusSettler) SettleSignedTransfer(_ context.Context, tx *token.Transaction) (*marketapi.SettledTransferResult, error) {
	if err := tx.Verify(); err != nil {
		return nil, err
	}
	sender := tx.SenderID()
	if tx.To == "" {
		return nil, token.ErrEmptyRecipient
	}
	if tx.To == sender {
		return nil, token.ErrSelfTransfer
	}
	result := &marketapi.SettledTransferResult{
		Transfer: marketapi.TransferView{
			From:      sender,
			To:        tx.To,
			Amount:    tx.Amount,
			Nonce:     tx.Nonce,
			Timestamp: tx.Timestamp,
		},
	}
	// Affordability: consensus deterministically skips an unaffordable transfer.
	bal, err := f.mkt.Ledger().Balance(sender)
	if err != nil {
		return nil, err
	}
	if bal < tx.Amount {
		result.Committed = true
		result.Applied = false
		return result, errTransferUnaffordable
	}
	if err := f.mkt.Ledger().Transfer(sender, tx.To, tx.Amount); err != nil {
		return nil, err
	}
	idx := uint64(len(f.history))
	view := marketapi.TransferView{
		Index:       idx,
		From:        sender,
		To:          tx.To,
		Amount:      tx.Amount,
		Nonce:       tx.Nonce,
		BlockHeight: idx,
		Timestamp:   tx.Timestamp,
	}
	f.history = append(f.history, view)
	result.Transfer = view
	result.Committed = true
	result.Applied = true
	return result, nil
}

func (f *fakeConsensusSettler) History(start uint64, limit int) ([]marketapi.TransferView, uint64, error) {
	total := uint64(len(f.history))
	if start >= total {
		return nil, total, nil
	}
	out := f.history[start:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return append([]marketapi.TransferView(nil), out...), total, nil
}

func (f *fakeConsensusSettler) TransferAt(index uint64) (*marketapi.TransferView, error) {
	if index >= uint64(len(f.history)) {
		return nil, marketapi.ErrTransferNotFound
	}
	v := f.history[index]
	return &v, nil
}

// errTransferUnaffordable mirrors an unaffordable consensus settlement; it maps
// to FailedPrecondition via marketapi.mapSettlementError's default case.
var errTransferUnaffordable = errorsNew("transfer did not apply (unaffordable)")

// errorsNew is a tiny indirection so this test file does not need an errors
// import just for one sentinel.
func errorsNew(msg string) error { return &settlerError{msg} }

type settlerError struct{ msg string }

func (e *settlerError) Error() string { return e.msg }

// TestCLI_ConnectionRefused asserts a readable error when no node is listening.
func TestCLI_ConnectionRefused(t *testing.T) {
	// Nothing listening on this port.
	out, err := run(t, "127.0.0.1:1", "--timeout", "2s", "health")
	if err == nil {
		t.Fatalf("expected error dialing dead endpoint, got output: %s", out)
	}
	if !strings.Contains(err.Error(), "cannot reach node") {
		t.Fatalf("expected friendly connection error, got: %v", err)
	}
}
