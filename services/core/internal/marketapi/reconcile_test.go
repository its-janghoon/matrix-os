package marketapi

import (
	"context"
	"errors"
	"testing"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// fakeReconciler is a marketapi.Reconciler test double: it returns a fixed
// snapshot or a fixed error, so the GetBridgeReconciliation handler can be
// exercised without standing up a real bridge. It records how many times it was
// called so a test can assert the handler actually consulted it.
type fakeReconciler struct {
	snap  *BridgeSnapshot
	err   error
	calls int
}

func (f *fakeReconciler) Reconcile() (*BridgeSnapshot, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.snap, nil
}

// newReconcileHarness stands up a MarketService with the given Reconciler
// (which may be nil to model a node with no bridge configured) over a temp
// store, with no auth so the test can call the RPC directly. It returns a wired
// client.
func newReconcileHarness(t *testing.T, reconciler Reconciler) marketv1.MarketServiceClient {
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

	srv, err := NewServer(Config{
		Addr:       "127.0.0.1:0",
		Market:     mkt,
		Settled:    settled,
		Chain:      chain,
		Reconciler: reconciler,
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

	return marketv1.NewMarketServiceClient(conn)
}

// TestGetBridgeReconciliation_ReturnsSnapshot proves the handler copies the
// reconciler's snapshot into the response verbatim, including OutstandingERC20
// rendered as a decimal string and the stated block height.
func TestGetBridgeReconciliation_ReturnsSnapshot(t *testing.T) {
	// outstanding 7 native -> 7 * 1e9 wrapped, but assert against the token
	// conversion so the test tracks the single source of truth.
	outstandingNative := uint64(7)
	wantERC20 := token.NativeToERC20(outstandingNative)
	fake := &fakeReconciler{snap: &BridgeSnapshot{
		LockedNative:      12,
		UnlockedNative:    5,
		OutstandingNative: outstandingNative,
		EscrowBalance:     outstandingNative,
		OutstandingERC20:  wantERC20,
		BlockHeight:       42,
	}}
	client := newReconcileHarness(t, fake)

	resp, err := client.GetBridgeReconciliation(context.Background(), &marketv1.GetBridgeReconciliationRequest{})
	if err != nil {
		t.Fatalf("GetBridgeReconciliation: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("reconciler called %d times, want 1", fake.calls)
	}
	if resp.GetLockedNative() != 12 || resp.GetUnlockedNative() != 5 {
		t.Fatalf("locked/unlocked = %d/%d, want 12/5", resp.GetLockedNative(), resp.GetUnlockedNative())
	}
	if resp.GetOutstandingNative() != outstandingNative {
		t.Fatalf("outstanding = %d, want %d", resp.GetOutstandingNative(), outstandingNative)
	}
	if resp.GetEscrowBalance() != outstandingNative {
		t.Fatalf("escrow = %d, want %d", resp.GetEscrowBalance(), outstandingNative)
	}
	if resp.GetOutstandingErc20() != wantERC20.String() {
		t.Fatalf("outstanding erc20 = %q, want %q", resp.GetOutstandingErc20(), wantERC20.String())
	}
	if resp.GetBlockHeight() != 42 {
		t.Fatalf("block height = %d, want 42", resp.GetBlockHeight())
	}
}

// TestGetBridgeReconciliation_NilOutstandingERC20 proves a snapshot with a nil
// big.Int (a defensive edge) renders as "0" rather than panicking.
func TestGetBridgeReconciliation_NilOutstandingERC20(t *testing.T) {
	fake := &fakeReconciler{snap: &BridgeSnapshot{OutstandingERC20: nil}}
	client := newReconcileHarness(t, fake)

	resp, err := client.GetBridgeReconciliation(context.Background(), &marketv1.GetBridgeReconciliationRequest{})
	if err != nil {
		t.Fatalf("GetBridgeReconciliation: %v", err)
	}
	if resp.GetOutstandingErc20() != "0" {
		t.Fatalf("outstanding erc20 = %q, want \"0\"", resp.GetOutstandingErc20())
	}
}

// TestGetBridgeReconciliation_NoBridge proves a node with no bridge configured
// (a nil Reconciler) returns FailedPrecondition rather than nil-panicking.
func TestGetBridgeReconciliation_NoBridge(t *testing.T) {
	client := newReconcileHarness(t, nil)

	_, err := client.GetBridgeReconciliation(context.Background(), &marketv1.GetBridgeReconciliationRequest{})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition with no bridge, got %v (%v)", status.Code(err), err)
	}
}

// TestGetBridgeReconciliation_Mismatch proves an escrow/accounting mismatch
// (the reconciler erroring, mirroring Bridge.Reconcile) surfaces as Internal:
// a broken 1:1 backing invariant is a server fault, not a bad request.
func TestGetBridgeReconciliation_Mismatch(t *testing.T) {
	fake := &fakeReconciler{err: errors.New("bridge: reconciliation mismatch: escrow balance 3 != outstanding 5")}
	client := newReconcileHarness(t, fake)

	_, err := client.GetBridgeReconciliation(context.Background(), &marketv1.GetBridgeReconciliationRequest{})
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal on mismatch, got %v (%v)", status.Code(err), err)
	}
}
