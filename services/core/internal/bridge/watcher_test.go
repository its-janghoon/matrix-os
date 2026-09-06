package bridge

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// fakeEthClient is a deterministic in-memory EthClient: it serves a fixed head
// and a set of Burned logs keyed by block, so the Watcher's scan/apply/cursor
// logic can be tested without a network or a chain.
type fakeEthClient struct {
	mu   sync.Mutex
	head uint64
	// logsByBlock maps a block number to the Burned logs emitted in it.
	logsByBlock map[uint64][]EthLog
	getLogsCall int
}

func (f *fakeEthClient) BlockNumber(_ context.Context) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.head, nil
}

func (f *fakeEthClient) FilterBurnedLogs(_ context.Context, _ Address, from, to uint64) ([]EthLog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getLogsCall++
	var out []EthLog
	for b := from; b <= to; b++ {
		out = append(out, f.logsByBlock[b]...)
	}
	return out, nil
}

// makeBurnedLog builds an EthLog matching the WrappedMatrix Burned event ABI
// layout for (burner, nativeRecipient, amount), the same layout DecodeBurnedLog
// parses and the BridgeE2E hardhat test cross-checks.
func makeBurnedLog(t *testing.T, burner Address, nativeRecipient string, amount *big.Int, txHash string, logIndex uint64) EthLog {
	t.Helper()

	var burnerTopic [wordLen]byte
	copy(burnerTopic[wordLen-AddressLen:], burner[:])

	// data = head/tail encoding of (string, uint256):
	//   word0: offset to string tail (0x40)
	//   word1: amount
	//   word2: string length
	//   word3+: string bytes right-padded to a word multiple
	var data []byte
	data = append(data, leftPad32(big.NewInt(0x40).Bytes())...)
	data = append(data, bigTo32(amount)...)
	data = append(data, leftPad32(big.NewInt(int64(len(nativeRecipient))).Bytes())...)
	strBytes := []byte(nativeRecipient)
	// right-pad to a multiple of 32
	padded := make([]byte, ((len(strBytes)+wordLen-1)/wordLen)*wordLen)
	copy(padded, strBytes)
	if len(padded) == 0 {
		padded = make([]byte, wordLen)
	}
	data = append(data, padded...)

	return EthLog{
		Topics:   [][wordLen]byte{BurnedEventTopic(), burnerTopic},
		Data:     data,
		TxHash:   txHash,
		LogIndex: logIndex,
	}
}

// newWatcherTestBridge builds a Bridge over a temp kv/ledger with a pre-funded
// escrow so ProcessBurn can release native to a recipient.
func newWatcherTestBridge(t *testing.T, escrowFunding uint64) (*Bridge, *market.Ledger) {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ledger := market.NewLedger(store)
	if escrowFunding > 0 {
		if err := ledger.Credit(EscrowAccount, escrowFunding); err != nil {
			t.Fatalf("Credit escrow: %v", err)
		}
	}
	params := AttestationParams{ChainID: big.NewInt(31337)}
	return New(ledger, store, params), ledger
}

// TestWatcher_AppliesBurnAndAdvancesCursor asserts the Watcher scans a range,
// decodes and applies a Burned log through ProcessBurn (releasing escrow to the
// native recipient), advances its cursor, and treats a re-observation as a
// benign no-op (replay-safe).
func TestWatcher_AppliesBurnAndAdvancesCursor(t *testing.T) {
	// Escrow holds 100 native base units so ProcessBurn can release from it.
	br, ledger := newWatcherTestBridge(t, 100)

	recipient, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	recipientID := recipient.AccountID()

	// One burn of 5 native units worth of wrapped ERC-20 at block 3.
	native := uint64(5)
	erc20 := token.NativeToERC20(native)
	var burner Address
	burner[19] = 0xab
	fake := &fakeEthClient{
		head: 5,
		logsByBlock: map[uint64][]EthLog{
			3: {makeBurnedLog(t, burner, recipientID, erc20, "0xabc", 0)},
		},
	}

	var seen []DecodedBurn
	w, err := NewWatcher(WatcherConfig{
		Client:     fake,
		Bridge:     br,
		StartBlock: 0,
		OnBurn:     func(d DecodedBurn) { seen = append(seen, d) },
	})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	applied, err := w.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1", applied)
	}
	if w.Cursor() != 6 {
		t.Fatalf("cursor = %d, want 6 (head+1)", w.Cursor())
	}
	if len(seen) != 1 || seen[0].NativeRecipient != recipientID {
		t.Fatalf("OnBurn not invoked with the recipient: %+v", seen)
	}

	// Escrow released `native` units to the recipient.
	bal, _ := ledger.Balance(recipientID)
	if bal != native {
		t.Fatalf("recipient balance = %d, want %d", bal, native)
	}

	// Re-scanning the same range (cursor reset) is a benign no-op: the burn id is
	// already processed, so nothing new is applied and no balance changes.
	w.mu.Lock()
	w.next = 0
	w.mu.Unlock()
	applied, err = w.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll replay: %v", err)
	}
	if applied != 0 {
		t.Fatalf("replay applied = %d, want 0 (already processed)", applied)
	}
	bal, _ = ledger.Balance(recipientID)
	if bal != native {
		t.Fatalf("recipient balance after replay = %d, want %d (unchanged)", bal, native)
	}
}

// TestWatcher_RespectsConfirmations asserts blocks within the confirmation depth
// of head are not scanned yet.
func TestWatcher_RespectsConfirmations(t *testing.T) {
	br, ledger := newWatcherTestBridge(t, 100)
	recipient, _ := token.GenerateAccount()
	recipientID := recipient.AccountID()

	var burner Address
	fake := &fakeEthClient{
		head: 10,
		logsByBlock: map[uint64][]EthLog{
			// Burn at block 9; with 3 confirmations, target = 10-3 = 7, so it is
			// NOT yet final and must not be applied.
			9: {makeBurnedLog(t, burner, recipientID, token.NativeToERC20(2), "0xdef", 0)},
		},
	}
	w, err := NewWatcher(WatcherConfig{Client: fake, Bridge: br, Confirmations: 3})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	applied, err := w.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if applied != 0 {
		t.Fatalf("applied = %d, want 0 (burn not yet final)", applied)
	}
	if bal, _ := ledger.Balance(recipientID); bal != 0 {
		t.Fatalf("recipient balance = %d, want 0", bal)
	}

	// Advance head so the burn at 9 becomes final (target = 13-3 = 10 >= 9).
	fake.mu.Lock()
	fake.head = 13
	fake.mu.Unlock()
	applied, err = w.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll after confirmations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1 after confirmations", applied)
	}
	if bal, _ := ledger.Balance(recipientID); bal != 2 {
		t.Fatalf("recipient balance = %d, want 2", bal)
	}
}

// TestParseHexUint covers the JSON-RPC hex helpers used by the HTTP client.
func TestParseHexUint(t *testing.T) {
	cases := map[string]uint64{"0x0": 0, "0x10": 16, "0xff": 255, "": 0, "20": 32}
	for in, want := range cases {
		got, err := parseHexUint(in)
		if err != nil {
			t.Fatalf("parseHexUint(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("parseHexUint(%q) = %d, want %d", in, got, want)
		}
	}
	if s := toHexUint(255); s != "0xff" {
		t.Fatalf("toHexUint(255) = %q, want 0xff", s)
	}
	if _, err := parseHexUint("0xzz"); err == nil {
		t.Fatalf("expected error for invalid hex")
	}
}

// memCursorStore is an in-memory CursorStore for exercising the Watcher's
// persistence path without a pebble store.
type memCursorStore struct {
	mu   sync.Mutex
	data map[string][]byte
	// putErr, when set, fails every Put so the "cursor write failure must not
	// stop the relay" behavior can be asserted.
	putErr error
}

func newMemCursorStore() *memCursorStore {
	return &memCursorStore{data: map[string][]byte{}}
}

func (m *memCursorStore) Get(key []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[string(key)]
	if !ok {
		return nil, nil
	}
	return v, nil
}

func (m *memCursorStore) Put(key, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.putErr != nil {
		return m.putErr
	}
	m.data[string(key)] = append([]byte(nil), value...)
	return nil
}

// TestWatcher_PersistsCursorAndResumesWithoutRescanning proves the persisted
// cursor does the job it exists for: a fresh Watcher over the same store starts
// at the next unscanned block, so a restart does NOT re-request the already
// scanned range. Asserting on the fake client's getLogs call count (and on the
// range it is asked for) is what makes this a real test of the optimization
// rather than just of the stored number.
func TestWatcher_PersistsCursorAndResumesWithoutRescanning(t *testing.T) {
	br, ledger := newWatcherTestBridge(t, 100)
	recipient, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	recipientID := recipient.AccountID()

	var burner Address
	burner[19] = 0xcd
	fake := &fakeEthClient{
		head: 5,
		logsByBlock: map[uint64][]EthLog{
			3: {makeBurnedLog(t, burner, recipientID, token.NativeToERC20(7), "0xfeed", 0)},
		},
	}
	store := newMemCursorStore()

	// First process: scan [0,5], apply the burn, persist the cursor at 6.
	first, err := NewWatcher(WatcherConfig{
		Client:     fake,
		Bridge:     br,
		StartBlock: 0,
		Store:      store,
	})
	if err != nil {
		t.Fatalf("NewWatcher (first): %v", err)
	}
	if applied, err := first.Poll(context.Background()); err != nil || applied != 1 {
		t.Fatalf("first Poll = (%d, %v), want (1, nil)", applied, err)
	}
	if first.Cursor() != 6 {
		t.Fatalf("first cursor = %d, want 6", first.Cursor())
	}
	if bal, _ := ledger.Balance(recipientID); bal != 7 {
		t.Fatalf("recipient balance = %d, want 7", bal)
	}

	// A restart: a brand new Watcher over the same store, still configured with
	// StartBlock 0. Without persistence it would resume at 0 and re-scan.
	second, err := NewWatcher(WatcherConfig{
		Client:     fake,
		Bridge:     br,
		StartBlock: 0,
		Store:      store,
	})
	if err != nil {
		t.Fatalf("NewWatcher (second): %v", err)
	}
	if second.Cursor() != 6 {
		t.Fatalf("resumed cursor = %d, want 6 (persisted), not 0 (StartBlock)", second.Cursor())
	}

	// Nothing final is left to scan (head is 5, cursor is 6), so the resumed
	// watcher issues no getLogs call at all: the re-scan is genuinely skipped.
	callsBefore := fake.getLogsCall
	if applied, err := second.Poll(context.Background()); err != nil || applied != 0 {
		t.Fatalf("resumed Poll = (%d, %v), want (0, nil)", applied, err)
	}
	if fake.getLogsCall != callsBefore {
		t.Fatalf("resumed watcher issued %d getLogs call(s); want 0 (nothing to re-scan)",
			fake.getLogsCall-callsBefore)
	}

	// New blocks arrive: the resumed watcher scans forward from 6, never back
	// over the already-applied range.
	fake.mu.Lock()
	fake.head = 8
	fake.logsByBlock[7] = []EthLog{makeBurnedLog(t, burner, recipientID, token.NativeToERC20(3), "0xbeef", 0)}
	fake.mu.Unlock()

	if applied, err := second.Poll(context.Background()); err != nil || applied != 1 {
		t.Fatalf("forward Poll = (%d, %v), want (1, nil)", applied, err)
	}
	if bal, _ := ledger.Balance(recipientID); bal != 10 {
		t.Fatalf("recipient balance = %d, want 10 (7 + 3, each burn applied once)", bal)
	}
	if second.Cursor() != 9 {
		t.Fatalf("cursor after forward scan = %d, want 9", second.Cursor())
	}
}

// TestWatcher_PersistedCursorNeverRewindsBehindStartBlock asserts a stale cursor
// cannot drag the scan back into a range the operator deliberately excluded by
// raising start_block: the watcher takes whichever is further ahead.
func TestWatcher_PersistedCursorNeverRewindsBehindStartBlock(t *testing.T) {
	br, _ := newWatcherTestBridge(t, 100)
	fake := &fakeEthClient{head: 100}
	store := newMemCursorStore()
	if err := store.Put([]byte(watcherCursorKey), encodeU64(10)); err != nil {
		t.Fatalf("seed cursor: %v", err)
	}

	// Operator raised start_block past the stale cursor: start_block wins.
	ahead, err := NewWatcher(WatcherConfig{Client: fake, Bridge: br, StartBlock: 50, Store: store})
	if err != nil {
		t.Fatalf("NewWatcher (ahead): %v", err)
	}
	if ahead.Cursor() != 50 {
		t.Fatalf("cursor = %d, want 50 (StartBlock ahead of stale cursor)", ahead.Cursor())
	}

	// Cursor ahead of start_block: the cursor wins (normal resume).
	behind, err := NewWatcher(WatcherConfig{Client: fake, Bridge: br, StartBlock: 5, Store: store})
	if err != nil {
		t.Fatalf("NewWatcher (behind): %v", err)
	}
	if behind.Cursor() != 10 {
		t.Fatalf("cursor = %d, want 10 (persisted cursor ahead of StartBlock)", behind.Cursor())
	}
}

// TestWatcher_CorruptPersistedCursorIsRejected asserts a truncated cursor value
// fails construction loudly rather than being silently read as some other block
// height, which would silently skip or re-scan an arbitrary range.
func TestWatcher_CorruptPersistedCursorIsRejected(t *testing.T) {
	br, _ := newWatcherTestBridge(t, 0)
	store := newMemCursorStore()
	if err := store.Put([]byte(watcherCursorKey), []byte{0x01, 0x02}); err != nil {
		t.Fatalf("seed corrupt cursor: %v", err)
	}
	if _, err := NewWatcher(WatcherConfig{Client: &fakeEthClient{}, Bridge: br, Store: store}); err == nil {
		t.Fatal("expected NewWatcher to reject a corrupt persisted cursor")
	}
}

// TestWatcher_CursorWriteFailureDoesNotStopRelay asserts the relay keeps
// applying burns when the cursor cannot be persisted. The cursor is an
// optimization, so losing it must degrade to "re-scan after restart", never to
// "stop relaying" or "skip a burn".
func TestWatcher_CursorWriteFailureDoesNotStopRelay(t *testing.T) {
	br, ledger := newWatcherTestBridge(t, 100)
	recipient, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	recipientID := recipient.AccountID()

	var burner Address
	fake := &fakeEthClient{
		head: 4,
		logsByBlock: map[uint64][]EthLog{
			2: {makeBurnedLog(t, burner, recipientID, token.NativeToERC20(6), "0x0bad", 0)},
		},
	}
	store := newMemCursorStore()
	store.putErr = errors.New("disk full")

	var reported []error
	w, err := NewWatcher(WatcherConfig{
		Client:  fake,
		Bridge:  br,
		Store:   store,
		OnError: func(e error) { reported = append(reported, e) },
	})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	applied, err := w.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll must not fail on a cursor write error: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1 (burn applied despite cursor write failure)", applied)
	}
	if bal, _ := ledger.Balance(recipientID); bal != 6 {
		t.Fatalf("recipient balance = %d, want 6", bal)
	}
	if len(reported) == 0 {
		t.Fatal("expected the cursor write failure to be reported via OnError")
	}
	// In-memory cursor still advanced, so this process does not re-scan.
	if w.Cursor() != 5 {
		t.Fatalf("cursor = %d, want 5 (in-memory advance survives a failed write)", w.Cursor())
	}
}
