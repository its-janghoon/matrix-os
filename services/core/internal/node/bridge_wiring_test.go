package node

import (
	"context"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/bridge"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// These tests cover the node's bridge subsystem wiring the way genesis_test.go
// covers the genesis path: by exercising the exact builders Node.Start calls
// (newConfiguredBridge, newConfiguredBridgeWatcher, runBridgeWatcher) against a
// real market ledger and KV store, without standing up the full libp2p node.
// The Ethereum client is a fake so the burn side is deterministic; the burn LOG
// bytes are the same layout internal/bridge decodes and the hardhat BridgeE2E
// test cross-checks.

const testChainID = int64(31337)

// testContract is a syntactically valid WrappedMatrix address for config tests.
const testContract = "0x00000000000000000000000000000000000000ab"

// fakeNodeEthClient is a deterministic EthClient serving a fixed head and a set
// of Burned logs keyed by block.
type fakeNodeEthClient struct {
	mu          sync.Mutex
	head        uint64
	logsByBlock map[uint64][]bridge.EthLog
	getLogsCall int
	headErr     error
}

func (f *fakeNodeEthClient) BlockNumber(_ context.Context) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.head, f.headErr
}

func (f *fakeNodeEthClient) FilterBurnedLogs(_ context.Context, _ bridge.Address, from, to uint64) ([]bridge.EthLog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getLogsCall++
	var out []bridge.EthLog
	for b := from; b <= to; b++ {
		out = append(out, f.logsByBlock[b]...)
	}
	return out, nil
}

// burnedLog builds an EthLog matching the WrappedMatrix Burned event ABI layout
// (indexed burner topic; data = head/tail encoded (string nativeRecipient,
// uint256 amount)), the same bytes bridge.DecodeBurnedLog parses.
func burnedLog(nativeRecipient string, erc20 *big.Int, txHash string, logIndex uint64) bridge.EthLog {
	const word = 32
	pad32 := func(b []byte) []byte {
		out := make([]byte, word)
		copy(out[word-len(b):], b)
		return out
	}

	var burnerTopic [word]byte
	burnerTopic[word-1] = 0xab

	var data []byte
	data = append(data, pad32(big.NewInt(0x40).Bytes())...)                        // offset to string tail
	data = append(data, pad32(erc20.Bytes())...)                                   // amount
	data = append(data, pad32(big.NewInt(int64(len(nativeRecipient))).Bytes())...) // string length
	strBytes := []byte(nativeRecipient)
	padded := make([]byte, ((len(strBytes)+word-1)/word)*word)
	if len(padded) == 0 {
		padded = make([]byte, word)
	}
	copy(padded, strBytes)
	data = append(data, padded...)

	return bridge.EthLog{
		Topics:   [][word]byte{bridge.BurnedEventTopic(), burnerTopic},
		Data:     data,
		TxHash:   txHash,
		LogIndex: logIndex,
	}
}

// zeroConfirmations is the explicit "act on head" setting a local instant-finality
// chain uses. It exists as a helper because the config field is a pointer so an
// unset value can default conservatively.
func zeroConfirmations() *uint64 {
	v := uint64(0)
	return &v
}

// TestBridgeConfig_OffByDefault asserts the subsystem is genuinely opt-in: a
// zero config builds no bridge and no watcher and does not error, so a node
// without bridge settings behaves exactly as it did before this wiring.
func TestBridgeConfig_OffByDefault(t *testing.T) {
	store, ledger := newBridgeTestLedger(t)

	b, err := newConfiguredBridge(ledger, store, BridgeConfig{})
	if err != nil {
		t.Fatalf("newConfiguredBridge with zero config: %v", err)
	}
	if b != nil {
		t.Fatal("expected no bridge for a zero config")
	}
	client, err := dialBridgeClient(BridgeConfig{})
	if err != nil {
		t.Fatalf("dialBridgeClient with zero config: %v", err)
	}
	if client != nil {
		t.Fatal("expected no eth client for a zero config")
	}
	w, err := newConfiguredBridgeWatcher(nil, store, nil, BridgeConfig{}, nil, nil)
	if err != nil {
		t.Fatalf("newConfiguredBridgeWatcher with zero config: %v", err)
	}
	if w != nil {
		t.Fatal("expected no watcher for a zero config")
	}
}

// TestBridgeConfig_IncompleteWatchConfigIsRejected asserts a config that asks
// for relaying but cannot relay fails at startup instead of coming up quiet. A
// node that silently ignores bridge.watch would leave burns unprocessed while
// looking healthy, which is the failure mode worth being loud about.
func TestBridgeConfig_IncompleteWatchConfigIsRejected(t *testing.T) {
	store, ledger := newBridgeTestLedger(t)

	enabled := func(mutate func(*BridgeConfig)) BridgeConfig {
		cfg := BridgeConfig{
			Contract: testContract,
			ChainID:  testChainID,
			Watch: BridgeWatchConfig{
				Enabled: true,
				RPCURL:  "http://127.0.0.1:8545",
			},
		}
		mutate(&cfg)
		return cfg
	}

	t.Run("watch enabled without a contract", func(t *testing.T) {
		cfg := enabled(func(c *BridgeConfig) { c.Contract = "" })
		if _, err := newConfiguredBridge(ledger, store, cfg); err == nil {
			t.Fatal("expected an error when bridge.watch is enabled with no contract")
		}
	})

	t.Run("invalid contract address", func(t *testing.T) {
		cfg := enabled(func(c *BridgeConfig) { c.Contract = "0xnothex" })
		if _, err := newConfiguredBridge(ledger, store, cfg); err == nil {
			t.Fatal("expected an error for a malformed contract address")
		}
	})

	t.Run("missing chain id", func(t *testing.T) {
		cfg := enabled(func(c *BridgeConfig) { c.ChainID = 0 })
		if _, err := newConfiguredBridge(ledger, store, cfg); err == nil {
			t.Fatal("expected an error for a zero chain id: attestations would bind to the wrong chain")
		}
	})

	t.Run("watch enabled without an rpc url", func(t *testing.T) {
		cfg := enabled(func(c *BridgeConfig) { c.Watch.RPCURL = "" })
		if _, err := dialBridgeClient(cfg); err == nil {
			t.Fatal("expected an error when bridge.watch is enabled with no rpc_url")
		}
	})

	t.Run("watch enabled with no bridge applier", func(t *testing.T) {
		cfg := enabled(func(*BridgeConfig) {})
		if _, err := newConfiguredBridgeWatcher(nil, store, &fakeNodeEthClient{}, cfg, nil, nil); err == nil {
			t.Fatal("expected an error when the watcher has no bridge to apply burns to")
		}
	})
}

// TestBridgeWatchConfig_ConfirmationsDefault asserts an unset confirmation depth
// defaults conservatively rather than to zero (a node pointed at a real endpoint
// must not release escrow on a reorgable block), while an explicit 0 is honoured
// for instant-finality dev chains.
func TestBridgeWatchConfig_ConfirmationsDefault(t *testing.T) {
	if got := (BridgeWatchConfig{}).confirmations(); got != defaultBridgeConfirmations {
		t.Fatalf("unset confirmations = %d, want the conservative default %d", got, defaultBridgeConfirmations)
	}
	if got := (BridgeWatchConfig{Confirmations: zeroConfirmations()}).confirmations(); got != 0 {
		t.Fatalf("explicit 0 confirmations = %d, want 0 honoured", got)
	}
	five := uint64(5)
	if got := (BridgeWatchConfig{Confirmations: &five}).confirmations(); got != 5 {
		t.Fatalf("explicit 5 confirmations = %d, want 5", got)
	}
}

// TestInitialize_LeavesBridgeDisabled asserts the config `matrix init` actually
// writes has a bridge section that is off and that round-trips through the
// loader, so a freshly initialized node never tries to relay against an
// endpoint the operator has not supplied. It reloads the written file (rather
// than inspecting the in-memory struct) because a marshal/unmarshal mismatch in
// the new section -- an unrepresentable duration, a pointer field -- would break
// `matrix init` itself.
func TestInitialize_LeavesBridgeDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Initialize(path); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), "bridge:") {
		t.Fatalf("generated config has no bridge section, so the knobs are undocumented:\n%s", raw)
	}

	// The generated file must load cleanly: this is what `matrixd` does on boot.
	n, err := New(context.Background(), path)
	if err != nil {
		t.Fatalf("New on the generated config: %v", err)
	}
	cfg := n.config
	if cfg.Bridge.bridgeEnabled() {
		t.Fatalf("generated config enables the bridge (contract %q); want it off", cfg.Bridge.Contract)
	}
	if cfg.Bridge.Watch.Enabled {
		t.Fatal("generated config enables bridge.watch; want it off")
	}
	// Unset confirmations must stay unset so the conservative default applies.
	if cfg.Bridge.Watch.Confirmations != nil {
		t.Fatalf("generated config pins confirmations to %d; want null so the default applies",
			*cfg.Bridge.Watch.Confirmations)
	}
	if got := cfg.Bridge.Watch.confirmations(); got != defaultBridgeConfirmations {
		t.Fatalf("generated config resolves to %d confirmations, want %d", got, defaultBridgeConfirmations)
	}

	// And the off config builds nothing, matching what Start would do.
	store, ledger := newBridgeTestLedger(t)
	b, err := newConfiguredBridge(ledger, store, cfg.Bridge)
	if err != nil || b != nil {
		t.Fatalf("newConfiguredBridge on the generated config = (%v, %v), want (nil, nil)", b, err)
	}
}

// TestNodeBridgeWiring_BurnUnlocksOnTheNodeLedger is the test that justifies
// this whole wiring. It builds the bridge the way Node.Start does -- over the
// node's OWN market ledger and KV store -- locks native MATRIX through it (so
// escrow is backed by a real lock rather than a seeded demo balance, which is
// exactly what cmd/bridge-watch cannot do), then lets the watcher observe the
// matching on-chain Burned event and asserts:
//
//   - the unlock lands on the SAME ledger that holds the lock,
//   - Reconcile agrees (escrow balance == locked - unlocked), which is only a
//     meaningful check because both halves share one ledger,
//   - re-observing the burn changes nothing (replay-safe).
func TestNodeBridgeWiring_BurnUnlocksOnTheNodeLedger(t *testing.T) {
	store, ledger := newBridgeTestLedger(t)

	user, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	userID := user.AccountID()
	const funded = uint64(1_000)
	const locked = uint64(400)
	if err := ledger.Credit(userID, funded); err != nil {
		t.Fatalf("Credit: %v", err)
	}

	cfg := BridgeConfig{
		Contract: testContract,
		ChainID:  testChainID,
		Watch: BridgeWatchConfig{
			Enabled:       true,
			RPCURL:        "http://127.0.0.1:8545",
			StartBlock:    0,
			Confirmations: zeroConfirmations(),
		},
	}

	b, err := newConfiguredBridge(ledger, store, cfg)
	if err != nil {
		t.Fatalf("newConfiguredBridge: %v", err)
	}
	if b == nil {
		t.Fatal("expected a bridge for a configured contract")
	}

	// The native->wrapped half, on this node's ledger: escrow the user's MATRIX.
	var l1Recipient bridge.Address
	l1Recipient[19] = 0x01
	lockEvent, err := b.Lock(userID, l1Recipient, locked)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if escrow, _ := ledger.Balance(bridge.EscrowAccount); escrow != locked {
		t.Fatalf("escrow after lock = %d, want %d", escrow, locked)
	}
	if bal, _ := ledger.Balance(userID); bal != funded-locked {
		t.Fatalf("user balance after lock = %d, want %d", bal, funded-locked)
	}

	// The wrapped->native half: an on-chain burn of part of that wrapped supply,
	// naming a native recipient.
	recipient, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount(recipient): %v", err)
	}
	recipientID := recipient.AccountID()
	const unlockNative = uint64(150)
	fake := &fakeNodeEthClient{
		head: 4,
		logsByBlock: map[uint64][]bridge.EthLog{
			2: {burnedLog(recipientID, token.NativeToERC20(unlockNative), "0xnode1", 0)},
		},
	}

	var seen []bridge.DecodedBurn
	var watchErrs []error
	w, err := newConfiguredBridgeWatcher(b, store, fake, cfg,
		func(d bridge.DecodedBurn) { seen = append(seen, d) },
		func(e error) { watchErrs = append(watchErrs, e) },
	)
	if err != nil {
		t.Fatalf("newConfiguredBridgeWatcher: %v", err)
	}
	if w == nil {
		t.Fatal("expected a watcher for an enabled bridge.watch")
	}

	applied, err := w.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1", applied)
	}
	if len(watchErrs) != 0 {
		t.Fatalf("unexpected watcher errors: %v", watchErrs)
	}
	if len(seen) != 1 || seen[0].NativeRecipient != recipientID {
		t.Fatalf("OnBurn not called with the burn's native recipient: %+v", seen)
	}

	// The unlock hit the node's own ledger, released from the same escrow the
	// lock filled.
	if bal, _ := ledger.Balance(recipientID); bal != unlockNative {
		t.Fatalf("recipient balance = %d, want %d", bal, unlockNative)
	}
	if escrow, _ := ledger.Balance(bridge.EscrowAccount); escrow != locked-unlockNative {
		t.Fatalf("escrow after unlock = %d, want %d", escrow, locked-unlockNative)
	}

	// Reconcile is meaningful here precisely because one ledger holds both halves.
	rec, err := b.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rec.LockedNative != locked || rec.UnlockedNative != unlockNative {
		t.Fatalf("reconciliation = locked %d / unlocked %d, want %d / %d",
			rec.LockedNative, rec.UnlockedNative, locked, unlockNative)
	}
	if rec.OutstandingNative != locked-unlockNative || rec.EscrowBalance != rec.OutstandingNative {
		t.Fatalf("outstanding %d / escrow %d disagree (want %d)",
			rec.OutstandingNative, rec.EscrowBalance, locked-unlockNative)
	}
	if want := token.NativeToERC20(locked - unlockNative); rec.OutstandingERC20.Cmp(want) != 0 {
		t.Fatalf("outstanding ERC20 = %s, want %s", rec.OutstandingERC20, want)
	}

	// The lock is still recorded and attestable (the mint half is untouched by
	// the unlock half sharing the ledger).
	if got, err := b.GetLock(lockEvent.LockID); err != nil || got.NativeAmount != locked {
		t.Fatalf("GetLock = (%+v, %v), want the recorded lock of %d", got, err, locked)
	}

	// Replay: re-observing the same burn must not release escrow twice.
	fake.mu.Lock()
	fake.head = 9
	fake.logsByBlock[7] = []bridge.EthLog{burnedLog(recipientID, token.NativeToERC20(unlockNative), "0xnode1", 0)}
	fake.mu.Unlock()
	if _, err := w.Poll(context.Background()); err != nil {
		t.Fatalf("Poll (replay): %v", err)
	}
	if bal, _ := ledger.Balance(recipientID); bal != unlockNative {
		t.Fatalf("recipient balance after replay = %d, want %d (unchanged)", bal, unlockNative)
	}
	if _, err := b.Reconcile(); err != nil {
		t.Fatalf("Reconcile after replay: %v", err)
	}
}

// TestNodeBridgeWiring_ResumesFromPersistedCursorAcrossRestart asserts the
// wiring's cursor persistence works through the node's real KV store: a
// simulated matrixd restart resumes at the next unscanned block instead of
// re-scanning from start_block. Without this, an in-node watcher pointed at a
// contract deployed far behind head would re-scan that entire range on every
// node restart.
func TestNodeBridgeWiring_ResumesFromPersistedCursorAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := BridgeConfig{
		Contract: testContract,
		ChainID:  testChainID,
		Watch: BridgeWatchConfig{
			Enabled:       true,
			RPCURL:        "http://127.0.0.1:8545",
			StartBlock:    100,
			Confirmations: zeroConfirmations(),
		},
	}

	recipient, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	recipientID := recipient.AccountID()

	fake := &fakeNodeEthClient{
		head: 120,
		logsByBlock: map[uint64][]bridge.EthLog{
			110: {burnedLog(recipientID, token.NativeToERC20(9), "0xresume", 0)},
		},
	}

	// First boot: scan [100,120] and apply the burn.
	func() {
		store, err := kv.New(kv.Config{Path: dir})
		if err != nil {
			t.Fatalf("kv.New (boot 1): %v", err)
		}
		defer func() { _ = store.Close() }()
		ledger := market.NewLedger(store)
		if err := ledger.Credit(bridge.EscrowAccount, 100); err != nil {
			t.Fatalf("Credit escrow: %v", err)
		}
		b, err := newConfiguredBridge(ledger, store, cfg)
		if err != nil {
			t.Fatalf("newConfiguredBridge (boot 1): %v", err)
		}
		w, err := newConfiguredBridgeWatcher(b, store, fake, cfg, nil, nil)
		if err != nil {
			t.Fatalf("newConfiguredBridgeWatcher (boot 1): %v", err)
		}
		if w.Cursor() != 100 {
			t.Fatalf("boot 1 cursor = %d, want 100 (start_block, no persisted cursor)", w.Cursor())
		}
		if applied, err := w.Poll(context.Background()); err != nil || applied != 1 {
			t.Fatalf("boot 1 Poll = (%d, %v), want (1, nil)", applied, err)
		}
		if bal, _ := ledger.Balance(recipientID); bal != 9 {
			t.Fatalf("boot 1 recipient balance = %d, want 9", bal)
		}
	}()

	// Second boot: the same store and the same config. The watcher must resume at
	// 121, issue no getLogs for the already-scanned range, and leave balances be.
	func() {
		store, err := kv.New(kv.Config{Path: dir})
		if err != nil {
			t.Fatalf("kv.New (boot 2): %v", err)
		}
		defer func() { _ = store.Close() }()
		ledger := market.NewLedger(store)
		b, err := newConfiguredBridge(ledger, store, cfg)
		if err != nil {
			t.Fatalf("newConfiguredBridge (boot 2): %v", err)
		}
		w, err := newConfiguredBridgeWatcher(b, store, fake, cfg, nil, nil)
		if err != nil {
			t.Fatalf("newConfiguredBridgeWatcher (boot 2): %v", err)
		}
		if w.Cursor() != 121 {
			t.Fatalf("boot 2 cursor = %d, want 121 (persisted), not %d (start_block)",
				w.Cursor(), cfg.Watch.StartBlock)
		}
		callsBefore := fake.getLogsCall
		if applied, err := w.Poll(context.Background()); err != nil || applied != 0 {
			t.Fatalf("boot 2 Poll = (%d, %v), want (0, nil)", applied, err)
		}
		if fake.getLogsCall != callsBefore {
			t.Fatalf("boot 2 issued %d getLogs call(s); want 0 (range already scanned)",
				fake.getLogsCall-callsBefore)
		}
		if bal, _ := ledger.Balance(recipientID); bal != 9 {
			t.Fatalf("boot 2 recipient balance = %d, want 9 (unchanged, applied once)", bal)
		}
	}()
}

// TestNodeBridgeWiring_CursorIsNamespacedPerContract asserts two deployments
// sharing one store keep separate cursors, so enabling a second bridge never
// makes the first skip blocks it has not scanned.
func TestNodeBridgeWiring_CursorIsNamespacedPerContract(t *testing.T) {
	store, ledger := newBridgeTestLedger(t)
	fake := &fakeNodeEthClient{head: 50}

	cfgA := BridgeConfig{
		Contract: testContract,
		ChainID:  testChainID,
		Watch:    BridgeWatchConfig{Enabled: true, RPCURL: "http://x", Confirmations: zeroConfirmations()},
	}
	cfgB := cfgA
	cfgB.Contract = "0x00000000000000000000000000000000000000cd"

	bA, err := newConfiguredBridge(ledger, store, cfgA)
	if err != nil {
		t.Fatalf("newConfiguredBridge(A): %v", err)
	}
	wA, err := newConfiguredBridgeWatcher(bA, store, fake, cfgA, nil, nil)
	if err != nil {
		t.Fatalf("newConfiguredBridgeWatcher(A): %v", err)
	}
	if _, err := wA.Poll(context.Background()); err != nil {
		t.Fatalf("Poll(A): %v", err)
	}
	if wA.Cursor() != 51 {
		t.Fatalf("A cursor = %d, want 51", wA.Cursor())
	}

	// B has never scanned anything, so it must start at its own start_block, not
	// inherit A's cursor.
	bB, err := newConfiguredBridge(ledger, store, cfgB)
	if err != nil {
		t.Fatalf("newConfiguredBridge(B): %v", err)
	}
	wB, err := newConfiguredBridgeWatcher(bB, store, fake, cfgB, nil, nil)
	if err != nil {
		t.Fatalf("newConfiguredBridgeWatcher(B): %v", err)
	}
	if wB.Cursor() != 0 {
		t.Fatalf("B cursor = %d, want 0 (its own namespace, not A's cursor)", wB.Cursor())
	}
}

// TestRunBridgeWatcher_JoinsOnContextCancel asserts the watcher goroutine is
// joinable, which is what lets Node.Stop wait for it before closing the ledger
// and KV store it writes to.
func TestRunBridgeWatcher_JoinsOnContextCancel(t *testing.T) {
	store, ledger := newBridgeTestLedger(t)
	cfg := BridgeConfig{
		Contract: testContract,
		ChainID:  testChainID,
		Watch: BridgeWatchConfig{
			Enabled:       true,
			RPCURL:        "http://127.0.0.1:8545",
			Confirmations: zeroConfirmations(),
			PollInterval:  5 * time.Millisecond,
		},
	}
	b, err := newConfiguredBridge(ledger, store, cfg)
	if err != nil {
		t.Fatalf("newConfiguredBridge: %v", err)
	}
	w, err := newConfiguredBridgeWatcher(b, store, &fakeNodeEthClient{head: 1}, cfg, nil, nil)
	if err != nil {
		t.Fatalf("newConfiguredBridgeWatcher: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var exitErr error
	var exitOnce sync.Once
	done := runBridgeWatcher(ctx, w, func(err error) { exitOnce.Do(func() { exitErr = err }) })

	// Let it poll at least once, then stop it the way Node.Stop does.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("bridge watcher goroutine did not exit after context cancel")
	}
	if !errors.Is(exitErr, context.Canceled) {
		t.Fatalf("watcher exit error = %v, want context.Canceled", exitErr)
	}
}

// newBridgeTestLedger opens a temp KV store and a market ledger over it, the
// same pair Node.Start hands the bridge.
func newBridgeTestLedger(t *testing.T) (*kv.Store, *market.Ledger) {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, market.NewLedger(store)
}
