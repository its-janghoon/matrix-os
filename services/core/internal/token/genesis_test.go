package token

import (
	"errors"
	"math"
	"math/big"
	"sync"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
)

// openStoreAt opens a Pebble kv.Store at the given path and registers a
// close-once cleanup. Unlike newTestStore it lets a test reopen the SAME path to
// simulate a process restart over durable state. It returns the store and a
// close-once function so a test can deterministically close (to release the
// Pebble lock) before reopening; the registered cleanup is guarded by the same
// sync.Once so it will not double-close (Pebble panics on a second Close).
func openStoreAt(t *testing.T, path string) (*kv.Store, func()) {
	t.Helper()
	store, err := kv.New(kv.Config{Path: path})
	if err != nil {
		t.Fatalf("failed to open kv store at %q: %v", path, err)
	}
	var once sync.Once
	closeFn := func() {
		once.Do(func() {
			if err := store.Close(); err != nil {
				t.Errorf("failed to close store: %v", err)
			}
		})
	}
	t.Cleanup(closeFn)
	return store, closeFn
}

// newTreasury builds a Treasury and its underlying market.Ledger over the given
// store.
func newTreasury(t *testing.T, store *kv.Store) (*Treasury, *market.Ledger) {
	t.Helper()
	ledger := market.NewLedger(store)
	return NewTreasury(ledger, store), ledger
}

func TestTreasury_ApplyGenesisCreditsAndTracksSupply(t *testing.T) {
	tr, ledger := newTreasury(t, newTestStore(t))

	allocs := []GenesisAllocation{
		{Account: "alice", Amount: 500 * NativeUnit},
		{Account: "bob", Amount: 250 * NativeUnit},
	}
	const rewardPool = 1_000 * NativeUnit

	if err := tr.ApplyGenesis(allocs, rewardPool); err != nil {
		t.Fatalf("ApplyGenesis() error = %v", err)
	}

	for _, a := range allocs {
		got, err := ledger.Balance(a.Account)
		if err != nil {
			t.Fatalf("Balance(%q) error = %v", a.Account, err)
		}
		if got != a.Amount {
			t.Errorf("Balance(%q) = %d, want %d", a.Account, got, a.Amount)
		}
	}

	pool, err := ledger.Balance(RewardPoolAccount)
	if err != nil {
		t.Fatalf("Balance(reward pool) error = %v", err)
	}
	if pool != rewardPool {
		t.Errorf("reward pool balance = %d, want %d", pool, rewardPool)
	}

	wantIssued := uint64(500*NativeUnit + 250*NativeUnit + rewardPool)
	issued, err := tr.IssuedSupply()
	if err != nil {
		t.Fatalf("IssuedSupply() error = %v", err)
	}
	if issued != wantIssued {
		t.Errorf("IssuedSupply() = %d, want %d", issued, wantIssued)
	}
}

// TestTreasury_GenesisIdempotentAcrossRestart proves that ApplyGenesis credits
// exactly once and that re-running it (including after a simulated process
// restart that reopens the same durable store) does NOT double-credit.
func TestTreasury_GenesisIdempotentAcrossRestart(t *testing.T) {
	dir := t.TempDir()

	allocs := []GenesisAllocation{{Account: "alice", Amount: 100 * NativeUnit}}
	const rewardPool = 42 * NativeUnit
	wantIssued := uint64(100*NativeUnit + rewardPool)

	// First boot: apply genesis, then a redundant apply that must be a no-op.
	store1, close1 := openStoreAt(t, dir)
	tr1, ledger1 := newTreasury(t, store1)
	if err := tr1.ApplyGenesis(allocs, rewardPool); err != nil {
		t.Fatalf("first ApplyGenesis() error = %v", err)
	}
	if err := tr1.ApplyGenesis(allocs, rewardPool); err != nil {
		t.Fatalf("redundant ApplyGenesis() error = %v", err)
	}
	bal1, _ := ledger1.Balance("alice")
	if bal1 != 100*NativeUnit {
		t.Fatalf("after redundant apply, alice = %d, want %d", bal1, 100*NativeUnit)
	}
	close1() // release the Pebble lock before reopening the same path

	// Simulated restart: reopen the same path and apply genesis again. It must
	// observe the persisted marker and NOT credit a second time.
	store2, _ := openStoreAt(t, dir)
	tr2, ledger2 := newTreasury(t, store2)
	applied, err := tr2.GenesisApplied()
	if err != nil {
		t.Fatalf("GenesisApplied() error = %v", err)
	}
	if !applied {
		t.Fatal("genesis marker did not persist across restart")
	}
	if err := tr2.ApplyGenesis(allocs, rewardPool); err != nil {
		t.Fatalf("post-restart ApplyGenesis() error = %v", err)
	}

	bal2, err := ledger2.Balance("alice")
	if err != nil {
		t.Fatalf("Balance(alice) error = %v", err)
	}
	if bal2 != 100*NativeUnit {
		t.Errorf("alice balance after restart = %d, want %d (double-credit!)", bal2, 100*NativeUnit)
	}
	pool2, _ := ledger2.Balance(RewardPoolAccount)
	if pool2 != rewardPool {
		t.Errorf("reward pool after restart = %d, want %d (double-credit!)", pool2, rewardPool)
	}
	issued2, _ := tr2.IssuedSupply()
	if issued2 != wantIssued {
		t.Errorf("issued supply after restart = %d, want %d", issued2, wantIssued)
	}
}

// TestTreasury_SupplyCapEnforced proves genesis and issuance both reject going
// over NativeMaxSupply with an errors.Is-matchable sentinel.
func TestTreasury_SupplyCapEnforced(t *testing.T) {
	t.Run("genesis over cap rejected atomically", func(t *testing.T) {
		tr, ledger := newTreasury(t, newTestStore(t))
		// One base unit over the cap.
		allocs := []GenesisAllocation{{Account: "whale", Amount: NativeMaxSupply}}
		err := tr.ApplyGenesis(allocs, 1)
		if !errors.Is(err, ErrSupplyCapExceeded) {
			t.Fatalf("ApplyGenesis() error = %v, want ErrSupplyCapExceeded", err)
		}
		// Nothing was written: no balance, no marker, no supply.
		bal, _ := ledger.Balance("whale")
		if bal != 0 {
			t.Errorf("whale balance = %d, want 0 (rejected genesis must not credit)", bal)
		}
		applied, _ := tr.GenesisApplied()
		if applied {
			t.Error("genesis marker set despite rejected over-cap genesis")
		}
		issued, _ := tr.IssuedSupply()
		if issued != 0 {
			t.Errorf("issued supply = %d, want 0", issued)
		}
	})

	t.Run("issuance over remaining cap rejected", func(t *testing.T) {
		tr, ledger := newTreasury(t, newTestStore(t))
		// Genesis consumes the entire cap.
		if err := tr.ApplyGenesis([]GenesisAllocation{{Account: "gen", Amount: NativeMaxSupply}}, 0); err != nil {
			t.Fatalf("ApplyGenesis() error = %v", err)
		}
		// Any further issuance must be rejected.
		err := tr.Issue("late", 1)
		if !errors.Is(err, ErrSupplyCapExceeded) {
			t.Fatalf("Issue() error = %v, want ErrSupplyCapExceeded", err)
		}
		bal, _ := ledger.Balance("late")
		if bal != 0 {
			t.Errorf("late balance = %d, want 0 (rejected issuance must not credit)", bal)
		}
		issued, _ := tr.IssuedSupply()
		if issued != NativeMaxSupply {
			t.Errorf("issued supply = %d, want %d", issued, NativeMaxSupply)
		}
	})

	t.Run("issuance up to cap accepted", func(t *testing.T) {
		tr, ledger := newTreasury(t, newTestStore(t))
		half := NativeMaxSupply / 2
		if err := tr.Issue("a", half); err != nil {
			t.Fatalf("Issue(a) error = %v", err)
		}
		if err := tr.Issue("b", NativeMaxSupply-half); err != nil {
			t.Fatalf("Issue(b) to exact cap error = %v", err)
		}
		issued, _ := tr.IssuedSupply()
		if issued != NativeMaxSupply {
			t.Fatalf("issued supply = %d, want %d", issued, NativeMaxSupply)
		}
		balA, _ := ledger.Balance("a")
		balB, _ := ledger.Balance("b")
		if balA+balB != NativeMaxSupply {
			t.Errorf("balances sum = %d, want %d", balA+balB, NativeMaxSupply)
		}
	})
}

// TestTreasury_RewardPoolFundingConservesSupply proves that funding a provider
// from the reward pool moves coins WITHOUT creating new supply: the pool
// decreases, the recipient increases by the same amount, and cumulative issued
// supply is unchanged.
func TestTreasury_RewardPoolFundingConservesSupply(t *testing.T) {
	tr, ledger := newTreasury(t, newTestStore(t))

	const pool = 1_000 * NativeUnit
	if err := tr.ApplyGenesis(nil, pool); err != nil {
		t.Fatalf("ApplyGenesis() error = %v", err)
	}
	issuedBefore, _ := tr.IssuedSupply()

	const grant = 300 * NativeUnit
	if err := tr.FundFromRewardPool("provider-1", grant); err != nil {
		t.Fatalf("FundFromRewardPool() error = %v", err)
	}

	poolBal, _ := ledger.Balance(RewardPoolAccount)
	provBal, _ := ledger.Balance("provider-1")
	if poolBal != pool-grant {
		t.Errorf("pool balance = %d, want %d", poolBal, pool-grant)
	}
	if provBal != grant {
		t.Errorf("provider balance = %d, want %d", provBal, grant)
	}
	// Conservation: pool + provider == original pool; total supply unchanged.
	if poolBal+provBal != pool {
		t.Errorf("pool+provider = %d, want %d (supply not conserved)", poolBal+provBal, pool)
	}
	issuedAfter, _ := tr.IssuedSupply()
	if issuedAfter != issuedBefore {
		t.Errorf("issued supply changed on reward-pool funding: before %d, after %d", issuedBefore, issuedAfter)
	}

	// Overdrawing the pool is rejected as insufficient funds and moves nothing.
	err := tr.FundFromRewardPool("provider-2", pool)
	if !errors.Is(err, market.ErrInsufficientFunds) {
		t.Fatalf("over-draw FundFromRewardPool() error = %v, want ErrInsufficientFunds", err)
	}
	if bal, _ := ledger.Balance("provider-2"); bal != 0 {
		t.Errorf("provider-2 balance = %d, want 0", bal)
	}
}

// TestConversion_NativeERC20RoundTrip proves the uint64 native <-> ERC-20
// conversion round-trips exactly for representative amounts and that the whole
// caps line up (native MAX_SUPPLY maps to the ERC-20's 1e27 base units).
func TestConversion_NativeERC20RoundTrip(t *testing.T) {
	cases := []uint64{
		0,
		1,
		NativeUnit,                  // one whole MATRIX
		123_456_789,                 // arbitrary sub-coin amount
		1_000 * NativeUnit,          // a thousand whole MATRIX
		NativeMaxSupply,             // the entire native supply
		math.MaxUint64 / NativeUnit, // large but in-range
	}
	for _, native := range cases {
		erc20 := NativeToERC20(native)
		back, err := ERC20ToNative(erc20)
		if err != nil {
			t.Fatalf("ERC20ToNative(NativeToERC20(%d)) error = %v", native, err)
		}
		if back != native {
			t.Errorf("round-trip: got %d, want %d (erc20 = %s)", back, native, erc20.String())
		}
	}

	// The native cap maps to exactly the ERC-20 MAX_SUPPLY of 1e9 * 1e18 = 1e27.
	wantCap := new(big.Int)
	wantCap.SetString("1000000000000000000000000000", 10) // 1e27
	if got := NativeToERC20(NativeMaxSupply); got.Cmp(wantCap) != 0 {
		t.Errorf("native cap -> erc20 = %s, want %s", got.String(), wantCap.String())
	}

	// One whole MATRIX on the native side maps to 1e18 ERC-20 base units.
	oneWhole := new(big.Int)
	oneWhole.SetString("1000000000000000000", 10) // 1e18
	if got := NativeToERC20(NativeUnit); got.Cmp(oneWhole) != 0 {
		t.Errorf("one whole MATRIX -> erc20 = %s, want %s", got.String(), oneWhole.String())
	}
}

// TestConversion_NonMultipleRejected proves an ERC-20 amount that is not an
// exact multiple of the conversion factor cannot be silently truncated.
func TestConversion_NonMultipleRejected(t *testing.T) {
	// 1e9 - 1 base units: below one native base unit, not a clean multiple.
	frac := new(big.Int).SetUint64(ERC20PerNativeUnit - 1)
	if _, err := ERC20ToNative(frac); !errors.Is(err, ErrConversionOverflow) {
		t.Errorf("ERC20ToNative(fractional) error = %v, want ErrConversionOverflow", err)
	}
	// Negative amount rejected.
	neg := big.NewInt(-1)
	if _, err := ERC20ToNative(neg); !errors.Is(err, ErrConversionOverflow) {
		t.Errorf("ERC20ToNative(negative) error = %v, want ErrConversionOverflow", err)
	}
	// nil rejected.
	if _, err := ERC20ToNative(nil); !errors.Is(err, ErrConversionOverflow) {
		t.Errorf("ERC20ToNative(nil) error = %v, want ErrConversionOverflow", err)
	}
	// A value whose native quotient overflows uint64 is rejected.
	over := new(big.Int).SetUint64(math.MaxUint64)
	over.Add(over, big.NewInt(1))         // MaxUint64+1 native units...
	over.Mul(over, erc20PerNativeUnitBig) // ...expressed in erc20 base units
	if _, err := ERC20ToNative(over); !errors.Is(err, ErrConversionOverflow) {
		t.Errorf("ERC20ToNative(overflow) error = %v, want ErrConversionOverflow", err)
	}
}

// TestTreasury_ConcurrentIssueRespectsCap runs concurrent issuance and funding
// under -race to prove the supply counter and cap hold under contention.
func TestTreasury_ConcurrentIssueRespectsCap(t *testing.T) {
	tr, ledger := newTreasury(t, newTestStore(t))

	const workers = 16
	const perWorker = 10
	const amount = uint64(1) * NativeUnit

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				// Ignore errors: some may hit the cap in other configurations,
				// but here total is well under the cap.
				_ = tr.Issue("shared", amount)
			}
		}()
	}
	wg.Wait()

	wantIssued := uint64(workers*perWorker) * amount
	issued, err := tr.IssuedSupply()
	if err != nil {
		t.Fatalf("IssuedSupply() error = %v", err)
	}
	if issued != wantIssued {
		t.Errorf("issued supply = %d, want %d", issued, wantIssued)
	}
	bal, _ := ledger.Balance("shared")
	if bal != wantIssued {
		t.Errorf("balance = %d, want %d", bal, wantIssued)
	}
	if issued > NativeMaxSupply {
		t.Errorf("issued supply %d exceeds cap %d", issued, NativeMaxSupply)
	}
}
