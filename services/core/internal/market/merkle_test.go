package market

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
)

func merkleLedger(t *testing.T, balances map[string]uint64) *Ledger {
	t.Helper()
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	l := NewLedger(store)
	for account, amount := range balances {
		if err := l.Credit(account, amount); err != nil {
			t.Fatalf("Credit(%s): %v", account, err)
		}
	}
	return l
}

// TestEveryBalanceCanBeProven is the capability the flat hash did not have: a
// single figure checked by someone who holds nothing else. It is what turns the
// claim that wrapped supply is backed by native escrow from an assertion into
// something an outside party can verify.
func TestEveryBalanceCanBeProven(t *testing.T) {
	balances := map[string]uint64{
		"bridge/escrow": 50_000_000_000_000_000,
		"alice":         100,
		"bob":           250,
		"carol":         0,
		"dave":          7,
	}
	l := merkleLedger(t, balances)

	root, err := l.StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}

	for account, want := range balances {
		proof, err := l.ProveAccount(account)
		if err != nil {
			t.Fatalf("ProveAccount(%s): %v", account, err)
		}
		if proof.Balance != want {
			t.Fatalf("proof for %s says %d, want %d", account, proof.Balance, want)
		}
		if !bytes.Equal(proof.Root, root) {
			t.Fatalf("proof for %s is against a different root than the ledger's", account)
		}
		if err := VerifyAccountProof(proof, root); err != nil {
			t.Fatalf("VerifyAccountProof(%s): %v", account, err)
		}
	}
}

// TestAProofCannotClaimABalanceItDoesNotHave is why the verifier recomputes the
// leaf rather than trusting the one in the proof. Otherwise anyone holding a
// valid proof could restate the number in it.
func TestAProofCannotClaimABalanceItDoesNotHave(t *testing.T) {
	l := merkleLedger(t, map[string]uint64{"alice": 100, "bob": 250, "carol": 7})
	root, err := l.StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}
	proof, err := l.ProveAccount("alice")
	if err != nil {
		t.Fatalf("ProveAccount: %v", err)
	}

	inflated := *proof
	inflated.Balance = 1_000_000
	if err := VerifyAccountProof(&inflated, root); err == nil {
		t.Fatal("a proof restating the balance verified")
	}

	renamed := *proof
	renamed.Account = "bob"
	if err := VerifyAccountProof(&renamed, root); err == nil {
		t.Fatal("a proof renaming the account verified")
	}

	flipped := *proof
	flipped.Left = append([]bool(nil), proof.Left...)
	if len(flipped.Left) > 0 {
		flipped.Left[0] = !flipped.Left[0]
		if err := VerifyAccountProof(&flipped, root); err == nil {
			t.Fatal("a proof with a sibling on the wrong side verified")
		}
	}
}

// TestAnOddLeafIsPromotedNotDuplicated guards the classic Merkle mistake.
// Hashing a lone node with a copy of itself makes two different leaf sets
// produce one root, which is a proof anyone can forge.
func TestAnOddLeafIsPromotedNotDuplicated(t *testing.T) {
	three := merkleRootOf([][]byte{
		MerkleLeaf("a", 1), MerkleLeaf("b", 2), MerkleLeaf("c", 3),
	})
	// The set a duplicating implementation would collide with: the odd leaf
	// written out twice.
	four := merkleRootOf([][]byte{
		MerkleLeaf("a", 1), MerkleLeaf("b", 2), MerkleLeaf("c", 3), MerkleLeaf("c", 3),
	})
	if bytes.Equal(three, four) {
		t.Fatal("three leaves and the same three with the last repeated produced one root")
	}
}

// TestALeafCannotBePassedOffAsAnInternalNode covers the other structural forgery
// the domain prefixes prevent: presenting a pair of hashes as an account's leaf.
func TestALeafCannotBePassedOffAsAnInternalNode(t *testing.T) {
	left, right := MerkleLeaf("a", 1), MerkleLeaf("b", 2)
	node := merkleNode(left, right)

	// Build an "account" whose leaf encoding is the concatenation an internal node
	// hashes. If the two domains were not separated, this would hash to the same
	// value and a proof could be built for a balance nobody holds.
	var forged []byte
	forged = append(forged, left...)
	forged = append(forged, right...)
	if bytes.Equal(MerkleLeaf(string(forged), 0), node) {
		t.Fatal("a leaf hashed to the same value as an internal node")
	}
}

// TestTheRootFollowsTheLedger is the agreement property the root has to keep now
// that it is also a proof target: any difference in any balance changes it.
func TestTheRootFollowsTheLedger(t *testing.T) {
	l := merkleLedger(t, map[string]uint64{"alice": 100, "bob": 250})
	before, err := l.StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}

	if err := l.Credit("alice", 1); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	after, err := l.StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}
	if bytes.Equal(before, after) {
		t.Fatal("one base unit changed nothing in the root")
	}

	// And two ledgers holding the same balances agree however they got there,
	// because the leaves are read in the store's order rather than in the order
	// they were written.
	other := merkleLedger(t, map[string]uint64{})
	for _, step := range []struct {
		account string
		amount  uint64
	}{{"bob", 200}, {"alice", 101}, {"bob", 50}} {
		if err := other.Credit(step.account, step.amount); err != nil {
			t.Fatalf("Credit: %v", err)
		}
	}
	otherRoot, err := other.StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}
	if !bytes.Equal(after, otherRoot) {
		t.Fatal("two ledgers holding the same balances produced different roots")
	}
}

// TestAnUntouchedAccountHasNothingToProve keeps two different facts apart. An
// account the ledger has never seen has no leaf; one written and spent down to
// zero does. Blurring them would let a proof assert a zero balance for an
// account that was never in the ledger at all.
func TestAnUntouchedAccountHasNothingToProve(t *testing.T) {
	l := merkleLedger(t, map[string]uint64{"alice": 5})
	if _, err := l.ProveAccount("nobody"); !errors.Is(err, ErrProofNotFound) {
		t.Fatalf("ProveAccount(nobody) = %v, want ErrProofNotFound", err)
	}

	if err := l.Credit("spent", 10); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if err := l.Debit("spent", 10); err != nil {
		t.Fatalf("Debit: %v", err)
	}
	proof, err := l.ProveAccount("spent")
	if err != nil {
		t.Fatalf("an account written down to zero should still be provable: %v", err)
	}
	if proof.Balance != 0 {
		t.Fatalf("balance = %d, want 0", proof.Balance)
	}
	root, err := l.StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}
	if err := VerifyAccountProof(proof, root); err != nil {
		t.Fatalf("VerifyAccountProof: %v", err)
	}
}

// TestProofsHoldAtEverySize walks the shapes the tree takes, because the
// promotion rule only fires at some of them and an off-by-one in the position
// arithmetic shows up only for particular counts.
func TestProofsHoldAtEverySize(t *testing.T) {
	for n := 1; n <= 33; n++ {
		balances := make(map[string]uint64, n)
		for i := 0; i < n; i++ {
			balances[fmt.Sprintf("account-%03d", i)] = uint64(i) * 7
		}
		l := merkleLedger(t, balances)
		root, err := l.StateRoot()
		if err != nil {
			t.Fatalf("n=%d StateRoot: %v", n, err)
		}
		for account := range balances {
			proof, err := l.ProveAccount(account)
			if err != nil {
				t.Fatalf("n=%d ProveAccount(%s): %v", n, account, err)
			}
			if err := VerifyAccountProof(proof, root); err != nil {
				t.Fatalf("n=%d proof for %s did not verify: %v", n, account, err)
			}
		}
	}
}

// TestAnEmptyLedgerHasAStableRoot covers the state a chain starts in, before
// genesis has credited anything. It must be a definite value rather than a zero
// hash, so "empty" is a statement two nodes can agree on.
func TestAnEmptyLedgerHasAStableRoot(t *testing.T) {
	a := merkleLedger(t, nil)
	b := merkleLedger(t, nil)
	ra, err := a.StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}
	rb, err := b.StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}
	if !bytes.Equal(ra, rb) {
		t.Fatal("two empty ledgers disagreed")
	}
	if len(ra) != 32 {
		t.Fatalf("the empty root is %d bytes, want 32", len(ra))
	}
	var zero [32]byte
	if bytes.Equal(ra, zero[:]) {
		t.Fatal("the empty root is all zeroes, which is indistinguishable from an unset field")
	}
	// And it differs from a ledger holding a single zero balance, which is a
	// different fact.
	c := merkleLedger(t, map[string]uint64{"someone": 0})
	rc, err := c.StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}
	if bytes.Equal(ra, rc) {
		t.Fatal("an empty ledger and one holding a zero balance produced the same root")
	}
}
