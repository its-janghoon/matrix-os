package consensus

import (
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// --- the arithmetic ----------------------------------------------------

func TestFeeForDoesNotOverflow(t *testing.T) {
	// The whole native supply at the maximum rate. amount*bps would be 1e20,
	// well past what a uint64 holds, so a naive implementation wraps here - and
	// a wrapped fee is a wrong balance on every node rather than an error
	// anywhere.
	const cap = uint64(1e18)
	got := FeeFor(cap, MaxFeeBasisPoints)
	if want := uint64(1e16); got != want {
		t.Fatalf("FeeFor(1e18, 100bps) = %d, want %d (1%%)", got, want)
	}
	if got > cap {
		t.Fatalf("fee %d exceeds the amount %d it was taken from", got, cap)
	}

	// And at the extreme end of the type.
	if got := FeeFor(^uint64(0), MaxFeeBasisPoints); got > ^uint64(0)/50 {
		t.Fatalf("FeeFor(max uint64, 100bps) = %d, which is more than 2%% - it wrapped", got)
	}
}

func TestFeeForRate(t *testing.T) {
	cases := []struct {
		amount uint64
		bps    uint32
		want   uint64
	}{
		{0, 100, 0},
		{1000, 0, 0},       // no rate, no fee
		{10_000, 100, 100}, // 1%
		{10_000, 50, 50},   // 0.5%
		{10_000, 1, 1},     // 0.01%
		{99, 100, 0},       // a fee below one base unit rounds to nothing
		{100, 100, 1},
		{123_456_789, 100, 1_234_567},
	}
	for _, c := range cases {
		if got := FeeFor(c.amount, c.bps); got != c.want {
			t.Errorf("FeeFor(%d, %d) = %d, want %d", c.amount, c.bps, got, c.want)
		}
	}
}

// The ceiling is in code, not in config validation, so a bad config cannot
// charge more than one percent however it is written.
func TestARateAboveTheCeilingRefusesToStart(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	vs, err := NewValidatorSet([]ed25519.PublicKey{acct.PublicKey})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	defer store.Close()
	bus := newMemBus()

	_, err = New(Config{
		Transport:      bus.endpoint("solo"),
		Validators:     vs,
		Chain:          NewBlockChain(store),
		Ledger:         market.NewLedger(store),
		Self:           acct,
		FeeBasisPoints: MaxFeeBasisPoints + 1,
	})
	if err == nil {
		t.Fatal("New accepted a fee above the ceiling; a mistyped rate must not become policy")
	}
}

// Protocol operations are not charged: taxing a bond would make posting stake
// lossy, and a set change carries no value to tax.
func TestProtocolOperationsPayNoFee(t *testing.T) {
	acct, _ := token.GenerateAccount()
	id := acct.AccountID()

	if paysFee(BondAccount(id)) {
		t.Error("a bond is charged a fee")
	}
	if paysFee(WithdrawRecipient(id)) {
		t.Error("a withdrawal is charged a fee")
	}
	if paysFee(AddValidatorRecipient(acct.PublicKey)) {
		t.Error("a set change is charged a fee")
	}
	if paysFee(FeeAccrualAccount()) {
		t.Error("the fee accrual account is charged a fee")
	}
	if paysFee("") {
		t.Error("an empty recipient is charged a fee")
	}
	if !paysFee(id) {
		t.Error("an ordinary account is not charged a fee")
	}
}

// --- the split ---------------------------------------------------------

// The remainder of an uneven split has to stay somewhere rather than be lost or
// handed to whoever sorts first.
func TestFeeSplitLeavesNoDustBehindAndLosesNothing(t *testing.T) {
	accts, pubs := testKeys(t, 3)
	vs, err := NewValidatorSet(pubs)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	defer store.Close()
	ledger := market.NewLedger(store)

	// 100 fees over 3 equal validators: 33 each, 1 left over.
	if err := ledger.Credit(FeeAccrualAccount(), 100); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return distributeFeesLocked(ltx, vs)
	}); err != nil {
		t.Fatalf("distributeFeesLocked: %v", err)
	}

	var paid uint64
	for _, a := range accts {
		bal, _ := ledger.Balance(a.AccountID())
		if bal != 33 {
			t.Fatalf("validator got %d, want 33", bal)
		}
		paid += bal
	}
	dust, _ := ledger.Balance(FeeAccrualAccount())
	if dust != 1 {
		t.Fatalf("accrual account holds %d, want the 1 unit that does not divide", dust)
	}
	if paid+dust != 100 {
		t.Fatalf("the split lost value: paid %d + dust %d != 100", paid, dust)
	}

	// The dust is picked up by the next distribution rather than accumulating
	// forever: add 2 more and it divides evenly.
	if err := ledger.Credit(FeeAccrualAccount(), 2); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return distributeFeesLocked(ltx, vs)
	}); err != nil {
		t.Fatalf("distributeFeesLocked: %v", err)
	}
	if dust, _ := ledger.Balance(FeeAccrualAccount()); dust != 0 {
		t.Fatalf("accrual account holds %d after an even split, want 0", dust)
	}
}

// The split is pro rata by voting power, which is what makes a bond earn
// something proportional to what it risks.
func TestFeeSplitIsProRataByVotingPower(t *testing.T) {
	accts, pubs := testKeys(t, 3)
	base, _ := NewValidatorSet(pubs)
	vs, err := base.WithPower(map[string]uint64{
		accts[0].AccountID(): 700,
		accts[1].AccountID(): 200,
		accts[2].AccountID(): 100,
	})
	if err != nil {
		t.Fatalf("WithPower: %v", err)
	}

	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	defer store.Close()
	ledger := market.NewLedger(store)
	if err := ledger.Credit(FeeAccrualAccount(), 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return distributeFeesLocked(ltx, vs)
	}); err != nil {
		t.Fatalf("distributeFeesLocked: %v", err)
	}

	want := []uint64{700, 200, 100}
	for i, a := range accts {
		bal, _ := ledger.Balance(a.AccountID())
		if bal != want[i] {
			t.Fatalf("validator with power %d got %d, want %d", vs.Power(a.AccountID()), bal, want[i])
		}
	}
}

// A near-cap fee split across near-cap stakes is where a uint64 multiply would
// wrap. It must be exact instead.
func TestFeeSplitSurvivesSupplyScaleNumbers(t *testing.T) {
	accts, pubs := testKeys(t, 2)
	base, _ := NewValidatorSet(pubs)
	const huge = uint64(4e17)
	vs, err := base.WithPower(map[string]uint64{
		accts[0].AccountID(): huge,
		accts[1].AccountID(): huge,
	})
	if err != nil {
		t.Fatalf("WithPower: %v", err)
	}

	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	defer store.Close()
	ledger := market.NewLedger(store)
	const fees = uint64(1e16)
	if err := ledger.Credit(FeeAccrualAccount(), fees); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		return distributeFeesLocked(ltx, vs)
	}); err != nil {
		t.Fatalf("distributeFeesLocked: %v", err)
	}

	var paid uint64
	for _, a := range accts {
		bal, _ := ledger.Balance(a.AccountID())
		if bal != fees/2 {
			t.Fatalf("validator got %d, want %d - the multiply wrapped", bal, fees/2)
		}
		paid += bal
	}
	dust, _ := ledger.Balance(FeeAccrualAccount())
	if paid+dust != fees {
		t.Fatalf("the split lost value at supply scale: %d + %d != %d", paid, dust, fees)
	}
}

// --- end to end --------------------------------------------------------

// feeCluster builds a cluster charging the given rate.
func feeCluster(t *testing.T, n int, bps uint32) ([]*testNode, func()) {
	t.Helper()
	return newCluster(t, n, func(c *Config) {
		c.ProposeInterval = 4 * time.Millisecond
		c.RoundTimeout = 40 * time.Millisecond
		c.HeadAnnounceInterval = 20 * time.Millisecond
		c.EpochLength = 4
		c.FeeBasisPoints = bps
	})
}

// The feature, end to end: a transfer is charged, the recipient gets the rest,
// the validators are paid, and not one base unit is created or destroyed.
func TestATransferIsChargedAndTheValidatorsArePaid(t *testing.T) {
	nodes, stop := feeCluster(t, 4, MaxFeeBasisPoints)
	defer stop()

	// A payer that is NOT a validator, so the money it spends and the money the
	// validators earn are different balances and the accounting below is
	// unambiguous.
	payer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	const funded = 1_000_000
	const amount = 500_000
	mintAll(t, nodes, payer.AccountID(), funded)

	recipient := "provider-being-paid"
	tx := signedTransfer(t, payer, recipient, amount, 1)
	for _, nd := range nodes {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}

	wantFee := FeeFor(amount, MaxFeeBasisPoints) // 5000
	waitFor(t, 20*time.Second, "the transfer to settle on every node", func() bool {
		for _, nd := range nodes {
			bal, err := nd.ledger.Balance(recipient)
			if err != nil || bal != amount-wantFee {
				return false
			}
		}
		return true
	})

	for i, nd := range nodes {
		// The payer is debited the full amount it signed for.
		if bal, _ := nd.ledger.Balance(payer.AccountID()); bal != funded-amount {
			t.Fatalf("node %d: payer balance %d, want %d", i, bal, funded-amount)
		}
		// The recipient gets the amount less the cut.
		if bal, _ := nd.ledger.Balance(recipient); bal != amount-wantFee {
			t.Fatalf("node %d: recipient balance %d, want %d", i, bal, amount-wantFee)
		}

		// Every base unit of the fee is either with a validator or still dust.
		var toValidators uint64
		for _, v := range nodes {
			bal, _ := nd.ledger.Balance(v.acct.AccountID())
			toValidators += bal
		}
		dust, _ := nd.ledger.Balance(FeeAccrualAccount())
		if toValidators+dust != wantFee {
			t.Fatalf("node %d: %d to validators + %d dust != the %d fee taken",
				i, toValidators, dust, wantFee)
		}
		// Four equal validators, 5000 fee: 1250 each, no remainder.
		for j, v := range nodes {
			bal, _ := nd.ledger.Balance(v.acct.AccountID())
			if bal != wantFee/4 {
				t.Fatalf("node %d says validator %d earned %d, want %d", i, j, bal, wantFee/4)
			}
		}
	}
}

// With no rate configured nothing is charged, which is what every other test in
// this package relies on.
func TestNoRateChargesNothing(t *testing.T) {
	nodes, stop := feeCluster(t, 4, 0)
	defer stop()

	payer := nodes[0].acct
	mintAll(t, nodes, payer.AccountID(), 1000)
	tx := signedTransfer(t, payer, "recipient", 400, 1)
	for _, nd := range nodes {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
	waitFor(t, 20*time.Second, "the transfer to settle", func() bool {
		bal, err := nodes[0].ledger.Balance("recipient")
		return err == nil && bal == 400
	})
	if dust, _ := nodes[0].engine.AccruedFees(); dust != 0 {
		t.Fatalf("accrued fees = %d with no rate configured, want 0", dust)
	}
}

// Bonding must not be charged. A validator that lost a cut of every bond would
// be paying to put capital at risk.
func TestBondingIsNotCharged(t *testing.T) {
	nodes, stop := feeCluster(t, 4, MaxFeeBasisPoints)
	defer stop()

	bonder := nodes[0].acct
	const bond = 200_000
	mintAll(t, nodes, bonder.AccountID(), bond)

	tx, err := nodes[0].engine.SubmitBond(bonder, bond, 1)
	if err != nil {
		t.Fatalf("SubmitBond: %v", err)
	}
	for _, nd := range nodes[1:] {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
	waitFor(t, 20*time.Second, "the bond to commit", func() bool {
		got, err := nodes[0].engine.BondedStake(bonder.AccountID())
		return err == nil && got == bond
	})

	if got, _ := nodes[0].engine.BondedStake(bonder.AccountID()); got != bond {
		t.Fatalf("bonded %d of %d; a cut was taken out of the bond", got, bond)
	}
}

// Fees must be paid in the SAME critical section as the transfers, so a block
// never leaves a fee taken with no transfer or the reverse. The observable
// property is conservation: at every point, total credited equals total debited.
func TestFeesConserveTheLedgerUnderTraffic(t *testing.T) {
	nodes, stop := feeCluster(t, 4, MaxFeeBasisPoints)
	defer stop()

	payer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	const funded = 10_000_000
	mintAll(t, nodes, payer.AccountID(), funded)

	stopTraffic := keepTrafficFlowingAmount(t, nodes, payer, 10_000)
	defer stopTraffic()

	waitFor(t, 20*time.Second, "a run of charged blocks", func() bool {
		bal, err := nodes[0].ledger.Balance(payer.AccountID())
		if err != nil {
			return false
		}
		// Something has moved and the chain has committed a few blocks.
		return bal < funded && nodes[0].engine.Height() > 3
	})
	// stop reports how many transfers it submitted, so the sum below covers
	// exactly the recipients that exist.
	submitted := stopTraffic()

	// Let the chain settle, then check conservation on every node.
	time.Sleep(500 * time.Millisecond)
	for i, nd := range nodes {
		total := uint64(0)
		total += mustBalance(t, nd.ledger, payer.AccountID())
		total += mustBalance(t, nd.ledger, FeeAccrualAccount())
		for _, v := range nodes {
			total += mustBalance(t, nd.ledger, v.acct.AccountID())
		}
		for j := 0; j <= submitted; j++ {
			total += mustBalance(t, nd.ledger, recipientName(j))
		}
		if total != funded {
			t.Fatalf("node %d: the ledger holds %d of the %d funded; the fee path created or destroyed value",
				i, total, funded)
		}
	}
}

func mustBalance(t *testing.T, l *market.Ledger, id string) uint64 {
	t.Helper()
	bal, err := l.Balance(id)
	if err != nil {
		t.Fatalf("balance of %s: %v", id, err)
	}
	return bal
}

// A fee below one base unit rounds to nothing rather than to one, so a stream of
// tiny transfers is not a way to charge more than the rate.
func TestATinyTransferIsNotOvercharged(t *testing.T) {
	if got := FeeFor(1, MaxFeeBasisPoints); got != 0 {
		t.Fatalf("FeeFor(1, 100bps) = %d, want 0", got)
	}
	// 1% of 99 is 0.99, which is not one unit.
	if got := FeeFor(99, MaxFeeBasisPoints); got != 0 {
		t.Fatalf("FeeFor(99, 100bps) = %d, want 0", got)
	}
}
