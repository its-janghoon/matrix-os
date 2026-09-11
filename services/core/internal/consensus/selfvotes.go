package consensus

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/ecirlabs/matrix-core/internal/kv"
)

// selfVoteKeyPrefix stores this node's own signed votes for the height it is
// currently deciding, keyed so that one height's records sort together.
const selfVoteKeyPrefix = "consensus/selfvote/"

// SelfVoteStore persists the votes this node has cast at the height it has not
// committed yet.
//
// It exists because a vote is a signature, and a signature does not stop
// existing when the process does. The engine holds prevotes, precommits and the
// lock in memory only, so a validator restarted mid-height came back with no
// record of having voted, cast a second vote for a different block at the same
// height and round, and produced objective equivocation evidence against
// itself. Nothing about that is Byzantine - it is the same honest operator
// restarting a service - but the protocol cannot tell the difference, and
// should not be asked to: two conflicting signatures under one key are exactly
// what slashing is for. A production launch lost a validator's whole bond this
// way, twice.
//
// So the record is written before the vote is published. What is on disk is
// therefore a superset of what the network has seen, which is the safe
// direction: a vote persisted but never sent costs one round, while a vote sent
// but never persisted costs the bond.
//
// Only this node's own votes are kept, and only for the uncommitted height.
// Peers' votes need no such treatment: they are re-gossiped, and a node that
// forgets them has merely to be told again. It is this node's own history that
// cannot be reconstructed from the network without risking a second signature.
type SelfVoteStore struct {
	store *kv.Store
	mu    sync.Mutex
}

// NewSelfVoteStore wraps a kv store. Its keys are disjoint from the block,
// ledger and evidence namespaces.
func NewSelfVoteStore(store *kv.Store) *SelfVoteStore {
	return &SelfVoteStore{store: store}
}

// selfVoteKey renders the storage key: prefix + big-endian height + big-endian
// round + phase. One vote per height, round and phase is the whole point, so
// the key is exactly that tuple and a re-record of the same position overwrites
// rather than accumulates.
func selfVoteKey(height, round uint64, typ VoteType) []byte {
	buf := make([]byte, 0, len(selfVoteKeyPrefix)+17)
	buf = append(buf, selfVoteKeyPrefix...)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], height)
	buf = append(buf, n[:]...)
	binary.BigEndian.PutUint64(n[:], round)
	buf = append(buf, n[:]...)
	return append(buf, uint8(typ))
}

// selfVoteHeightPrefix is the key range holding one height's records.
func selfVoteHeightPrefix(height uint64) []byte {
	buf := make([]byte, 0, len(selfVoteKeyPrefix)+8)
	buf = append(buf, selfVoteKeyPrefix...)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], height)
	return append(buf, n[:]...)
}

// Record persists a vote this node has signed. It must be called, and must
// succeed, before the vote is published.
func (s *SelfVoteStore) Record(v *Vote) error {
	if s == nil || s.store == nil || v == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("consensus: encode own vote: %w", err)
	}
	if err := s.store.Put(selfVoteKey(v.Height, v.Round, v.Type), body); err != nil {
		return fmt.Errorf("consensus: write own vote: %w", err)
	}
	return nil
}

// AtHeight returns every vote this node recorded at height, in round then phase
// order.
func (s *SelfVoteStore) AtHeight(height uint64) ([]Vote, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []Vote
	if err := s.store.Iterate(selfVoteHeightPrefix(height), func(key, value []byte) error {
		var v Vote
		if err := json.Unmarshal(value, &v); err != nil {
			return fmt.Errorf("consensus: decode own vote %q: %w", key, err)
		}
		out = append(out, v)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// PruneBelow drops the records for heights this node has already committed.
//
// A committed height is settled: the chain itself is now the record of what
// this node agreed to, and no further vote will be cast there. Keeping the
// votes would grow without bound for no benefit.
func (s *SelfVoteStore) PruneBelow(height uint64) error {
	if s == nil || s.store == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var stale [][]byte
	if err := s.store.Iterate([]byte(selfVoteKeyPrefix), func(key, _ []byte) error {
		at := len(selfVoteKeyPrefix)
		if len(key) < at+8 {
			return nil
		}
		if binary.BigEndian.Uint64(key[at:at+8]) < height {
			stale = append(stale, append([]byte(nil), key...))
		}
		return nil
	}); err != nil {
		return err
	}
	for _, key := range stale {
		if err := s.store.Delete(key); err != nil {
			return fmt.Errorf("consensus: prune own votes: %w", err)
		}
	}
	return nil
}
