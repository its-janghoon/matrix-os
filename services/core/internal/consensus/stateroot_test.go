package consensus

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestTheCachedDigestFollowsLedgerWritesNotBlockHeights is the bug this cache
// had and the reason it is keyed the way it is. The ledger is written from more
// than the block-apply path, and a digest cached per height was computed before
// such a write and compared after one - so two nodes disagreed forever about a
// state they actually shared.
func TestTheCachedDigestFollowsLedgerWritesNotBlockHeights(t *testing.T) {
	eng := blockTimeEngine(t, time.Now)

	eng.mu.Lock()
	first := append([]byte(nil), eng.stateRootLocked()...)
	eng.mu.Unlock()

	// A write that is not a block commit, which is exactly what seeding a genesis
	// balance is.
	if err := eng.ledger.Credit("somebody", 1); err != nil {
		t.Fatalf("credit: %v", err)
	}

	eng.mu.Lock()
	second := append([]byte(nil), eng.stateRootLocked()...)
	eng.mu.Unlock()

	if bytes.Equal(first, second) {
		t.Fatal("the cached digest survived a ledger write, so it describes a state that no longer exists")
	}

	// And with no write in between it is stable, or every block would carry a
	// different root for the same state.
	eng.mu.Lock()
	third := append([]byte(nil), eng.stateRootLocked()...)
	eng.mu.Unlock()
	if !bytes.Equal(second, third) {
		t.Fatal("the digest changed with no ledger write between the two reads")
	}
}

// TestABlockIsRefusedWhenItsLedgerDisagrees is the whole point: a proposer whose
// balances differ from this node's cannot get a vote out of it. Before this
// check the two produced identical block hashes and different balances, and
// nothing anywhere reported it.
func TestABlockIsRefusedWhenItsLedgerDisagrees(t *testing.T) {
	eng := blockTimeEngine(t, time.Now)

	eng.mu.Lock()
	mine := append([]byte(nil), eng.stateRootLocked()...)
	eng.mu.Unlock()

	good := &Block{Height: 0, Version: ProtocolVersionGenesis, StateRoot: mine}
	eng.mu.Lock()
	err := eng.verifyBlockStateRootLocked(good)
	eng.mu.Unlock()
	if err != nil {
		t.Fatalf("a matching state root was refused: %v", err)
	}

	for _, tc := range []struct {
		name string
		root []byte
	}{
		{"a different ledger", bytes.Repeat([]byte{0xaa}, 32)},
		{"no state root at all", nil},
		// A block is a message from the network and the length of this field is
		// whatever its sender put there. The mismatch branch used to render it by
		// slicing a fixed 8 bytes, so a peer could crash every honest node that
		// checked its block by proposing a short root - and that branch runs
		// precisely when a block is already suspect. These must be refused, not
		// panicked on.
		{"a one-byte root", []byte{0x01}},
		{"a seven-byte root", bytes.Repeat([]byte{0xaa}, 7)},
		{"a root longer than a digest", bytes.Repeat([]byte{0xaa}, 129)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eng.mu.Lock()
			err := eng.verifyBlockStateRootLocked(&Block{Height: 0, StateRoot: tc.root})
			eng.mu.Unlock()
			if !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("err = %v, want ErrInvalidMessage", err)
			}
		})
	}
}

// TestTheProtocolVersionScheduleActivatesByHeight covers the mechanism that
// makes the NEXT rule change a release rather than another coordinated restart:
// every node switches at the same height because the height is agreed, not the
// moment.
func TestTheProtocolVersionScheduleActivatesByHeight(t *testing.T) {
	eng := blockTimeEngine(t, time.Now)
	upgrades, err := normalizeUpgrades([]ProtocolUpgrade{{Height: 100, Version: 2}, {Height: 50, Version: 2}})
	if err != nil {
		t.Fatalf("normalizeUpgrades: %v", err)
	}
	eng.mu.Lock()
	eng.protocolUpgrades = upgrades
	eng.mu.Unlock()

	for _, tc := range []struct {
		height uint64
		want   uint32
	}{
		{0, ProtocolVersionGenesis},
		{49, ProtocolVersionGenesis},
		{50, 2},
		{100, 2},
		{10_000, 2},
	} {
		if got := eng.protocolVersionAt(tc.height); got != tc.want {
			t.Fatalf("version at height %d = %d, want %d", tc.height, got, tc.want)
		}
	}

	// A block built under the wrong rules is refused in BOTH directions: too low
	// is a node that has not upgraded, too high is one that upgraded early. Either
	// way the two are not running the same rules, and a vote would claim they are.
	eng.mu.Lock()
	defer eng.mu.Unlock()
	if err := eng.verifyBlockVersionLocked(&Block{Height: 60, Version: 2}); err != nil {
		t.Fatalf("the scheduled version was refused: %v", err)
	}
	if err := eng.verifyBlockVersionLocked(&Block{Height: 60, Version: 1}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("an un-upgraded proposer's block = %v, want ErrInvalidMessage", err)
	}
	if err := eng.verifyBlockVersionLocked(&Block{Height: 10, Version: 2}); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("an early-upgraded proposer's block = %v, want ErrInvalidMessage", err)
	}
}

// TestAnUnusableUpgradeScheduleIsRefusedAtStartup catches the two schedules a
// node cannot act on, at construction rather than at the height they would
// break.
func TestAnUnusableUpgradeScheduleIsRefusedAtStartup(t *testing.T) {
	if _, err := normalizeUpgrades([]ProtocolUpgrade{{Height: 10, Version: 3}, {Height: 10, Version: 4}}); err == nil {
		t.Fatal("two versions activating at one height should be refused")
	}
	if _, err := normalizeUpgrades([]ProtocolUpgrade{{Height: 10, Version: 3}, {Height: 20, Version: 2}}); err == nil {
		t.Fatal("a schedule that goes backwards should be refused")
	}
	// An empty schedule is the ordinary case and means the genesis version
	// forever, which is right until a change is actually planned.
	got, err := normalizeUpgrades(nil)
	if err != nil {
		t.Fatalf("an empty schedule: %v", err)
	}
	if len(got) != 1 || got[0].Version != ProtocolVersionGenesis || got[0].Height != 0 {
		t.Fatalf("an empty schedule normalized to %v", got)
	}
}

// TestTheStateRootIsSigned proves a relaying peer cannot swap the root on a
// block's way past, which would turn the check into something an attacker
// controls.
func TestTheStateRootIsSigned(t *testing.T) {
	eng := blockTimeEngine(t, time.Now)
	b := &Block{
		Height:        0,
		PrevBlockHash: make([]byte, HashSize),
		ProposerID:    eng.selfID,
		Timestamp:     time.Now().Unix(),
		Version:       ProtocolVersionGenesis,
		StateRoot:     bytes.Repeat([]byte{0x01}, 32),
	}
	if err := b.Sign(eng.self.PrivateKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := b.VerifySignature(eng.self.PublicKey); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}

	b.StateRoot = bytes.Repeat([]byte{0x02}, 32)
	if err := b.VerifySignature(eng.self.PublicKey); err == nil {
		t.Fatal("swapping the state root must invalidate the block signature")
	}
	b.StateRoot = bytes.Repeat([]byte{0x01}, 32)
	b.Version++
	if err := b.VerifySignature(eng.self.PublicKey); err == nil {
		t.Fatal("changing the protocol version must invalidate the block signature")
	}
}

// TestADifferentGenesisIsCaughtAtTheFirstBlock is why there is no separate
// genesis hash field. Two nodes seeded differently reach different roots
// immediately, so the check that catches a rules disagreement at block N also
// catches a config disagreement at block 0 - which is the earliest anything
// could.
//
// It matters because nothing else compared genesis at all. ApplyGenesis looks
// only at a local marker, so two nodes with different allocations would commit
// together happily and hold permanently different balances.
func TestADifferentGenesisIsCaughtAtTheFirstBlock(t *testing.T) {
	seeded := blockTimeEngine(t, time.Now)
	unseeded := blockTimeEngine(t, time.Now)

	// One operator's genesis allocates something the other's does not.
	if err := seeded.ledger.Credit("a-founder", 50_000_000); err != nil {
		t.Fatalf("Credit: %v", err)
	}

	seeded.mu.Lock()
	theirs := append([]byte(nil), seeded.stateRootLocked()...)
	seeded.mu.Unlock()

	// The node with the other genesis refuses the very first block, and says so.
	unseeded.mu.Lock()
	err := unseeded.verifyBlockStateRootLocked(&Block{Height: 0, StateRoot: theirs})
	unseeded.mu.Unlock()
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("a block from a node with a different genesis = %v, want ErrInvalidMessage", err)
	}
	if !strings.Contains(err.Error(), "different rules") && !strings.Contains(err.Error(), "different balances") {
		t.Fatalf("the refusal did not explain what to compare: %v", err)
	}
}
