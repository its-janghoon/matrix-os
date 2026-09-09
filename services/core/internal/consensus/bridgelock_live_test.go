package consensus

import (
	"math/big"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/bridge"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// A lock has to actually COMMIT on a running cluster, not just pass its unit
// tests.
//
// This exists because it did not. Driven against a real matrixd, an ordinary
// transfer committed and a lock sat in the mempool until the client gave up -
// with nothing in any log to say why, because the proposer SKIPS a
// reserved-recipient transaction its own verification refuses and a skipped
// transaction is silent by design. Every unit test passed the whole time: they
// call verifyBridgeLockLocked and applyBridgeLock directly, and neither of those
// is the thing that decides whether a block carries the transaction.

func TestALockCommitsOnARunningCluster(t *testing.T) {
	for _, size := range []int{1, 4} {
		t.Run(sizeName(size), func(t *testing.T) { lockCommitsOn(t, size) })
	}
}

func sizeName(n int) string {
	if n == 1 {
		return "solo validator"
	}
	return "four validators"
}

func lockCommitsOn(t *testing.T, size int) {
	locker := &fakeLocker{escrow: bridge.EscrowAccount}
	nodes, stop := newCluster(t, size, func(c *Config) { c.BridgeLocker = locker })
	defer stop()

	payer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	for _, nd := range nodes {
		if err := nd.ledger.Credit(payer.AccountID(), 2*token.MinBridgeLockAmount); err != nil {
			t.Fatalf("credit: %v", err)
		}
	}

	var addr [20]byte
	for i := range addr {
		addr[i] = 0xa1
	}
	tx := &token.Transaction{
		From:      payer.PublicKey,
		To:        BridgeLockRecipient(addr),
		Amount:    token.MinBridgeLockAmount,
		Nonce:     1,
		Timestamp: time.Now().UnixNano(),
		PrevHash:  make([]byte, 32),
	}
	if err := tx.Sign(payer.PrivateKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := nodes[0].engine.Submit(tx); err != nil {
		t.Fatalf("Submit refused the lock outright: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if bal, err := nodes[0].ledger.Balance(bridge.EscrowAccount); err == nil && bal == token.MinBridgeLockAmount {
			if calls, _, _ := locker.snapshot(); calls == 0 {
				t.Fatal("escrow moved but the lock was never recorded, so nothing could attest to it")
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	bal, _ := nodes[0].ledger.Balance(bridge.EscrowAccount)
	calls, _, _ := locker.snapshot()
	t.Fatalf("the lock never committed: escrow holds %d after 15s, recorded %d times. A lock "+
		"that Submit accepts and no block ever carries is invisible - the proposer skips a "+
		"reserved transaction its own verification refuses, silently", bal, calls)
}

// realLocker is the node's bridgeLockAdapter, rebuilt here so the cluster test
// can drive the REAL bridge. The adapter itself lives in internal/node, which
// consensus must not import; what matters is that the thing behind the interface
// is a genuine *bridge.Bridge with its own mutex and its own view of the ledger,
// not a struct that counts calls.
type realLocker struct{ b *bridge.Bridge }

func (r realLocker) RecordLock(ltx market.LedgerTx, id [32]byte, from string, to [20]byte, amount uint64) error {
	return r.b.RecordLock(ltx, id, from, bridge.Address(to), amount)
}
func (r realLocker) EscrowAccount() string { return r.b.EscrowAccount() }

// A REAL bridge behind BridgeLocker must not wedge the node.
//
// This is the test that was missing, and its absence cost a running network. The
// unit tests and the cluster test above both use fakeLocker, which takes no
// locks; the real bridge took two, in the opposite order from the caller.
// applyBridgeLock runs inside the engine's ledger critical section and called
// RecordLock, which opened its own - a non-reentrant mutex, re-entered. The
// consensus driver goroutine parked forever on the first lock the chain ever
// committed, so the node stopped making blocks and every RPC that touches a
// balance hung. Nothing in any log said so: a deadlocked goroutine is silent.
//
// So the property here is not "the lock is recorded". It is "the node is still
// alive afterwards", which is why an ordinary transfer follows the lock and has
// to commit too.
func TestARealBridgeDoesNotWedgeTheNode(t *testing.T) {
	var locker realLocker
	nodes, stop := newCluster(t, 1, func(c *Config) {
		store, err := kv.New(kv.Config{Path: t.TempDir()})
		if err != nil {
			t.Fatalf("kv: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		locker = realLocker{b: bridge.NewConsensusOrdered(c.Ledger, store, bridge.AttestationParams{
			ChainID:        big.NewInt(31337),
			BridgeContract: bridge.Address{0xbb},
		})}
		c.BridgeLocker = locker
	})
	// Not a plain defer: stop() calls Engine.Wait, and the whole failure mode
	// under test is a driver goroutine that never returns. Without a bound, the
	// test reports the deadlock and then hangs the package cleaning up after it.
	defer stopWithin(t, stop, 15*time.Second)

	payer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	if err := nodes[0].ledger.Credit(payer.AccountID(), 2*token.MinBridgeLockAmount); err != nil {
		t.Fatalf("credit: %v", err)
	}

	var addr [20]byte
	for i := range addr {
		addr[i] = 0xa1
	}
	lock := signedTx(t, payer, BridgeLockRecipient(addr), token.MinBridgeLockAmount, 1)
	if err := nodes[0].engine.Submit(lock); err != nil {
		t.Fatalf("Submit refused the lock outright: %v", err)
	}

	// Every read below goes through a watchdog, because the failure this guards
	// against is a HANG: with the ledger write lock held forever, ledger.Balance
	// blocks in RLock and a plain poll loop would sit here until the package
	// timeout with a 1500-line stack dump instead of a sentence.
	if bal := balanceWithin(t, nodes[0].ledger, bridge.EscrowAccount, token.MinBridgeLockAmount, 15*time.Second); bal != token.MinBridgeLockAmount {
		t.Fatalf("escrow holds %d, want %d: the lock did not commit", bal, token.MinBridgeLockAmount)
	}
	id := DeriveLockID(1, payer.AccountID(), addr, token.MinBridgeLockAmount)
	ev, err := locker.b.GetLock(id)
	if err != nil {
		t.Fatalf("the escrow moved but the bridge has no lock %x to attest to: %v", id, err)
	}
	if ev.NativeAmount != token.MinBridgeLockAmount {
		t.Fatalf("recorded %d native, want %d", ev.NativeAmount, token.MinBridgeLockAmount)
	}

	// The node has to still be a node. A driver that deadlocked while applying
	// the lock produces no further blocks, and the escrow assertion above cannot
	// tell that apart from a driver that is fine.
	after := signedTx(t, payer, "1111111111111111111111111111111111111111111111111111111111111111", 100, 2)
	if err := nodes[0].engine.Submit(after); err != nil {
		t.Fatalf("Submit after the lock: %v", err)
	}
	if bal := balanceWithin(t, nodes[0].ledger,
		"1111111111111111111111111111111111111111111111111111111111111111", 100, 15*time.Second); bal != 100 {
		t.Fatalf("the transfer after the lock never committed (recipient holds %d): the node "+
			"stopped producing blocks when the lock was applied", bal)
	}

	// Reconcile is the operator's backing check and it takes the ledger too, so a
	// half-held section shows up here as a hang rather than a number.
	type recResult struct {
		r   *bridge.Reconciliation
		err error
	}
	done := make(chan recResult, 1)
	go func() {
		r, err := locker.b.Reconcile()
		done <- recResult{r, err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Reconcile: %v", got.err)
		}
		if got.r.OutstandingNative != token.MinBridgeLockAmount || got.r.EscrowBalance != token.MinBridgeLockAmount {
			t.Fatalf("Reconcile reports outstanding %d escrow %d, want %d/%d",
				got.r.OutstandingNative, got.r.EscrowBalance, token.MinBridgeLockAmount, token.MinBridgeLockAmount)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Reconcile blocked for 10s: something is still holding the ledger")
	}
}

// signedTx builds and signs a transaction, because three of them in one test is
// three chances to forget the signature and read the resulting silence as a
// consensus bug.
func signedTx(t *testing.T, from *token.Account, to string, amount, nonce uint64) *token.Transaction {
	t.Helper()
	tx := &token.Transaction{
		From:      from.PublicKey,
		To:        to,
		Amount:    amount,
		Nonce:     nonce,
		Timestamp: time.Now().UnixNano(),
		PrevHash:  make([]byte, 32),
	}
	if err := tx.Sign(from.PrivateKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tx
}

// balanceWithin polls for an expected balance and, when the value never
// arrives, says WHY: a wrong number and a read that never returns are different
// failures, and only one of them means the ledger is wedged. Both are invisible
// in a plain poll loop, which just hangs until the package timeout dumps 1500
// lines of stack.
func balanceWithin(t *testing.T, l *market.Ledger, account string, want uint64, within time.Duration) uint64 {
	t.Helper()
	found := make(chan uint64, 1)
	go func() {
		for {
			if bal, err := l.Balance(account); err == nil && bal == want {
				found <- bal
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	select {
	case bal := <-found:
		return bal
	case <-time.After(within):
	}
	// One more read, on its own clock. If THIS does not come back, no poll above
	// came back either and the balance printed by the caller would be a stale
	// reading from before whatever is holding the lock took it.
	type reading struct {
		bal uint64
		err error
	}
	one := make(chan reading, 1)
	go func() {
		bal, err := l.Balance(account)
		one <- reading{bal, err}
	}()
	select {
	case r := <-one:
		if r.err != nil {
			t.Fatalf("reading %s: %v", account, r.err)
		}
		return r.bal
	case <-time.After(2 * time.Second):
		t.Fatalf("a single balance read of %s did not return in 2s: the ledger write lock is "+
			"held by something that is never giving it back", account)
		return 0
	}
}

// stopWithin shuts a cluster down without letting a wedged engine take the test
// binary with it. A leaked driver goroutine costs nothing once the test has
// already failed; a cleanup that blocks forever costs the whole package.
func stopWithin(t *testing.T, stop func(), within time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(within):
		t.Logf("the cluster did not shut down in %s: a consensus driver is still parked", within)
	}
}
