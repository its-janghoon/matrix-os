package consensus

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"sort"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// ErrEmptyValidatorSet is returned when a ValidatorSet is constructed with no
// members.
var ErrEmptyValidatorSet = errors.New("consensus: validator set must not be empty")

// ValidatorSet is the FIXED set of validator identities that participate in
// consensus. Membership and ordering are immutable for the life of the set:
// the ordering (by account ID) is what makes the round-robin leader schedule
// deterministic and identical on every node, so all nodes agree on who the
// leader is for any given round without extra coordination.
type ValidatorSet struct {
	// ids is the deterministic, sorted list of validator account IDs. Index in
	// this slice is the validator's position in the round-robin schedule.
	ids []string
	// keys maps account ID -> public key for O(1) membership checks and signature
	// verification.
	keys map[string]ed25519.PublicKey
}

// NewValidatorSet builds a fixed validator set from a list of ed25519 public
// keys. Duplicate keys are collapsed. The resulting order is deterministic
// (sorted by account ID) regardless of input order, so every node that is given
// the same set of keys derives the identical leader schedule. It returns
// ErrEmptyValidatorSet when no valid keys are supplied.
func NewValidatorSet(keys []ed25519.PublicKey) (*ValidatorSet, error) {
	m := make(map[string]ed25519.PublicKey)
	for _, k := range keys {
		if len(k) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: public key must be %d bytes", ErrInvalidMessage, ed25519.PublicKeySize)
		}
		id := token.AccountIDFromPublicKey(k)
		if _, ok := m[id]; ok {
			continue
		}
		kc := make(ed25519.PublicKey, len(k))
		copy(kc, k)
		m[id] = kc
	}
	if len(m) == 0 {
		return nil, ErrEmptyValidatorSet
	}
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return &ValidatorSet{ids: ids, keys: m}, nil
}

// Len returns the number of validators in the set.
func (vs *ValidatorSet) Len() int { return len(vs.ids) }

// IDs returns a copy of the sorted validator account IDs.
func (vs *ValidatorSet) IDs() []string {
	out := make([]string, len(vs.ids))
	copy(out, vs.ids)
	return out
}

// Contains reports whether id is a validator.
func (vs *ValidatorSet) Contains(id string) bool {
	_, ok := vs.keys[id]
	return ok
}

// PublicKey returns the public key for validator id, and whether it exists.
func (vs *ValidatorSet) PublicKey(id string) (ed25519.PublicKey, bool) {
	k, ok := vs.keys[id]
	return k, ok
}

// LeaderForRound returns the account ID of the round-robin leader for round r.
// The leader is ids[r mod N], so leadership rotates through the sorted set and
// every node computes the same leader for a given round.
func (vs *ValidatorSet) LeaderForRound(round uint64) string {
	n := uint64(len(vs.ids))
	return vs.ids[round%n]
}

// IsLeader reports whether id is the leader for round r.
func (vs *ValidatorSet) IsLeader(id string, round uint64) bool {
	return vs.LeaderForRound(round) == id
}

// Quorum returns the number of votes required to commit: a strict Byzantine
// quorum of more than 2/3 of the set, i.e. floor(2N/3)+1. For N=3f+1 this is
// 2f+1, the classic BFT quorum that guarantees any two quorums intersect in at
// least one honest validator, so two conflicting blocks can never both commit at
// the same height.
func (vs *ValidatorSet) Quorum() int {
	n := len(vs.ids)
	return (2*n)/3 + 1
}
