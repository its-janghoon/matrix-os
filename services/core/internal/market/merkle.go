package market

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// A Merkle tree over every balance, so one balance can be PROVEN without
// sending the whole ledger.
//
// WHY A TREE AND NOT THE FOLD IT REPLACES. A flat hash over every balance answers
// "do two nodes agree", which is what stopped silent divergence. It cannot answer
// "prove this account holds this much", because checking it means recomputing it,
// which means holding every balance - so an outside party has no way to verify a
// single figure without being handed the entire ledger and trusting it is
// complete.
//
// That question is not academic here. Wrapped supply on another chain is supposed
// to be backed one-for-one by native coins in the escrow account, and the only
// evidence for that was the attestors' word. With a tree, the escrow balance comes
// with a proof anyone can check against a root the validators signed into a block.
// It is the difference between an audited reserve and an asserted one, and it is
// the question a listing review asks.
//
// LAYOUT. Leaves are the balances in the store's key order, which is sorted, so
// every node builds the same tree from the same state without imposing an order
// of its own. Leaf and internal hashes carry different domain prefixes: without
// them an internal node's bytes could be presented as a leaf, and a proof could
// be built for a "balance" that is really a pair of hashes.
//
// An odd node at any level is PROMOTED unchanged rather than hashed with a copy
// of itself. Duplicating is the classic mistake - it makes two different leaf
// sets produce one root, which is a forgeable proof - and promotion has no such
// twin.

const (
	// merkleLeafPrefix and merkleNodePrefix keep a leaf hash and an internal hash
	// in different domains, so no internal node can be passed off as a leaf.
	merkleLeafPrefix byte = 0x00
	merkleNodePrefix byte = 0x01
)

// merkleEmptyDomain is the root of a ledger with no balances at all. It is a
// distinct constant rather than a zero hash so "empty" is a statement rather than
// an absence.
var merkleEmptyDomain = []byte("matrix/ledger/merkle/v1/empty")

// ErrProofNotFound is returned when an account has no balance to prove.
var ErrProofNotFound = errors.New("market: account has no balance in the ledger")

// AccountProof is the evidence that one account holds one balance under a root.
type AccountProof struct {
	// Account and Balance are what is being proven.
	Account string `json:"account"`
	Balance uint64 `json:"balance"`
	// Root is the tree root this proof is against. It is the same value a block
	// carries as its state root, so a verifier can check the proof against
	// something the validators signed rather than against something this node
	// said.
	Root []byte `json:"root"`
	// Siblings are the hashes needed to walk from the leaf to the root, lowest
	// level first.
	Siblings [][]byte `json:"siblings"`
	// Left says, for each sibling, whether it sits on the left. Without it a
	// verifier would have to guess the order and half the proofs would verify to
	// the wrong root.
	Left []bool `json:"left"`
}

// MerkleLeaf hashes one account's balance. It is exported because a verifier
// that holds only the proof has to compute the same leaf.
func MerkleLeaf(account string, balance uint64) []byte {
	h := sha256.New()
	_, _ = h.Write([]byte{merkleLeafPrefix})
	var scratch [8]byte
	binary.BigEndian.PutUint64(scratch[:], uint64(len(account)))
	_, _ = h.Write(scratch[:])
	_, _ = h.Write([]byte(account))
	binary.BigEndian.PutUint64(scratch[:], balance)
	_, _ = h.Write(scratch[:])
	return h.Sum(nil)
}

// merkleNode hashes two children.
func merkleNode(left, right []byte) []byte {
	h := sha256.New()
	_, _ = h.Write([]byte{merkleNodePrefix})
	_, _ = h.Write(left)
	_, _ = h.Write(right)
	return h.Sum(nil)
}

// merkleRootOf folds a level of hashes up to a single root, promoting an odd
// node rather than duplicating it.
func merkleRootOf(level [][]byte) []byte {
	if len(level) == 0 {
		// sha256.Sum256(domain), NOT h.Sum(domain): Sum APPENDS the digest of what
		// was written to its argument, so hashing nothing and slicing the prefix
		// back off threw the domain away and returned sha256("") - a value with no
		// domain separation at all, which is the one thing the constant exists to
		// provide.
		sum := sha256.Sum256(merkleEmptyDomain)
		return sum[:]
	}
	for len(level) > 1 {
		next := make([][]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			if i+1 == len(level) {
				// Promoted, not paired with itself: duplicating makes two different
				// leaf sets produce one root, which is a proof anyone can forge.
				next = append(next, level[i])
				continue
			}
			next = append(next, merkleNode(level[i], level[i+1]))
		}
		level = next
	}
	return level[0]
}

// leaves reads every balance as a leaf hash, in store order, and reports where
// each account sits.
func (l *Ledger) leaves() ([][]byte, map[string]int, []uint64, error) {
	var (
		hashes   [][]byte
		balances []uint64
	)
	index := make(map[string]int)
	err := l.ForEachBalance(func(account string, balance uint64) error {
		index[account] = len(hashes)
		hashes = append(hashes, MerkleLeaf(account, balance))
		balances = append(balances, balance)
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return hashes, index, balances, nil
}

// StateRoot returns the Merkle root over every balance.
//
// It is what a block commits to, so two nodes that have applied the same blocks
// and reached different balances produce different roots and one of them refuses
// to vote - and it is also what an AccountProof verifies against, so a single
// balance can be checked by someone holding nothing else.
func (l *Ledger) StateRoot() ([]byte, error) {
	hashes, _, _, err := l.leaves()
	if err != nil {
		return nil, fmt.Errorf("market: compute the ledger state root: %w", err)
	}
	return merkleRootOf(hashes), nil
}

// ProveAccount builds the proof that an account holds its current balance.
func (l *Ledger) ProveAccount(account string) (*AccountProof, error) {
	hashes, index, balances, err := l.leaves()
	if err != nil {
		return nil, fmt.Errorf("market: build the account proof: %w", err)
	}
	pos, ok := index[account]
	if !ok {
		// An account the ledger has never touched has no leaf, so there is nothing
		// to prove. It is distinct from a zero balance that was written, which does
		// have a leaf: "never seen" and "seen and empty" are different facts and a
		// proof must not blur them.
		return nil, fmt.Errorf("%w: %q", ErrProofNotFound, account)
	}

	proof := &AccountProof{Account: account, Balance: balances[pos]}
	level := hashes
	for len(level) > 1 {
		next := make([][]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			if i+1 == len(level) {
				if i == pos {
					// Promoted: this level contributes no sibling, and the position
					// carries up unchanged.
					pos = len(next)
				}
				next = append(next, level[i])
				continue
			}
			if i == pos {
				proof.Siblings = append(proof.Siblings, level[i+1])
				proof.Left = append(proof.Left, false)
				pos = len(next)
			} else if i+1 == pos {
				proof.Siblings = append(proof.Siblings, level[i])
				proof.Left = append(proof.Left, true)
				pos = len(next)
			}
			next = append(next, merkleNode(level[i], level[i+1]))
		}
		level = next
	}
	proof.Root = level[0]
	return proof, nil
}

// VerifyAccountProof checks a proof against a root, recomputing the leaf from
// the account and balance it claims.
//
// It is a free function taking only the proof and the root, because that is what
// an outside verifier holds: it recomputes the leaf itself rather than trusting
// the one in the proof, so a proof that names a balance the leaf does not encode
// cannot verify.
func VerifyAccountProof(proof *AccountProof, root []byte) error {
	if proof == nil {
		return fmt.Errorf("market: no proof supplied")
	}
	if len(proof.Siblings) != len(proof.Left) {
		return fmt.Errorf("market: proof has %d siblings and %d positions",
			len(proof.Siblings), len(proof.Left))
	}
	running := MerkleLeaf(proof.Account, proof.Balance)
	for i, sibling := range proof.Siblings {
		if proof.Left[i] {
			running = merkleNode(sibling, running)
		} else {
			running = merkleNode(running, sibling)
		}
	}
	if !bytes.Equal(running, root) {
		return fmt.Errorf("market: proof for %q does not lead to the root", proof.Account)
	}
	return nil
}
