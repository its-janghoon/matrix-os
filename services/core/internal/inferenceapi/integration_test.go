package inferenceapi

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	inferencev1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/inference/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/inference"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/ecirlabs/matrix-core/internal/transport"
)

// memBus is a minimal in-memory gossip bus so a single-node consensus engine can
// run without libp2p, backing the settlement path exercised over gRPC.
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

type memAccounts struct{ m map[string]*token.Account }

func (a memAccounts) Account(id string) (*token.Account, bool) { acct, ok := a.m[id]; return acct, ok }

// TestInferenceService_EndToEnd drives the full inference happy path over gRPC:
// submit an inference job to a provider whose backend is a stub, fulfill it, and
// confirm the job completes with a completion and settled units, and that the
// job can be fetched back.
func TestInferenceService_EndToEnd(t *testing.T) {
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
	buyerID := buyer.AccountID()
	providerID := provider.AccountID()
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

	srv, err := NewServer(Config{Addr: "127.0.0.1:0", Inference: infSvc})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := inferencev1.NewInferenceServiceClient(conn)

	// Submit an inference job.
	subResp, err := client.SubmitInferenceJob(ctx, &inferencev1.SubmitInferenceJobRequest{
		Buyer:         buyerID,
		Provider:      providerID,
		Model:         "stub",
		Prompt:        "hello world",
		UnitsEstimate: 8,
	})
	if err != nil {
		t.Fatalf("SubmitInferenceJob: %v", err)
	}
	if subResp.GetJob().GetStatus() != inferencev1.InferenceJobStatus_INFERENCE_JOB_STATUS_PENDING {
		t.Fatalf("expected PENDING, got %v", subResp.GetJob().GetStatus())
	}
	jobID := subResp.GetJob().GetId()

	// Fulfill it.
	fulResp, err := client.FulfillInferenceJob(ctx, &inferencev1.FulfillInferenceJobRequest{Id: jobID})
	if err != nil {
		t.Fatalf("FulfillInferenceJob: %v", err)
	}
	if fulResp.GetJob().GetStatus() != inferencev1.InferenceJobStatus_INFERENCE_JOB_STATUS_COMPLETED {
		t.Fatalf("expected COMPLETED, got %v", fulResp.GetJob().GetStatus())
	}
	if fulResp.GetJob().GetCompletion() != "echo: user: hello world" {
		t.Fatalf("unexpected completion: %q", fulResp.GetJob().GetCompletion())
	}
	if fulResp.GetJob().GetUnits() != 7 {
		t.Fatalf("settled units = %d, want 7", fulResp.GetJob().GetUnits())
	}

	// GetInferenceJob returns the same completed job.
	getResp, err := client.GetInferenceJob(ctx, &inferencev1.GetInferenceJobRequest{Id: jobID})
	if err != nil {
		t.Fatalf("GetInferenceJob: %v", err)
	}
	if getResp.GetJob().GetStatus() != inferencev1.InferenceJobStatus_INFERENCE_JOB_STATUS_COMPLETED {
		t.Fatalf("GetInferenceJob status = %v", getResp.GetJob().GetStatus())
	}

	// Settlement moved credits on the shared ledger: buyer 1000-7, provider 7.
	deadline := time.After(3 * time.Second)
	for {
		bb, _ := mkt.Ledger().Balance(buyerID)
		pb, _ := mkt.Ledger().Balance(providerID)
		if bb == 993 && pb == 7 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("balances did not settle: buyer=%d provider=%d", bb, pb)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// TestInferenceService_Errors asserts error mapping across the gRPC boundary.
func TestInferenceService_Errors(t *testing.T) {
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
	vs, _ := consensus.NewValidatorSet([]ed25519.PublicKey{validator.PublicKey})
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
	infSvc, err := inference.NewService(inference.Config{
		Market:   mkt,
		Registry: inference.NewRegistry(),
		Settler:  engine,
		Accounts: memAccounts{m: map[string]*token.Account{}},
	})
	if err != nil {
		t.Fatalf("inference.NewService: %v", err)
	}
	srv, err := NewServer(Config{Addr: "127.0.0.1:0", Inference: infSvc})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := inferencev1.NewInferenceServiceClient(conn)

	// GetInferenceJob on unknown job -> NotFound.
	if _, err := client.GetInferenceJob(ctx, &inferencev1.GetInferenceJobRequest{Id: "nope"}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v (%v)", status.Code(err), err)
	}

	// Submit to a provider with no backend registered -> FailedPrecondition.
	if _, err := client.SubmitInferenceJob(ctx, &inferencev1.SubmitInferenceJobRequest{
		Buyer:    "b",
		Provider: "no-backend",
		Prompt:   "hi",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for missing backend, got %v (%v)", status.Code(err), err)
	}

	// Empty prompt+messages -> InvalidArgument.
	if _, err := client.SubmitInferenceJob(ctx, &inferencev1.SubmitInferenceJobRequest{
		Buyer:    "b",
		Provider: "no-backend",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for empty prompt, got %v (%v)", status.Code(err), err)
	}
}
