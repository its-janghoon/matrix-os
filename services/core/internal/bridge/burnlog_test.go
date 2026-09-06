package bridge

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// abiEncodeStringUint reproduces the Solidity ABI encoding of the non-indexed
// arguments of `event Burned(address indexed, string nativeRecipient, uint256
// amount)` exactly as the EVM lays them out in log data:
//
//	word0 = 0x40 (offset to the string tail, in bytes, from the start of data)
//	word1 = amount (uint256, big-endian)
//	word2 = string length
//	word3.. = string bytes, right-padded to a multiple of 32
//
// It is the encoder the WrappedMatrix.burn emit produces; DecodeBurnedLog must
// invert it byte-for-byte.
func abiEncodeStringUint(s string, amount *big.Int) []byte {
	out := make([]byte, 0, 4*wordLen)
	// word0: offset to string = 0x40 (two head words precede the tail).
	off := make([]byte, wordLen)
	off[wordLen-1] = 0x40
	out = append(out, off...)
	// word1: amount.
	out = append(out, leftPad32(amount.Bytes())...)
	// word2: string length.
	l := make([]byte, wordLen)
	big.NewInt(int64(len(s))).FillBytes(l)
	out = append(out, l...)
	// tail: string bytes right-padded to a multiple of 32.
	b := []byte(s)
	padded := ((len(b) + wordLen - 1) / wordLen) * wordLen
	buf := make([]byte, padded)
	copy(buf, b)
	out = append(out, buf...)
	return out
}

// addrTopic left-pads a 20-byte address into a 32-byte indexed topic word, as
// the EVM does for an indexed address parameter.
func addrTopic(a Address) [wordLen]byte {
	var t [wordLen]byte
	copy(t[wordLen-AddressLen:], a[:])
	return t
}

// TestBurnedEventTopic_MatchesSignatureHash pins topics[0] to the keccak256 of
// the exact Solidity event signature. This is the same 32-byte value ethers.js
// computes as the WrappedMatrix `Burned` event topic, so a decoder keyed on it
// selects real Burned logs and rejects everything else.
func TestBurnedEventTopic_MatchesSignatureHash(t *testing.T) {
	got := BurnedEventTopic()
	want := keccak256([]byte("Burned(address,string,uint256)"))
	if hex.EncodeToString(got[:]) != hex.EncodeToString(want) {
		t.Fatalf("topic = %x, want %x", got, want)
	}
}

// TestDecodeBurnedLog_RoundTrip feeds a log whose bytes are laid out exactly like
// a real on-chain Burned emission through the decoder and asserts every field.
func TestDecodeBurnedLog_RoundTrip(t *testing.T) {
	burner, err := ParseAddress("0x1111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	recipient := "b0a1c2d3e4f500112233445566778899aabbccddeeff00112233445566778899"
	amount := new(big.Int).Mul(big.NewInt(42), big.NewInt(1_000_000_000)) // 42 native units in ERC-20 scale

	log := EthLog{
		Topics:   [][wordLen]byte{BurnedEventTopic(), addrTopic(burner)},
		Data:     abiEncodeStringUint(recipient, amount),
		TxHash:   "0xabc0000000000000000000000000000000000000000000000000000000000def",
		LogIndex: 3,
	}

	d, err := DecodeBurnedLog(log)
	if err != nil {
		t.Fatalf("DecodeBurnedLog: %v", err)
	}
	if d.Burner != burner {
		t.Errorf("burner = %s, want %s", d.Burner, burner)
	}
	if d.NativeRecipient != recipient {
		t.Errorf("nativeRecipient = %q, want %q", d.NativeRecipient, recipient)
	}
	if d.ERC20Amount.Cmp(amount) != 0 {
		t.Errorf("amount = %s, want %s", d.ERC20Amount, amount)
	}
	wantID := "0xabc0000000000000000000000000000000000000000000000000000000000def:3"
	if d.ID != wantID {
		t.Errorf("id = %q, want %q", d.ID, wantID)
	}
}

// TestDecodeBurnedLog_Rejects covers the malformed / wrong-event guards.
func TestDecodeBurnedLog_Rejects(t *testing.T) {
	valid := abiEncodeStringUint("acct", big.NewInt(1_000_000_000))
	burner, _ := ParseAddress("0x2222222222222222222222222222222222222222")

	tests := []struct {
		name string
		log  EthLog
	}{
		{
			name: "wrong topic",
			log: EthLog{
				Topics:   [][wordLen]byte{{0x01}, addrTopic(burner)},
				Data:     valid,
				TxHash:   "0xdead",
				LogIndex: 0,
			},
		},
		{
			name: "missing burner topic",
			log: EthLog{
				Topics:   [][wordLen]byte{BurnedEventTopic()},
				Data:     valid,
				TxHash:   "0xdead",
				LogIndex: 0,
			},
		},
		{
			name: "no tx hash",
			log: EthLog{
				Topics:   [][wordLen]byte{BurnedEventTopic(), addrTopic(burner)},
				Data:     valid,
				TxHash:   "",
				LogIndex: 0,
			},
		},
		{
			name: "truncated data",
			log: EthLog{
				Topics:   [][wordLen]byte{BurnedEventTopic(), addrTopic(burner)},
				Data:     valid[:wordLen],
				TxHash:   "0xdead",
				LogIndex: 0,
			},
		},
		{
			name: "non-zero address padding",
			log: EthLog{
				Topics:   [][wordLen]byte{BurnedEventTopic(), {0x00: 0xff}},
				Data:     valid,
				TxHash:   "0xdead",
				LogIndex: 0,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeBurnedLog(tc.log); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

// TestDecodeBurnedLog_DrivesProcessBurn is the closed-loop proof: a real-shaped
// Burned log is decoded into a BurnEvent and fed through ProcessBurn against a
// bridge that has escrowed native MATRIX, asserting the native unlock lands and
// the 1:1 backing reconciles. This is the wrapped-burn -> native-unlock half of
// the loop, now driven from an on-chain event payload rather than a hand-built
// BurnEvent.
func TestDecodeBurnedLog_DrivesProcessBurn(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}
	ledger := mkt.Ledger()

	// Fund a user via honest genesis issuance and lock native into escrow so the
	// bridge has backing to release on the burn.
	user, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	userID := user.AccountID()
	treasury := token.NewTreasury(ledger, store)
	if err := treasury.ApplyGenesis(nil, token.NativeMaxSupply); err != nil {
		t.Fatalf("ApplyGenesis: %v", err)
	}
	const lockAmount = 1000
	if err := treasury.FundFromRewardPool(userID, lockAmount); err != nil {
		t.Fatalf("FundFromRewardPool: %v", err)
	}

	chainID := big.NewInt(31337)
	var contract Address
	copy(contract[:], []byte("wrappedmatrixcontract"))
	b := New(ledger, store, AttestationParams{ChainID: chainID, BridgeContract: contract})

	recipientEth, _ := ParseAddress("0x3333333333333333333333333333333333333333")
	if _, err := b.Lock(userID, recipientEth, lockAmount); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	// A burn on Ethereum releases part of the escrow back to the native user.
	const unlockNative = 400
	burnAmountERC20 := token.NativeToERC20(unlockNative)
	log := EthLog{
		Topics:   [][wordLen]byte{BurnedEventTopic(), addrTopic(recipientEth)},
		Data:     abiEncodeStringUint(userID, burnAmountERC20),
		TxHash:   "0xfeed000000000000000000000000000000000000000000000000000000000001",
		LogIndex: 7,
	}

	burn, err := DecodeBurnEvent(log)
	if err != nil {
		t.Fatalf("DecodeBurnEvent: %v", err)
	}
	if err := b.ProcessBurn(burn); err != nil {
		t.Fatalf("ProcessBurn: %v", err)
	}

	// The native user received the unlocked amount.
	bal, _ := ledger.Balance(userID)
	if bal != unlockNative {
		t.Errorf("user balance after unlock = %d, want %d", bal, unlockNative)
	}
	// Escrow now backs only the still-locked remainder, and reconciliation holds.
	rec, err := b.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rec.OutstandingNative != lockAmount-unlockNative {
		t.Errorf("outstanding = %d, want %d", rec.OutstandingNative, lockAmount-unlockNative)
	}
	if rec.EscrowBalance != lockAmount-unlockNative {
		t.Errorf("escrow = %d, want %d", rec.EscrowBalance, lockAmount-unlockNative)
	}

	// Replaying the identical log (same txHash:logIndex) is rejected: the id is
	// the replay key, so a re-decoded duplicate burn cannot double-unlock.
	if err := b.ProcessBurn(burn); err == nil {
		t.Fatal("expected ErrBurnAlreadyProcessed on replay of the same burn log")
	}
}
