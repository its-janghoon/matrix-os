package consensus

import (
	"encoding/hex"
	"strings"
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

type fakeLocker struct {
	calls   int
	lastID  [32]byte
	lastAmt uint64
	escrow  string
	err     error
}

func (f *fakeLocker) RecordLock(id [32]byte, from string, to [20]byte, amount uint64) error {
	f.calls++
	f.lastID = id
	f.lastAmt = amount
	return f.err
}
func (f *fakeLocker) EscrowAccount() string { return f.escrow }

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

// TestALockCarriesValueButAZeroLockDoesNot. This is the one reserved recipient
// besides a stake bond that legitimately holds money, so the value guard has to
// let it through - and still refuse the degenerate case.
func TestALockCarriesValueButAZeroLockDoesNot(t *testing.T) {
	to := BridgeLockRecipient(ethAddr(9))
	if err := isPermanentlyInvalidReserved(&token.Transaction{To: to, Amount: 1_000}); err != nil {
		t.Fatalf("a funded lock was refused: %v", err)
	}
	e := &Engine{}
	if err := e.verifyBridgeLockLocked(&token.Transaction{To: to, Amount: 0}); err == nil {
		t.Fatal("a zero lock was accepted; it would mint nothing and consume a lock id")
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
	if locker.calls != 1 || locker.lastAmt != 4_000 {
		t.Fatalf("the lock was not recorded: %d calls, amount %d", locker.calls, locker.lastAmt)
	}
	want := DeriveLockID(3, acct.AccountID(), ethAddr(0x7e), 4_000)
	if locker.lastID != want {
		t.Fatalf("recorded id %x, want %x", locker.lastID, want)
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
	if locker.calls != 0 {
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
