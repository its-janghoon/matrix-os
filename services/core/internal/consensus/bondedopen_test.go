package consensus

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/libp2p/go-libp2p/core/peer"
)

func newBondedOpenSubmitOnlyEngine(t *testing.T, maxMempool int) (*Engine, *market.Ledger) {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	self, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	vs, err := NewValidatorSet([]ed25519.PublicKey{self.PublicKey})
	if err != nil {
		t.Fatalf("NewValidatorSet: %v", err)
	}
	ledger := market.NewLedger(store)
	eng, err := New(Config{
		Transport:       newMemBus().endpoint(peer.ID("bonded-open-submit-only")),
		Validators:      vs,
		Chain:           NewBlockChain(store),
		Ledger:          ledger,
		Self:            self,
		ProposeInterval: time.Hour,
		RoundTimeout:    time.Hour,
		Evidence:        NewEvidenceStore(store),
		Sets:            NewSetStore(store),
		Stake:           NewStakeLedger(ledger, store),
		MembershipMode:  MembershipBondedOpen,
		MinBond:         100,
		MaxMempoolTxs:   maxMempool,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return eng, ledger
}

func publishMembershipTx(t *testing.T, ep Transport, tx *token.Transaction) {
	t.Helper()
	body, err := json.Marshal(tx)
	if err != nil {
		t.Fatalf("marshal membership transaction: %v", err)
	}
	if err := ep.Publish(context.Background(), TopicMembership, body); err != nil {
		t.Fatalf("publish membership transaction: %v", err)
	}
}

func TestOperatorApprovedModeDoesNotJoinMembershipGossip(t *testing.T) {
	nodes, stop := newCluster(t, 2, nil)
	defer stop()
	bus := nodes[0].bus
	bus.mu.Lock()
	subscribers := len(bus.subs[TopicMembership])
	bus.mu.Unlock()
	if subscribers != 0 {
		t.Fatalf("operator-approved cluster joined bonded-open membership gossip with %d subscribers", subscribers)
	}
}

// The membership topic is public because a candidate is not a validator yet.
// Public must not mean free shared-mempool capacity: a zero-balance identity's
// nonce/signature variants are rejected before insertion on every subscriber,
// leaving room for ordinary transactions.
func TestBondedOpenOutsiderGossipCannotCrowdOutTransactions(t *testing.T) {
	const cap = 4
	nodes, stop := newCluster(t, 4, func(c *Config) {
		c.MembershipMode = MembershipBondedOpen
		c.MinBond = 100
		c.ZeroMinBond = false
		c.MaxMempoolTxs = cap
		c.ProposeInterval = time.Hour
		c.RoundTimeout = time.Hour
	})
	defer stop()

	outsider, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	relay := nodes[0].bus.endpoint(peer.ID("hostile-membership-relay"))
	for nonce := uint64(0); nonce < 32; nonce++ {
		publishMembershipTx(t, relay, signedTransfer(t, outsider, BondAccount(outsider.AccountID()), 1, nonce))
		publishMembershipTx(t, relay, signedTransfer(t, outsider, AddValidatorRecipient(outsider.PublicKey), 0, 100+nonce))
		publishMembershipTx(t, relay, signedTransfer(t, outsider, RemoveValidatorRecipient(outsider.AccountID()), 0, 200+nonce))
		publishMembershipTx(t, relay, signedTransfer(t, outsider, WithdrawRecipient(outsider.AccountID()), 0, 300+nonce))
	}

	// Delivery and handling are asynchronous. Give every subscriber time to
	// consume the complete hostile burst, then inspect the actual retained state.
	time.Sleep(250 * time.Millisecond)
	for i, nd := range nodes {
		nd.engine.mu.Lock()
		held := len(nd.engine.mempool)
		slots := len(nd.engine.pendingMembership)
		nd.engine.mu.Unlock()
		if held != 0 || slots != 0 {
			t.Fatalf("node %d retained hostile membership gossip: mempool=%d slots=%d", i, held, slots)
		}
	}

	// Even state-valid one-unit bonds from many separately funded identities
	// stay inside the membership quota rather than taking the entire shared cap.
	for i := 0; i < 16; i++ {
		funded, err := token.GenerateAccount()
		if err != nil {
			t.Fatalf("GenerateAccount: %v", err)
		}
		mintAll(t, nodes, funded.AccountID(), 1)
		publishMembershipTx(t, relay, signedTransfer(t, funded, BondAccount(funded.AccountID()), 1, uint64(i)))
	}
	time.Sleep(250 * time.Millisecond)
	for i, nd := range nodes {
		nd.engine.mu.Lock()
		held := len(nd.engine.mempool)
		slots := len(nd.engine.pendingMembership)
		limit := membershipMempoolLimit(nd.engine.maxMempoolTxs)
		nd.engine.mu.Unlock()
		if held > limit || slots > limit {
			t.Fatalf("node %d let funded membership Sybils exceed quota %d: mempool=%d slots=%d", i, limit, held, slots)
		}
	}

	honest := nodes[0].acct
	mintAll(t, nodes, honest.AccountID(), 10)
	tx := signedTransfer(t, honest, "ordinary-recipient", 1, 999)
	for i, nd := range nodes {
		if err := nd.engine.Submit(tx); err != nil {
			t.Fatalf("node %d refused ordinary work after hostile gossip: %v", i, err)
		}
	}
}

// Stable slots are keyed by identity and operation rather than the signed
// transaction. Re-signing or changing a nonce therefore cannot turn one funded
// identity into an unbounded number of pending membership requests.
func TestBondedOpenMembershipVariantsShareStableSlots(t *testing.T) {
	eng, ledger := newBondedOpenSubmitOnlyEngine(t, 64)

	candidate, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	if err := ledger.Credit(candidate.AccountID(), 200); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if err := ledger.Transfer(candidate.AccountID(), BondAccount(candidate.AccountID()), 100); err != nil {
		t.Fatalf("seed bond: %v", err)
	}
	for nonce := uint64(0); nonce < 32; nonce++ {
		if err := eng.Submit(signedTransfer(t, candidate, AddValidatorRecipient(candidate.PublicKey), 0, nonce)); err != nil {
			t.Fatalf("admission variant %d: %v", nonce, err)
		}
	}

	bonder, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	if err := ledger.Credit(bonder.AccountID(), 100); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	for nonce := uint64(0); nonce < 32; nonce++ {
		if err := eng.Submit(signedTransfer(t, bonder, BondAccount(bonder.AccountID()), 1, nonce)); err != nil {
			t.Fatalf("bond variant %d: %v", nonce, err)
		}
	}

	eng.mu.Lock()
	held := len(eng.mempool)
	slots := len(eng.pendingMembership)
	eng.mu.Unlock()
	if held != 2 || slots != 2 {
		t.Fatalf("membership variants occupy %d mempool entries and %d stable slots, want 2/2", held, slots)
	}
}

func TestBondedOpenStaleMembershipEntriesAreEvicted(t *testing.T) {
	t.Run("bond that becomes unfunded", func(t *testing.T) {
		eng, ledger := newBondedOpenSubmitOnlyEngine(t, 8)
		candidate, err := token.GenerateAccount()
		if err != nil {
			t.Fatalf("GenerateAccount: %v", err)
		}
		if err := ledger.Credit(candidate.AccountID(), 100); err != nil {
			t.Fatalf("Credit: %v", err)
		}
		if err := eng.Submit(signedTransfer(t, candidate, BondAccount(candidate.AccountID()), 100, 1)); err != nil {
			t.Fatalf("Submit bond: %v", err)
		}
		if err := ledger.Transfer(candidate.AccountID(), "spent-elsewhere", 100); err != nil {
			t.Fatalf("spend balance: %v", err)
		}
		eng.mu.Lock()
		eng.pruneStaleMembershipLocked()
		held := len(eng.mempool)
		slots := len(eng.pendingMembership)
		eng.mu.Unlock()
		if held != 0 || slots != 0 {
			t.Fatalf("unfunded bond survived eviction: mempool=%d slots=%d", held, slots)
		}
	})

	t.Run("admission overtaken by committed state", func(t *testing.T) {
		eng, ledger := newBondedOpenSubmitOnlyEngine(t, 8)
		candidate, err := token.GenerateAccount()
		if err != nil {
			t.Fatalf("GenerateAccount: %v", err)
		}
		if err := ledger.Credit(candidate.AccountID(), 100); err != nil {
			t.Fatalf("Credit: %v", err)
		}
		if err := ledger.Transfer(candidate.AccountID(), BondAccount(candidate.AccountID()), 100); err != nil {
			t.Fatalf("seed bond: %v", err)
		}
		if err := eng.Submit(signedTransfer(t, candidate, AddValidatorRecipient(candidate.PublicKey), 0, 1)); err != nil {
			t.Fatalf("Submit admission: %v", err)
		}
		eng.mu.Lock()
		eng.pendingChanges = append(eng.pendingChanges, SetChange{
			Kind: SetChangeAdd, ValidatorID: candidate.AccountID(), PublicKey: candidate.PublicKey,
		})
		eng.pruneStaleMembershipLocked()
		held := len(eng.mempool)
		slots := len(eng.pendingMembership)
		eng.mu.Unlock()
		if held != 0 || slots != 0 {
			t.Fatalf("overtaken admission survived eviction: mempool=%d slots=%d", held, slots)
		}
	})
}

// A withdrawal with a real bond is allowed to wait while its owner is still a
// validator. The self-exit uses a different stable slot; after the epoch change
// and unbonding delay, the queued withdrawal can become valid without a retry.
func TestBondedOpenLockedWithdrawalIsRetained(t *testing.T) {
	eng, ledger := newBondedOpenSubmitOnlyEngine(t, 8)
	if err := ledger.Credit(eng.selfID, 100); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if err := ledger.Transfer(eng.selfID, BondAccount(eng.selfID), 100); err != nil {
		t.Fatalf("seed bond: %v", err)
	}
	withdraw := signedTransfer(t, eng.self, WithdrawRecipient(eng.selfID), 0, 1)
	if err := eng.Submit(withdraw); err != nil {
		t.Fatalf("locked withdrawal should be retained: %v", err)
	}

	eng.mu.Lock()
	eng.pruneStaleMembershipLocked()
	held := len(eng.mempool)
	slots := len(eng.pendingMembership)
	eng.mu.Unlock()
	if held != 1 || slots != 1 {
		t.Fatalf("locked withdrawal was evicted: mempool=%d slots=%d", held, slots)
	}
}

// Honest mempools serialize operations for one identity, but block validity
// must enforce the same rule against a malicious leader that bypasses Submit.
func TestBondedOpenBlockRejectsConflictingOperationsForOneIdentity(t *testing.T) {
	eng, ledger := newBondedOpenSubmitOnlyEngine(t, 8)
	candidate, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	if err := ledger.Credit(candidate.AccountID(), 200); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if err := ledger.Transfer(candidate.AccountID(), BondAccount(candidate.AccountID()), 100); err != nil {
		t.Fatalf("seed bond: %v", err)
	}
	admit := signedTransfer(t, candidate, AddValidatorRecipient(candidate.PublicKey), 0, 1)
	topUp := signedTransfer(t, candidate, BondAccount(candidate.AccountID()), 100, 2)

	eng.mu.Lock()
	eng.headHash = append([]byte(nil), genesisPrevHash...)
	block := buildSignedBlock(t, eng.self, 0, 0, genesisPrevHash, []token.Transaction{*admit, *topUp})
	err = eng.verifyBlockForHeightLocked(block)
	eng.mu.Unlock()
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("block with two membership operations for one identity = %v, want ErrInvalidMessage", err)
	}
}

func prepareEngineLeaderHeight(eng *Engine) {
	for height := uint64(0); ; height++ {
		if eng.vset().LeaderFor(height, 0) == eng.selfID {
			eng.height = height
			eng.round = 0
			eng.headHash = append([]byte(nil), genesisPrevHash...)
			return
		}
	}
}

func TestBondedOpenProposalRespectsAggregateSetBounds(t *testing.T) {
	t.Run("admissions do not exceed maximum", func(t *testing.T) {
		eng, ledger := newBondedOpenSubmitOnlyEngine(t, 16)
		keys := []ed25519.PublicKey{eng.self.PublicKey}
		for len(keys) < MaxValidators-1 {
			acct, err := token.GenerateAccount()
			if err != nil {
				t.Fatalf("GenerateAccount: %v", err)
			}
			keys = append(keys, acct.PublicKey)
		}
		vs, err := NewValidatorSet(keys)
		if err != nil {
			t.Fatalf("NewValidatorSet: %v", err)
		}
		eng.validatorSet.Store(vs)

		for nonce := uint64(1); nonce <= 2; nonce++ {
			candidate, err := token.GenerateAccount()
			if err != nil {
				t.Fatalf("GenerateAccount: %v", err)
			}
			if err := ledger.Credit(candidate.AccountID(), 100); err != nil {
				t.Fatalf("Credit: %v", err)
			}
			if err := ledger.Transfer(candidate.AccountID(), BondAccount(candidate.AccountID()), 100); err != nil {
				t.Fatalf("seed bond: %v", err)
			}
			if err := eng.Submit(signedTransfer(t, candidate, AddValidatorRecipient(candidate.PublicKey), 0, nonce)); err != nil {
				t.Fatalf("Submit admission: %v", err)
			}
		}

		eng.mu.Lock()
		prepareEngineLeaderHeight(eng)
		block, _ := eng.buildProposalLocked()
		if block == nil {
			eng.mu.Unlock()
			t.Fatal("proposal builder returned no block")
		}
		err = eng.verifyBlockForHeightLocked(block)
		changes := eng.setChangesInLocked(block)
		eng.mu.Unlock()
		if err != nil {
			t.Fatalf("builder produced a block it rejects: %v", err)
		}
		if len(changes) != 1 {
			t.Fatalf("proposal carries %d admissions with one validator slot left, want 1", len(changes))
		}
	})

	t.Run("exits do not empty set", func(t *testing.T) {
		eng, _ := newBondedOpenSubmitOnlyEngine(t, 8)
		other, err := token.GenerateAccount()
		if err != nil {
			t.Fatalf("GenerateAccount: %v", err)
		}
		vs, err := NewValidatorSet([]ed25519.PublicKey{eng.self.PublicKey, other.PublicKey})
		if err != nil {
			t.Fatalf("NewValidatorSet: %v", err)
		}
		eng.validatorSet.Store(vs)
		if err := eng.Submit(signedTransfer(t, eng.self, RemoveValidatorRecipient(eng.selfID), 0, 1)); err != nil {
			t.Fatalf("Submit self exit: %v", err)
		}
		if err := eng.Submit(signedTransfer(t, other, RemoveValidatorRecipient(other.AccountID()), 0, 2)); err != nil {
			t.Fatalf("Submit other exit: %v", err)
		}

		eng.mu.Lock()
		prepareEngineLeaderHeight(eng)
		block, _ := eng.buildProposalLocked()
		if block == nil {
			eng.mu.Unlock()
			t.Fatal("proposal builder returned no block")
		}
		err = eng.verifyBlockForHeightLocked(block)
		changes := eng.setChangesInLocked(block)
		eng.mu.Unlock()
		if err != nil {
			t.Fatalf("builder produced a block it rejects: %v", err)
		}
		if len(changes) != 1 {
			t.Fatalf("proposal carries %d exits from a two-validator set, want 1", len(changes))
		}
	})
}

// End-to-end bonded-open behavior: an outsider self-bonds, is admitted without
// an operator allow-list, survives a restart from persisted set state, and then
// self-exits. Only the candidate signs the admission and exit.
func TestBondedOpenMembershipLifecycle(t *testing.T) {
	nodes, stop := newCluster(t, 4, func(c *Config) {
		c.MembershipMode = MembershipBondedOpen
		c.MinBond = 100
		c.ZeroMinBond = false
		c.EpochLength = 2
		c.UnbondingPeriod = 4
		c.ProposeInterval = 4 * time.Millisecond
		c.RoundTimeout = 40 * time.Millisecond
		c.HeadAnnounceInterval = 20 * time.Millisecond
	})
	defer stop()
	genesis := genesisSetOf(t, nodes)

	candidate, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	premature := signedTransfer(t, candidate, AddValidatorRecipient(candidate.PublicKey), 0, 1)
	if err := nodes[0].engine.Submit(premature); !errors.Is(err, ErrInsufficientBond) {
		t.Fatalf("under-bonded admission = %v, want ErrInsufficientBond", err)
	}

	mintAll(t, nodes, candidate.AccountID(), 100)
	if _, err := nodes[0].engine.SubmitBond(candidate, 100, 2); err != nil {
		t.Fatalf("SubmitBond: %v", err)
	}
	waitFor(t, 20*time.Second, "candidate bond to commit on every node", func() bool {
		for _, nd := range nodes {
			bonded, err := nd.engine.BondedStake(candidate.AccountID())
			if err != nil || bonded != 100 {
				return false
			}
		}
		return true
	})

	admit := signedTransfer(t, candidate, AddValidatorRecipient(candidate.PublicKey), 0, 3)
	if err := nodes[0].engine.Submit(admit); err != nil {
		t.Fatalf("self-signed admission: %v", err)
	}
	waitFor(t, 20*time.Second, "candidate admission on every node", func() bool {
		for _, nd := range nodes {
			if !nd.engine.vset().Contains(candidate.AccountID()) {
				return false
			}
		}
		return true
	})

	// Start a fresh engine from node 0's chain and stores with only the genesis
	// set in config. Start must restore the admitted set before any network work.
	nd := nodes[0]
	fresh, err := New(Config{
		Transport:       nd.bus.endpoint(peer.ID("bonded-open-restart")),
		Validators:      genesis,
		Chain:           nd.chain,
		Ledger:          nd.ledger,
		Self:            nd.acct,
		ProposeInterval: time.Hour,
		RoundTimeout:    time.Hour,
		Evidence:        nd.evidence,
		Sets:            nd.sets,
		Stake:           nd.engine.stake,
		MembershipMode:  MembershipBondedOpen,
		MinBond:         100,
	})
	if err != nil {
		t.Fatalf("New restarted engine: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := fresh.Start(ctx); err != nil {
		t.Fatalf("Start restarted engine: %v", err)
	}
	fresh.Wait()
	if !fresh.vset().Contains(candidate.AccountID()) {
		t.Fatal("restarted engine forgot the permissionlessly admitted validator")
	}

	// Queue the withdrawal while the candidate is still active. It must survive
	// the self-exit and unbonding delay rather than requiring resubmission.
	withdraw := signedTransfer(t, candidate, WithdrawRecipient(candidate.AccountID()), 0, 4)
	if err := nodes[0].engine.Submit(withdraw); err != nil {
		t.Fatalf("queue locked withdrawal: %v", err)
	}
	stopTraffic := keepTrafficFlowing(t, nodes, nodes[0].acct)
	defer stopTraffic()

	exit := signedTransfer(t, candidate, RemoveValidatorRecipient(candidate.AccountID()), 0, 5)
	if err := nodes[0].engine.Submit(exit); err != nil {
		t.Fatalf("self-signed exit: %v", err)
	}
	waitFor(t, 20*time.Second, "candidate exit on every node", func() bool {
		for _, nd := range nodes {
			if nd.engine.vset().Contains(candidate.AccountID()) {
				return false
			}
		}
		return true
	})
	waitFor(t, 20*time.Second, "queued withdrawal after unbonding", func() bool {
		for _, nd := range nodes {
			bonded, err := nd.engine.BondedStake(candidate.AccountID())
			if err != nil || bonded != 0 {
				return false
			}
		}
		return true
	})
	stopTraffic()
	waitFor(t, 5*time.Second, "membership slots to clear after withdrawal", func() bool {
		for _, nd := range nodes {
			nd.engine.mu.Lock()
			_, pending := nd.engine.pendingMembership["stake:withdraw:"+candidate.AccountID()]
			nd.engine.mu.Unlock()
			if pending {
				return false
			}
		}
		return true
	})

	for i, node := range nodes {
		balance, err := node.ledger.Balance(candidate.AccountID())
		if err != nil {
			t.Fatalf("node %d candidate balance: %v", i, err)
		}
		if balance != 100 {
			t.Fatalf("node %d candidate balance after queued withdrawal = %d, want 100", i, balance)
		}
		saved, _, err := node.sets.LoadActive()
		if err != nil {
			t.Fatalf("node %d LoadActive: %v", i, err)
		}
		if saved == nil || saved.Contains(candidate.AccountID()) {
			t.Fatalf("node %d did not persist the self-exit", i)
		}
	}
}
