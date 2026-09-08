package agentapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ecirlabs/matrix-core/internal/admin"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Storage rent. The two things that make rent either work or silently not work
// are the ROUNDING and the EVICTION, so most of this file is about those.

const (
	oneMiB = uint64(1 << 20)
	day    = 24 * time.Hour
)

func rentManager(t *testing.T, price uint64, grace time.Duration) (*Manager, *fakeSettler, *token.Account) {
	t.Helper()
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	settler := &fakeSettler{applied: true}
	mgr, err := NewManager(ManagerConfig{
		Store: newTestStore(t),
		Meter: MeterConfig{
			Recipient:    "operator",
			StoragePrice: price,
			RentGrace:    grace,
		},
		Settler:  settler,
		Accounts: fakeAccounts{acct.AccountID(): acct},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return mgr, settler, acct
}

// TestRentForAWholeDayIsThePriceTimesTheMegabytes is the arithmetic stated
// plainly: the quoted unit is credits per MiB per day, so one MiB for one day at
// price 100 is 100 credits, and the whole day is paid for.
func TestRentForAWholeDayIsThePriceTimesTheMegabytes(t *testing.T) {
	credits, covered := rentOwed(oneMiB, 100, day)
	if credits != 100 {
		t.Fatalf("credits = %d, want 100", credits)
	}
	if covered != day {
		t.Fatalf("covered = %v, want %v", covered, day)
	}

	// And it scales on both axes.
	if c, _ := rentOwed(4*oneMiB, 100, day); c != 400 {
		t.Fatalf("4 MiB for a day = %d credits, want 400", c)
	}
	if c, _ := rentOwed(oneMiB, 100, 7*day); c != 700 {
		t.Fatalf("1 MiB for a week = %d credits, want 700", c)
	}
}

// TestASubCreditHourChargesNothingAndPaysForNoTime is the rounding rule. An hour
// of rent on a small module is a fraction of a credit. The wrong answer is to
// truncate to zero AND advance the watermark, which charges nothing forever and
// gets worse the more often the sweep runs.
func TestASubCreditHourChargesNothingAndPaysForNoTime(t *testing.T) {
	// 1 MiB at 10 credits/MiB/day is 10 credits a day, so an hour is 0.41.
	credits, covered := rentOwed(oneMiB, 10, time.Hour)
	if credits != 0 {
		t.Fatalf("credits = %d, want 0 for a sub-credit hour", credits)
	}
	if covered != 0 {
		t.Fatalf("covered = %v, want 0: advancing the watermark for time nobody paid "+
			"for is exactly how rent silently becomes free", covered)
	}
}

// TestHourlySweepsChargeTheSameAsOneDailySweep is the property the rounding rule
// exists for. Whatever the sweep interval, a day of storage costs a day of rent.
// A truncating implementation charges 0 for all 24 hourly sweeps.
func TestHourlySweepsChargeTheSameAsOneDailySweep(t *testing.T) {
	const price = 10 // 1 MiB/day costs 10 credits, so an hour is 0.41 credits

	daily, _ := rentOwed(oneMiB, price, day)

	var hourly uint64
	var unpaid time.Duration
	for i := 0; i < 24; i++ {
		unpaid += time.Hour
		credits, covered := rentOwed(oneMiB, price, unpaid)
		hourly += credits
		unpaid -= covered
	}

	if hourly != daily {
		t.Fatalf("24 hourly sweeps charged %d, one daily sweep charged %d; the interval "+
			"must not change the price", hourly, daily)
	}
	if hourly == 0 {
		t.Fatal("charged nothing across a whole day")
	}
	// And the leftover is strictly less than one credit's worth, so nothing is
	// quietly written off either.
	if leftover, _ := rentOwed(oneMiB, price, unpaid); leftover != 0 {
		t.Fatalf("%v of unpaid time is still worth %d credits; it should be under one",
			unpaid, leftover)
	}
}

// TestAHugeModuleOverAYearDoesNotOverflow. The intermediate product is
// bytes x price x nanoseconds, which passes 2^64 long before any of the three
// is unreasonable. A wrapped multiply would produce a wrong bill, not an error.
func TestAHugeModuleOverAYearDoesNotOverflow(t *testing.T) {
	size := uint64(32 << 20) // DefaultMaxModuleBytes
	credits, _ := rentOwed(size, 1_000_000, 365*day)
	want := uint64(32) * 1_000_000 * 365
	if credits != want {
		t.Fatalf("credits = %d, want %d; the accrual wrapped", credits, want)
	}
}

func TestRentIsOffByDefault(t *testing.T) {
	if (MeterConfig{}).RentEnabled() {
		t.Fatal("rent is on with a zero config; storage price is monetary policy and " +
			"must be opted into")
	}
	mgr, settler, _ := rentManager(t, 0, 0)
	if _, err := mgr.Deploy(context.Background(), "free", guestWasm(t), guestLimits(), ""); err != nil {
		t.Fatalf("Deploy with rent off: %v", err)
	}
	out, err := mgr.SweepRent(context.Background(), time.Now().Add(365*day))
	if err != nil {
		t.Fatalf("SweepRent: %v", err)
	}
	if out.Charged != 0 || settler.calls != 0 {
		t.Fatalf("charged %d over %d settler calls with rent disabled", out.Charged, settler.calls)
	}
}

// TestTheDeployerIsRecordedAndSurvivesARestart. Rent has to find the payer an
// hour later and after a restart, which is the whole reason the field exists.
func TestTheDeployerIsRecordedAndSurvivesARestart(t *testing.T) {
	mgr, _, acct := rentManager(t, 100, 0)
	res, err := mgr.Deploy(context.Background(), "owned", guestWasm(t), guestLimits(), acct.AccountID())
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res.Deployment.Deployer != acct.AccountID() {
		t.Fatalf("Deployer = %q, want %q", res.Deployment.Deployer, acct.AccountID())
	}
	if res.Deployment.RentPaidThroughNS == 0 {
		t.Fatal("RentPaidThroughNS is zero on a fresh deploy; rent would not start until " +
			"the first sweep, making the gap free")
	}

	restarted, err := NewManager(ManagerConfig{
		Store:    mgr.store,
		Meter:    mgr.meter,
		Settler:  mgr.settler,
		Accounts: mgr.accounts,
	})
	if err != nil {
		t.Fatalf("NewManager (restart): %v", err)
	}
	got, err := restarted.Get("owned")
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	if got.Deployer != acct.AccountID() {
		t.Fatalf("Deployer after restart = %q, want %q", got.Deployer, acct.AccountID())
	}
}

// TestADeployWithNobodyToBillIsRefusedWhenRentIsOn. Storing bytes with no owner
// is the exact failure rent closes, so it must not be reachable by omitting the
// deployer.
func TestADeployWithNobodyToBillIsRefusedWhenRentIsOn(t *testing.T) {
	mgr, _, _ := rentManager(t, 100, 0)
	_, err := mgr.Deploy(context.Background(), "orphan", guestWasm(t), guestLimits(), "")
	if !errors.Is(err, ErrNoSigningAccount) {
		t.Fatalf("err = %v, want ErrNoSigningAccount", err)
	}
	if _, getErr := mgr.Get("orphan"); getErr == nil {
		t.Fatal("the refused deploy was stored anyway")
	}
}

// TestRentIsChargedToTheDeployerThroughConsensus is the end-to-end path: a real
// record, a real sweep, and a settled transfer to the configured recipient.
func TestRentIsChargedToTheDeployerThroughConsensus(t *testing.T) {
	mgr, settler, acct := rentManager(t, 1000, 0)
	res, err := mgr.Deploy(context.Background(), "billed", guestWasm(t), guestLimits(), acct.AccountID())
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	size := res.Deployment.ModuleSize
	before := settler.calls

	start := time.Unix(0, res.Deployment.RentPaidThroughNS)
	out, err := mgr.SweepRent(context.Background(), start.Add(30*day))
	if err != nil {
		t.Fatalf("SweepRent: %v", err)
	}

	want, _ := rentOwed(size, 1000, 30*day)
	if want == 0 {
		t.Fatalf("test is vacuous: %d bytes for 30 days at 1000 rounds to no rent", size)
	}
	if out.Charged != want {
		t.Fatalf("charged %d, want %d", out.Charged, want)
	}
	if settler.calls != before+1 {
		t.Fatalf("settler called %d times, want one charge", settler.calls-before)
	}
	if settler.lastFrom != acct.AccountID() || settler.lastTo != "operator" {
		t.Fatalf("transfer was %s -> %s, want %s -> operator",
			settler.lastFrom, settler.lastTo, acct.AccountID())
	}

	rec, err := mgr.Get("billed")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.RentPaid != want {
		t.Fatalf("RentPaid = %d, want %d", rec.RentPaid, want)
	}
	// Sweeping again immediately must charge nothing: the watermark moved.
	again, err := mgr.SweepRent(context.Background(), start.Add(30*day))
	if err != nil {
		t.Fatalf("SweepRent (again): %v", err)
	}
	if again.Charged != 0 {
		t.Fatalf("a repeated sweep charged %d; the watermark did not advance", again.Charged)
	}
}

// TestAnUnpayableDeploymentIsEvictedOnlyAfterTheGracePeriod. Eviction is what
// makes rent a bound on disk instead of an unpayable debt, and it deletes a
// customer's data - so it must happen, and must not happen early.
func TestAnUnpayableDeploymentIsEvictedOnlyAfterTheGracePeriod(t *testing.T) {
	const grace = 72 * time.Hour
	mgr, settler, acct := rentManager(t, 100_000, grace)
	res, err := mgr.Deploy(context.Background(), "broke", guestWasm(t), guestLimits(), acct.AccountID())
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	start := time.Unix(0, res.Deployment.RentPaidThroughNS)

	// The payer's balance runs dry: the charge commits but is not applied.
	settler.applied = false

	out, err := mgr.SweepRent(context.Background(), start.Add(10*day))
	if err != nil {
		t.Fatalf("SweepRent: %v", err)
	}
	if len(out.Delinquent) != 1 || len(out.Evicted) != 0 {
		t.Fatalf("first unpaid sweep: delinquent %v evicted %v, want one delinquent and "+
			"no eviction", out.Delinquent, out.Evicted)
	}
	if _, err := mgr.Get("broke"); err != nil {
		t.Fatalf("the deployment was removed before its grace ran out: %v", err)
	}

	// Still inside the grace period.
	out, err = mgr.SweepRent(context.Background(), start.Add(10*day).Add(grace-time.Minute))
	if err != nil {
		t.Fatalf("SweepRent (inside grace): %v", err)
	}
	if len(out.Evicted) != 0 {
		t.Fatalf("evicted %v one minute before the grace period ended", out.Evicted)
	}

	// Past it.
	out, err = mgr.SweepRent(context.Background(), start.Add(10*day).Add(grace))
	if err != nil {
		t.Fatalf("SweepRent (past grace): %v", err)
	}
	if len(out.Evicted) != 1 || out.Evicted[0] != "broke" {
		t.Fatalf("evicted %v, want [broke]", out.Evicted)
	}
	if _, err := mgr.Get("broke"); err == nil {
		t.Fatal("the record survived eviction")
	}
	// The bytes have to be gone, not just the record: reclaiming the disk is the
	// entire point of evicting.
	if raw, err := mgr.store.Get([]byte(modulePrefix + "broke")); err == nil && len(raw) > 0 {
		t.Fatalf("%d module bytes survived eviction", len(raw))
	}
}

// TestPayingAgainClearsDelinquency. A deployer who tops up must not stay on the
// eviction clock.
func TestPayingAgainClearsDelinquency(t *testing.T) {
	mgr, settler, acct := rentManager(t, 100_000, 72*time.Hour)
	res, err := mgr.Deploy(context.Background(), "recovers", guestWasm(t), guestLimits(), acct.AccountID())
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	start := time.Unix(0, res.Deployment.RentPaidThroughNS)

	settler.applied = false
	if _, err := mgr.SweepRent(context.Background(), start.Add(10*day)); err != nil {
		t.Fatalf("SweepRent: %v", err)
	}
	rec, _ := mgr.Get("recovers")
	if rec.DelinquentSinceNS == 0 {
		t.Fatal("no delinquency recorded after a failed charge")
	}

	settler.applied = true
	if _, err := mgr.SweepRent(context.Background(), start.Add(11*day)); err != nil {
		t.Fatalf("SweepRent (paid): %v", err)
	}
	rec, _ = mgr.Get("recovers")
	if rec.DelinquentSinceNS != 0 {
		t.Fatalf("DelinquentSinceNS = %d after paying; the eviction clock is still running",
			rec.DelinquentSinceNS)
	}
}

// TestARecordFromBeforeRentIsNotBackCharged. Turning rent on must not present
// an existing deployer with a bill for the period when storage was free, and
// must never evict them for a debt they had no chance to pay.
func TestARecordFromBeforeRentIsNotBackCharged(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	store := newTestStore(t)

	// Deployed a year ago, with rent off.
	free, err := NewManager(ManagerConfig{Store: store})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if _, err := free.Deploy(context.Background(), "legacy", guestWasm(t), guestLimits(), acct.AccountID()); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	old, _ := free.Get("legacy")
	old.CreatedAtNS = time.Now().Add(-365 * day).UnixNano()
	old.RentPaidThroughNS = 0
	if err := free.saveRecord(old); err != nil {
		t.Fatalf("saveRecord: %v", err)
	}

	// The operator turns rent on.
	settler := &fakeSettler{applied: true}
	paid, err := NewManager(ManagerConfig{
		Store:    store,
		Meter:    MeterConfig{Recipient: "operator", StoragePrice: 1_000_000},
		Settler:  settler,
		Accounts: fakeAccounts{acct.AccountID(): acct},
	})
	if err != nil {
		t.Fatalf("NewManager (rent on): %v", err)
	}

	now := time.Now()
	out, err := paid.SweepRent(context.Background(), now)
	if err != nil {
		t.Fatalf("SweepRent: %v", err)
	}
	if out.Charged != 0 || settler.calls != 0 {
		t.Fatalf("charged %d for a year of storage that was advertised as free", out.Charged)
	}
	rec, _ := paid.Get("legacy")
	if rec.RentPaidThroughNS < now.UnixNano() {
		t.Fatalf("the rent clock started at %d, before the sweep at %d; rent must start "+
			"when rent starts", rec.RentPaidThroughNS, now.UnixNano())
	}
	// From here it bills normally.
	out, err = paid.SweepRent(context.Background(), now.Add(30*day))
	if err != nil {
		t.Fatalf("SweepRent (after): %v", err)
	}
	if out.Charged == 0 {
		t.Fatal("charged nothing for the 30 days after rent was turned on")
	}
}

// TestAnUnownedRecordIsSkippedNotEvicted. A record written before the Deployer
// field has nobody to bill. Evicting it would delete an operator's own agents on
// upgrade.
func TestAnUnownedRecordIsSkippedNotEvicted(t *testing.T) {
	mgr, settler, _ := rentManager(t, 100_000, time.Nanosecond)
	// Reach past Deploy, which now refuses an unowned deploy when rent is on, to
	// construct the record an older binary would have left behind.
	rec := Deployment{ID: "ancient", Status: StatusDeployed, ModuleSize: 4 << 20,
		CreatedAtNS: time.Now().Add(-365 * day).UnixNano()}
	if err := mgr.saveRecord(rec); err != nil {
		t.Fatalf("saveRecord: %v", err)
	}

	out, err := mgr.SweepRent(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("SweepRent: %v", err)
	}
	if len(out.Unowned) != 1 || out.Unowned[0] != "ancient" {
		t.Fatalf("unowned = %v, want [ancient]", out.Unowned)
	}
	if len(out.Evicted) != 0 {
		t.Fatalf("evicted %v; an upgrade must not delete records that predate the owner "+
			"field", out.Evicted)
	}
	if settler.calls != 0 {
		t.Fatalf("charged an unowned record %d times", settler.calls)
	}
}

// TestRedeployingDoesNotResetTheRentClock. Otherwise the bill is avoidable by
// re-pushing the same module before every sweep.
func TestRedeployingDoesNotResetTheRentClock(t *testing.T) {
	mgr, _, acct := rentManager(t, 1000, 0)
	res, err := mgr.Deploy(context.Background(), "again", guestWasm(t), guestLimits(), acct.AccountID())
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	first := res.Deployment.RentPaidThroughNS

	// Age the watermark, then re-deploy the same id.
	rec, _ := mgr.Get("again")
	rec.RentPaidThroughNS = first - int64(10*day)
	if err := mgr.saveRecord(rec); err != nil {
		t.Fatalf("saveRecord: %v", err)
	}
	if _, err := mgr.Deploy(context.Background(), "again", guestWasm(t), guestLimits(), acct.AccountID()); err != nil {
		t.Fatalf("re-Deploy: %v", err)
	}

	got, _ := mgr.Get("again")
	if got.RentPaidThroughNS != first-int64(10*day) {
		t.Fatalf("re-deploying moved the watermark to %d from %d; ten days of rent were "+
			"erased by re-pushing the module", got.RentPaidThroughNS, first-int64(10*day))
	}
}

// TestRentNeedsSomewhereToPayAndAWayToPay. A node configured to charge rent it
// cannot collect must refuse to start rather than come up quietly free.
func TestRentNeedsSomewhereToPayAndAWayToPay(t *testing.T) {
	base := func() ManagerConfig {
		acct, _ := token.GenerateAccount()
		return ManagerConfig{
			Store:    newTestStore(t),
			Meter:    MeterConfig{Recipient: "operator", StoragePrice: 10},
			Settler:  &fakeSettler{applied: true},
			Accounts: fakeAccounts{acct.AccountID(): acct},
		}
	}
	if _, err := NewManager(base()); err != nil {
		t.Fatalf("a complete rent config was refused: %v", err)
	}
	for name, break_ := range map[string]func(*ManagerConfig){
		"no recipient": func(c *ManagerConfig) { c.Meter.Recipient = "" },
		"no settler":   func(c *ManagerConfig) { c.Settler = nil },
		"no accounts":  func(c *ManagerConfig) { c.Accounts = nil },
	} {
		cfg := base()
		break_(&cfg)
		if _, err := NewManager(cfg); err == nil {
			t.Fatalf("%s: a node came up charging rent with no way to collect it", name)
		}
	}
}

// The paying account is the caller's own, not the one the request names.
//
// This is the authorization half of rent. Before it, `deployer` was a string the
// client chose and the interceptor only checked that the key was valid - so any
// caller who could deploy could bill any account the node holds a key for. One
// run charged to a stranger is bad; rent charged to a stranger is a bill that
// repeats every hour for as long as the bytes sit there.

func authWithKey(t *testing.T, key, account string) *admin.Authenticator {
	t.Helper()
	a := admin.NewAuthenticator()
	if err := a.AddKey(&admin.APIKey{Key: key, Role: admin.RoleAdmin, Name: "k", Account: account}); err != nil {
		t.Fatalf("AddKey: %v", err)
	}
	return a
}

func keyCtx(key string) context.Context {
	return metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization", "Bearer "+key))
}

func TestTheDeployIsChargedToTheCallersOwnAccount(t *testing.T) {
	svc := &Service{auth: authWithKey(t, "k1", "acct-caller")}

	// Naming nobody: the key's account is used.
	got, err := svc.payerFor(keyCtx("k1"), "")
	if err != nil {
		t.Fatalf("payerFor: %v", err)
	}
	if got != "acct-caller" {
		t.Fatalf("payer = %q, want acct-caller", got)
	}

	// Naming its own account: allowed, same answer.
	if got, err = svc.payerFor(keyCtx("k1"), "acct-caller"); err != nil || got != "acct-caller" {
		t.Fatalf("payerFor(own) = %q, %v", got, err)
	}
}

func TestNamingSomeoneElsesAccountIsRefused(t *testing.T) {
	svc := &Service{auth: authWithKey(t, "k1", "acct-caller")}

	_, err := svc.payerFor(keyCtx("k1"), "acct-victim")
	if err == nil {
		t.Fatal("a caller billed an account that is not theirs")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", status.Code(err))
	}
	// Refused rather than silently redirected to the caller: someone who asked to
	// bill another account should be told no, not billed themselves.
	if !strings.Contains(err.Error(), "acct-victim") {
		t.Fatalf("the refusal does not say what was refused: %v", err)
	}
}

func TestAnInvalidKeyNamesNoPayer(t *testing.T) {
	svc := &Service{auth: authWithKey(t, "k1", "acct-caller")}
	if _, err := svc.payerFor(keyCtx("wrong"), ""); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("err = %v, want Unauthenticated", err)
	}
}

func TestAKeyWithNoAccountCannotNameOne(t *testing.T) {
	svc := &Service{auth: authWithKey(t, "k1", "")}

	// It may still deploy unmetered, naming nobody.
	if got, err := svc.payerFor(keyCtx("k1"), ""); err != nil || got != "" {
		t.Fatalf("payerFor = %q, %v; a key with no account should name no payer", got, err)
	}
	// But it cannot point the bill somewhere.
	if _, err := svc.payerFor(keyCtx("k1"), "acct-victim"); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("err = %v, want PermissionDenied", err)
	}
}

func TestWithNoAuthenticatorTheRequestStands(t *testing.T) {
	svc := &Service{}
	got, err := svc.payerFor(context.Background(), "acct-local")
	if err != nil {
		t.Fatalf("payerFor: %v", err)
	}
	if got != "acct-local" {
		t.Fatalf("payer = %q, want acct-local: an unauthenticated node is the operator's "+
			"own choice and behaves as it did before", got)
	}
}
