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

// ValidatorSet is an immutable snapshot of who validates and with how much
// weight. Membership, ordering and power are fixed for the life of the value:
// the ordering (by account ID) is what makes the leader schedule deterministic
// and identical on every node, and the power is what a quorum is measured in.
//
// A new set is produced at each epoch boundary rather than mutated, so a set
// already deciding a height cannot change underneath it.
type ValidatorSet struct {
	// ids is the deterministic, sorted list of validator account IDs. Index in
	// this slice is the validator's position in the round-robin schedule.
	ids []string
	// keys maps account ID -> public key for O(1) membership checks and signature
	// verification.
	keys map[string]ed25519.PublicKey
	// power maps account ID -> voting power, which is the validator's bonded
	// stake in native base units. A set built without stake gives every member
	// power 1, which makes every quorum below identical to a headcount.
	power map[string]uint64
	// total is the sum of power, precomputed because every quorum check needs it.
	total uint64
}

// NewValidatorSet builds a validator set from a list of ed25519 public keys,
// giving every member power 1. Duplicate keys are collapsed. The resulting
// order is deterministic (sorted by account ID) regardless of input order, so
// every node given the same keys derives the identical leader schedule. It
// returns ErrEmptyValidatorSet when no valid keys are supplied.
//
// Equal power is the unstaked case: total power equals the headcount and every
// quorum is the classic floor(2N/3)+1. It is what a permissioned network of
// operators who know each other runs on. Use WithPower for a set whose weight
// comes from bonded stake.
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
	power := make(map[string]uint64, len(m))
	for id := range m {
		ids = append(ids, id)
		power[id] = 1
	}
	sort.Strings(ids)
	return &ValidatorSet{ids: ids, keys: m, power: power, total: uint64(len(ids))}, nil
}

// WithPower returns a copy of the set whose voting power is taken from stake,
// which maps account id -> bonded native base units.
//
// A member missing from stake, or bonded zero, keeps power 1 rather than
// dropping to zero. Zero-power members would be dead weight in every quorum
// while still taking their turn as leader, so a set that cannot be weighted is
// better left unweighted than left half-weighted. Whether an unbonded validator
// belongs in the set at all is decided when it is admitted, not here.
func (vs *ValidatorSet) WithPower(stake map[string]uint64) (*ValidatorSet, error) {
	power := make(map[string]uint64, len(vs.ids))
	var total uint64
	for _, id := range vs.ids {
		p := stake[id]
		if p == 0 {
			p = 1
		}
		if total > maxTotalPower-p {
			return nil, fmt.Errorf("%w: total voting power would overflow", ErrInvalidMessage)
		}
		power[id] = p
		total += p
	}
	keys := make(map[string]ed25519.PublicKey, len(vs.keys))
	for id, k := range vs.keys {
		keys[id] = k
	}
	return &ValidatorSet{
		ids:   append([]string(nil), vs.ids...),
		keys:  keys,
		power: power,
		total: total,
	}, nil
}

// maxTotalPower bounds the sum of voting power so QuorumPower's arithmetic
// cannot overflow. Native MATRIX is capped at 1e18 base units, so a real total
// is far below this; the bound exists so a malformed stake map is an error
// rather than a wraparound that would make some tiny vote set look like a
// quorum.
const maxTotalPower = ^uint64(0) / 4

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

// LeaderFor returns the account ID of the round-robin leader for a given
// (height, round). The leader is ids[(height+round) mod N], so leadership
// rotates through the sorted set and every node computes the same leader,
// because both height and round are values the whole network agrees on.
//
// HEIGHT is in there, and it has to be. Leadership used to depend on the round
// alone, and the round resets to zero on every commit - so on a healthy chain
// round 0 came round again at every height and ids[0] proposed every block for
// the life of the network. Nothing rotated, because rotation only happened on a
// TIMEOUT and a leader that keeps committing never times out. That gave one
// validator a permanent veto over what got into a block: it could drop any
// transaction indefinitely and never lose its turn. It also meant a transaction
// submitted to any other node was never proposed at all, since a node can only
// propose from its own mempool.
//
// Including the height makes the turn pass on every commit, which is what
// "round-robin leader" was always supposed to mean.
func (vs *ValidatorSet) LeaderFor(height, round uint64) string {
	n := uint64(len(vs.ids))
	// height+round can overflow only after 2^64 blocks; the wrap would still be
	// deterministic and identical on every node, so it is not a correctness
	// concern.
	return vs.ids[(height+round)%n]
}

// IsLeader reports whether id is the leader for a given (height, round).
func (vs *ValidatorSet) IsLeader(id string, height, round uint64) bool {
	return vs.LeaderFor(height, round) == id
}

// Power returns the voting power of id, and zero for a non-member.
func (vs *ValidatorSet) Power(id string) uint64 { return vs.power[id] }

// TotalPower returns the sum of every member's voting power.
func (vs *ValidatorSet) TotalPower() uint64 { return vs.total }

// QuorumPower returns the voting power required to commit: strictly more than
// two thirds of the total, i.e. floor(2T/3)+1.
//
// It is measured in POWER, not in heads. With equal power that is exactly the
// classic floor(2N/3)+1 and nothing about the protocol changes. With power from
// bonded stake it is what makes the set safe to open: a headcount quorum can be
// bought for the price of N minimum bonds under N identities, because identities
// are free and only the bond is not. Weighting the quorum by stake prices an
// attack at two thirds of everything bonded, whatever number of identities it is
// spread across.
//
// The intersection argument is unchanged, just denominated differently: any two
// sets holding more than 2T/3 power each must share more than T/3 power, so they
// cannot endorse conflicting blocks unless validators holding more than a third
// of the stake equivocate - and that is exactly the fault threshold the protocol
// assumes and the offence it slashes for.
func (vs *ValidatorSet) QuorumPower() uint64 {
	return (2*vs.total)/3 + 1
}

// PowerOfVoters sums the voting power of the given voter ids, ignoring any that
// are not members. It is how every quorum check is evaluated.
func (vs *ValidatorSet) PowerOfVoters(voters map[string]Vote) uint64 {
	var sum uint64
	for id := range voters {
		sum += vs.power[id]
	}
	return sum
}

// PowerOfSet sums the voting power of the given voter ids.
func (vs *ValidatorSet) PowerOfSet(voters map[string]struct{}) uint64 {
	var sum uint64
	for id := range voters {
		sum += vs.power[id]
	}
	return sum
}
