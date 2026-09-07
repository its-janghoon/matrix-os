package bridge

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

func orderHarness(t *testing.T, consensusOrdered bool) (*Bridge, *market.Ledger) {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ledger := market.NewLedger(store)
	params := AttestationParams{ChainID: big.NewInt(31337)}
	if consensusOrdered {
		return NewConsensusOrdered(ledger, store, params), ledger
	}
	return New(ledger, store, params), ledger
}

const orderTestAccount = "dfb781c10b66f1eb88372bbb038a03d2b0b507e8d772aacaaa39acc70ca5070f"

// TestConsensusOrderedBridgeRefusesDirectProcessBurn is the guard for the fork.
// A per-node relayer releases escrow on its own ledger and nowhere else, so on a
// validator set the nodes' escrow balances and their 1:1 backing invariant
// diverge - the same divergence that made FundAccount unsafe, except this one
// moves real collateral. Wiring the bridge itself into a watcher on such a
// network must fail loudly rather than silently fork.
func TestConsensusOrderedBridgeRefusesDirectProcessBurn(t *testing.T) {
	b, ledger := orderHarness(t, true)
	if err := ledger.Credit(EscrowAccount, 1000); err != nil {
		t.Fatalf("credit escrow: %v", err)
	}

	err := b.ProcessBurn(BurnEvent{
		ID:          "0xdeadbeef:0",
		ToAccount:   orderTestAccount,
		ERC20Amount: token.NativeToERC20(500),
	})
	if !errors.Is(err, ErrConsensusOrdered) {
		t.Fatalf("error = %v, want ErrConsensusOrdered", err)
	}
	bal, err := ledger.Balance(EscrowAccount)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if bal != 1000 {
		t.Fatalf("escrow = %d, want 1000 unchanged after a refused direct burn", bal)
	}
}

// TestDirectBridgeRefusesAttestedUnlock is the other half of the exclusivity.
// ProcessBurn takes b.mu then the ledger; ApplyAttestedUnlock is called with the
// ledger already held. Two orders over the same two locks is a deadlock waiting
// for the run where both paths are live, so the modes exclude each other rather
// than relying on the caller.
func TestDirectBridgeRefusesAttestedUnlock(t *testing.T) {
	b, ledger := orderHarness(t, false)
	if err := ledger.Credit(EscrowAccount, 1000); err != nil {
		t.Fatalf("credit escrow: %v", err)
	}
	err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return b.ApplyAttestedUnlock(ltx, BurnIDHash("0xdeadbeef:0"), orderTestAccount, 500)
	})
	if !errors.Is(err, ErrNotConsensusOrdered) {
		t.Fatalf("error = %v, want ErrNotConsensusOrdered", err)
	}
}

// TestAttestedUnlockReleasesEscrowExactlyOnce covers the working path and the
// idempotence the consensus interface contract requires: a node that applies the
// same committed block twice has to reach the same escrow balance.
func TestAttestedUnlockReleasesEscrowExactlyOnce(t *testing.T) {
	b, ledger := orderHarness(t, true)
	if err := ledger.Credit(EscrowAccount, 1000); err != nil {
		t.Fatalf("credit escrow: %v", err)
	}
	hash := BurnIDHash("0xdeadbeef:0")

	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return b.ApplyAttestedUnlock(ltx, hash, orderTestAccount, 500)
	}); err != nil {
		t.Fatalf("first unlock: %v", err)
	}

	err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return b.ApplyAttestedUnlock(ltx, hash, orderTestAccount, 500)
	})
	if !errors.Is(err, ErrBurnAlreadyProcessed) {
		t.Fatalf("second unlock error = %v, want ErrBurnAlreadyProcessed", err)
	}

	escrow, err := ledger.Balance(EscrowAccount)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if escrow != 500 {
		t.Fatalf("escrow = %d, want 500: the burn was released twice", escrow)
	}
	paid, err := ledger.Balance(orderTestAccount)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if paid != 500 {
		t.Fatalf("recipient = %d, want 500", paid)
	}
}

// TestAttestedUnlockAndProcessBurnShareTheSameReplayMarker: the two paths must
// track the same burn as the same burn, or a network that switched modes could
// release one burn twice.
func TestAttestedUnlockAndProcessBurnShareTheSameReplayMarker(t *testing.T) {
	burnID := "0xdeadbeef:7"

	direct, ledger := orderHarness(t, false)
	if err := ledger.Credit(EscrowAccount, 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := direct.ProcessBurn(BurnEvent{
		ID:          burnID,
		ToAccount:   orderTestAccount,
		ERC20Amount: token.NativeToERC20(500),
	}); err != nil {
		t.Fatalf("ProcessBurn: %v", err)
	}

	// Same store, now read by a consensus-ordered bridge: the marker written by
	// the direct path must already block the attested path.
	direct.consensusOrdered = true
	err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return direct.ApplyAttestedUnlock(ltx, BurnIDHash(burnID), orderTestAccount, 500)
	})
	if !errors.Is(err, ErrBurnAlreadyProcessed) {
		t.Fatalf("error = %v, want ErrBurnAlreadyProcessed: the two paths do not share a replay marker", err)
	}
}

// TestAttestedUnlockRejectsNonsense pins the input checks, since consensus hands
// these values straight from a recipient string.
func TestAttestedUnlockRejectsNonsense(t *testing.T) {
	b, ledger := orderHarness(t, true)
	if err := ledger.Credit(EscrowAccount, 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	cases := []struct {
		name    string
		hash    string
		account string
		amount  uint64
	}{
		{"empty burn id", "", orderTestAccount, 500},
		{"empty recipient", BurnIDHash("a"), "", 500},
		{"zero amount", BurnIDHash("a"), orderTestAccount, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ledger.Atomically(func(ltx market.LedgerTx) error {
				return b.ApplyAttestedUnlock(ltx, tc.hash, tc.account, tc.amount)
			})
			if err == nil {
				t.Fatal("want an error")
			}
		})
	}
	bal, err := ledger.Balance(EscrowAccount)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if bal != 1000 {
		t.Fatalf("escrow = %d, want 1000 unchanged", bal)
	}
}
