package marketapi

import (
	"context"
	"testing"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ecirlabs/matrix-core/internal/admin"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// fundTestAPIKey is the API key the authenticated fund harness accepts.
const fundTestAPIKey = "fund-test-key"

// authCtx returns a context carrying a valid API key so a call passes the
// require-auth interceptor.
func authCtx(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", fundTestAPIKey)
}

// newFundHarness stands up a MarketService backed by a real Treasury funder over
// a temp store, applies a genesis reward pool of the given size, and returns a
// wired client plus the market for balance assertions. The server enforces
// authentication (FundAccount refuses to run on an unauthenticated server), so
// callers must use authCtx to reach FundAccount successfully.
func newFundHarness(t *testing.T, rewardPool uint64) (marketv1.MarketServiceClient, *market.Market) {
	t.Helper()
	client, mkt, _ := newFundHarnessWithAuth(t, rewardPool, true)
	return client, mkt
}

// newFundHarnessWithAuth is newFundHarness with control over whether the server
// enforces authentication, so a test can prove FundAccount is rejected on an
// unauthenticated (ACLs-off) server. It returns the authenticator (nil when auth
// is disabled) so a test could add more keys if needed.
func newFundHarnessWithAuth(t *testing.T, rewardPool uint64, withAuth bool) (marketv1.MarketServiceClient, *market.Market, *admin.Authenticator) {
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
	treasury := token.NewTreasury(mkt.Ledger(), store)
	if err := treasury.ApplyGenesis(nil, rewardPool); err != nil {
		t.Fatalf("ApplyGenesis: %v", err)
	}

	var auth *admin.Authenticator
	if withAuth {
		auth = admin.NewAuthenticator()
		if err := auth.AddKey(&admin.APIKey{Key: fundTestAPIKey, Role: admin.RoleAdmin}); err != nil {
			t.Fatalf("AddKey: %v", err)
		}
	}

	srv, err := NewServer(Config{
		Addr:    "127.0.0.1:0",
		Auth:    auth,
		Market:  mkt,
		Settled: settled,
		Chain:   chain,
		Funder:  treasury,
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

	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return marketv1.NewMarketServiceClient(conn), mkt, auth
}

// TestFundAccount_MovesRewardPoolFunds proves FundAccount moves native MATRIX
// from the reward pool to a recipient, that the recipient balance and the
// returned balance agree, and that the reward-pool balance decreases by exactly
// the funded amount (supply is conserved, not minted).
func TestFundAccount_MovesRewardPoolFunds(t *testing.T) {
	const rewardPool = uint64(1_000_000)
	client, mkt := newFundHarness(t, rewardPool)
	ctx := authCtx(context.Background())

	const amount = uint64(250_000)
	resp, err := client.FundAccount(ctx, &marketv1.FundAccountRequest{Account: "buyer-1", Amount: amount})
	if err != nil {
		t.Fatalf("FundAccount: %v", err)
	}
	if resp.GetAccount() != "buyer-1" {
		t.Fatalf("account = %q, want buyer-1", resp.GetAccount())
	}
	if resp.GetBalance() != amount {
		t.Fatalf("returned balance = %d, want %d", resp.GetBalance(), amount)
	}

	bal, _ := mkt.Ledger().Balance("buyer-1")
	if bal != amount {
		t.Fatalf("ledger balance = %d, want %d", bal, amount)
	}
	poolBal, _ := mkt.Ledger().Balance(token.RewardPoolAccount)
	if poolBal != rewardPool-amount {
		t.Fatalf("reward pool balance = %d, want %d (pool drained by exactly the funded amount)", poolBal, rewardPool-amount)
	}
}

// TestFundAccount_RejectsOverPool asserts a request exceeding the reward-pool
// balance is rejected with FailedPrecondition and moves no funds.
func TestFundAccount_RejectsOverPool(t *testing.T) {
	const rewardPool = uint64(100)
	client, mkt := newFundHarness(t, rewardPool)
	ctx := authCtx(context.Background())

	_, err := client.FundAccount(ctx, &marketv1.FundAccountRequest{Account: "buyer-1", Amount: rewardPool + 1})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition over-pool, got %v (%v)", status.Code(err), err)
	}
	if bal, _ := mkt.Ledger().Balance("buyer-1"); bal != 0 {
		t.Fatalf("buyer balance = %d, want 0 (no funds moved on rejection)", bal)
	}
	if poolBal, _ := mkt.Ledger().Balance(token.RewardPoolAccount); poolBal != rewardPool {
		t.Fatalf("reward pool = %d, want %d (unchanged on rejection)", poolBal, rewardPool)
	}
}

// TestFundAccount_ValidatesArgs asserts empty account / zero amount are
// InvalidArgument.
func TestFundAccount_ValidatesArgs(t *testing.T) {
	client, _ := newFundHarness(t, 1_000)
	ctx := authCtx(context.Background())

	if _, err := client.FundAccount(ctx, &marketv1.FundAccountRequest{Account: "", Amount: 10}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty account: expected InvalidArgument, got %v (%v)", status.Code(err), err)
	}
	if _, err := client.FundAccount(ctx, &marketv1.FundAccountRequest{Account: "x", Amount: 0}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("zero amount: expected InvalidArgument, got %v (%v)", status.Code(err), err)
	}
}

// TestFundAccount_UnimplementedWithoutFunder asserts a service wired without a
// funder reports Unimplemented rather than panicking.
func TestFundAccount_UnimplementedWithoutFunder(t *testing.T) {
	h := newTestHarness(t) // no funder
	ctx := context.Background()

	if _, err := h.client.FundAccount(ctx, &marketv1.FundAccountRequest{Account: "x", Amount: 1}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("expected Unimplemented without funder, got %v (%v)", status.Code(err), err)
	}
}

// TestFundAccount_RejectedWithoutAuth is the security regression test for the
// unauthenticated reward-pool drain: a funder-backed server that does NOT
// enforce authentication (ACLs off) must reject FundAccount rather than move
// reward-pool MATRIX for an unauthenticated caller. Unlike SubmitSignedTransfer,
// a funding request carries no signature, so serving it open would let any client
// drain the pool into an account it names.
func TestFundAccount_RejectedWithoutAuth(t *testing.T) {
	const rewardPool = uint64(1_000_000)
	client, mkt, _ := newFundHarnessWithAuth(t, rewardPool, false)
	ctx := context.Background()

	_, err := client.FundAccount(ctx, &marketv1.FundAccountRequest{Account: "attacker", Amount: 500_000})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition on unauthenticated funding, got %v (%v)", status.Code(err), err)
	}
	// No funds moved: the attacker got nothing and the pool is intact.
	if bal, _ := mkt.Ledger().Balance("attacker"); bal != 0 {
		t.Fatalf("attacker balance = %d, want 0 (no funds moved on rejection)", bal)
	}
	if poolBal, _ := mkt.Ledger().Balance(token.RewardPoolAccount); poolBal != rewardPool {
		t.Fatalf("reward pool = %d, want %d (unchanged on rejection)", poolBal, rewardPool)
	}
}

// TestFundAccount_RejectedWithoutCredentials asserts that even on an
// auth-enforcing server, a call missing credentials is rejected by the
// interceptor (Unauthenticated) and moves no funds.
func TestFundAccount_RejectedWithoutCredentials(t *testing.T) {
	const rewardPool = uint64(1_000_000)
	client, mkt := newFundHarness(t, rewardPool)
	ctx := context.Background() // no authCtx: no credentials

	_, err := client.FundAccount(ctx, &marketv1.FundAccountRequest{Account: "attacker", Amount: 500_000})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated without credentials, got %v (%v)", status.Code(err), err)
	}
	if poolBal, _ := mkt.Ledger().Balance(token.RewardPoolAccount); poolBal != rewardPool {
		t.Fatalf("reward pool = %d, want %d (unchanged on rejection)", poolBal, rewardPool)
	}
}
