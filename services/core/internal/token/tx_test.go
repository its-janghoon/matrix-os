package token

import (
	"errors"
	"testing"
	"time"
)

// signedTx builds a signed transaction from sender to recipient with the given
// amount, nonce and prevHash.
func signedTx(t *testing.T, sender *Account, to string, amount, nonce uint64, prevHash []byte) *Transaction {
	t.Helper()
	tx := &Transaction{
		From:      sender.PublicKey,
		To:        to,
		Amount:    amount,
		Nonce:     nonce,
		Timestamp: time.Now().UnixNano(),
		PrevHash:  prevHash,
	}
	if err := tx.Sign(sender.PrivateKey); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	return tx
}

func TestTransaction_SignVerify(t *testing.T) {
	sender := mustAccount(t)
	recipient := mustAccount(t)

	tx := signedTx(t, sender, recipient.AccountID(), 100, 0, genesisPrevHash)
	if err := tx.Verify(); err != nil {
		t.Fatalf("Verify() on freshly signed tx error = %v", err)
	}
}

func TestTransaction_SignWithWrongKey(t *testing.T) {
	sender := mustAccount(t)
	other := mustAccount(t)

	tx := &Transaction{
		From:     sender.PublicKey,
		To:       "someone",
		Amount:   1,
		Nonce:    0,
		PrevHash: genesisPrevHash,
	}
	// Signing with a key that does not match From must be refused.
	if err := tx.Sign(other.PrivateKey); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("Sign() with mismatched key error = %v, want ErrInvalidTransaction", err)
	}
}

func TestTransaction_VerifyRejectsTampering(t *testing.T) {
	sender := mustAccount(t)
	recipient := mustAccount(t)

	tests := []struct {
		name    string
		tamper  func(tx *Transaction)
		wantErr error
	}{
		{
			name:    "tampered amount",
			tamper:  func(tx *Transaction) { tx.Amount += 1 },
			wantErr: ErrInvalidSignature,
		},
		{
			name:    "tampered nonce",
			tamper:  func(tx *Transaction) { tx.Nonce += 1 },
			wantErr: ErrInvalidSignature,
		},
		{
			name:    "tampered recipient",
			tamper:  func(tx *Transaction) { tx.To = "attacker" },
			wantErr: ErrInvalidSignature,
		},
		{
			name:    "tampered prevhash",
			tamper:  func(tx *Transaction) { tx.PrevHash = []byte("different") },
			wantErr: ErrInvalidSignature,
		},
		{
			name:    "tampered signature",
			tamper:  func(tx *Transaction) { tx.Signature[0] ^= 0xff },
			wantErr: ErrInvalidSignature,
		},
		{
			name:    "cleared signature",
			tamper:  func(tx *Transaction) { tx.Signature = nil },
			wantErr: ErrUnsignedTransaction,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := signedTx(t, sender, recipient.AccountID(), 100, 0, genesisPrevHash)
			tt.tamper(tx)
			if err := tx.Verify(); !errors.Is(err, tt.wantErr) {
				t.Errorf("Verify() after %s error = %v, want %v", tt.name, err, tt.wantErr)
			}
		})
	}
}
