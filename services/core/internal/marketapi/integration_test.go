package marketapi

import (
	"context"
	"testing"
	"time"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// testHarness wires a real Market/chain/settled ledger over a temp store and
// serves the MarketService on a real 127.0.0.1:0 listener, returning a client
// wired to it. Everything is torn down via t.Cleanup.
type testHarness struct {
	client  marketv1.MarketServiceClient
	market  *market.Market
	chain   *token.Chain
	settled *token.SettledLedger
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()

	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}
	chain := token.NewChain(store)
	settled := token.NewSettledLedger(mkt.Ledger(), chain)

	// No exchange (nil) and no auth: exercise the local order book + signed
	// settlement surface unauthenticated.
	srv, err := NewServer(Config{
		Addr:    "127.0.0.1:0",
		Market:  mkt,
		Settled: settled,
		Chain:   chain,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	// Start binds the listener synchronously before serving, so the OS-assigned
	// port is available immediately.
	addr := srv.Addr()
	if addr == "" {
		t.Fatal("server address is empty after Start")
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return &testHarness{
		client:  marketv1.NewMarketServiceClient(conn),
		market:  mkt,
		chain:   chain,
		settled: settled,
	}
}

// fundedAccount generates an account and mints it initial credits directly on
// the ledger (the unsigned bootstrap-mint path SettledLedger documents), so it
// can subsequently be the signed sender of a transfer.
func fundedAccount(t *testing.T, m *market.Market, credits uint64) *token.Account {
	t.Helper()
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	if err := m.Ledger().Credit(acct.AccountID(), credits); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	return acct
}

// signTransfer builds and signs a token.Transaction from sender to `to` for
// `amount`, chaining onto the current chain head with the given nonce.
func signTransfer(t *testing.T, chain *token.Chain, sender *token.Account, to string, amount, nonce uint64) *token.Transaction {
	t.Helper()
	prev, err := chain.HeadHash()
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}
	tx := &token.Transaction{
		From:      sender.PublicKey,
		To:        to,
		Amount:    amount,
		Nonce:     nonce,
		Timestamp: time.Now().UnixNano(),
		PrevHash:  prev,
	}
	if err := tx.Sign(sender.PrivateKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tx
}

// TestMarketService_EndToEnd drives the full happy path over gRPC: register a
// provider, credit a buyer via a signed transfer, submit and complete a job,
// then assert balances and that a chain transaction was recorded.
func TestMarketService_EndToEnd(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()

	// Register a provider.
	regResp, err := h.client.RegisterProvider(ctx, &marketv1.RegisterProviderRequest{
		Id:           "provider-1",
		Capacity:     100,
		PricePerUnit: 5,
	})
	if err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	if regResp.GetProvider().GetAvailable() != 100 {
		t.Fatalf("expected available 100, got %d", regResp.GetProvider().GetAvailable())
	}
	if regResp.GetProvider().GetOrigin() != marketv1.ProviderOrigin_PROVIDER_ORIGIN_LOCAL {
		t.Fatalf("expected LOCAL origin, got %v", regResp.GetProvider().GetOrigin())
	}

	// Fund a treasury account, then move credits to the buyer via a signed
	// transfer that flows through SubmitSignedTransfer (the FEAT-002 signed path).
	treasury := fundedAccount(t, h.market, 1000)
	buyer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	buyerID := buyer.AccountID()

	tx := signTransfer(t, h.chain, treasury, buyerID, 200, 0)
	xferResp, err := h.client.SubmitSignedTransfer(ctx, &marketv1.SubmitSignedTransferRequest{
		FromPublicKey: []byte(treasury.PublicKey),
		To:            buyerID,
		Amount:        200,
		Nonce:         0,
		PrevHash:      tx.PrevHash,
		Signature:     tx.Signature,
		Timestamp:     tx.Timestamp,
	})
	if err != nil {
		t.Fatalf("SubmitSignedTransfer: %v", err)
	}
	if xferResp.GetTransaction().GetHeight() != 0 {
		t.Fatalf("expected first tx at height 0, got %d", xferResp.GetTransaction().GetHeight())
	}
	if xferResp.GetTransaction().GetTo() != buyerID {
		t.Fatalf("expected transfer recipient %q, got %q", buyerID, xferResp.GetTransaction().GetTo())
	}

	// Buyer balance should now be 200.
	balResp, err := h.client.GetBalance(ctx, &marketv1.GetBalanceRequest{Account: buyerID})
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if balResp.GetBalance() != 200 {
		t.Fatalf("expected buyer balance 200, got %d", balResp.GetBalance())
	}

	// Submit a job: 10 units * 5 credits = 50.
	jobResp, err := h.client.SubmitJob(ctx, &marketv1.SubmitJobRequest{
		Buyer:    buyerID,
		Provider: "provider-1",
		Units:    10,
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	if jobResp.GetJob().GetStatus() != marketv1.JobStatus_JOB_STATUS_PENDING {
		t.Fatalf("expected PENDING job, got %v", jobResp.GetJob().GetStatus())
	}
	if jobResp.GetJob().GetPrice() != 50 {
		t.Fatalf("expected price 50, got %d", jobResp.GetJob().GetPrice())
	}
	jobID := jobResp.GetJob().GetId()

	// Complete the job; credits should move buyer -> provider.
	compResp, err := h.client.CompleteJob(ctx, &marketv1.CompleteJobRequest{Id: jobID})
	if err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	if compResp.GetJob().GetStatus() != marketv1.JobStatus_JOB_STATUS_COMPLETED {
		t.Fatalf("expected COMPLETED job, got %v", compResp.GetJob().GetStatus())
	}

	// Buyer paid 50 (200 - 50 = 150); provider received 50.
	buyerBal, err := h.client.GetBalance(ctx, &marketv1.GetBalanceRequest{Account: buyerID})
	if err != nil {
		t.Fatalf("GetBalance(buyer): %v", err)
	}
	if buyerBal.GetBalance() != 150 {
		t.Fatalf("expected buyer balance 150, got %d", buyerBal.GetBalance())
	}
	provBal, err := h.client.GetBalance(ctx, &marketv1.GetBalanceRequest{Account: "provider-1"})
	if err != nil {
		t.Fatalf("GetBalance(provider): %v", err)
	}
	if provBal.GetBalance() != 50 {
		t.Fatalf("expected provider balance 50, got %d", provBal.GetBalance())
	}

	// A chain transaction was recorded by the signed transfer: exactly one.
	txsResp, err := h.client.ListTransactions(ctx, &marketv1.ListTransactionsRequest{})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if txsResp.GetChainLength() != 1 {
		t.Fatalf("expected chain length 1, got %d", txsResp.GetChainLength())
	}
	if len(txsResp.GetTransactions()) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txsResp.GetTransactions()))
	}

	// GetTransaction by height returns the same record.
	getTx, err := h.client.GetTransaction(ctx, &marketv1.GetTransactionRequest{Height: 0})
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if getTx.GetTransaction().GetAmount() != 200 {
		t.Fatalf("expected tx amount 200, got %d", getTx.GetTransaction().GetAmount())
	}

	// ListProviders (local only) returns provider-1.
	provs, err := h.client.ListProviders(ctx, &marketv1.ListProvidersRequest{})
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(provs.GetProviders()) != 1 || provs.GetProviders()[0].GetId() != "provider-1" {
		t.Fatalf("unexpected providers: %+v", provs.GetProviders())
	}
}

// TestMarketService_ErrorMapping asserts that market sentinel errors map to the
// expected gRPC status codes across the boundary.
func TestMarketService_ErrorMapping(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()

	// GetJob on an unknown job -> NotFound.
	if _, err := h.client.GetJob(ctx, &marketv1.GetJobRequest{Id: "nope"}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound for unknown job, got %v (%v)", status.Code(err), err)
	}

	// SubmitJob against an unknown provider -> NotFound.
	_, err := h.client.SubmitJob(ctx, &marketv1.SubmitJobRequest{
		Buyer:    "buyer",
		Provider: "ghost",
		Units:    1,
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound for unknown provider, got %v (%v)", status.Code(err), err)
	}

	// Register a provider, then submit a job the buyer cannot afford (buyer has
	// zero balance) -> FailedPrecondition (insufficient funds).
	if _, err := h.client.RegisterProvider(ctx, &marketv1.RegisterProviderRequest{
		Id:           "provider-1",
		Capacity:     100,
		PricePerUnit: 10,
	}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	_, err = h.client.SubmitJob(ctx, &marketv1.SubmitJobRequest{
		Buyer:    "broke-buyer",
		Provider: "provider-1",
		Units:    5,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for insufficient funds, got %v (%v)", status.Code(err), err)
	}

	// GetTransaction out of range -> NotFound.
	if _, err := h.client.GetTransaction(ctx, &marketv1.GetTransactionRequest{Height: 99}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound for out-of-range height, got %v (%v)", status.Code(err), err)
	}

	// SubmitSignedTransfer with a bad signature -> InvalidArgument.
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	prev, err := h.chain.HeadHash()
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}
	if _, err := h.client.SubmitSignedTransfer(ctx, &marketv1.SubmitSignedTransferRequest{
		FromPublicKey: []byte(acct.PublicKey),
		To:            "someone",
		Amount:        1,
		Nonce:         0,
		PrevHash:      prev,
		Signature:     []byte("not-a-valid-signature"),
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for bad signature, got %v (%v)", status.Code(err), err)
	}
}
