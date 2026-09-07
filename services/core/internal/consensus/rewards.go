package consensus

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
)

// Provider rewards: paying out the genesis pool for supplying compute.
//
// The token policy allocates a share of the genesis pool to compute providers.
// Until now that was an allocation with no mechanism: the pool sat there and the
// only way out of it was an admin-authenticated `matrix fund`, which is a
// faucet, not a reward. This is the mechanism.
//
// THE HARD PART is that consensus cannot see work. A job lives in the
// marketplace, whose provider list and job records are per-node state that no
// quorum ever ordered, so the chain has no way to know that a provider served
// anything. All the chain can see is that native MATRIX moved from one account
// to another. That rules out the obvious design - pay a provider a percentage of
// what it was paid - because a percentage of a transfer is a MONEY PUMP: send
// coins to an account you also control, collect the percentage, send them back,
// repeat. The pool drains to whoever loops fastest, and nothing about it looks
// irregular.
//
// So the emission is a FIXED SCHEDULE, not a percentage. Each block pays out at
// most E(height) from the pool, whatever happens in it, and that amount is
// SHARED among the registered providers that were paid in the block, pro rata by
// how much they were paid. Faking settlements can move a share of E toward the
// faker; it cannot increase E. The pool therefore empties on schedule and not
// faster, which is what an emission schedule is for. Competition and the
// protocol fee do the rest: capturing a large share means out-transacting honest
// volume and paying the fee on every unit of it.
//
// WHO IS ELIGIBLE is a registry the chain keeps, changed the same way the
// validator set is: a transaction to a reserved recipient, valid only from a
// sitting validator, that each node votes for only if its own operator approved
// it. So a quorum of operators decides who earns from the pool. That is the same
// trust model already accepted for admitting a validator, and it is what keeps
// the emission pointed at parties who actually supply compute rather than at
// whoever happens to receive a transfer.
//
// Registration takes effect from the NEXT block, so a block cannot register an
// account and pay it in the same breath.

const (
	providerChangePrefix = "market/provider/"
	providerAddPrefix    = providerChangePrefix + "add/"
	providerRemovePrefix = providerChangePrefix + "remove/"

	// registeredProviderKeyPrefix + <id> marks a reward-eligible provider.
	registeredProviderKeyPrefix = "market/providers/registered/"

	// DefaultProviderEmissionHalfLife is how many committed blocks halve the
	// per-block emission when a half-life is not configured.
	//
	// The schedule is a right shift, so the emission reaches zero after 64
	// half-lives and the pool is never drained to the last unit by an
	// asymptote that never arrives.
	DefaultProviderEmissionHalfLife uint64 = 1_000_000
)

// ErrNotRegisteredProvider is returned when a provider change would remove an
// account that is not registered, or register one that already is.
var ErrNotRegisteredProvider = errors.New("consensus: provider registration would change nothing")

// ProviderChangeKind is what a provider change does.
type ProviderChangeKind string

const (
	ProviderChangeAdd    ProviderChangeKind = "add"
	ProviderChangeRemove ProviderChangeKind = "remove"
)

// ProviderChange is one registration or deregistration.
type ProviderChange struct {
	Kind ProviderChangeKind `json:"kind"`
	// ProviderID is the ACCOUNT the reward is paid to, not a marketplace
	// provider name. The reward is a credit, so it has to name something that
	// can hold a balance.
	ProviderID string `json:"provider_id"`
}

// String renders the change the way an operator writes it in
// market.approved_providers, which is also how it reads in a log.
func (c ProviderChange) String() string { return string(c.Kind) + ":" + c.ProviderID }

// Recipient renders the reserved recipient that encodes this change.
func (c ProviderChange) Recipient() string {
	if c.Kind == ProviderChangeAdd {
		return providerAddPrefix + c.ProviderID
	}
	return providerRemovePrefix + c.ProviderID
}

// IsProviderChangeRecipient reports whether a recipient encodes a provider
// registry change.
func IsProviderChangeRecipient(to string) bool {
	return strings.HasPrefix(to, providerChangePrefix)
}

// ParseProviderChange decodes a change from a transaction recipient.
func ParseProviderChange(to string) (ProviderChange, error) {
	switch {
	case strings.HasPrefix(to, providerAddPrefix):
		id := strings.TrimPrefix(to, providerAddPrefix)
		if err := checkAccountID(id); err != nil {
			return ProviderChange{}, fmt.Errorf("provider add target: %w", err)
		}
		return ProviderChange{Kind: ProviderChangeAdd, ProviderID: id}, nil
	case strings.HasPrefix(to, providerRemovePrefix):
		id := strings.TrimPrefix(to, providerRemovePrefix)
		if err := checkAccountID(id); err != nil {
			return ProviderChange{}, fmt.Errorf("provider remove target: %w", err)
		}
		return ProviderChange{Kind: ProviderChangeRemove, ProviderID: id}, nil
	default:
		return ProviderChange{}, fmt.Errorf("%w: %q is not a provider registry change", ErrInvalidMessage, to)
	}
}

// ParseProviderChangeSpec decodes the form an operator writes in
// market.approved_providers: "add:<account id>" or "remove:<account id>".
func ParseProviderChangeSpec(spec string) (ProviderChange, error) {
	trimmed := strings.ToLower(strings.TrimSpace(spec))
	kind, target, ok := strings.Cut(trimmed, ":")
	if !ok {
		return ProviderChange{}, fmt.Errorf("%w: %q is not a provider change spec, want add:<account id> or remove:<account id>",
			ErrInvalidMessage, spec)
	}
	switch ProviderChangeKind(kind) {
	case ProviderChangeAdd:
		return ParseProviderChange(providerAddPrefix + target)
	case ProviderChangeRemove:
		return ParseProviderChange(providerRemovePrefix + target)
	default:
		return ProviderChange{}, fmt.Errorf("%w: %q is not a provider change kind, want add or remove",
			ErrInvalidMessage, kind)
	}
}

// ProviderRegistry is the set of accounts the chain pays provider rewards to.
//
// It is committed state: every entry got there because a quorum of validators
// committed a block carrying the registration, so every node has the same
// registry and computes the same reward split.
type ProviderRegistry struct {
	store *kv.Store
}

// NewProviderRegistry wraps a kv store.
func NewProviderRegistry(store *kv.Store) *ProviderRegistry {
	return &ProviderRegistry{store: store}
}

func registeredProviderKey(id string) []byte { return []byte(registeredProviderKeyPrefix + id) }

// Apply records a provider change. It is idempotent.
func (r *ProviderRegistry) Apply(c ProviderChange) error {
	if r == nil || r.store == nil {
		return nil
	}
	if c.Kind == ProviderChangeAdd {
		if err := r.store.Put(registeredProviderKey(c.ProviderID), []byte{1}); err != nil {
			return fmt.Errorf("consensus: register provider %s: %w", c.ProviderID, err)
		}
		return nil
	}
	if err := r.store.Delete(registeredProviderKey(c.ProviderID)); err != nil {
		return fmt.Errorf("consensus: deregister provider %s: %w", c.ProviderID, err)
	}
	return nil
}

// IsRegistered reports whether an account earns provider rewards.
func (r *ProviderRegistry) IsRegistered(id string) (bool, error) {
	if r == nil || r.store == nil {
		return false, nil
	}
	body, err := r.store.Get(registeredProviderKey(id))
	if err != nil {
		return false, fmt.Errorf("consensus: read provider registration for %s: %w", id, err)
	}
	return body != nil, nil
}

// All returns every registered provider account, sorted.
func (r *ProviderRegistry) All() ([]string, error) {
	if r == nil || r.store == nil {
		return nil, nil
	}
	var out []string
	if err := r.store.Iterate([]byte(registeredProviderKeyPrefix), func(key, _ []byte) error {
		out = append(out, strings.TrimPrefix(string(key), registeredProviderKeyPrefix))
		return nil
	}); err != nil {
		return nil, fmt.Errorf("consensus: list registered providers: %w", err)
	}
	sort.Strings(out)
	return out, nil
}

// EmissionFor returns the per-block provider emission at a height: perBlock
// halved once per halfLife blocks, and zero once the shift exhausts it.
//
// A right shift rather than a multiply by a fraction, so the schedule is exact
// integer arithmetic that every node computes identically - a decay expressed as
// a floating-point rate would be a different number on a different platform, and
// a different number is a different balance.
func EmissionFor(height, perBlock, halfLife uint64) uint64 {
	if perBlock == 0 {
		return 0
	}
	if halfLife == 0 {
		halfLife = DefaultProviderEmissionHalfLife
	}
	halvings := height / halfLife
	if halvings >= 64 {
		return 0
	}
	return perBlock >> halvings
}

// distributeEmissionLocked pays the block's emission from the reward pool to the
// registered providers that were credited in it, pro rata by how much they were
// credited. It returns what was actually paid.
//
// Bounded three ways: by the schedule, by what the pool still holds, and by the
// providers being registered. The indivisible remainder simply stays in the
// pool - it is the source, so leftover is unspent rather than lost.
func distributeEmissionLocked(
	ltx market.LedgerTx,
	registry *ProviderRegistry,
	credited map[string]uint64,
	emission uint64,
) (uint64, error) {
	if emission == 0 || len(credited) == 0 || registry == nil {
		return 0, nil
	}

	// Deterministic order, and only registered providers count toward the
	// weights - otherwise the share of an unregistered recipient would silently
	// change everyone else's.
	ids := make([]string, 0, len(credited))
	for id := range credited {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	eligible := make([]string, 0, len(ids))
	var totalWeight uint64
	for _, id := range ids {
		registered, err := registry.IsRegistered(id)
		if err != nil {
			return 0, err
		}
		if !registered || credited[id] == 0 {
			continue
		}
		// Saturate rather than wrap: the weights are only ever used as a ratio,
		// so a saturated total still splits the emission sensibly, whereas a
		// wrapped one would hand almost all of it to one provider.
		if totalWeight > ^uint64(0)-credited[id] {
			totalWeight = ^uint64(0)
		} else {
			totalWeight += credited[id]
		}
		eligible = append(eligible, id)
	}
	if len(eligible) == 0 || totalWeight == 0 {
		return 0, nil
	}

	pool, err := ltx.Balance(rewardPoolAccount)
	if err != nil {
		return 0, fmt.Errorf("consensus: read the reward pool: %w", err)
	}
	budget := emission
	if budget > pool {
		// The pool is what there is. An empty pool ends the emission, which is
		// the schedule reaching its end rather than an error.
		budget = pool
	}
	if budget == 0 {
		return 0, nil
	}

	budgetBig := new(big.Int).SetUint64(budget)
	totalBig := new(big.Int).SetUint64(totalWeight)
	share := new(big.Int)

	var paid uint64
	for _, id := range eligible {
		share.SetUint64(credited[id])
		share.Mul(share, budgetBig)
		share.Div(share, totalBig)
		if share.Sign() == 0 || !share.IsUint64() {
			continue
		}
		amount := share.Uint64()
		if amount == 0 {
			continue
		}
		if err := ltx.Transfer(rewardPoolAccount, id, amount); err != nil {
			return paid, fmt.Errorf("consensus: pay provider reward to %s: %w", id, err)
		}
		paid += amount
	}
	return paid, nil
}
