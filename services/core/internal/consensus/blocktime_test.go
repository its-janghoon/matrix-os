package consensus

import (
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
	"github.com/libp2p/go-libp2p/core/peer"
)

// blockTimeEngine builds an engine whose clock the test drives, so a proposer
// and a validator whose clocks disagree can both be simulated.
func blockTimeEngine(t *testing.T, clock func() time.Time) *Engine {
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
	bus := newMemBus()
	eng, err := New(Config{
		Transport:       bus.endpoint(peer.ID("block-time")),
		Validators:      vs,
		Chain:           NewBlockChain(store),
		Ledger:          market.NewLedger(store),
		Self:            self,
		ProposeInterval: time.Hour,
		RoundTimeout:    time.Hour,
		Sets:            NewSetStore(store),
		ZeroMinBond:     true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	eng.now = clock
	return eng
}

// TestBlockTimestampMustBeNearTheValidatorsClock is the rule that keeps the
// timestamp meaningful. A proposer free to stamp any value could date a block to
// next year, and every explorer and exchange reading the chain would show it.
func TestBlockTimestampMustBeNearTheValidatorsClock(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	eng := blockTimeEngine(t, func() time.Time { return base })

	for _, tc := range []struct {
		name      string
		stamp     int64
		wantValid bool
	}{
		{"now", base.Unix(), true},
		{"a minute behind, inside the window", base.Add(-time.Minute).Unix(), true},
		{"a minute ahead, inside the window", base.Add(time.Minute).Unix(), true},
		{"an hour ahead", base.Add(time.Hour).Unix(), false},
		{"an hour behind", base.Add(-time.Hour).Unix(), false},
		{"a year ahead", base.Add(365 * 24 * time.Hour).Unix(), false},
		{"unset", 0, false},
		{"negative", -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eng.mu.Lock()
			err := eng.verifyBlockTimestampLocked(&Block{Height: 0, Timestamp: tc.stamp})
			eng.mu.Unlock()
			if tc.wantValid && err != nil {
				t.Fatalf("stamp %d should be accepted: %v", tc.stamp, err)
			}
			if !tc.wantValid && !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("stamp %d = %v, want ErrInvalidMessage", tc.stamp, err)
			}
		})
	}
}

// TestAProposersClockGoingBackwardsDoesNotStallIt covers the case that would
// otherwise take a node out of rotation: NTP steps its clock back, and it starts
// proposing blocks stamped before their own parent, which no honest validator
// accepts. The proposer would stall only its own turns, which is a slow and
// confusing failure.
func TestAProposersClockGoingBackwardsDoesNotStallIt(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	now := base
	eng := blockTimeEngine(t, func() time.Time { return now })

	// A committed parent stamped at base.
	parent := &Block{
		Height:        0,
		PrevBlockHash: make([]byte, HashSize),
		ProposerID:    eng.selfID,
		Timestamp:     base.Unix(),
	}
	if err := parent.Sign(eng.self.PrivateKey); err != nil {
		t.Fatalf("sign parent: %v", err)
	}
	if _, err := eng.chain.Commit(parent, nil); err != nil {
		t.Fatalf("commit parent: %v", err)
	}
	eng.mu.Lock()
	eng.height = 1
	eng.mu.Unlock()

	// The clock steps back five seconds.
	now = base.Add(-5 * time.Second)

	eng.mu.Lock()
	stamp := eng.proposalTimestampLocked()
	eng.mu.Unlock()
	if stamp <= parent.Timestamp {
		t.Fatalf("proposal stamp %d is not after the parent's %d; the proposer would stall itself",
			stamp, parent.Timestamp)
	}

	// And that stamp has to be one a validator accepts.
	eng.mu.Lock()
	err := eng.verifyBlockTimestampLocked(&Block{Height: 1, Timestamp: stamp})
	eng.mu.Unlock()
	if err != nil {
		t.Fatalf("the proposer's own stamp was rejected: %v", err)
	}
}

// TestTimestampMustMoveForward pins monotonicity. Without it a chain's times
// could zigzag, and anything ordering events by block time - a deposit history,
// an explorer - would show them out of order.
func TestTimestampMustMoveForward(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	eng := blockTimeEngine(t, func() time.Time { return base })

	parent := &Block{
		Height:        0,
		PrevBlockHash: make([]byte, HashSize),
		ProposerID:    eng.selfID,
		Timestamp:     base.Unix(),
	}
	if err := parent.Sign(eng.self.PrivateKey); err != nil {
		t.Fatalf("sign parent: %v", err)
	}
	if _, err := eng.chain.Commit(parent, nil); err != nil {
		t.Fatalf("commit parent: %v", err)
	}
	eng.mu.Lock()
	eng.height = 1
	eng.mu.Unlock()

	for _, tc := range []struct {
		name      string
		stamp     int64
		wantValid bool
	}{
		{"equal to the parent", base.Unix(), false},
		{"before the parent", base.Add(-time.Second).Unix(), false},
		{"after the parent", base.Add(time.Second).Unix(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eng.mu.Lock()
			err := eng.verifyBlockTimestampLocked(&Block{Height: 1, Timestamp: tc.stamp})
			eng.mu.Unlock()
			if tc.wantValid != (err == nil) {
				t.Fatalf("stamp %d: err = %v, wanted valid=%v", tc.stamp, err, tc.wantValid)
			}
		})
	}
}

// TestTheTimestampIsSigned proves the field is inside the block signature, so a
// relaying peer cannot restamp a block on its way past.
func TestTheTimestampIsSigned(t *testing.T) {
	eng := blockTimeEngine(t, time.Now)
	b := &Block{
		Height:        0,
		PrevBlockHash: make([]byte, HashSize),
		ProposerID:    eng.selfID,
		Timestamp:     time.Now().Unix(),
	}
	if err := b.Sign(eng.self.PrivateKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := b.VerifySignature(eng.self.PublicKey); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}

	b.Timestamp++
	if err := b.VerifySignature(eng.self.PublicKey); err == nil {
		t.Fatal("restamping a signed block must invalidate its signature")
	}
}
