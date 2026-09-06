package cli

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/inference"
	"github.com/ecirlabs/matrix-core/internal/inferenceapi"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/ecirlabs/matrix-core/internal/transport"
)

// memBus is a minimal in-memory gossip bus so a single-node consensus engine can
// back the inference settlement path without libp2p, mirroring the pattern in
// internal/inferenceapi/integration_test.go.
type memBus struct {
	subs map[string][]chan transport.Message
}

func newMemBus() *memBus { return &memBus{subs: make(map[string][]chan transport.Message)} }

func (b *memBus) Subscribe(ctx context.Context, topic string) (<-chan transport.Message, error) {
	ch := make(chan transport.Message, 1024)
	b.subs[topic] = append(b.subs[topic], ch)
	go func() { <-ctx.Done() }()
	return ch, nil
}

func (b *memBus) Publish(ctx context.Context, topic string, data []byte) error {
	for _, c := range b.subs[topic] {
		select {
		case c <- transport.Message{Topic: topic, Payload: append([]byte(nil), data...)}:
		default:
		}
	}
	return nil
}

// memAccounts is an in-memory inference Accounts resolver keyed by account ID.
type memAccounts struct{ m map[string]*token.Account }

func (a memAccounts) Account(id string) (*token.Account, bool) { acct, ok := a.m[id]; return acct, ok }

// startInferenceServer stands up an in-process inferenceapi server backed by a
// real inference.Service (consensus-backed settler, echo backend), returning the
// bound address plus the buyer/provider IDs and market so callers can assert.
func startInferenceServer(t *testing.T) (addr, buyerID, providerID string, mkt *market.Market) {
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
	})
	if err != nil {
		t.Fatalf("consensus.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("engine.Start: %v", err)
	}

	buyer, _ := token.GenerateAccount()
	provider, _ := token.GenerateAccount()
	buyerID = buyer.AccountID()
	providerID = provider.AccountID()
	if err := mkt.Ledger().Credit(buyerID, 1000); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if err := mkt.RegisterProvider(market.Provider{ID: providerID, Capacity: 10000, PricePerUnit: 1}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	registry := inference.NewRegistry()
	if err := registry.Register(providerID, inference.NewEchoBackend()); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	infSvc, err := inference.NewService(inference.Config{
		Market:   mkt,
		Registry: registry,
		Settler:  engine,
		Accounts: memAccounts{m: map[string]*token.Account{buyerID: buyer}},
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

	addr = srv.Addr()
	if addr == "" {
		t.Fatal("inference server address empty after Start")
	}
	return addr, buyerID, providerID, mkt
}

// TestCLI_InferenceSubmit drives `matrix inference submit` in-process against a
// live InferenceService and asserts the reserve -> fulfill -> settle -> completed
// loop: the returned job is COMPLETED with the deterministic echo completion and
// the settled units are reflected on the ledger.
func TestCLI_InferenceSubmit(t *testing.T) {
	addr, buyerID, providerID, mkt := startInferenceServer(t)

	out, err := run(t, "unused:0", "inference", "submit",
		"--inference-addr", addr,
		"--buyer", buyerID,
		"--provider", providerID,
		"--prompt", "hello world",
		"--units", "8",
	)
	if err != nil {
		t.Fatalf("inference submit: %v (%s)", err, out)
	}
	if !strings.Contains(out, "completed") {
		t.Fatalf("expected completed status: %s", out)
	}
	if !strings.Contains(out, "echo: user: hello world") {
		t.Fatalf("expected echo completion: %s", out)
	}

	// The deterministic echo cost for "hello world" is 7 units; the buyer paid it
	// and the provider received it on the shared ledger.
	buyerBal, _ := mkt.Ledger().Balance(buyerID)
	provBal, _ := mkt.Ledger().Balance(providerID)
	if buyerBal != 993 || provBal != 7 {
		t.Fatalf("balances: buyer=%d provider=%d, want buyer=993 provider=7", buyerBal, provBal)
	}
}

// TestCLI_InferenceGet asserts a submitted job can be fetched back by ID via
// `matrix inference get`.
func TestCLI_InferenceGet(t *testing.T) {
	addr, buyerID, providerID, _ := startInferenceServer(t)

	// Submit (with fulfill) and read the job id out of JSON output.
	out, err := run(t, "unused:0", "--json", "inference", "submit",
		"--inference-addr", addr,
		"--buyer", buyerID,
		"--provider", providerID,
		"--prompt", "hello world",
	)
	if err != nil {
		t.Fatalf("inference submit: %v (%s)", err, out)
	}
	var submitted inferenceJobRow
	if err := json.Unmarshal([]byte(out), &submitted); err != nil {
		t.Fatalf("decode submit json: %v (%s)", err, out)
	}
	if submitted.ID == "" {
		t.Fatalf("expected job id in submit output: %s", out)
	}

	getOut, err := run(t, "unused:0", "inference", "get",
		"--inference-addr", addr,
		"--id", submitted.ID,
	)
	if err != nil {
		t.Fatalf("inference get: %v (%s)", err, getOut)
	}
	if !strings.Contains(getOut, submitted.ID) {
		t.Fatalf("expected job id %s in get output: %s", submitted.ID, getOut)
	}
	if !strings.Contains(getOut, "completed") {
		t.Fatalf("expected completed status in get output: %s", getOut)
	}
}
