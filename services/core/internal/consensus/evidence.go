package consensus

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/ecirlabs/matrix-core/internal/kv"
)

// evidenceKeyPrefix stores equivocation evidence, keyed by height then voter so
// the records for a height sort together.
const evidenceKeyPrefix = "consensus/evidence/"

// Equivocation is proof that one validator voted for two different things in
// the same phase of the same round.
//
// It is the one Byzantine act this protocol can prove rather than infer. An
// honest validator prevotes once and precommits once per round; two signed votes
// from the same key, at the same height, round and phase, for different blocks,
// cannot both come from an honest node. Nobody has to be trusted to report it -
// the two signatures ARE the report, and any node can check them.
//
// Until this existed, a validator could double-sign and leave no trace: the
// engine simply kept whichever vote arrived first per block hash and counted on.
// That is a bad property for a network whose security is supposed to be worth
// something, because an attack that leaves no evidence cannot be answered.
type Equivocation struct {
	// Height, Round and Type are the position both votes claim.
	Height uint64   `json:"height"`
	Round  uint64   `json:"round"`
	Type   VoteType `json:"type"`
	// VoterID is the offending validator.
	VoterID string `json:"voter_id"`
	// A and B are the two conflicting votes, in a stable order (by block hash)
	// so the same equivocation always produces the same record.
	A Vote `json:"a"`
	B Vote `json:"b"`
}

// NewEquivocation orders two conflicting votes into a record. It does not
// validate them; use Verify.
func NewEquivocation(a, b Vote) Equivocation {
	if fmt.Sprintf("%x", b.BlockHash) < fmt.Sprintf("%x", a.BlockHash) {
		a, b = b, a
	}
	return Equivocation{Height: a.Height, Round: a.Round, Type: a.Type, VoterID: a.VoterID, A: a, B: b}
}

// Verify confirms the record really is proof of equivocation by a member of vs:
// both votes verify cryptographically, both are from the same validator at the
// same height, round and phase, and they endorse DIFFERENT blocks.
//
// Everything here is checkable from the record alone. A node acts on evidence it
// received from a stranger only because of that.
func (eq *Equivocation) Verify(vs *ValidatorSet) error {
	if eq == nil {
		return fmt.Errorf("%w: nil evidence", ErrInvalidMessage)
	}
	if eq.A.VoterID != eq.B.VoterID {
		return fmt.Errorf("%w: evidence names two different voters", ErrInvalidMessage)
	}
	if eq.VoterID != eq.A.VoterID {
		return fmt.Errorf("%w: evidence voter does not match its votes", ErrInvalidMessage)
	}
	if !vs.Contains(eq.VoterID) {
		return fmt.Errorf("%w: %s is not a validator", ErrNotValidator, eq.VoterID)
	}
	if eq.A.Height != eq.B.Height || eq.A.Round != eq.B.Round || eq.A.Type != eq.B.Type {
		return fmt.Errorf("%w: the two votes are not for the same height, round and phase", ErrInvalidMessage)
	}
	if eq.Height != eq.A.Height || eq.Round != eq.A.Round || eq.Type != eq.A.Type {
		return fmt.Errorf("%w: evidence header does not match its votes", ErrInvalidMessage)
	}
	if bytesEqual(eq.A.BlockHash, eq.B.BlockHash) {
		// The same vote twice is gossip doing its job, not misbehaviour.
		return fmt.Errorf("%w: both votes endorse the same block, which is not equivocation", ErrInvalidMessage)
	}
	if err := eq.A.Verify(); err != nil {
		return fmt.Errorf("%w: first vote: %v", ErrInvalidSignature, err)
	}
	if err := eq.B.Verify(); err != nil {
		return fmt.Errorf("%w: second vote: %v", ErrInvalidSignature, err)
	}
	return nil
}

// Key is the stable identity of this equivocation: one per validator per
// (height, round, phase). It keeps a node from storing or re-gossiping the same
// offence forever.
func (eq *Equivocation) Key() string {
	return fmt.Sprintf("%d/%d/%d/%s", eq.Height, eq.Round, uint8(eq.Type), eq.VoterID)
}

// EvidenceStore persists equivocation records.
//
// Evidence is kept forever, deliberately: it is the record of who broke the
// protocol, and a network that forgets that has no basis for removing anyone.
type EvidenceStore struct {
	store *kv.Store
	mu    sync.Mutex
}

// NewEvidenceStore wraps a kv store. Its keys are disjoint from the block and
// ledger namespaces.
func NewEvidenceStore(store *kv.Store) *EvidenceStore {
	return &EvidenceStore{store: store}
}

// evidenceKey renders the storage key: prefix + big-endian height + voter, so
// records for a height are contiguous and ordered.
func evidenceKey(eq *Equivocation) []byte {
	buf := make([]byte, 0, len(evidenceKeyPrefix)+8+len(eq.Key()))
	buf = append(buf, evidenceKeyPrefix...)
	var h [8]byte
	binary.BigEndian.PutUint64(h[:], eq.Height)
	buf = append(buf, h[:]...)
	return append(buf, eq.Key()...)
}

// Record stores evidence, returning whether it was new. Storing an offence
// already on record is a no-op success.
func (s *EvidenceStore) Record(eq *Equivocation) (bool, error) {
	if s == nil || s.store == nil {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := evidenceKey(eq)
	existing, err := s.store.Get(key)
	if err != nil {
		return false, fmt.Errorf("consensus: read evidence: %w", err)
	}
	if existing != nil {
		return false, nil
	}
	body, err := json.Marshal(eq)
	if err != nil {
		return false, fmt.Errorf("consensus: encode evidence: %w", err)
	}
	if err := s.store.Put(key, body); err != nil {
		return false, fmt.Errorf("consensus: write evidence: %w", err)
	}
	return true, nil
}

// All returns every stored record, ordered by height then voter.
func (s *EvidenceStore) All() ([]Equivocation, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	type record struct {
		key  string
		body []byte
	}
	var records []record
	if err := s.store.Iterate([]byte(evidenceKeyPrefix), func(key, value []byte) error {
		records = append(records, record{key: string(key), body: append([]byte(nil), value...)})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("consensus: scan evidence: %w", err)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].key < records[j].key })

	out := make([]Equivocation, 0, len(records))
	for _, r := range records {
		var eq Equivocation
		if err := json.Unmarshal(r.body, &eq); err != nil {
			return nil, fmt.Errorf("consensus: decode evidence %q: %w", r.key, err)
		}
		out = append(out, eq)
	}
	return out, nil
}

// ByValidator returns the records against one validator.
func (s *EvidenceStore) ByValidator(voterID string) ([]Equivocation, error) {
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	out := make([]Equivocation, 0, 2)
	for _, eq := range all {
		if eq.VoterID == voterID {
			out = append(out, eq)
		}
	}
	return out, nil
}
