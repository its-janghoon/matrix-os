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

// Rotating the maintainer account.
//
// maintainer_account used to be startup config and nothing else, so moving to a
// fresh key meant editing YAML on every validator and restarting them - and
// because the share is consensus arithmetic, a network part-way through that
// edit computes different balances from the same block, which is a fork. The
// operation most likely to be needed was the one most likely to break the chain.

func rotateEngine(t *testing.T, maintainer string) *Engine {
	t.Helper()
	return &Engine{maintainerShareBPS: 2000}
}

func TestOnlyTheCurrentMaintainerMayRotate(t *testing.T) {
	current, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	stranger, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	successor := newAccountID(t)

	e := rotateEngine(t, current.AccountID())
	e.setMaintainerAccount(current.AccountID())

	// The account being paid may name its successor.
	ok := &token.Transaction{To: MaintainerRotateRecipient(successor), From: current.PublicKey}
	if err := e.verifyMaintainerRotateLocked(ok); err != nil {
		t.Fatalf("the current maintainer was refused its own rotation: %v", err)
	}

	// Nobody else may. This is the whole authorization: the signature over the
	// transaction is verified before this runs, so proving the sender is the
	// maintainer proves the maintainer authorized it.
	bad := &token.Transaction{To: MaintainerRotateRecipient(successor), From: stranger.PublicKey}
	if err := e.verifyMaintainerRotateLocked(bad); err == nil {
		t.Fatal("a stranger redirected the maintainer fee to an account of their choosing")
	}
}

func TestRotationRefusesAnUnspendableSuccessor(t *testing.T) {
	current, _ := token.GenerateAccount()
	e := rotateEngine(t, current.AccountID())
	e.setMaintainerAccount(current.AccountID())

	for _, bad := range []string{"", "my-wallet", strings.ToUpper(newAccountID(t)), newAccountID(t)[:63]} {
		tx := &token.Transaction{To: maintainerRotatePrefix + bad, From: current.PublicKey}
		if err := e.verifyMaintainerRotateLocked(tx); err == nil {
			t.Fatalf("accepted a rotation to %q; the fee would be paid there every block, "+
				"forever, and look exactly like it worked", bad)
		}
		// And Submit refuses it outright, so it cannot sit in a mempool.
		if err := isPermanentlyInvalidReserved(tx); err == nil {
			t.Fatalf("isPermanentlyInvalidReserved accepted a rotation to %q", bad)
		}
	}
}

func TestRotationCarriesNoValue(t *testing.T) {
	current, _ := token.GenerateAccount()
	tx := &token.Transaction{
		To:     MaintainerRotateRecipient(newAccountID(t)),
		From:   current.PublicKey,
		Amount: 1,
	}
	if err := isPermanentlyInvalidReserved(tx); err == nil {
		t.Fatal("value sent to a rotation recipient was allowed; it would be stranded at an " +
			"id nobody holds a key for")
	}
}

func TestRotationToTheSameAccountIsRefused(t *testing.T) {
	current, _ := token.GenerateAccount()
	e := rotateEngine(t, current.AccountID())
	e.setMaintainerAccount(current.AccountID())
	tx := &token.Transaction{
		To:   MaintainerRotateRecipient(current.AccountID()),
		From: current.PublicKey,
	}
	if err := e.verifyMaintainerRotateLocked(tx); err == nil {
		t.Fatal("a no-op rotation was accepted")
	}
}

func TestNothingToRotateWhenNoMaintainerIsSet(t *testing.T) {
	sender, _ := token.GenerateAccount()
	e := rotateEngine(t, "")
	e.setMaintainerAccount("")
	tx := &token.Transaction{To: MaintainerRotateRecipient(newAccountID(t)), From: sender.PublicKey}
	if err := e.verifyMaintainerRotateLocked(tx); err == nil {
		t.Fatal("a rotation was accepted on a network that pays no maintainer share, which " +
			"would let anyone appoint themselves")
	}
}

// TestApplyingARotationChangesWhoIsPaid ties the recipient to the money: after
// the rotation applies, the fee distribution pays the successor.
func TestApplyingARotationChangesWhoIsPaid(t *testing.T) {
	current, _ := token.GenerateAccount()
	successor := newAccountID(t)
	e := rotateEngine(t, current.AccountID())
	e.setMaintainerAccount(current.AccountID())

	tx := &token.Transaction{To: MaintainerRotateRecipient(successor), From: current.PublicKey}
	if err := e.applyMaintainerRotate(tx); err != nil {
		t.Fatalf("applyMaintainerRotate: %v", err)
	}
	if got := e.MaintainerAccountInForce(); got != successor {
		t.Fatalf("account in force = %q, want %q", got, successor)
	}

	accts, pubs := testKeys(t, 2)
	vs, _ := NewValidatorSet(pubs)
	ledger, done := feeLedger(t)
	defer done()
	if err := ledger.Credit(FeeAccrualAccount(), 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return distributeFeesLocked(ltx, vs, e.MaintainerAccountInForce(), e.maintainerShareBPS)
	}); err != nil {
		t.Fatalf("distributeFeesLocked: %v", err)
	}
	if bal, _ := ledger.Balance(successor); bal != 200 {
		t.Fatalf("the successor was paid %d, want 200", bal)
	}
	if bal, _ := ledger.Balance(current.AccountID()); bal != 0 {
		t.Fatalf("the previous maintainer was still paid %d after rotating away", bal)
	}
	_ = accts
}

// TestAMalformedRotationInACommittedBlockDoesNotWedge. Block validation refuses
// these, so one arriving here means a block was committed that should not have
// been. A node must not rotate to it and must not stop.
func TestAMalformedRotationInACommittedBlockDoesNotWedge(t *testing.T) {
	current, _ := token.GenerateAccount()
	e := rotateEngine(t, current.AccountID())
	e.setMaintainerAccount(current.AccountID())

	tx := &token.Transaction{To: maintainerRotatePrefix + "not-an-account", From: current.PublicKey}
	if err := e.applyMaintainerRotate(tx); err != nil {
		t.Fatalf("a malformed rotation wedged the node: %v", err)
	}
	if got := e.MaintainerAccountInForce(); got != current.AccountID() {
		t.Fatalf("account in force = %q; a malformed rotation moved the money", got)
	}
}

func TestARotationIsAReservedRecipientAndPaysNoFee(t *testing.T) {
	to := MaintainerRotateRecipient(newAccountID(t))
	if !IsReservedRecipient(to) {
		t.Fatal("a rotation is not a reserved recipient, so it would appear in transaction " +
			"history as an ordinary transfer")
	}
	if paysFee(to) {
		t.Fatal("a rotation pays the protocol fee; it carries no value and the fee would be zero, " +
			"but a list that is right by accident is one edit away from being wrong")
	}
}
