package inferenceapi

import (
	"context"
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	inferencev1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/inference/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/inference"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// The stream's FINAL FRAME must carry the working the settlement bills for.
//
// inference.GetJob blanks the completion and the working while a job awaits
// payment, and on the client-signed path that withholding IS the enforcement -
// the provider has already done the work by then, and the text is the only thing
// holding the buyer to the bargain.
//
// The escrowed path inherits that method and must not inherit the rule. Here the
// provider already holds the reservation, so nothing is being enforced; what the
// rule does instead is bill a buyer for text they are forbidden to read. Their
// own ceiling, computed without it, then comes out TIGHTER than the node's and
// refuses an invoice the node considers honest - which reads to them as a seller
// overcharging.
//
// The first live escrowed sale on this chain did exactly that: 479 units asked,
// 193 the most the buyer could account for, and the difference was working they
// had been charged for and never shown.

// workingBackend answers briefly and thinks at length, the shape that makes the
// two ceilings differ.
type workingBackend struct{ answer, working string }

func (b workingBackend) Name() string { return "working" }

func (b workingBackend) Infer(context.Context, inference.InferenceRequest) (inference.InferenceResponse, error) {
	u := inference.Usage{PromptTokens: 12, CompletionTokens: 300, TotalTokens: 312}
	return inference.InferenceResponse{
		Model:      "m",
		Completion: b.answer,
		Reasoning:  b.working,
		Usage:      u,
		Units:      inference.UnitsFor(u),
	}, nil
}

// collectStream captures what a server-streaming handler sends.
type collectStream struct {
	grpc.ServerStream
	ctx   context.Context
	frame []*inferencev1.StreamEscrowedInferenceJobResponse
}

func (c *collectStream) Context() context.Context { return c.ctx }
func (c *collectStream) Send(m *inferencev1.StreamEscrowedInferenceJobResponse) error {
	c.frame = append(c.frame, m)
	return nil
}
func (c *collectStream) SetHeader(metadata.MD) error  { return nil }
func (c *collectStream) SendHeader(metadata.MD) error { return nil }
func (c *collectStream) SetTrailer(metadata.MD)       {}

func TestTheFinalFrameCarriesTheWorkingTheBillCountsIt(t *testing.T) {
	const answer = "A marketplace connects buyers and sellers."
	working := strings.Repeat("weighing how to phrase this. ", 40)

	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}
	validator, _ := token.GenerateAccount()
	vs, err := consensus.NewValidatorSet([]ed25519.PublicKey{validator.PublicKey})
	if err != nil {
		t.Fatalf("NewValidatorSet: %v", err)
	}
	engine, err := consensus.New(consensus.Config{
		Transport: newMemBus(), Validators: vs, Chain: consensus.NewBlockChain(store),
		Ledger: mkt.Ledger(), Self: validator,
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

	// Escrow turns on at height 1 - a schedule cannot name 0, where the genesis
	// version already sits - so a deposit submitted at height 0 waits in the
	// mempool as "not yet" and the client's five-second wait expires first. A
	// property of a chain one block old, not of the path under test.
	deadline := time.Now().Add(30 * time.Second)
	for engine.Height() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("the chain is still at height %d after 30s", engine.Height())
		}
		time.Sleep(20 * time.Millisecond)
	}

	buyer, _ := token.GenerateAccount()
	provider, _ := token.GenerateAccount()
	buyerID, providerID := buyer.AccountID(), provider.AccountID()
	if err := mkt.Ledger().Credit(buyerID, 10_000_000); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if err := mkt.RegisterProvider(market.Provider{ID: providerID, Capacity: 1_000_000, PricePerUnit: 1}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	registry := inference.NewRegistry()
	if err := registry.Register(providerID, workingBackend{answer: answer, working: working}); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	infSvc, err := inference.NewService(inference.Config{
		Market: mkt, Registry: registry, Settler: engine,
		Accounts: memAccounts{m: map[string]*token.Account{buyerID: buyer}},
	})
	if err != nil {
		t.Fatalf("inference.NewService: %v", err)
	}
	svc := &Service{inf: infSvc}

	job, err := infSvc.SubmitInferenceJob(buyerID, providerID,
		inference.InferenceRequest{Prompt: "what is a marketplace for?", Model: "m"}, 4000)
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	plan, err := infSvc.ReserveEscrow(job.ID, nil)
	if err != nil {
		t.Fatalf("ReserveEscrow: %v", err)
	}
	tx := plan.Request.Transaction(buyer.PublicKey)
	if err := tx.Sign(buyer.PrivateKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := infSvc.FundEscrow(context.Background(), job.ID, tx); err != nil {
		t.Fatalf("FundEscrow: %v", err)
	}

	cs := &collectStream{ctx: context.Background()}
	if err := svc.StreamEscrowedInferenceJob(
		&inferencev1.StreamEscrowedInferenceJobRequest{Id: job.ID}, cs); err != nil {
		t.Fatalf("StreamEscrowedInferenceJob: %v", err)
	}
	if len(cs.frame) == 0 {
		t.Fatal("no frames")
	}
	final := cs.frame[len(cs.frame)-1]
	if final.GetPayment() == nil {
		t.Fatal("the last frame carries no settlement")
	}
	if got := final.GetJob().GetReasoning(); got != working {
		t.Fatalf("the final frame carries %d bytes of working, want %d: the buyer is being "+
			"billed for text they were not given, and their own ceiling will refuse the bill",
			len(got), len(working))
	}

	// The property that matters: a buyer computing the ceiling from THIS FRAME
	// can account for the bill. Without the working they could not.
	req := inference.InferenceRequest{Prompt: "what is a marketplace for?", Model: "m"}
	amount := final.GetPayment().GetAmount()
	if ceiling := inference.MaxUnitsFor(req, final.GetJob().GetCompletion(), final.GetJob().GetReasoning()); amount > ceiling {
		t.Fatalf("billed %d, over the %d a buyer can account for from this frame", amount, ceiling)
	}
	if bare := inference.MaxUnitsFor(req, final.GetJob().GetCompletion(), ""); amount <= bare {
		t.Fatalf("billed %d, which is under the %d ceiling computed WITHOUT the working - "+
			"this test cannot tell the two apart, so lengthen the working", amount, bare)
	}
}
