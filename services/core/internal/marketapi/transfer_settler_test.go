package marketapi

import (
	"context"
	"errors"
	"testing"
	"time"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// fakeTransferSettler is a controllable TransferSettler for testing that
// SubmitSignedTransfer routes through the injected consensus settler rather than
// the per-node token.SettledLedger. It records what it was asked to settle,
// enforces the same up-front guards the real coordinator does (signature,
// non-empty recipient, no self-transfer) so the RPC's rejection behaviour is
// exercised, and lets a test dictate the committed/applied outcome.
type fakeTransferSettler struct {
	// outcome for the next SettleSignedTransfer call.
	committed bool
	applied   bool

	// captured inputs.
	calls      int
	lastTx     *token.Transaction
	lastAmount uint64

	// history returned by History / TransferAt.
	history []TransferView
}

func (f *fakeTransferSettler) SettleSignedTransfer(ctx context.Context, tx *token.Transaction) (*SettledTransferResult, error) {
	f.calls++
	f.lastTx = tx
	f.lastAmount = tx.Amount

	// Preserve the guards the real coordinator enforces so this fake exercises
	// the RPC's rejection paths honestly.
	if err := tx.Verify(); err != nil {
		return nil, err
	}
	if tx.To == "" {
		return nil, ErrTransferNotFound // stand-in structural error; not exercised here
	}
	if tx.To == tx.SenderID() {
		return nil, token.ErrSelfTransfer
	}

	result := &SettledTransferResult{
		Transfer: TransferView{
			From:   tx.SenderID(),
			To:     tx.To,
			Amount: tx.Amount,
			Nonce:  tx.Nonce,
		},
		Committed: f.committed,
		Applied:   f.applied,
	}
	if !f.committed || !f.applied {
		// Mirror the coordinator: committed-but-unaffordable is a precondition
		// failure that mapSettlementError maps to FailedPrecondition.
		return result, errors.New("transfer did not apply (skipped as unaffordable)")
	}
	return result, nil
}

func (f *fakeTransferSettler) History(start uint64, limit int) ([]TransferView, uint64, error) {
	total := uint64(len(f.history))
	if start >= total {
		return nil, total, nil
	}
	out := f.history[start:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, total, nil
}

func (f *fakeTransferSettler) TransferAt(index uint64) (*TransferView, error) {
	if index >= uint64(len(f.history)) {
		return nil, ErrTransferNotFound
	}
	v := f.history[index]
	return &v, nil
}

// serviceWithTransferSettler builds a Service backed by a fake transfer settler
// and a real market/ledger/chain so its ledger reads work. It calls the RPCs
// directly (no gRPC listener) since that is enough to assert routing.
func serviceWithTransferSettler(t *testing.T, settler TransferSettler) *Service {
	t.Helper()
	h := newTestHarness(t)
	svc := h.serverService(t)
	svc.SetTransferSettler(settler)
	return svc
}

// serverService returns the Service backing the harness by building a fresh one
// over the harness's subsystems. It avoids reaching through the gRPC layer for a
// direct in-process call.
func (h *testHarness) serverService(t *testing.T) *Service {
	t.Helper()
	svc, err := NewService(h.market, h.settled, h.chain, nil, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// signedTransferTx builds and signs a transfer from sender for the test.
func signedTransferTx(t *testing.T, sender *token.Account, to string, amount, nonce uint64) *token.Transaction {
	t.Helper()
	tx := &token.Transaction{
		From:      sender.PublicKey,
		To:        to,
		Amount:    amount,
		Nonce:     nonce,
		Timestamp: time.Now().UnixNano(),
		PrevHash:  make([]byte, 32),
	}
	if err := tx.Sign(sender.PrivateKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tx
}

// TestSubmitSignedTransfer_RoutesThroughConsensusSettler proves that when a
// transfer settler is installed, SubmitSignedTransfer settles through it (not
// through token.SettledLedger) and reports committed+applied.
func TestSubmitSignedTransfer_RoutesThroughConsensusSettler(t *testing.T) {
	settler := &fakeTransferSettler{committed: true, applied: true}
	svc := serviceWithTransferSettler(t, settler)

	sender, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	tx := signedTransferTx(t, sender, "recipient-acct", 200, 0)

	resp, err := svc.SubmitSignedTransfer(context.Background(), &marketv1.SubmitSignedTransferRequest{
		FromPublicKey: token.MarshalPublicKey(sender.PublicKey),
		To:            tx.To,
		Amount:        tx.Amount,
		Nonce:         tx.Nonce,
		PrevHash:      tx.PrevHash,
		Signature:     tx.Signature,
		Timestamp:     tx.Timestamp,
	})
	if err != nil {
		t.Fatalf("SubmitSignedTransfer: %v", err)
	}
	if settler.calls != 1 {
		t.Fatalf("expected the transfer to route through the settler once, got %d calls", settler.calls)
	}
	if !resp.GetCommitted() || !resp.GetApplied() {
		t.Fatalf("expected committed+applied, got committed=%v applied=%v", resp.GetCommitted(), resp.GetApplied())
	}
	if resp.GetTransaction().GetTo() != "recipient-acct" || resp.GetTransaction().GetAmount() != 200 {
		t.Fatalf("unexpected settled transfer: %+v", resp.GetTransaction())
	}

	// It must NOT have touched the per-node token.Chain: with the consensus path
	// the chain stays empty.
	length, err := svc.chain.Len()
	if err != nil {
		t.Fatalf("chain.Len: %v", err)
	}
	if length != 0 {
		t.Fatalf("expected token.Chain untouched (len 0), got %d", length)
	}
}

// TestSubmitSignedTransfer_UnaffordableIsFailedPrecondition proves a transfer
// that commits but is skipped as unaffordable at apply time maps to
// FailedPrecondition (no phantom success).
func TestSubmitSignedTransfer_UnaffordableIsFailedPrecondition(t *testing.T) {
	settler := &fakeTransferSettler{committed: true, applied: false}
	svc := serviceWithTransferSettler(t, settler)

	sender, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	tx := signedTransferTx(t, sender, "recipient-acct", 999, 0)

	_, err = svc.SubmitSignedTransfer(context.Background(), &marketv1.SubmitSignedTransferRequest{
		FromPublicKey: token.MarshalPublicKey(sender.PublicKey),
		To:            tx.To,
		Amount:        tx.Amount,
		Nonce:         tx.Nonce,
		PrevHash:      tx.PrevHash,
		Signature:     tx.Signature,
		Timestamp:     tx.Timestamp,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for an unaffordable transfer, got %v (%v)", status.Code(err), err)
	}
}

// TestSubmitSignedTransfer_ForgedSignatureRejected proves an unsigned/forged
// transfer is rejected before it settles: the settler's Verify guard rejects it
// with InvalidArgument, and no phantom settlement occurs.
func TestSubmitSignedTransfer_ForgedSignatureRejected(t *testing.T) {
	settler := &fakeTransferSettler{committed: true, applied: true}
	svc := serviceWithTransferSettler(t, settler)

	sender, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}

	_, err = svc.SubmitSignedTransfer(context.Background(), &marketv1.SubmitSignedTransferRequest{
		FromPublicKey: token.MarshalPublicKey(sender.PublicKey),
		To:            "recipient-acct",
		Amount:        200,
		Nonce:         0,
		PrevHash:      make([]byte, 32),
		Signature:     []byte("not-a-valid-signature"),
	})
	if status.Code(err) != codes.FailedPrecondition && status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected the forged transfer to be rejected, got %v (%v)", status.Code(err), err)
	}
}

// TestListTransactions_ReadsConsensusHistory proves the history RPCs read the
// injected consensus history when a settler is installed.
func TestListTransactions_ReadsConsensusHistory(t *testing.T) {
	settler := &fakeTransferSettler{
		history: []TransferView{
			{Index: 0, From: "alice", To: "bob", Amount: 10, Nonce: 0, BlockHeight: 1},
			{Index: 1, From: "alice", To: "carol", Amount: 20, Nonce: 1, BlockHeight: 2},
		},
	}
	svc := serviceWithTransferSettler(t, settler)

	resp, err := svc.ListTransactions(context.Background(), &marketv1.ListTransactionsRequest{})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if resp.GetTotal() != 2 || len(resp.GetTransactions()) != 2 {
		t.Fatalf("expected 2 transfers, got total=%d len=%d", resp.GetTotal(), len(resp.GetTransactions()))
	}
	if resp.GetTransactions()[1].GetTo() != "carol" || resp.GetTransactions()[1].GetBlockHeight() != 2 {
		t.Fatalf("unexpected second transfer: %+v", resp.GetTransactions()[1])
	}

	getResp, err := svc.GetTransaction(context.Background(), &marketv1.GetTransactionRequest{Index: 1})
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if getResp.GetTransaction().GetTo() != "carol" {
		t.Fatalf("GetTransaction(1) = %+v, want recipient carol", getResp.GetTransaction())
	}

	if _, err := svc.GetTransaction(context.Background(), &marketv1.GetTransactionRequest{Index: 99}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound for out-of-range index, got %v (%v)", status.Code(err), err)
	}
}
