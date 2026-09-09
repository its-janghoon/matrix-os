package consensus

import (
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/bridge"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// The consensus-ordered bridge lock.
//
// Before this, bridge.Lock moved native into escrow with a direct ledger write
// and had one caller: a CLI against a throwaway ledger. No RPC reached it, so no
// wMATRIX could ever be minted. Exposing it as it stood would have moved
// collateral on one node and nowhere else - the unlock bug again, in the
// direction that creates supply.

func ethAddr(b byte) [20]byte {
	var a [20]byte
	for i := range a {
		a[i] = b
	}
	return a
}

// fakeLocker is guarded because a CLUSTER test hands one of these to every
// node, and each node applies the block on its own goroutine. Unguarded
// counters here are a data race that -race fails on, and that a plain run turns
// into an occasional wrong count rather than an error.
type fakeLocker struct {
	mu      sync.Mutex
	calls   int
	lastID  [32]byte
	lastAmt uint64
	escrow  string
	err     error
}

func (f *fakeLocker) RecordLock(_ market.LedgerTx, id [32]byte, from string, to [20]byte, amount uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastID = id
	f.lastAmt = amount
	return f.err
}

// EscrowAccount is read while the engine holds the ledger, and escrow is set
// once before the cluster starts, but it is guarded anyway: an unguarded read
// of a field another test writes is the kind of race that only shows up on the
// run you cannot reproduce.
func (f *fakeLocker) EscrowAccount() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.escrow
}

// snapshot reads the counters the way a test goroutine must: under the lock the
// engine goroutines write them under.
func (f *fakeLocker) snapshot() (calls int, lastID [32]byte, lastAmt uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.lastID, f.lastAmt
}

// TestTheLockIdMatchesTheBridgesOwnDerivation. The chain derives the id and the
// bridge attests to it. If the two disagreed, every attestation would name a
// lock the contract could not match, and nothing would mint.
func TestTheLockIdMatchesTheBridgesOwnDerivation(t *testing.T) {
	recipient := ethAddr(0xab)
	const from = "aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44"

	mine := DeriveLockID(7, from, recipient, 4_000_000_000)
	theirs := bridge.DeriveLockID(7, from, bridge.Address(recipient), 4_000_000_000)
	if mine != theirs {
		t.Fatalf("consensus derives %x, the bridge derives %x; an attestation would name a "+
			"lock the contract cannot match", mine, theirs)
	}
}

func TestTheLockIdIsUniquePerTransaction(t *testing.T) {
	const from = "aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44"
	const other = "bb11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44"
	base := DeriveLockID(1, from, ethAddr(1), 100)

	for name, got := range map[string][32]byte{
		"different nonce":     DeriveLockID(2, from, ethAddr(1), 100),
		"different sender":    DeriveLockID(1, other, ethAddr(1), 100),
		"different recipient": DeriveLockID(1, from, ethAddr(2), 100),
		"different amount":    DeriveLockID(1, from, ethAddr(1), 101),
	} {
		if got == base {
			t.Fatalf("%s produced the same lock id", name)
		}
	}
}

func TestALockRecipientRoundTrips(t *testing.T) {
	want := ethAddr(0x3c)
	to := BridgeLockRecipient(want)
	if !IsBridgeLockRecipient(to) || !IsReservedRecipient(to) {
		t.Fatalf("%q is not recognised as a reserved bridge lock", to)
	}
	got, err := ParseBridgeLock(to)
	if err != nil || got != want {
		t.Fatalf("ParseBridgeLock(%q) = %x, %v", to, got, err)
	}
}

func TestAMalformedLockRecipientIsRefusedForever(t *testing.T) {
	// An address with hex LETTERS in it, so the uppercase case below is a real
	// case: ethAddr(1) hexes to "0101..." where ToUpper is a no-op.
	a1 := ethAddr(0xab)
	good := hex.EncodeToString(a1[:])
	for _, bad := range []string{
		"",
		good[:38],
		good + "00",
		strings.ToUpper(good),
		"zz" + good[2:],
	} {
		tx := &token.Transaction{To: bridgeLockPrefix + bad, Amount: 10}
		if _, err := ParseBridgeLock(tx.To); err == nil {
			t.Fatalf("ParseBridgeLock accepted %q", bad)
		}
		if err := isPermanentlyInvalidReserved(tx); err == nil {
			t.Fatalf("a lock to %q was allowed into a mempool", bad)
		}
	}
}

// TestBridgeLockMinimumIsIdenticalAtAdmissionAndBlockVerification pins the
// boundary in both consensus gates. A transaction admitted to the mempool must
// never be one that every validator will later refuse in a block.
func TestBridgeLockMinimumIsIdenticalAtAdmissionAndBlockVerification(t *testing.T) {
	to := BridgeLockRecipient(ethAddr(9))
	e := &Engine{}
	cases := []struct {
		name   string
		amount uint64
		valid  bool
	}{
		{name: "zero", amount: 0},
		{name: "one below minimum", amount: token.MinBridgeLockAmount - 1},
		{name: "exact minimum", amount: token.MinBridgeLockAmount, valid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := &token.Transaction{To: to, Amount: tc.amount}
			for gate, err := range map[string]error{
				"permanent-invalid admission": isPermanentlyInvalidReserved(tx),
				"block verification":          e.verifyBridgeLockLocked(tx),
			} {
				if tc.valid && err != nil {
					t.Fatalf("%s rejected exact-minimum lock: %v", gate, err)
				}
				if !tc.valid && !errors.Is(err, ErrInvalidMessage) {
					t.Fatalf("%s error = %v, want ErrInvalidMessage", gate, err)
				}
			}
		})
	}
}

func TestBelowMinimumBridgeLockIsRejectedBeforeMempoolInsertion(t *testing.T) {
	eng := newSubmitOnlyEngine(t, 8)
	sender, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	tx := signedTransfer(t, sender, BridgeLockRecipient(ethAddr(7)), token.MinBridgeLockAmount-1, 0)
	if err := eng.Submit(tx); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("Submit error = %v, want ErrInvalidMessage", err)
	}
	if got := eng.MempoolLen(); got != 0 {
		t.Fatalf("mempool holds %d transactions after permanent rejection, want 0", got)
	}
}

// TestALockPaysNoFee is load-bearing rather than cosmetic. Escrow must receive
// the FULL amount, because the wrapped supply minted against it is computed from
// what the user locked. A fee would mint more wrapped than the escrow holds.
func TestALockPaysNoFee(t *testing.T) {
	if paysFee(BridgeLockRecipient(ethAddr(4))) {
		t.Fatal("a bridge lock pays the protocol fee, so the escrow would receive less than " +
			"the amount minted against it and the 1:1 backing would break by exactly the fee")
	}
}

// TestTheEscrowNameMatchesTheBridges. consensus must not import the bridge, so
// it carries a fallback copy of the escrow account name for nodes with no bridge
// configured. Two spellings would put collateral where the backing check does
// not look.
func TestTheEscrowNameMatchesTheBridges(t *testing.T) {
	if defaultEscrowAccount != bridge.EscrowAccount {
		t.Fatalf("consensus escrows to %q, the bridge holds %q",
			defaultEscrowAccount, bridge.EscrowAccount)
	}
}

func TestApplyingALockMovesValueToEscrowAndRecordsIt(t *testing.T) {
	acct, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	ledger, done := feeLedger(t)
	defer done()
	if err := ledger.Credit(acct.AccountID(), 10_000); err != nil {
		t.Fatalf("credit: %v", err)
	}

	locker := &fakeLocker{escrow: bridge.EscrowAccount}
	e := &Engine{bridgeLocker: locker}
	tx := &token.Transaction{
		To:     BridgeLockRecipient(ethAddr(0x7e)),
		From:   acct.PublicKey,
		Amount: 4_000,
		Nonce:  3,
	}

	var ok bool
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		var err error
		ok, err = e.applyBridgeLock(ltx, tx)
		return err
	}); err != nil {
		t.Fatalf("applyBridgeLock: %v", err)
	}
	if !ok {
		t.Fatal("an affordable lock was skipped")
	}
	if bal, _ := ledger.Balance(bridge.EscrowAccount); bal != 4_000 {
		t.Fatalf("escrow holds %d, want the full 4000", bal)
	}
	if bal, _ := ledger.Balance(acct.AccountID()); bal != 6_000 {
		t.Fatalf("sender holds %d, want 6000", bal)
	}
	calls, lastID, lastAmt := locker.snapshot()
	if calls != 1 || lastAmt != 4_000 {
		t.Fatalf("the lock was not recorded: %d calls, amount %d", calls, lastAmt)
	}
	want := DeriveLockID(3, acct.AccountID(), ethAddr(0x7e), 4_000)
	if lastID != want {
		t.Fatalf("recorded id %x, want %x", lastID, want)
	}
}

// TestAnUnaffordableLockIsSkippedNotWedged. Every node sees the same prior
// balances, so every node makes the same skip decision - the rule an ordinary
// transfer already follows.
func TestAnUnaffordableLockIsSkippedNotWedged(t *testing.T) {
	acct, _ := token.GenerateAccount()
	ledger, done := feeLedger(t)
	defer done()
	if err := ledger.Credit(acct.AccountID(), 10); err != nil {
		t.Fatalf("credit: %v", err)
	}
	locker := &fakeLocker{escrow: bridge.EscrowAccount}
	e := &Engine{bridgeLocker: locker}
	tx := &token.Transaction{To: BridgeLockRecipient(ethAddr(1)), From: acct.PublicKey, Amount: 999, Nonce: 1}

	var ok bool
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		var err error
		ok, err = e.applyBridgeLock(ltx, tx)
		return err
	}); err != nil {
		t.Fatalf("applyBridgeLock: %v", err)
	}
	if ok {
		t.Fatal("an unaffordable lock reported success")
	}
	if calls, _, _ := locker.snapshot(); calls != 0 {
		t.Fatal("an unaffordable lock was recorded, so the bridge would attest to collateral " +
			"that was never escrowed")
	}
	if bal, _ := ledger.Balance(bridge.EscrowAccount); bal != 0 {
		t.Fatalf("escrow holds %d after a skipped lock", bal)
	}
}

// TestANodeWithNoBridgeStillEscrows. Otherwise its balances diverge from every
// node that has one, which is the divergence this whole change exists to avoid.
func TestANodeWithNoBridgeStillEscrows(t *testing.T) {
	acct, _ := token.GenerateAccount()
	ledger, done := feeLedger(t)
	defer done()
	if err := ledger.Credit(acct.AccountID(), 5_000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	e := &Engine{} // no bridge configured
	tx := &token.Transaction{To: BridgeLockRecipient(ethAddr(2)), From: acct.PublicKey, Amount: 5_000, Nonce: 1}

	var ok bool
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		var err error
		ok, err = e.applyBridgeLock(ltx, tx)
		return err
	}); err != nil {
		t.Fatalf("applyBridgeLock: %v", err)
	}
	if !ok {
		t.Fatal("a node with no bridge skipped the lock")
	}
	if bal, _ := ledger.Balance(bridge.EscrowAccount); bal != 5_000 {
		t.Fatalf("escrow holds %d on a bridgeless node, want 5000: its balances would diverge "+
			"from every node that has a bridge", bal)
	}
}

// TestTheRecipientFormatMatchesWhatClientsBuild pins the Go format against the
// literal every TypeScript client produces.
//
// The recipient IS the instruction. A client that builds the string wrongly does
// not get an error - it makes a transfer to a different reserved namespace, or
// to an account id nobody holds, and the money is gone somewhere quiet. The
// format therefore lives in packages/protocol (and, transcribed, in the SDK),
// and this is the third corner of that triangle: if Go ever changes the prefix
// or the case, this fails here rather than in someone's wallet.
func TestTheRecipientFormatMatchesWhatClientsBuild(t *testing.T) {
	// The exact string `bridgeLockRecipient('0x70997970C51812dc3A010C7d01b50e0d17dc79C8')`
	// returns in packages/protocol and packages/sdk.
	const fromTypeScript = "bridge/lock/70997970c51812dc3a010c7d01b50e0d17dc79c8"

	var addr [20]byte
	raw, err := hex.DecodeString("70997970c51812dc3a010c7d01b50e0d17dc79c8")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	copy(addr[:], raw)

	if got := BridgeLockRecipient(addr); got != fromTypeScript {
		t.Fatalf("Go builds %q, TypeScript builds %q; a client following one of them sends "+
			"money the other cannot find", got, fromTypeScript)
	}
	// And the chain accepts what the clients produce.
	if _, err := ParseBridgeLock(fromTypeScript); err != nil {
		t.Fatalf("consensus refuses the recipient every client builds: %v", err)
	}
}
