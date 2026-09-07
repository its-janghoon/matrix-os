package node

import (
	"context"
	"errors"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// fakeTransferEngine is a controllable TransferEngine for unit-testing the
// TransferSettlementCoordinator's guard and confirmation semantics without a
// real consensus engine.
type fakeTransferEngine struct {
	submitted int
	lastTx    *token.Transaction

	committed bool
	applied   bool
	waitErr   error

	history []consensus.CommittedTransfer
}

func (f *fakeTransferEngine) Submit(tx *token.Transaction) error {
	f.submitted++
	f.lastTx = tx
	return nil
}

func (f *fakeTransferEngine) WaitForSettlement(_ context.Context, _ *token.Transaction) (bool, bool, error) {
	if f.waitErr != nil {
		return false, false, f.waitErr
	}
	return f.committed, f.applied, nil
}

func (f *fakeTransferEngine) CommittedTransfers(start uint64, limit int) ([]consensus.CommittedTransfer, uint64, error) {
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

func (f *fakeTransferEngine) CommittedTransferAt(index uint64) (*consensus.CommittedTransfer, error) {
	if index >= uint64(len(f.history)) {
		return nil, consensus.ErrHeightOutOfRange
	}
	t := f.history[index]
	return &t, nil
}

func signTransferFor(t *testing.T, sender *token.Account, to string, amount, nonce uint64) *token.Transaction {
	t.Helper()
	tx := &token.Transaction{
		From:     sender.PublicKey,
		To:       to,
		Amount:   amount,
		Nonce:    nonce,
		PrevHash: make([]byte, token.HashSize),
	}
	if err := tx.Sign(sender.PrivateKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tx
}

// TestTransferCoordinator_SettlesAppliedTransfer proves the happy path submits
// the signed transfer and reports committed+applied, and locates it in history.
func TestTransferCoordinator_SettlesAppliedTransfer(t *testing.T) {
	sender, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	tx := signTransferFor(t, sender, "recipient", 100, 0)

	eng := &fakeTransferEngine{
		committed: true,
		applied:   true,
		history: []consensus.CommittedTransfer{
			{Index: 0, Height: 3, From: sender.AccountID(), To: "recipient", Amount: 100, Nonce: 0},
		},
	}
	c, err := NewTransferSettlementCoordinator(eng)
	if err != nil {
		t.Fatalf("NewTransferSettlementCoordinator: %v", err)
	}

	res, err := c.SettleSignedTransfer(context.Background(), tx)
	if err != nil {
		t.Fatalf("SettleSignedTransfer: %v", err)
	}
	if eng.submitted != 1 {
		t.Fatalf("expected exactly one Submit, got %d", eng.submitted)
	}
	if !res.Committed || !res.Applied {
		t.Fatalf("expected committed+applied, got %+v", res)
	}
	if res.Transfer.Index != 0 || res.Transfer.BlockHeight != 3 {
		t.Fatalf("expected transfer located at index 0 height 3, got %+v", res.Transfer)
	}
}

// TestTransferCoordinator_RejectsBadTransfersBeforeSubmit proves the guards
// token.SettledLedger.Settle enforced are preserved: an unsigned/forged
// transfer, an empty recipient, and a self-transfer are all rejected BEFORE the
// transfer is submitted to consensus.
func TestTransferCoordinator_RejectsBadTransfersBeforeSubmit(t *testing.T) {
	sender, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}

	cases := []struct {
		name    string
		tx      *token.Transaction
		wantErr error
	}{
		{
			name: "forged signature",
			tx: &token.Transaction{
				From:      sender.PublicKey,
				To:        "recipient",
				Amount:    10,
				PrevHash:  make([]byte, token.HashSize),
				Signature: []byte("nope"),
			},
			wantErr: nil, // any non-nil error; Verify rejects it
		},
		{
			name:    "empty recipient",
			tx:      signTransferFor(t, sender, "", 10, 0),
			wantErr: ErrEmptyRecipient,
		},
		{
			name:    "self transfer",
			tx:      signTransferFor(t, sender, sender.AccountID(), 10, 0),
			wantErr: ErrSelfTransfer,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := &fakeTransferEngine{committed: true, applied: true}
			c, err := NewTransferSettlementCoordinator(eng)
			if err != nil {
				t.Fatalf("NewTransferSettlementCoordinator: %v", err)
			}
			_, err = c.SettleSignedTransfer(context.Background(), tc.tx)
			if err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error wrapping %v, got %v", tc.wantErr, err)
			}
			if eng.submitted != 0 {
				t.Fatalf("expected no Submit for a rejected transfer, got %d", eng.submitted)
			}
		})
	}
}

// TestTransferCoordinator_UnaffordableReportsNotApplied proves a
// committed-but-not-applied outcome is reported as ErrTransferNotApplied so the
// RPC can map it to a precondition failure.
func TestTransferCoordinator_UnaffordableReportsNotApplied(t *testing.T) {
	sender, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	tx := signTransferFor(t, sender, "recipient", 1000, 0)

	eng := &fakeTransferEngine{committed: true, applied: false}
	c, err := NewTransferSettlementCoordinator(eng)
	if err != nil {
		t.Fatalf("NewTransferSettlementCoordinator: %v", err)
	}
	res, err := c.SettleSignedTransfer(context.Background(), tx)
	if !errors.Is(err, ErrTransferNotApplied) {
		t.Fatalf("expected ErrTransferNotApplied, got %v", err)
	}
	if res == nil || res.Committed != true || res.Applied != false {
		t.Fatalf("expected committed=true applied=false result, got %+v", res)
	}
}

// TestTransferCoordinator_HistoryPassthrough proves History/TransferAt map
// consensus transfers to the marketapi view and translate the out-of-range
// error to the marketapi NotFound sentinel.
func TestTransferCoordinator_HistoryPassthrough(t *testing.T) {
	eng := &fakeTransferEngine{
		history: []consensus.CommittedTransfer{
			{Index: 0, Height: 1, From: "a", To: "b", Amount: 5, Nonce: 0},
		},
	}
	c, err := NewTransferSettlementCoordinator(eng)
	if err != nil {
		t.Fatalf("NewTransferSettlementCoordinator: %v", err)
	}

	views, total, err := c.History(0, 0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if total != 1 || len(views) != 1 || views[0].To != "b" || views[0].BlockHeight != 1 {
		t.Fatalf("unexpected history: total=%d views=%+v", total, views)
	}

	if _, err := c.TransferAt(99); err == nil {
		t.Fatalf("expected an error for out-of-range index")
	}
}
