package consensus

import (
	"errors"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// --- the schedule ------------------------------------------------------

func TestEmissionHalvesAndReachesZero(t *testing.T) {
	const per = uint64(1024)
	const half = uint64(100)

	cases := []struct {
		height uint64
		want   uint64
	}{
		{0, 1024},
		{99, 1024},
		{100, 512},
		{250, 256},
		{1000, 1}, // ten halvings: 1024 >> 10
		{1100, 0}, // eleven: exhausted
		{1_000_000, 0},
	}
	for _, c := range cases {
		if got := EmissionFor(c.height, per, half); got != c.want {
			t.Errorf("EmissionFor(%d) = %d, want %d", c.height, got, c.want)
		}
	}

	// No schedule configured pays nothing, at any height.
	if got := EmissionFor(0, 0, half); got != 0 {
		t.Errorf("EmissionFor with no per-block amount = %d, want 0", got)
	}
	// A zero half-life takes the default rather than dividing by zero.
	if got := EmissionFor(0, per, 0); got != per {
		t.Errorf("EmissionFor with a zero half-life = %d, want %d", got, per)
	}
	// The shift cannot run off the end of the type.
	if got := EmissionFor(^uint64(0), per, 1); got != 0 {
		t.Errorf("EmissionFor at the maximum height = %d, want 0", got)
	}
}

// --- the registry ------------------------------------------------------

func TestProviderChangeRoundTrip(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	id := acct.AccountID()

	add := ProviderChange{Kind: ProviderChangeAdd, ProviderID: id}
	if !IsProviderChangeRecipient(add.Recipient()) {
		t.Fatalf("%q not recognised as a provider change", add.Recipient())
	}
	parsed, err := ParseProviderChange(add.Recipient())
	if err != nil {
		t.Fatalf("ParseProviderChange: %v", err)
	}
	if parsed != add {
		t.Fatalf("parsed = %+v, want %+v", parsed, add)
	}
	if add.String() != "add:"+id {
		t.Fatalf("String() = %q", add.String())
	}
	// What the node prints must parse back out of a config.
	spec, err := ParseProviderChangeSpec(add.String())
	if err != nil {
		t.Fatalf("ParseProviderChangeSpec: %v", err)
	}
	if spec != add {
		t.Fatalf("spec = %+v, want %+v", spec, add)
	}

	for _, bad := range []string{
		"", "add", "add:", "add:short", "promote:" + id, "market/provider/add/", id,
	} {
		if _, err := ParseProviderChangeSpec(bad); err == nil {
			t.Fatalf("ParseProviderChangeSpec(%q) succeeded", bad)
		}
	}
	if IsProviderChangeRecipient("native/reward-pool") {
		t.Fatal("the reward pool was read as a provider change")
	}
}

func TestProviderRegistryRoundTrip(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	defer store.Close()
	reg := NewProviderRegistry(store)

	a, _ := token.GenerateAccount()
	b, _ := token.GenerateAccount()

	if got, _ := reg.IsRegistered(a.AccountID()); got {
		t.Fatal("a fresh registry has members")
	}
	for _, acct := range []*token.Account{a, b} {
		if err := reg.Apply(ProviderChange{Kind: ProviderChangeAdd, ProviderID: acct.AccountID()}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
	}
	if got, _ := reg.IsRegistered(a.AccountID()); !got {
		t.Fatal("a registered provider does not read back")
	}
	all, err := reg.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("All() = %v, want 2 members", all)
	}
	// Sorted, so every node iterates in the same order.
	if all[0] > all[1] {
		t.Fatalf("All() is not sorted: %v", all)
	}

	if err := reg.Apply(ProviderChange{Kind: ProviderChangeRemove, ProviderID: a.AccountID()}); err != nil {
		t.Fatalf("Apply(remove): %v", err)
	}
	if got, _ := reg.IsRegistered(a.AccountID()); got {
		t.Fatal("a deregistered provider is still registered")
	}
	// Idempotent both ways.
	if err := reg.Apply(ProviderChange{Kind: ProviderChangeRemove, ProviderID: a.AccountID()}); err != nil {
		t.Fatalf("removing twice: %v", err)
	}
}

// --- the split ---------------------------------------------------------

func newRewardLedger(t *testing.T, pool uint64) (*market.Ledger, *ProviderRegistry) {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ledger := market.NewLedger(store)
	if pool > 0 {
		if err := ledger.Credit(token.RewardPoolAccount, pool); err != nil {
			t.Fatalf("credit pool: %v", err)
		}
	}
	return ledger, NewProviderRegistry(store)
}

// The emission is shared by how much each registered provider was paid.
func TestEmissionIsSharedBySettlementsReceived(t *testing.T) {
	ledger, reg := newRewardLedger(t, 1_000_000)
	a, _ := token.GenerateAccount()
	b, _ := token.GenerateAccount()
	for _, acct := range []*token.Account{a, b} {
		if err := reg.Apply(ProviderChange{Kind: ProviderChangeAdd, ProviderID: acct.AccountID()}); err != nil {
			t.Fatalf("register: %v", err)
		}
	}

	credited := map[string]uint64{
		a.AccountID(): 750,
		b.AccountID(): 250,
	}
	var paid uint64
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		var err error
		paid, err = distributeEmissionLocked(ltx, reg, credited, 1000)
		return err
	}); err != nil {
		t.Fatalf("distributeEmissionLocked: %v", err)
	}
	if paid != 1000 {
		t.Fatalf("paid %d, want the whole 1000 emission", paid)
	}
	if bal, _ := ledger.Balance(a.AccountID()); bal != 750 {
		t.Fatalf("provider paid 750 of 1000 got %d of the emission, want 750", bal)
	}
	if bal, _ := ledger.Balance(b.AccountID()); bal != 250 {
		t.Fatalf("provider paid 250 of 1000 got %d of the emission, want 250", bal)
	}
	if pool, _ := ledger.Balance(token.RewardPoolAccount); pool != 1_000_000-1000 {
		t.Fatalf("pool = %d, want %d", pool, 1_000_000-1000)
	}
}

// An UNREGISTERED recipient earns nothing and does not dilute anyone. That is
// what points the pool at parties who supply compute rather than at whoever
// happens to receive a transfer.
func TestAnUnregisteredRecipientEarnsNothing(t *testing.T) {
	ledger, reg := newRewardLedger(t, 1_000_000)
	registered, _ := token.GenerateAccount()
	stranger, _ := token.GenerateAccount()
	if err := reg.Apply(ProviderChange{Kind: ProviderChangeAdd, ProviderID: registered.AccountID()}); err != nil {
		t.Fatalf("register: %v", err)
	}

	credited := map[string]uint64{
		registered.AccountID(): 100,
		stranger.AccountID():   900,
	}
	var paid uint64
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		var err error
		paid, err = distributeEmissionLocked(ltx, reg, credited, 1000)
		return err
	}); err != nil {
		t.Fatalf("distributeEmissionLocked: %v", err)
	}
	if bal, _ := ledger.Balance(stranger.AccountID()); bal != 0 {
		t.Fatalf("an unregistered recipient earned %d", bal)
	}
	// The registered provider is the only weight, so it takes the whole
	// emission - the stranger's 900 did not dilute it.
	if bal, _ := ledger.Balance(registered.AccountID()); bal != 1000 {
		t.Fatalf("the registered provider got %d, want the whole 1000", bal)
	}
	if paid != 1000 {
		t.Fatalf("paid %d, want 1000", paid)
	}
}

// THE PROPERTY THIS DESIGN EXISTS FOR. A percentage of what a provider is paid
// would be a money pump: send coins to an account you also control, collect the
// percentage, send them back, repeat. A fixed per-block budget means the pool
// cannot be drained faster than the schedule however much volume is faked.
func TestFakedVolumeCannotIncreaseWhatThePoolPays(t *testing.T) {
	ledger, reg := newRewardLedger(t, 1_000_000)
	farmer, _ := token.GenerateAccount()
	if err := reg.Apply(ProviderChange{Kind: ProviderChangeAdd, ProviderID: farmer.AccountID()}); err != nil {
		t.Fatalf("register: %v", err)
	}

	const emission = uint64(500)
	// A modest settlement and an absurd one pay out exactly the same, because
	// the budget is the budget.
	for _, volume := range []uint64{1, 1_000, 1_000_000_000, ^uint64(0)} {
		before, _ := ledger.Balance(token.RewardPoolAccount)
		var paid uint64
		if err := ledger.Atomically(func(ltx market.LedgerTx) error {
			var err error
			paid, err = distributeEmissionLocked(ltx, reg, map[string]uint64{farmer.AccountID(): volume}, emission)
			return err
		}); err != nil {
			t.Fatalf("distributeEmissionLocked(volume=%d): %v", volume, err)
		}
		after, _ := ledger.Balance(token.RewardPoolAccount)
		if paid != emission {
			t.Fatalf("volume %d drew %d from the pool, want exactly the %d emission", volume, paid, emission)
		}
		if before-after != emission {
			t.Fatalf("volume %d moved %d out of the pool, want %d", volume, before-after, emission)
		}
	}
}

// An empty pool ends the emission rather than failing, and never pays out more
// than it holds.
func TestAnEmptyPoolEndsTheEmission(t *testing.T) {
	ledger, reg := newRewardLedger(t, 120)
	p, _ := token.GenerateAccount()
	if err := reg.Apply(ProviderChange{Kind: ProviderChangeAdd, ProviderID: p.AccountID()}); err != nil {
		t.Fatalf("register: %v", err)
	}
	credited := map[string]uint64{p.AccountID(): 10}

	// First block takes 100 of the 120 there is.
	var paid uint64
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		var err error
		paid, err = distributeEmissionLocked(ltx, reg, credited, 100)
		return err
	}); err != nil {
		t.Fatalf("distributeEmissionLocked: %v", err)
	}
	if paid != 100 {
		t.Fatalf("paid %d, want 100", paid)
	}
	// Second block wants 100 and only 20 is left.
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		var err error
		paid, err = distributeEmissionLocked(ltx, reg, credited, 100)
		return err
	}); err != nil {
		t.Fatalf("distributeEmissionLocked: %v", err)
	}
	if paid != 20 {
		t.Fatalf("paid %d from a pool holding 20, want 20", paid)
	}
	if pool, _ := ledger.Balance(token.RewardPoolAccount); pool != 0 {
		t.Fatalf("pool = %d, want 0", pool)
	}
	// Third block pays nothing and does not error.
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		var err error
		paid, err = distributeEmissionLocked(ltx, reg, credited, 100)
		return err
	}); err != nil {
		t.Fatalf("distributeEmissionLocked on an empty pool: %v", err)
	}
	if paid != 0 {
		t.Fatalf("an empty pool paid %d", paid)
	}
}

// --- the engine --------------------------------------------------------

func rewardCluster(t *testing.T, n int, perBlock uint64, approved ...string) ([]*testNode, func()) {
	t.Helper()
	return newCluster(t, n, func(c *Config) {
		c.ProposeInterval = 4 * time.Millisecond
		c.RoundTimeout = 40 * time.Millisecond
		c.HeadAnnounceInterval = 20 * time.Millisecond
		c.EpochLength = 4
		c.ProviderEmissionPerBlock = perBlock
		c.ProviderEmissionHalfLife = 1_000_000
		c.ApprovedProviders = approved
	})
}

// A registration needs a quorum of operators, exactly like admitting a
// validator: one node cannot point the pool at an account of its choosing.
func TestRegisteringAProviderNeedsAQuorumOfOperators(t *testing.T) {
	provider, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	approval := "add:" + provider.AccountID()

	// Two of four approve: one short of quorum.
	next := 0
	nodes, stop := newCluster(t, 4, func(c *Config) {
		c.ProposeInterval = 4 * time.Millisecond
		c.RoundTimeout = 40 * time.Millisecond
		c.EpochLength = 4
		c.ProviderEmissionPerBlock = 1000
		if next < 2 {
			c.ApprovedProviders = []string{approval}
		}
		next++
	})
	defer stop()

	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()

	waitFor(t, 20*time.Second, "the chain to make progress", func() bool {
		return nodes[0].engine.Height() >= 6
	})

	for i, nd := range nodes {
		registered, err := nd.engine.IsRewardedProvider(provider.AccountID())
		if err != nil {
			t.Fatalf("node %d: %v", i, err)
		}
		if registered {
			t.Fatalf("node %d registered a provider only two of four operators approved", i)
		}
	}
}

// And with a quorum it lands, with nobody submitting a transaction.
func TestAQuorumOfOperatorsRegistersAProvider(t *testing.T) {
	provider, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	approval := "add:" + provider.AccountID()

	nodes, stop := rewardCluster(t, 4, 1000, approval)
	defer stop()

	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()

	waitFor(t, 20*time.Second, "the provider to be registered everywhere", func() bool {
		for _, nd := range nodes {
			registered, err := nd.engine.IsRewardedProvider(provider.AccountID())
			if err != nil || !registered {
				return false
			}
		}
		return true
	})

	for i, nd := range nodes {
		all, err := nd.engine.RewardedProviders()
		if err != nil {
			t.Fatalf("node %d: %v", i, err)
		}
		if len(all) != 1 || all[0] != provider.AccountID() {
			t.Fatalf("node %d registry = %v, want just the one provider", i, all)
		}
	}
}

// A provider change from a non-validator makes the block invalid, so a client
// cannot register itself.
func TestAProviderChangeFromANonValidatorIsInvalid(t *testing.T) {
	nodes, stop := rewardCluster(t, 4, 1000)
	defer stop()

	outsider, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	change := ProviderChange{Kind: ProviderChangeAdd, ProviderID: outsider.AccountID()}
	tx := signedTransfer(t, outsider, change.Recipient(), 0, 1)

	eng := nodes[0].engine
	eng.mu.Lock()
	err = eng.verifyProviderChangeLocked(tx)
	eng.mu.Unlock()
	if !errors.Is(err, ErrNotValidator) {
		t.Fatalf("a provider change from a non-validator = %v, want ErrNotValidator", err)
	}

	// A change carrying value is invalid too.
	valued := signedTransfer(t, nodes[0].acct, change.Recipient(), 5, 1)
	eng.mu.Lock()
	err = eng.verifyProviderChangeLocked(valued)
	eng.mu.Unlock()
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("a provider change carrying value = %v, want ErrInvalidMessage", err)
	}
}

// The feature end to end: a registered provider is paid a settlement and earns
// an emission from the pool on top of it, and the pool is what shrank.
func TestARegisteredProviderEarnsFromThePool(t *testing.T) {
	provider, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	providerID := provider.AccountID()

	const emission = 400
	nodes, stop := rewardCluster(t, 4, emission, "add:"+providerID)
	defer stop()

	// Seed the pool on every node, the state genesis would have left.
	const pool = 1_000_000
	mintAll(t, nodes, token.RewardPoolAccount, pool)

	buyer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	mintAll(t, nodes, buyer.AccountID(), 100_000)

	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()

	waitFor(t, 20*time.Second, "the provider to be registered everywhere", func() bool {
		for _, nd := range nodes {
			registered, err := nd.engine.IsRewardedProvider(providerID)
			if err != nil || !registered {
				return false
			}
		}
		return true
	})

	// A settlement to the provider, after registration is in force.
	const price = 5_000
	tx := signedTransfer(t, buyer, providerID, price, 1)
	for _, nd := range nodes {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}

	waitFor(t, 20*time.Second, "the provider to be paid more than the settlement", func() bool {
		for _, nd := range nodes {
			bal, err := nd.ledger.Balance(providerID)
			if err != nil || bal <= price {
				return false
			}
		}
		return true
	})

	for i, nd := range nodes {
		bal, _ := nd.ledger.Balance(providerID)
		earned := bal - price
		if earned == 0 {
			t.Fatalf("node %d: the provider earned no emission", i)
		}
		// It cannot have earned more than one block's emission from one block.
		if earned > emission {
			t.Fatalf("node %d: the provider earned %d from a %d-per-block emission", i, earned, emission)
		}
		// And the pool is what paid for it.
		poolLeft, _ := nd.ledger.Balance(token.RewardPoolAccount)
		if poolLeft >= pool {
			t.Fatalf("node %d: the pool did not shrink (%d)", i, poolLeft)
		}
		if pool-poolLeft != earned {
			t.Fatalf("node %d: the pool paid %d but the provider earned %d", i, pool-poolLeft, earned)
		}
	}
}

// With no emission configured the pool is not touched, which is the default.
func TestNoEmissionLeavesThePoolAlone(t *testing.T) {
	nodes, stop := rewardCluster(t, 4, 0)
	defer stop()

	const pool = 1_000
	mintAll(t, nodes, token.RewardPoolAccount, pool)

	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()
	waitFor(t, 20*time.Second, "a run of blocks", func() bool {
		return nodes[0].engine.Height() >= 4
	})

	for i, nd := range nodes {
		if got, _ := nd.ledger.Balance(token.RewardPoolAccount); got != pool {
			t.Fatalf("node %d: pool = %d, want the untouched %d", i, got, pool)
		}
	}
}
