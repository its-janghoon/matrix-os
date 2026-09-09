package marketapi

import (
	"bytes"
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

type fakeReadinessSigner struct {
	proof *BridgeReadinessProof
	err   error
	got   []byte
}

func (f *fakeReadinessSigner) SignBridgeReadiness(challenge []byte) (*BridgeReadinessProof, error) {
	f.got = append([]byte(nil), challenge...)
	return f.proof, f.err
}

func newReadinessHarness(t *testing.T, signer BridgeReadinessSigner) marketv1.MarketServiceClient {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatal(err)
	}
	chain := token.NewChain(store)
	srv, err := NewServer(Config{
		Addr:            "127.0.0.1:0",
		Market:          mkt,
		Settled:         token.NewSettledLedger(mkt.Ledger(), chain),
		Chain:           chain,
		ReadinessSigner: signer,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := srv.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return marketv1.NewMarketServiceClient(conn)
}

func TestGetBridgeReadinessReturnsChallengeBoundFields(t *testing.T) {
	challenge := bytes.Repeat([]byte{0xa5}, 32)
	proof := &BridgeReadinessProof{
		ChainID:       84532,
		Contract:      "0x2222222222222222222222222222222222222222",
		Attestor:      "0x3333333333333333333333333333333333333333",
		MinLockNative: token.MinBridgeLockAmount,
		Challenge:     append([]byte(nil), challenge...),
		Signature:     bytes.Repeat([]byte{0x44}, 65),
	}
	fake := &fakeReadinessSigner{proof: proof}
	resp, err := newReadinessHarness(t, fake).GetBridgeReadiness(context.Background(), &marketv1.GetBridgeReadinessRequest{Challenge: challenge})
	if err != nil {
		t.Fatalf("GetBridgeReadiness: %v", err)
	}
	if !bytes.Equal(fake.got, challenge) || !bytes.Equal(resp.GetChallenge(), challenge) {
		t.Fatal("challenge was not passed through and echoed exactly")
	}
	if resp.GetChainId() != 84532 || resp.GetContract() != proof.Contract || resp.GetAttestor() != proof.Attestor || resp.GetMinLockNative() != token.MinBridgeLockAmount {
		t.Fatalf("unexpected readiness response: %+v", resp)
	}
	if len(resp.GetSignature()) != 65 {
		t.Fatalf("signature length = %d, want 65", len(resp.GetSignature()))
	}
}

func TestGetBridgeReadinessFailsClosedWithoutBridgeAndKey(t *testing.T) {
	_, err := newReadinessHarness(t, nil).GetBridgeReadiness(context.Background(), &marketv1.GetBridgeReadinessRequest{Challenge: make([]byte, 32)})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("code = %s, want FailedPrecondition: %v", status.Code(err), err)
	}
}

func TestGetBridgeReadinessRejectsMalformedChallenges(t *testing.T) {
	client := newReadinessHarness(t, &fakeReadinessSigner{})
	for _, size := range []int{0, 31, 33} {
		_, err := client.GetBridgeReadiness(context.Background(), &marketv1.GetBridgeReadinessRequest{Challenge: make([]byte, size)})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("size %d: code = %s, want InvalidArgument", size, status.Code(err))
		}
	}
}

func TestGetBridgeReadinessRejectsMalformedSignerOutput(t *testing.T) {
	for _, fake := range []*fakeReadinessSigner{
		{proof: nil},
		{proof: &BridgeReadinessProof{Challenge: make([]byte, 32), Signature: make([]byte, 64)}},
		{proof: &BridgeReadinessProof{Challenge: bytes.Repeat([]byte{1}, 32), Signature: make([]byte, 65)}},
		{proof: &BridgeReadinessProof{Challenge: make([]byte, 31), Signature: make([]byte, 65)}},
		{err: errors.New("key unavailable")},
	} {
		_, err := newReadinessHarness(t, fake).GetBridgeReadiness(context.Background(), &marketv1.GetBridgeReadinessRequest{Challenge: make([]byte, 32)})
		if status.Code(err) != codes.Internal {
			t.Fatalf("code = %s, want Internal: %v", status.Code(err), err)
		}
	}
}
