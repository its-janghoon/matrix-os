package bridge

import (
	"errors"
	"math/big"
	"path/filepath"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// newTestBridge builds a Bridge over a temporary Pebble store with a funded
// user account, returning the bridge, ledger, and the user account id.
func newTestBridge(t *testing.T, userStart uint64) (*Bridge, *market.Ledger, string) {
	t.Helper()
	store, err := kv.New(kv.Config{Path: filepath.Join(t.TempDir(), "db")})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ledger := market.NewLedger(store)
	if err := ledger.Credit("user", userStart); err != nil {
		t.Fatalf("credit user: %v", err)
	}
	b := New(ledger, store, testParams())
	return b, ledger, "user"
}

func TestLockEscrowsAndRecords(t *testing.T) {
	b, ledger, user := newTestBridge(t, 10*token.NativeUnit)
	recipient := Address{0xaa, 0xbb}

	ev, err := b.Lock(user, recipient, 3*token.NativeUnit)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	if ev.NativeAmount != 3*token.NativeUnit {
		t.Fatalf("unexpected native amount %d", ev.NativeAmount)
	}

	userBal, _ := ledger.Balance(user)
	if userBal != 7*token.NativeUnit {
		t.Fatalf("user balance = %d, want %d", userBal, 7*token.NativeUnit)
	}
	escrow, _ := ledger.Balance(EscrowAccount)
	if escrow != 3*token.NativeUnit {
		t.Fatalf("escrow = %d, want %d", escrow, 3*token.NativeUnit)
	}

	// ERC20 amount must be native * 1e9.
	want := token.NativeToERC20(3 * token.NativeUnit)
	if ev.ERC20Amount().Cmp(want) != 0 {
		t.Fatalf("erc20 amount = %s, want %s", ev.ERC20Amount(), want)
	}

	got, err := b.GetLock(ev.LockID)
	if err != nil {
		t.Fatalf("get lock: %v", err)
	}
	if got.Seq != ev.Seq || got.FromAccount != user {
		t.Fatalf("persisted lock mismatch: %+v", got)
	}
}

func TestLockInsufficientFunds(t *testing.T) {
	b, ledger, user := newTestBridge(t, token.NativeUnit)
	_, err := b.Lock(user, Address{0x01}, 5*token.NativeUnit)
	if !errors.Is(err, market.ErrInsufficientFunds) {
		t.Fatalf("expected ErrInsufficientFunds, got %v", err)
	}
	// Nothing should have moved.
	if bal, _ := ledger.Balance(user); bal != token.NativeUnit {
		t.Fatalf("user balance changed to %d", bal)
	}
	if esc, _ := ledger.Balance(EscrowAccount); esc != 0 {
		t.Fatalf("escrow changed to %d", esc)
	}
}

func TestLockUniqueIDs(t *testing.T) {
	b, _, user := newTestBridge(t, 10*token.NativeUnit)
	ev1, err := b.Lock(user, Address{0x01}, token.NativeUnit)
	if err != nil {
		t.Fatalf("lock1: %v", err)
	}
	ev2, err := b.Lock(user, Address{0x01}, token.NativeUnit)
	if err != nil {
		t.Fatalf("lock2: %v", err)
	}
	if ev1.LockID == ev2.LockID {
		t.Fatalf("expected distinct lock ids")
	}
	if ev2.Seq != ev1.Seq+1 {
		t.Fatalf("expected monotonic seq, got %d then %d", ev1.Seq, ev2.Seq)
	}
}

func TestProcessBurnUnlocks(t *testing.T) {
	b, ledger, user := newTestBridge(t, 10*token.NativeUnit)
	if _, err := b.Lock(user, Address{0x01}, 5*token.NativeUnit); err != nil {
		t.Fatalf("lock: %v", err)
	}

	burn := BurnEvent{
		ID:          "31337:0xabc:0",
		ToAccount:   user,
		ERC20Amount: token.NativeToERC20(2 * token.NativeUnit),
	}
	if err := b.ProcessBurn(burn); err != nil {
		t.Fatalf("process burn: %v", err)
	}

	// escrow 5 -> 3, user 5 -> 7.
	if esc, _ := ledger.Balance(EscrowAccount); esc != 3*token.NativeUnit {
		t.Fatalf("escrow = %d, want %d", esc, 3*token.NativeUnit)
	}
	if ub, _ := ledger.Balance(user); ub != 7*token.NativeUnit {
		t.Fatalf("user = %d, want %d", ub, 7*token.NativeUnit)
	}
}

func TestProcessBurnReplayRejected(t *testing.T) {
	b, _, user := newTestBridge(t, 10*token.NativeUnit)
	if _, err := b.Lock(user, Address{0x01}, 5*token.NativeUnit); err != nil {
		t.Fatalf("lock: %v", err)
	}
	burn := BurnEvent{ID: "burn-1", ToAccount: user, ERC20Amount: token.NativeToERC20(token.NativeUnit)}
	if err := b.ProcessBurn(burn); err != nil {
		t.Fatalf("first burn: %v", err)
	}
	err := b.ProcessBurn(burn)
	if !errors.Is(err, ErrBurnAlreadyProcessed) {
		t.Fatalf("expected ErrBurnAlreadyProcessed, got %v", err)
	}
}

func TestProcessBurnNonMultipleRejected(t *testing.T) {
	b, _, user := newTestBridge(t, 10*token.NativeUnit)
	if _, err := b.Lock(user, Address{0x01}, 5*token.NativeUnit); err != nil {
		t.Fatalf("lock: %v", err)
	}
	// Not an exact multiple of ERC20PerNativeUnit (1e9).
	burn := BurnEvent{ID: "b", ToAccount: user, ERC20Amount: big.NewInt(1_500_000_000)}
	if err := b.ProcessBurn(burn); !errors.Is(err, token.ErrConversionOverflow) {
		t.Fatalf("expected ErrConversionOverflow, got %v", err)
	}
}

func TestReconciliation(t *testing.T) {
	b, _, user := newTestBridge(t, 100*token.NativeUnit)

	// A sequence of locks and unlocks; reconciliation must hold at each step.
	steps := []struct {
		lock   bool
		amount uint64
		burnID string
	}{
		{lock: true, amount: 10 * token.NativeUnit},
		{lock: true, amount: 5 * token.NativeUnit},
		{lock: false, amount: 4 * token.NativeUnit, burnID: "b1"},
		{lock: true, amount: 20 * token.NativeUnit},
		{lock: false, amount: 11 * token.NativeUnit, burnID: "b2"},
	}

	var expectedOutstanding uint64
	for i, s := range steps {
		if s.lock {
			if _, err := b.Lock(user, Address{0x01}, s.amount); err != nil {
				t.Fatalf("step %d lock: %v", i, err)
			}
			expectedOutstanding += s.amount
		} else {
			burn := BurnEvent{ID: s.burnID, ToAccount: user, ERC20Amount: token.NativeToERC20(s.amount)}
			if err := b.ProcessBurn(burn); err != nil {
				t.Fatalf("step %d burn: %v", i, err)
			}
			expectedOutstanding -= s.amount
		}

		rec, err := b.Reconcile()
		if err != nil {
			t.Fatalf("step %d reconcile: %v", i, err)
		}
		if rec.OutstandingNative != expectedOutstanding {
			t.Fatalf("step %d outstanding = %d, want %d", i, rec.OutstandingNative, expectedOutstanding)
		}
		if rec.EscrowBalance != rec.OutstandingNative {
			t.Fatalf("step %d escrow %d != outstanding %d", i, rec.EscrowBalance, rec.OutstandingNative)
		}
		// 1:1 backing: wrapped ERC-20 must equal native converted by the factor.
		if rec.OutstandingERC20.Cmp(token.NativeToERC20(rec.OutstandingNative)) != 0 {
			t.Fatalf("step %d erc20 backing mismatch", i)
		}
	}
}

func TestLockThenAttestVerifies(t *testing.T) {
	b, _, user := newTestBridge(t, 10*token.NativeUnit)
	v1 := newTestAttestor(t)
	v2 := newTestAttestor(t)
	attestors := map[Address]bool{v1.Address(): true, v2.Address(): true}

	ev, err := b.Lock(user, Address{0x0a, 0x0b, 0x0c}, 4*token.NativeUnit)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	att, err := b.Attest(ev, []ValidatorSigner{{Label: "v1", Attestor: v1}, {Label: "v2", Attestor: v2}})
	if err != nil {
		t.Fatalf("attest: %v", err)
	}
	if err := att.Verify(attestors, 2, b.Params()); err != nil {
		t.Fatalf("attestation should verify: %v", err)
	}
	// The attested amount is the wrapped conversion of the locked native.
	if att.Amount.Cmp(token.NativeToERC20(4*token.NativeUnit)) != 0 {
		t.Fatalf("attested amount mismatch")
	}
}
