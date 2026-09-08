package consensus

import (
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// The maintainer share: a standing cut of the protocol fee for whoever keeps the
// network running.
//
// The properties that matter are that it is BOUNDED (a maintainer cannot take
// the budget that buys the network its security), that it is EXACT (it is
// consensus arithmetic, so a rounding difference between two nodes is a fork),
// and that it is OFF unless someone turns it on.

func feeLedger(t *testing.T) (*market.Ledger, func()) {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	return market.NewLedger(store), func() { _ = store.Close() }
}

func newAccountID(t *testing.T) string {
	t.Helper()
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	return acct.AccountID()
}

// TestTheMaintainerIsPaidBeforeValidatorsAndTheRestIsSplit is the whole
// mechanism in one: 1000 accrued, a 20% maintainer share, three equal
// validators. The maintainer gets 200 and the validators divide the 800.
func TestTheMaintainerIsPaidBeforeValidatorsAndTheRestIsSplit(t *testing.T) {
	accts, pubs := testKeys(t, 3)
	vs, err := NewValidatorSet(pubs)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	ledger, done := feeLedger(t)
	defer done()

	maintainer := newAccountID(t)
	if err := ledger.Credit(FeeAccrualAccount(), 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return distributeFeesLocked(ltx, vs, maintainer, 2000) // 20% of the fee
	}); err != nil {
		t.Fatalf("distributeFeesLocked: %v", err)
	}

	got, _ := ledger.Balance(maintainer)
	if got != 200 {
		t.Fatalf("maintainer got %d, want 200", got)
	}
	var toValidators uint64
	for _, a := range accts {
		bal, _ := ledger.Balance(a.AccountID())
		toValidators += bal
	}
	dust, _ := ledger.Balance(FeeAccrualAccount())
	if toValidators+dust != 800 {
		t.Fatalf("validators got %d with %d dust, want 800 between them", toValidators, dust)
	}
	// Nothing is created or lost: the fee is only ever redistributed.
	if got+toValidators+dust != 1000 {
		t.Fatalf("1000 accrued became %d", got+toValidators+dust)
	}
}

// TestNobodyIsPaidAMaintainerShareByDefault. It is a tax on users, so it is off
// until someone turns it on.
func TestNobodyIsPaidAMaintainerShareByDefault(t *testing.T) {
	accts, pubs := testKeys(t, 2)
	vs, _ := NewValidatorSet(pubs)
	ledger, done := feeLedger(t)
	defer done()

	if err := ledger.Credit(FeeAccrualAccount(), 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return distributeFeesLocked(ltx, vs, "", 0)
	}); err != nil {
		t.Fatalf("distributeFeesLocked: %v", err)
	}
	var total uint64
	for _, a := range accts {
		bal, _ := ledger.Balance(a.AccountID())
		total += bal
	}
	if total != 1000 {
		t.Fatalf("validators got %d of 1000; something took a cut with no share configured", total)
	}
}

// TestAnAccountWithNoShareAndAShareWithNoAccountBothPayNothing pins that the two
// halves are needed together, so a half-finished config cannot pay a stranger or
// silently pay nobody while charging users.
func TestAnAccountWithNoShareAndAShareWithNoAccountBothPayNothing(t *testing.T) {
	ledger, done := feeLedger(t)
	defer done()
	if err := ledger.Credit(FeeAccrualAccount(), 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	maintainer := newAccountID(t)

	for _, c := range []struct {
		name    string
		account string
		bps     uint32
	}{
		{"account but no share", maintainer, 0},
		{"share but no account", "", 2000},
	} {
		var took uint64
		if err := ledger.Atomically(func(ltx market.LedgerTx) error {
			var err error
			took, err = takeMaintainerShareLocked(ltx, 1000, c.account, c.bps)
			return err
		}); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if took != 0 {
			t.Fatalf("%s: took %d", c.name, took)
		}
	}
}

// TestTheShareIsExactAtEveryScale. This is consensus arithmetic: two nodes that
// round differently compute different balances from the same block, which is a
// fork. The accrual can also be near the supply cap, where a naive
// accrued*bps/10000 wraps.
func TestTheShareIsExactAtEveryScale(t *testing.T) {
	ledger, done := feeLedger(t)
	defer done()
	maintainer := newAccountID(t)

	cases := []struct {
		accrued uint64
		bps     uint32
		want    uint64
	}{
		{1000, 2000, 200},
		{1, 5000, 0},       // half of one base unit truncates to nothing
		{2, 5000, 1},       // and the next unit pays
		{9999, 1, 0},       // 0.01% of 9999 is 0.9999
		{10000, 1, 1},      // and of 10000 is exactly 1
		{1e18, 5000, 5e17}, // the whole native supply cap at the ceiling
		{1e18, 1, 1e14},    // and at the smallest share
	}
	for _, c := range cases {
		if err := ledger.Credit(FeeAccrualAccount(), c.accrued); err != nil {
			t.Fatalf("credit: %v", err)
		}
		var got uint64
		if err := ledger.Atomically(func(ltx market.LedgerTx) error {
			var err error
			got, err = takeMaintainerShareLocked(ltx, c.accrued, maintainer, c.bps)
			return err
		}); err != nil {
			t.Fatalf("accrued %d at %d bps: %v", c.accrued, c.bps, err)
		}
		if got != c.want {
			t.Fatalf("accrued %d at %d bps took %d, want %d", c.accrued, c.bps, got, c.want)
		}
		// Drain so the next case starts clean.
		if err := ledger.Atomically(func(ltx market.LedgerTx) error {
			bal, err := ltx.Balance(FeeAccrualAccount())
			if err != nil || bal == 0 {
				return err
			}
			return ltx.Transfer(FeeAccrualAccount(), maintainer, bal)
		}); err != nil {
			t.Fatalf("drain: %v", err)
		}
	}
}

// TestTheValidatorsAlwaysKeepAtLeastHalf. The fee exists to pay for validating.
// A maintainer funding themselves out of most of it would be spending the budget
// that buys the network its security, so the ceiling is in code rather than in
// config.
func TestTheValidatorsAlwaysKeepAtLeastHalf(t *testing.T) {
	if uint64(MaxMaintainerShareBasisPoints) > basisPointDivisor/2 {
		t.Fatalf("the ceiling is %d basis points, which lets a maintainer take more than "+
			"half the fee", MaxMaintainerShareBasisPoints)
	}
	// And the worst case against transferred value is bounded by the fee's own
	// ceiling: 1% of a transfer, half of which is the maintainer's.
	worst := FeeFor(1_000_000, MaxFeeBasisPoints)
	ledger, done := feeLedger(t)
	defer done()
	maintainer := newAccountID(t)
	if err := ledger.Credit(FeeAccrualAccount(), worst); err != nil {
		t.Fatalf("credit: %v", err)
	}
	var took uint64
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		var err error
		took, err = takeMaintainerShareLocked(ltx, worst, maintainer, MaxMaintainerShareBasisPoints)
		return err
	}); err != nil {
		t.Fatalf("atomically: %v", err)
	}
	if took != 5_000 {
		t.Fatalf("the maximum take on a 1,000,000 transfer is %d base units, want 5000 "+
			"(0.5%%)", took)
	}
}

// TestAMisconfiguredMaintainerRefusesToStart. Every one of these would otherwise
// be a fee taken every block, forever, into somewhere it cannot be recovered
// from - with nothing to notice it by.
func TestAMisconfiguredMaintainerRefusesToStart(t *testing.T) {
	valid := newAccountID(t)

	for name, cfg := range map[string]struct {
		account string
		bps     uint32
		wantMsg string
	}{
		"share over the ceiling": {valid, MaxMaintainerShareBasisPoints + 1, "ceiling"},
		"share with no account":  {"", 2000, "nobody to pay"},
		"reserved account":       {FeeAccrualAccount(), 2000, "reserved"},
		"reward pool":            {rewardPoolAccount, 2000, "reserved"},
		"not an account id":      {"my-wallet", 2000, "not an account id"},
		"uppercase hex":          {strings.ToUpper(valid), 2000, "not an account id"},
		"truncated id":           {valid[:63], 2000, "not an account id"},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkMaintainerConfig(cfg.account, cfg.bps)
			if err == nil {
				t.Fatalf("accepted account=%q bps=%d", cfg.account, cfg.bps)
			}
			if !strings.Contains(err.Error(), cfg.wantMsg) {
				t.Fatalf("err = %v, want it to mention %q", err, cfg.wantMsg)
			}
		})
	}

	if err := checkMaintainerConfig(valid, 2000); err != nil {
		t.Fatalf("a valid maintainer config was refused: %v", err)
	}
	if err := checkMaintainerConfig("", 0); err != nil {
		t.Fatalf("no maintainer at all was refused: %v", err)
	}
}

func TestIsAccountIDAcceptsOnlyLowercaseHex64(t *testing.T) {
	valid := newAccountID(t)
	if !isAccountID(valid) {
		t.Fatalf("a generated account id %q was rejected", valid)
	}
	for _, bad := range []string{"", "abc", strings.ToUpper(valid), valid + "0", valid[:63],
		strings.Replace(valid, valid[:1], "g", 1)} {
		if isAccountID(bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
}
