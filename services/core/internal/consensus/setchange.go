package consensus

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Validator-set change encoding and application.
//
// The set used to be a list in every node's config, immutable for the life of
// the process. Changing it meant editing every config and restarting every node
// at the same time, and a validator caught equivocating could not be removed at
// all without that coordination. This file makes the set a function of the
// COMMITTED CHAIN instead, which is the only way every node can agree on it: a
// change is a transaction, so it takes a quorum of precommits to land, and it
// takes effect for everyone at the same height.
//
// A change is encoded in a transaction's recipient, following the reserved
// account convention this codebase already uses for native/reward-pool and
// bridge/escrow:
//
//	consensus/validator/add/<64 hex chars>      the ed25519 public key to admit
//	consensus/validator/remove/<64 hex chars>   the account id to eject
//
// The amount is ignored and no credits move. What authorises the change is not
// the transaction's signature but the quorum that committed the block carrying
// it - and, separately, each operator's own approval list, which decides whether
// their node will vote for such a block at all (see Config.ApprovedSetChanges).
// A node that votes against a change still applies it if the network commits it:
// a vote is a veto attempt, the committed chain is the fact.
const (
	setChangePrefix = "consensus/validator/"
	addPrefix       = setChangePrefix + "add/"
	removePrefix    = setChangePrefix + "remove/"

	// pendingChangesKey holds changes seen in committed blocks that have not yet
	// reached an epoch boundary.
	pendingChangesKey = "consensus/validators/pending"
	// activeSetKey holds the set currently in force, so a restart resumes with
	// the set the chain says it should have rather than the one in config.
	activeSetKey = "consensus/validators/active"

	// MinValidators is the floor a set change may not cross. One validator is a
	// working (if pointless) network; zero is an unrecoverable chain, since there
	// would be nobody left to admit anyone.
	MinValidators = 1
	// MaxValidators bounds the set so a flood of admissions cannot make every
	// round unworkably large.
	MaxValidators = 128

	// DefaultEpochLength is how many committed blocks pass between set changes
	// taking effect. Batching them into an epoch means the set is stable for a
	// run of heights, which keeps the leader schedule predictable and gives a
	// lagging node a fixed point to catch up to.
	DefaultEpochLength = 100
)

// SetChangeKind is what a change does.
type SetChangeKind string

const (
	SetChangeAdd    SetChangeKind = "add"
	SetChangeRemove SetChangeKind = "remove"
)

// SetChange is one admission or ejection.
type SetChange struct {
	Kind SetChangeKind `json:"kind"`
	// ValidatorID is the account id affected.
	ValidatorID string `json:"validator_id"`
	// PublicKey is the key to admit. Empty for a removal, where the id is enough.
	PublicKey ed25519.PublicKey `json:"public_key,omitempty"`
	// Height is the committed height whose block carried it, for the record.
	Height uint64 `json:"height"`
}

// String renders the change the way an operator writes it in
// consensus.approved_changes, which is also how it reads in a log.
func (c SetChange) String() string {
	if c.Kind == SetChangeAdd {
		return "add:" + hex.EncodeToString(c.PublicKey)
	}
	return "remove:" + c.ValidatorID
}

// Recipient renders the reserved account id that encodes this change, which is
// what a submitter puts in a transaction's To field.
func (c SetChange) Recipient() string {
	if c.Kind == SetChangeAdd {
		return addPrefix + hex.EncodeToString(c.PublicKey)
	}
	return removePrefix + c.ValidatorID
}

// AddValidatorRecipient returns the transaction recipient that admits a key.
func AddValidatorRecipient(pub ed25519.PublicKey) string {
	return addPrefix + hex.EncodeToString(pub)
}

// RemoveValidatorRecipient returns the transaction recipient that ejects an id.
func RemoveValidatorRecipient(validatorID string) string {
	return removePrefix + validatorID
}

// IsSetChangeRecipient reports whether a recipient encodes a set change. It is
// used at apply time to tell a set change from an ordinary transfer.
func IsSetChangeRecipient(to string) bool {
	return strings.HasPrefix(to, setChangePrefix)
}

// ParseSetChange decodes a change from a transaction recipient. It returns an
// error for anything malformed, so a transfer to a lookalike account cannot be
// mistaken for a change.
func ParseSetChange(to string, height uint64) (SetChange, error) {
	switch {
	case strings.HasPrefix(to, addPrefix):
		raw := strings.TrimPrefix(to, addPrefix)
		key, err := hex.DecodeString(raw)
		if err != nil {
			return SetChange{}, fmt.Errorf("%w: add target is not hex: %v", ErrInvalidMessage, err)
		}
		if len(key) != ed25519.PublicKeySize {
			return SetChange{}, fmt.Errorf("%w: add target must be a %d-byte public key, got %d",
				ErrInvalidMessage, ed25519.PublicKeySize, len(key))
		}
		return SetChange{
			Kind:        SetChangeAdd,
			ValidatorID: token.AccountIDFromPublicKey(key),
			PublicKey:   key,
			Height:      height,
		}, nil

	case strings.HasPrefix(to, removePrefix):
		id := strings.TrimPrefix(to, removePrefix)
		raw, err := hex.DecodeString(id)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return SetChange{}, fmt.Errorf("%w: remove target must be a 64-hex-character account id",
				ErrInvalidMessage)
		}
		return SetChange{Kind: SetChangeRemove, ValidatorID: id, Height: height}, nil

	default:
		return SetChange{}, fmt.Errorf("%w: %q is not a validator set change", ErrInvalidMessage, to)
	}
}

// WithChanges returns a new set with the changes applied, or an error when the
// result would be unusable. The receiver is not modified: a ValidatorSet stays
// immutable, so a set already in use by a height cannot change under it.
//
// Order is fixed: removals first, then admissions. Without a rule, a block
// containing both "remove X" and "add X" would depend on iteration order, and
// two nodes could derive different sets from the same committed block - which is
// a fork.
func (vs *ValidatorSet) WithChanges(changes []SetChange) (*ValidatorSet, error) {
	keys := make(map[string]ed25519.PublicKey, len(vs.keys)+len(changes))
	for id, k := range vs.keys {
		keys[id] = k
	}

	for _, c := range changes {
		if c.Kind == SetChangeRemove {
			delete(keys, c.ValidatorID)
		}
	}
	for _, c := range changes {
		if c.Kind != SetChangeAdd {
			continue
		}
		if len(c.PublicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: add change has a %d-byte key", ErrInvalidMessage, len(c.PublicKey))
		}
		id := token.AccountIDFromPublicKey(c.PublicKey)
		kc := make(ed25519.PublicKey, len(c.PublicKey))
		copy(kc, c.PublicKey)
		keys[id] = kc
	}

	if len(keys) < MinValidators {
		return nil, fmt.Errorf("%w: the change would leave %d validators, below the minimum of %d",
			ErrInvalidMessage, len(keys), MinValidators)
	}
	if len(keys) > MaxValidators {
		return nil, fmt.Errorf("%w: the change would leave %d validators, above the maximum of %d",
			ErrInvalidMessage, len(keys), MaxValidators)
	}

	ids := make([]string, 0, len(keys))
	for id := range keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return &ValidatorSet{ids: ids, keys: keys}, nil
}

// PublicKeys returns the set's keys in the set's own deterministic order. It is
// what persists a set and what rebuilds one.
func (vs *ValidatorSet) PublicKeys() []ed25519.PublicKey {
	out := make([]ed25519.PublicKey, 0, len(vs.ids))
	for _, id := range vs.ids {
		k := vs.keys[id]
		kc := make(ed25519.PublicKey, len(k))
		copy(kc, k)
		out = append(out, kc)
	}
	return out
}

// SetStore persists the active validator set and the changes waiting for an
// epoch boundary.
//
// It exists so a restart resumes the set the CHAIN arrived at, not the one the
// config was written with. Without it, a node that had admitted a validator
// would forget on reboot and start rejecting that validator's votes.
type SetStore struct {
	store *kv.Store
}

// NewSetStore wraps a kv store.
func NewSetStore(store *kv.Store) *SetStore {
	return &SetStore{store: store}
}

type persistedSet struct {
	// Keys are hex-encoded ed25519 public keys.
	Keys []string `json:"keys"`
	// Height is the height the set took effect at, for diagnosis.
	Height uint64 `json:"height"`
}

// SaveActive records the set in force from a height.
func (s *SetStore) SaveActive(vs *ValidatorSet, height uint64) error {
	if s == nil || s.store == nil {
		return nil
	}
	rec := persistedSet{Height: height}
	for _, k := range vs.PublicKeys() {
		rec.Keys = append(rec.Keys, hex.EncodeToString(k))
	}
	body, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("consensus: encode active set: %w", err)
	}
	if err := s.store.Put([]byte(activeSetKey), body); err != nil {
		return fmt.Errorf("consensus: write active set: %w", err)
	}
	return nil
}

// LoadActive returns the persisted set, or nil when none has been saved (a
// first start, where config supplies the genesis set).
func (s *SetStore) LoadActive() (*ValidatorSet, uint64, error) {
	if s == nil || s.store == nil {
		return nil, 0, nil
	}
	body, err := s.store.Get([]byte(activeSetKey))
	if err != nil {
		return nil, 0, fmt.Errorf("consensus: read active set: %w", err)
	}
	if body == nil {
		return nil, 0, nil
	}
	var rec persistedSet
	if err := json.Unmarshal(body, &rec); err != nil {
		return nil, 0, fmt.Errorf("consensus: decode active set: %w", err)
	}
	keys := make([]ed25519.PublicKey, 0, len(rec.Keys))
	for _, h := range rec.Keys {
		raw, err := hex.DecodeString(h)
		if err != nil {
			return nil, 0, fmt.Errorf("consensus: active set holds a non-hex key: %w", err)
		}
		keys = append(keys, raw)
	}
	vs, err := NewValidatorSet(keys)
	if err != nil {
		return nil, 0, fmt.Errorf("consensus: rebuild active set: %w", err)
	}
	return vs, rec.Height, nil
}

// SavePending records changes seen in committed blocks that have not yet taken
// effect.
func (s *SetStore) SavePending(changes []SetChange) error {
	if s == nil || s.store == nil {
		return nil
	}
	body, err := json.Marshal(changes)
	if err != nil {
		return fmt.Errorf("consensus: encode pending set changes: %w", err)
	}
	if err := s.store.Put([]byte(pendingChangesKey), body); err != nil {
		return fmt.Errorf("consensus: write pending set changes: %w", err)
	}
	return nil
}

// LoadPending returns the changes awaiting an epoch boundary.
func (s *SetStore) LoadPending() ([]SetChange, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	body, err := s.store.Get([]byte(pendingChangesKey))
	if err != nil {
		return nil, fmt.Errorf("consensus: read pending set changes: %w", err)
	}
	if body == nil {
		return nil, nil
	}
	var out []SetChange
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("consensus: decode pending set changes: %w", err)
	}
	return out, nil
}

// ParseChangeSpec decodes the form an operator writes in
// consensus.approved_changes: "add:<64-hex-character public key>" or
// "remove:<64-hex-character account id>". It is the same text SetChange.String
// renders, so what a node prints can be pasted straight into a config, and a
// typo is an error at startup rather than an approval that silently matches
// nothing.
//
// The returned change carries no height; a spec is a statement about membership,
// not about a block.
func ParseChangeSpec(spec string) (SetChange, error) {
	trimmed := strings.ToLower(strings.TrimSpace(spec))
	kind, target, ok := strings.Cut(trimmed, ":")
	if !ok {
		return SetChange{}, fmt.Errorf("%w: %q is not a set change spec, want add:<hex public key> or remove:<account id>",
			ErrInvalidMessage, spec)
	}
	switch SetChangeKind(kind) {
	case SetChangeAdd:
		return ParseSetChange(addPrefix+target, 0)
	case SetChangeRemove:
		return ParseSetChange(removePrefix+target, 0)
	default:
		return SetChange{}, fmt.Errorf("%w: %q is not a set change kind, want add or remove", ErrInvalidMessage, kind)
	}
}

// InForce reports whether a set change has already taken effect against vs:
// an admission whose key is already a member, or an ejection whose id is
// already gone. Re-proposing such a change would commit a block that changes
// nothing, so a node skips it.
func (c SetChange) InForce(vs *ValidatorSet) bool {
	if c.Kind == SetChangeAdd {
		return vs.Contains(c.ValidatorID)
	}
	return !vs.Contains(c.ValidatorID)
}
