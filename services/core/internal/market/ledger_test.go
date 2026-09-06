package market

import (
	"errors"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
)

// newTestStore creates a real Pebble kv.Store on a temp dir and registers
// cleanup so tests exercise real persistence rather than mocks.
func newTestStore(t *testing.T) *kv.Store {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create kv store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("failed to close store: %v", err)
		}
	})
	return store
}

func TestLedger_CreditAndBalance(t *testing.T) {
	tests := []struct {
		name    string
		credits []uint64
		want    uint64
	}{
		{name: "single credit", credits: []uint64{100}, want: 100},
		{name: "multiple credits accumulate", credits: []uint64{100, 50, 25}, want: 175},
		{name: "zero credit", credits: []uint64{0}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ledger := NewLedger(newTestStore(t))
			const account = "alice"

			for _, c := range tt.credits {
				if err := ledger.Credit(account, c); err != nil {
					t.Fatalf("Credit() error = %v", err)
				}
			}

			got, err := ledger.Balance(account)
			if err != nil {
				t.Fatalf("Balance() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Balance() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLedger_BalanceUnknownAccount(t *testing.T) {
	ledger := NewLedger(newTestStore(t))
	got, err := ledger.Balance("nobody")
	if err != nil {
		t.Fatalf("Balance() error = %v", err)
	}
	if got != 0 {
		t.Errorf("Balance() = %d, want 0 for unknown account", got)
	}
}

func TestLedger_Debit(t *testing.T) {
	tests := []struct {
		name        string
		initial     uint64
		debit       uint64
		wantErr     error
		wantBalance uint64
	}{
		{name: "debit within balance", initial: 100, debit: 40, wantErr: nil, wantBalance: 60},
		{name: "debit exact balance", initial: 100, debit: 100, wantErr: nil, wantBalance: 0},
		{name: "debit beyond balance is rejected", initial: 100, debit: 150, wantErr: ErrInsufficientFunds, wantBalance: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ledger := NewLedger(newTestStore(t))
			const account = "bob"

			if err := ledger.Credit(account, tt.initial); err != nil {
				t.Fatalf("Credit() error = %v", err)
			}

			err := ledger.Debit(account, tt.debit)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Debit() error = %v, want %v", err, tt.wantErr)
			}

			got, err := ledger.Balance(account)
			if err != nil {
				t.Fatalf("Balance() error = %v", err)
			}
			if got != tt.wantBalance {
				t.Errorf("Balance() after Debit = %d, want %d", got, tt.wantBalance)
			}
		})
	}
}

func TestLedger_Transfer(t *testing.T) {
	t.Run("moves exact amount", func(t *testing.T) {
		ledger := NewLedger(newTestStore(t))
		if err := ledger.Credit("from", 100); err != nil {
			t.Fatalf("Credit() error = %v", err)
		}
		if err := ledger.Credit("to", 10); err != nil {
			t.Fatalf("Credit() error = %v", err)
		}

		if err := ledger.Transfer("from", "to", 30); err != nil {
			t.Fatalf("Transfer() error = %v", err)
		}

		fromBal, _ := ledger.Balance("from")
		toBal, _ := ledger.Balance("to")
		if fromBal != 70 {
			t.Errorf("from balance = %d, want 70", fromBal)
		}
		if toBal != 40 {
			t.Errorf("to balance = %d, want 40", toBal)
		}
	})

	t.Run("insufficient funds leaves both balances unchanged (atomic)", func(t *testing.T) {
		ledger := NewLedger(newTestStore(t))
		if err := ledger.Credit("from", 20); err != nil {
			t.Fatalf("Credit() error = %v", err)
		}
		if err := ledger.Credit("to", 5); err != nil {
			t.Fatalf("Credit() error = %v", err)
		}

		err := ledger.Transfer("from", "to", 50)
		if !errors.Is(err, ErrInsufficientFunds) {
			t.Fatalf("Transfer() error = %v, want ErrInsufficientFunds", err)
		}

		fromBal, _ := ledger.Balance("from")
		toBal, _ := ledger.Balance("to")
		if fromBal != 20 {
			t.Errorf("from balance = %d, want 20 (unchanged)", fromBal)
		}
		if toBal != 5 {
			t.Errorf("to balance = %d, want 5 (unchanged)", toBal)
		}
	})
}

func TestLedger_SelfTransfer(t *testing.T) {
	t.Run("affordable self-transfer is a no-op", func(t *testing.T) {
		ledger := NewLedger(newTestStore(t))
		if err := ledger.Credit("solo", 100); err != nil {
			t.Fatalf("Credit() error = %v", err)
		}

		// Transferring an affordable amount to oneself must leave the balance
		// unchanged rather than minting credits (regression: two batched writes
		// to the same key used to leave balance+amount).
		if err := ledger.Transfer("solo", "solo", 30); err != nil {
			t.Fatalf("Transfer() error = %v", err)
		}

		bal, _ := ledger.Balance("solo")
		if bal != 100 {
			t.Errorf("balance after self-transfer = %d, want 100 (unchanged)", bal)
		}
	})

	t.Run("unaffordable self-transfer still returns ErrInsufficientFunds", func(t *testing.T) {
		ledger := NewLedger(newTestStore(t))
		if err := ledger.Credit("solo", 20); err != nil {
			t.Fatalf("Credit() error = %v", err)
		}

		err := ledger.Transfer("solo", "solo", 50)
		if !errors.Is(err, ErrInsufficientFunds) {
			t.Fatalf("Transfer() error = %v, want ErrInsufficientFunds", err)
		}

		bal, _ := ledger.Balance("solo")
		if bal != 20 {
			t.Errorf("balance after rejected self-transfer = %d, want 20 (unchanged)", bal)
		}
	})
}

func TestLedger_Persistence(t *testing.T) {
	// A balance written by one Ledger must be readable by another Ledger over
	// the same store, proving state is really persisted through Pebble.
	store := newTestStore(t)
	ledger := NewLedger(store)
	if err := ledger.Credit("carol", 250); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}

	reopened := NewLedger(store)
	got, err := reopened.Balance("carol")
	if err != nil {
		t.Fatalf("Balance() error = %v", err)
	}
	if got != 250 {
		t.Errorf("persisted Balance() = %d, want 250", got)
	}
}
