package bridge

import (
	"context"
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
