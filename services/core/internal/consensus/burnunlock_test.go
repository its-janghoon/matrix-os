package consensus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/libp2p/go-libp2p/core/peer"
)

// fakeUnlocker records releases instead of touching a bridge, so these tests
// measure exactly what consensus decided rather than what a bridge did with it.
type fakeUnlocker struct {
	mu       sync.Mutex
	released map[string]uint64 // burnIDHash -> amount
	calls    int
}

func newFakeUnlocker() *fakeUnlocker {
	return &fakeUnlocker{released: make(map[string]uint64)}
}

func (f *fakeUnlocker) ApplyAttestedUnlock(_ market.LedgerTx, burnIDHash, _ string, native uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if _, done := f.released[burnIDHash]; done {
		return fmt.Errorf("%w: %s", ErrBurnAlreadyReleased, burnIDHash)
	}
	f.released[burnIDHash] = native
	return nil
}

func (f *fakeUnlocker) amountFor(burnIDHash string) (uint64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.released[burnIDHash]
	return v, ok
}

func (f *fakeUnlocker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

const testBurnHash = "1e0c5f1a3d4b6e7c8a9b0c1d2e3f40516273849506172839405162738495a6b7"

func testUnlock(t *testing.T, nodes []*testNode, amount uint64) BurnUnlock {
	t.Helper()
	return BurnUnlock{
		BurnIDHash:   testBurnHash,
		ToAccount:    nodes[0].acct.AccountID(),
		NativeAmount: amount,
	}
}

// waitForRelease waits for the unlocker to record a release, so a test does not
// depend on how many blocks the cluster took to get there.
func waitForRelease(t *testing.T, f *fakeUnlocker, hash string, timeout time.Duration) uint64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if amount, ok := f.amountFor(hash); ok {
			return amount
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no release for burn %s within %s", hash, timeout)
	return 0
}

// TestOneValidatorCannotReleaseEscrow is the property the whole
// consensus-ordered unlock exists for. Before it, whichever node's watcher saw
// a burn released escrow on its own ledger and nowhere else. One validator's
// word must move nothing.
func TestOneValidatorCannotReleaseEscrow(t *testing.T) {
	unlocker := newFakeUnlocker()
	nodes, stop := newCluster(t, 4, func(c *Config) { c.BurnUnlocker = unlocker })
	defer stop()

	unlock := testUnlock(t, nodes, 500)
	if _, err := nodes[0].engine.SubmitBurnAttestation(unlock); err != nil {
		t.Fatalf("SubmitBurnAttestation: %v", err)
	}

	// One of four validators is below quorum, so nothing may be released no
	// matter how many blocks go by.
	time.Sleep(2 * time.Second)
	if _, ok := unlocker.amountFor(testBurnHash); ok {
		t.Fatal("a single validator's attestation released escrow")
	}
	if unlocker.callCount() != 0 {
		t.Fatalf("the unlocker was called %d times below quorum", unlocker.callCount())
	}
}

// TestAQuorumOfAttestationsReleasesEscrowExactlyOnce covers the working path and
// the idempotence that makes it safe to replay: a fourth attestation arriving
// after quorum must not release a second time.
func TestAQuorumOfAttestationsReleasesEscrowExactlyOnce(t *testing.T) {
	unlocker := newFakeUnlocker()
	nodes, stop := newCluster(t, 4, func(c *Config) { c.BurnUnlocker = unlocker })
	defer stop()

	unlock := testUnlock(t, nodes, 500)
	for _, nd := range nodes {
		if _, err := nd.engine.SubmitBurnAttestation(unlock); err != nil {
			t.Fatalf("SubmitBurnAttestation: %v", err)
		}
	}

	if got := waitForRelease(t, unlocker, testBurnHash, 10*time.Second); got != 500 {
		t.Fatalf("released %d, want 500", got)
	}

	// Let the remaining attestations commit, then check nothing released twice.
	time.Sleep(1500 * time.Millisecond)
	if got, _ := unlocker.amountFor(testBurnHash); got != 500 {
		t.Fatalf("released total %d, want 500", got)
	}
}

// TestAttestationsThatDisagreeDoNotCombine is the one that decides whether the
// quorum means anything. Two validators saying "release 500 to A" and two
// saying "release 999 to A" are not four validators agreeing on anything, and
// must not add up to a quorum for either claim.
func TestAttestationsThatDisagreeDoNotCombine(t *testing.T) {
	unlocker := newFakeUnlocker()
	nodes, stop := newCluster(t, 4, func(c *Config) { c.BurnUnlocker = unlocker })
	defer stop()

	honest := testUnlock(t, nodes, 500)
	inflated := testUnlock(t, nodes, 999)

	for _, nd := range nodes[:2] {
		if _, err := nd.engine.SubmitBurnAttestation(honest); err != nil {
			t.Fatalf("submit honest: %v", err)
		}
	}
	for _, nd := range nodes[2:] {
		if _, err := nd.engine.SubmitBurnAttestation(inflated); err != nil {
			t.Fatalf("submit inflated: %v", err)
		}
	}

	time.Sleep(2 * time.Second)
	if amount, ok := unlocker.amountFor(testBurnHash); ok {
		t.Fatalf("a split vote released %d; two validators are not a quorum of four", amount)
	}
}

// TestANonValidatorAttestationIsRefused covers the padding attack: if an
// outsider's attestation counted, a set of n could be pushed over quorum by n
// strangers who never staked anything.
func TestANonValidatorAttestationIsRefused(t *testing.T) {
	unlocker := newFakeUnlocker()
	nodes, stop := newCluster(t, 4, func(c *Config) { c.BurnUnlocker = unlocker })
	defer stop()

	outsider, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	unlock := testUnlock(t, nodes, 500)

	// Submitted directly, bypassing SubmitBurnAttestation's own membership
	// check, so this exercises the block-level rule a malicious leader would hit.
	tx := signedTransfer(t, outsider, unlock.Recipient(), 0, 0)
	nd := nodes[0]
	nd.engine.mu.Lock()
	verr := nd.engine.verifyBurnUnlockLocked(tx)
	nd.engine.mu.Unlock()
	if verr == nil {
		t.Fatal("an outsider's burn attestation was accepted")
	}
	if !errors.Is(verr, ErrNotValidator) {
		t.Fatalf("error = %v, want ErrNotValidator", verr)
	}
}

// TestAnAttestationCarryingValueIsRefused: the recipient is a reserved marker,
// not an account. A transfer to it would strand real MATRIX at an id nobody
// holds a key for.
func TestAnAttestationCarryingValueIsRefused(t *testing.T) {
	nodes, stop := newCluster(t, 4, nil)
	defer stop()

	unlock := testUnlock(t, nodes, 500)
	tx := signedTransfer(t, nodes[0].acct, unlock.Recipient(), 7, 0)
	nd := nodes[0]
	nd.engine.mu.Lock()
	err := nd.engine.verifyBurnUnlockLocked(tx)
	nd.engine.mu.Unlock()
	if err == nil {
		t.Fatal("an attestation carrying value was accepted")
	}
	if !strings.Contains(err.Error(), "carries no value") {
		t.Fatalf("error = %v, want it to say the attestation carries no value", err)
	}
}

// TestABurnUnlockRecipientRoundTrips pins the encoding. The recipient IS the
// attestation's identity, so two spellings of one burn would split a quorum
// that should have combined - the same class of bug as the split vote above,
// but caused by the parser rather than by a liar.
func TestABurnUnlockRecipientRoundTrips(t *testing.T) {
	want := BurnUnlock{
		BurnIDHash:   testBurnHash,
		ToAccount:    "dfb781c10b66f1eb88372bbb038a03d2b0b507e8d772aacaaa39acc70ca5070f",
		NativeAmount: 1234567890,
	}
	got, err := ParseBurnUnlock(want.Recipient())
	if err != nil {
		t.Fatalf("ParseBurnUnlock: %v", err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

// TestMalformedBurnUnlockRecipientsAreRefused: each of these is a second
// spelling of something, an out-of-range value, or a lookalike account. Any one
// of them accepted is a way to split or forge a quorum.
func TestMalformedBurnUnlockRecipientsAreRefused(t *testing.T) {
	acct := "dfb781c10b66f1eb88372bbb038a03d2b0b507e8d772aacaaa39acc70ca5070f"
	cases := []struct {
		name string
		in   string
	}{
		{"not an unlock at all", "some-account"},
		{"prefix only", burnUnlockPrefix},
		{"missing the amount", burnUnlockPrefix + testBurnHash + "/" + acct},
		{"extra field", burnUnlockPrefix + testBurnHash + "/" + acct + "/1/1"},
		{"short burn hash", burnUnlockPrefix + "abcd/" + acct + "/1"},
		{"uppercase burn hash", burnUnlockPrefix + strings.ToUpper(testBurnHash) + "/" + acct + "/1"},
		{"non-hex burn hash", burnUnlockPrefix + strings.Repeat("z", 64) + "/" + acct + "/1"},
		{"recipient is not an account id", burnUnlockPrefix + testBurnHash + "/not-an-account/1"},
		{"zero amount", burnUnlockPrefix + testBurnHash + "/" + acct + "/0"},
		{"leading zero amount", burnUnlockPrefix + testBurnHash + "/" + acct + "/01"},
		{"negative amount", burnUnlockPrefix + testBurnHash + "/" + acct + "/-1"},
		{"amount overflows uint64", burnUnlockPrefix + testBurnHash + "/" + acct + "/18446744073709551616"},
		{"empty amount", burnUnlockPrefix + testBurnHash + "/" + acct + "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := ParseBurnUnlock(tc.in); err == nil {
				t.Fatalf("ParseBurnUnlock(%q) = %+v, want an error", tc.in, got)
			}
		})
	}
}

// TestABurnUnlockAttestationIsNotAnOrdinaryTransfer guards two things that both
// depend on the recipient namespace being recognized: the attestation must not
// be held to the sender-nonce uniqueness rule (this node's attestation counter
// is independent of its transfer nonces), and it must not appear in the
// user-visible transfer history as a payment.
func TestABurnUnlockAttestationIsNotAnOrdinaryTransfer(t *testing.T) {
	unlock := BurnUnlock{BurnIDHash: testBurnHash, ToAccount: "x", NativeAmount: 1}
	tx := &token.Transaction{To: unlock.Recipient()}

	if _, checked := nonceKey(tx); checked {
		t.Fatal("a burn attestation is subject to the transfer nonce rule; its counter is independent")
	}
	if isHistoryTransfer(tx) {
		t.Fatal("a burn attestation would show in the transfer history as a payment")
	}
}

// TestEveryReservedRecipientIsExcludedEverywhere is the guard for the drift that
// let provider registry changes leak into `matrix tx list` as phantom
// zero-value payments. Each of these namespaces names a consensus operation, not
// an account, so all of them must be out of the history AND out of the
// sender-nonce rule. Keeping the two lists in one place is what makes that true;
// this test is what notices if someone splits them again.
func TestEveryReservedRecipientIsExcludedEverywhere(t *testing.T) {
	acct := "dfb781c10b66f1eb88372bbb038a03d2b0b507e8d772aacaaa39acc70ca5070f"
	recipients := map[string]string{
		"bond":             BondAccount(acct),
		"withdrawal":       WithdrawRecipient(acct),
		"slash":            SlashRecipient(acct),
		"remove validator": RemoveValidatorRecipient(acct),
		"provider change":  ProviderChange{Kind: ProviderChangeAdd, ProviderID: "p1"}.Recipient(),
		"burn unlock":      BurnUnlock{BurnIDHash: testBurnHash, ToAccount: acct, NativeAmount: 1}.Recipient(),
	}
	for name, to := range recipients {
		t.Run(name, func(t *testing.T) {
			if !IsReservedRecipient(to) {
				t.Fatalf("%q is not recognized as reserved", to)
			}
			tx := &token.Transaction{To: to}
			if isHistoryTransfer(tx) {
				t.Fatalf("%q would appear in the transfer history as a payment", to)
			}
			if _, checked := nonceKey(tx); checked {
				t.Fatalf("%q is held to the sender-nonce rule, which its counter does not follow", to)
			}
		})
	}

	// And an ordinary transfer must still be both.
	tx := &token.Transaction{To: acct}
	if IsReservedRecipient(acct) {
		t.Fatal("a plain account id was treated as a reserved recipient")
	}
	if !isHistoryTransfer(tx) {
		t.Fatal("an ordinary transfer was excluded from the history")
	}
	if _, checked := nonceKey(tx); !checked {
		t.Fatal("an ordinary transfer is exempt from the sender-nonce rule")
	}
}

// TestAttestationsSurviveARestart: the tally must be a function of committed
// state, not of what this process happened to see. A node that restarts after
// two of four attestations have committed must still release on the third, not
// start counting again from zero.
func TestAttestationsSurviveARestart(t *testing.T) {
	unlocker := newFakeUnlocker()
	nodes, stop := newCluster(t, 4, func(c *Config) { c.BurnUnlocker = unlocker })
	defer stop()

	unlock := testUnlock(t, nodes, 500)
	// Two of four: below quorum (which is 3 of 4 by voting power).
	for _, nd := range nodes[:2] {
		if _, err := nd.engine.SubmitBurnAttestation(unlock); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
	time.Sleep(1500 * time.Millisecond)
	if _, ok := unlocker.amountFor(testBurnHash); ok {
		t.Fatal("two of four released escrow")
	}

	// Build a FRESH engine over node 0's existing chain, ledger and store - what
	// a restart is from this package's point of view - and let it rehydrate from
	// the committed blocks alone.
	nd := nodes[0]
	ctx, cancel := context.WithCancel(context.Background())
	fresh, err := New(Config{
		Transport:       nd.bus.endpoint(peer.ID("node-0-restarted")),
		Validators:      nd.engine.ValidatorSet(),
		Chain:           nd.chain,
		Ledger:          nd.ledger,
		Self:            nd.acct,
		ProposeInterval: 5 * time.Millisecond,
		RoundTimeout:    60 * time.Millisecond,
		Evidence:        nd.evidence,
		Sets:            nd.sets,
		ZeroMinBond:     true,
		BurnUnlocker:    unlocker,
	})
	if err != nil {
		t.Fatalf("rebuild engine: %v", err)
	}
	if err := fresh.Start(ctx); err != nil {
		cancel()
		t.Fatalf("start rebuilt engine: %v", err)
	}
	// Cancel BEFORE waiting, in one defer. Two separate defers run LIFO, so
	// `defer cancel()` then `defer fresh.Wait()` waits on an engine that has not
	// been told to stop, and the test hangs until the package timeout.
	defer func() {
		cancel()
		fresh.Wait()
	}()

	fresh.mu.Lock()
	attested := len(fresh.burnAttestations[unlock.Recipient()])
	fresh.mu.Unlock()
	if attested != 2 {
		t.Fatalf("a restarted node counted %d attestations, want the 2 already committed; "+
			"the tally is not derived from committed state", attested)
	}
}
