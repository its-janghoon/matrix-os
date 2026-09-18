package cli

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/inference"
	"github.com/ecirlabs/matrix-core/internal/inferenceapi"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// `matrix inference submit --escrowed` against a live node, end to end.
//
// It is the one path where a buyer keeps their own key AND sees the answer
// arrive, so what these check is that the money really moved the way the path
// claims: the deposit into the escrow account before anything runs, and the
// settlement out of it for what the answer cost rather than for the cap.

// startEscrowServer is startInferenceServer with the escrow rules ACTIVE and a
// caller-supplied buyer, so the wallet on disk and the buyer account are the
// same key - which is what --escrowed requires and refuses to proceed without.
func startEscrowServer(t *testing.T, buyer *token.Account) (addr, providerID string, mkt *market.Market, infSvc *inference.Service) {
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

	validator, _ := token.GenerateAccount()
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
		// Height 1 is the earliest a schedule can name: the genesis version sits
		// at height 0, so naming 0 would be two versions activating at once.
		ProtocolUpgrades: []consensus.ProtocolUpgrade{
			{Height: 1, Version: consensus.ProtocolVersionInferenceEscrow},
		},
	})
	if err != nil {
		t.Fatalf("consensus.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("engine.Start: %v", err)
	}

	// WAIT FOR THE ACTIVATION HEIGHT before anything is submitted.
	//
	// A schedule cannot name height 0 - the genesis version is already there -
	// so escrow turns on at height 1, and a chain that has only just started is
	// at height 0. A deposit submitted then is refused as "not yet" and waits in
	// the mempool, which on an otherwise idle chain is a wait for the stall timer
	// rather than for a block. That is a property of a chain one block old, not
	// of the path under test: on any real chain the activation is long past.
	waitForHeight(t, engine, 1)

	provider, _ := token.GenerateAccount()
	providerID = provider.AccountID()
	if err := mkt.Ledger().Credit(buyer.AccountID(), 1000); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if err := mkt.RegisterProvider(market.Provider{ID: providerID, Capacity: 10000, PricePerUnit: 1}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	registry := inference.NewRegistry()
	if err := registry.Register(providerID, inference.NewEchoBackend()); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	// An EMPTY accounts resolver. One is required, but it holds no key for the
	// buyer: the node must not be able to sign for them, or this test would pass
	// down the node-signed path and prove nothing about the one under test.
	infSvc, err = inference.NewService(inference.Config{
		Market:   mkt,
		Registry: registry,
		Settler:  engine,
		Accounts: memAccounts{m: map[string]*token.Account{}},
	})
	if err != nil {
		t.Fatalf("inference.NewService: %v", err)
	}

	srv, err := inferenceapi.NewServer(inferenceapi.Config{Addr: "127.0.0.1:0", Inference: infSvc})
	if err != nil {
		t.Fatalf("inferenceapi.NewServer: %v", err)
	}
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	return srv.Addr(), providerID, mkt, infSvc
}

// inferenceJobAwaitingSettlement leaves a funded job with its settlement
// computed and nobody holding it - what a crashed client, a closed tab or a
// refused bill leaves behind.
func inferenceJobAwaitingSettlement(
	t *testing.T, svc *inference.Service, buyer *token.Account, providerID string,
) string {
	t.Helper()
	job, err := svc.SubmitInferenceJob(buyer.AccountID(), providerID,
		inference.InferenceRequest{Prompt: "hello world"}, 500)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	plan, err := svc.ReserveEscrow(job.ID, nil)
	if err != nil {
		t.Fatalf("ReserveEscrow: %v", err)
	}
	// The BUYER's own key, passed in: this service holds none, on purpose, so
	// that nothing here can pass down the node-signed path by accident.
	tx := plan.Request.Transaction(buyer.PublicKey)
	if err := tx.Sign(buyer.PrivateKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := svc.FundEscrow(context.Background(), job.ID, tx); err != nil {
		t.Fatalf("FundEscrow: %v", err)
	}
	// Run it and DROP the settlement on the floor.
	if _, _, err := svc.StreamEscrowed(context.Background(), job.ID, nil, func(string) error { return nil }); err != nil {
		t.Fatalf("StreamEscrowed: %v", err)
	}
	return job.ID
}

// waitForHeight blocks until the chain has committed up to at least height h.
func waitForHeight(t *testing.T, engine *consensus.Engine, h uint64) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for engine.Height() < h {
		if time.Now().After(deadline) {
			t.Fatalf("the chain is still at height %d after 30s, want %d", engine.Height(), h)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCLI_InferenceSubmitEscrowed(t *testing.T) {
	walletPath := filepath.Join(t.TempDir(), "wallet.json")
	buyer, err := createWallet(walletPath)
	if err != nil {
		t.Fatalf("createWallet: %v", err)
	}
	addr, providerID, mkt, _ := startEscrowServer(t, buyer)

	out, err := run(t, "unused:0", "--json", "inference", "submit",
		"--escrowed",
		"--inference-addr", addr,
		"--buyer", buyer.AccountID(),
		"--provider", providerID,
		"--prompt", "hello world",
		"--units", "500",
		"--wallet", walletPath,
	)
	if err != nil {
		t.Fatalf("inference submit --escrowed: %v (%s)", err, out)
	}
	// The completion streamed to the same writer, so the JSON job is the tail of
	// the output rather than all of it. That interleaving is the point: the text
	// arrived before the job did.
	if !strings.Contains(out, "echo: user: hello world") {
		t.Fatalf("the answer did not stream: %s", out)
	}
	at := strings.Index(out, "{")
	if at < 0 {
		t.Fatalf("no job in the output: %s", out)
	}
	var job inferenceJobRow
	if err := json.Unmarshal([]byte(out[at:]), &job); err != nil {
		t.Fatalf("decode job json: %v (%s)", err, out[at:])
	}
	if !strings.Contains(strings.ToLower(job.Status), "completed") {
		t.Fatalf("job is %s, not completed: %s", job.Status, out)
	}

	// The money. A reservation of 500 was funded and all but the real cost came
	// back: the echo answer to "hello world" is 7 units at 1 each, and the buyer
	// started with 1000.
	buyerBal, _ := mkt.Ledger().Balance(buyer.AccountID())
	provBal, _ := mkt.Ledger().Balance(providerID)
	if provBal != 7 {
		t.Fatalf("provider received %d, want the 7 the answer cost", provBal)
	}
	if buyerBal != 993 {
		t.Fatalf("buyer holds %d, want 993: anything near 500 means the reservation was "+
			"not returned, which is the whole difference between this path and paying the cap",
			buyerBal)
	}
}

// The wallet must be the buyer's own, and saying so here beats letting the node
// answer with a signature mismatch several steps later - by which point a
// reservation may already be funded.
func TestCLI_InferenceSubmitEscrowedRefusesAStrangersWallet(t *testing.T) {
	walletPath := filepath.Join(t.TempDir(), "wallet.json")
	buyer, err := createWallet(walletPath)
	if err != nil {
		t.Fatalf("createWallet: %v", err)
	}
	addr, providerID, _, _ := startEscrowServer(t, buyer)
	stranger, _ := token.GenerateAccount()

	out, err := run(t, "unused:0", "inference", "submit",
		"--escrowed",
		"--inference-addr", addr,
		"--buyer", stranger.AccountID(),
		"--provider", providerID,
		"--prompt", "hello world",
		"--wallet", walletPath,
	)
	if err == nil {
		t.Fatalf("a stranger's wallet was accepted: %s", out)
	}
	if !strings.Contains(err.Error(), "they must match") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}

// --escrowed and --client-signed are two answers to the same question, and a
// command that quietly picked one would be paying by a rule the caller cannot
// see.
func TestCLI_InferenceSubmitRefusesBothPaymentPaths(t *testing.T) {
	_, err := run(t, "unused:0", "inference", "submit",
		"--escrowed", "--client-signed",
		"--buyer", "a", "--provider", "b", "--prompt", "c")
	if err == nil || !strings.Contains(err.Error(), "pick one") {
		t.Fatalf("both paths at once gave %v", err)
	}
}

// `matrix inference recover`, against a live node, on a job whose stream is gone.
//
// This is the way back from a refused bill. Without it, a buyer who exercises
// the one protection the escrowed path gives them - refusing a settlement they
// cannot account for - pays MORE than a buyer who signs whatever they are
// handed, because the reservation goes to the provider's claim at its expiry.
func TestCLI_InferenceRecoverSettlesAJobWhoseStreamIsGone(t *testing.T) {
	walletPath := filepath.Join(t.TempDir(), "wallet.json")
	buyer, err := createWallet(walletPath)
	if err != nil {
		t.Fatalf("createWallet: %v", err)
	}
	addr, providerID, mkt, svc := startEscrowServer(t, buyer)

	// Fund a reservation and run it, then throw the settlement away - which is
	// exactly what a crashed client, a closed tab or a refused bill leaves
	// behind.
	job := inferenceJobAwaitingSettlement(t, svc, buyer, providerID)

	before, _ := mkt.Ledger().Balance(buyer.AccountID())

	// READ ONLY first. It must show the bill and sign nothing.
	out, err := run(t, "unused:0", "inference", "recover",
		"--inference-addr", addr, "--id", job, "--wallet", walletPath)
	if err != nil {
		t.Fatalf("inference recover: %v (%s)", err, out)
	}
	if !strings.Contains(out, "Nothing was signed") {
		t.Fatalf("a read-only recover did not say so: %s", out)
	}
	if !strings.Contains(out, "asks:") {
		t.Fatalf("recover did not show the bill: %s", out)
	}
	after, _ := mkt.Ledger().Balance(buyer.AccountID())
	if after != before {
		t.Fatalf("a read-only recover moved money: %d -> %d", before, after)
	}

	// Then settle it.
	out, err = run(t, "unused:0", "inference", "recover", "--settle",
		"--inference-addr", addr, "--id", job, "--wallet", walletPath)
	if err != nil {
		t.Fatalf("inference recover --settle: %v (%s)", err, out)
	}
	if !strings.Contains(strings.ToLower(out), "completed") {
		t.Fatalf("the job is not completed after settling: %s", out)
	}
	// The balance goes UP, which is the whole point and is easy to assert
	// backwards: the reservation left the wallet when it was FUNDED, so settling
	// is when the change comes back. What the run really cost is the difference
	// between the starting balance and this one.
	settled, _ := mkt.Ledger().Balance(buyer.AccountID())
	if settled <= before {
		t.Fatalf("settling returned no change: %d -> %d; the reservation is still in escrow "+
			"and the provider will claim it", before, settled)
	}
	const started = 1000
	cost := started - settled
	if cost <= 0 {
		t.Fatalf("the run cost %d; consensus refuses a settlement of nothing", cost)
	}
	if cost >= 500 {
		t.Fatalf("the run cost %d of a 500 reservation - the change did not come back", cost)
	}
	t.Logf("recovered and settled: %d reserved, %d paid, %d returned", 500, cost, settled-before)
}

// A stranger cannot read a recovered settlement, because it carries the answer.
func TestCLI_InferenceRecoverRefusesAWalletThatCannotSettle(t *testing.T) {
	walletPath := filepath.Join(t.TempDir(), "wallet.json")
	buyer, err := createWallet(walletPath)
	if err != nil {
		t.Fatalf("createWallet: %v", err)
	}
	addr, providerID, _, svc := startEscrowServer(t, buyer)
	job := inferenceJobAwaitingSettlement(t, svc, buyer, providerID)

	strangerPath := filepath.Join(t.TempDir(), "wallet.json")
	if _, err := createWallet(strangerPath); err != nil {
		t.Fatalf("createWallet: %v", err)
	}
	out, err := run(t, "unused:0", "inference", "recover",
		"--inference-addr", addr, "--id", job, "--wallet", strangerPath)
	if err == nil {
		t.Fatalf("a stranger read the settlement and the answer: %s", out)
	}
	if !strings.Contains(err.Error(), "can settle this job") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}
