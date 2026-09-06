package token

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"github.com/cockroachdb/pebble"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
)

// This file defines the honest issuance rules for native MATRIX: a one-time
// genesis allocation, a persisted cumulative-issued-supply counter enforcing the
// NativeMaxSupply cap, and a genesis-funded reward pool that is the ONLY
// sanctioned source of provider/buyer balances beyond genesis. It replaces
// ad-hoc, unbounded market.Ledger.Credit minting with a supply-tracked path so
// no native MATRIX is created out of thin air past the cap.

// KV keys for native issuance state. These are new keys under the token/native
// namespace; they do not touch the existing market/balance/<account> layout, so
// this is additive and requires no data migration.
const (
	// genesisMarkerKey persists whether ApplyGenesis has already run. Its presence
	// makes genesis idempotent across restarts so balances cannot be double-credited.
	genesisMarkerKey = "token/native/genesis_applied"
	// issuedSupplyKey persists the cumulative native base units issued so far
	// (genesis allocations plus any later issuance). It is compared against
	// NativeMaxSupply to enforce the hard cap.
	issuedSupplyKey = "token/native/issued_supply"
)

// genesisMarkerValue is the sentinel byte written under genesisMarkerKey.
var genesisMarkerValue = []byte{1}

// ErrSupplyCapExceeded is returned when a genesis allocation or issuance would
// push the cumulative issued native supply above NativeMaxSupply. It is an
// errors.Is-matchable sentinel so callers can distinguish a cap violation from
// other failures.
var ErrSupplyCapExceeded = errors.New("token: native MATRIX supply cap exceeded")

// ErrGenesisAlreadyApplied is returned by ApplyGenesis when a genesis allocation
// has already been persisted for this store. It lets callers detect (and, if
// they choose, ignore) a redundant genesis call. ApplyGenesis itself treats an
// already-applied genesis as a no-op success unless the caller uses the strict
// variant.
var ErrGenesisAlreadyApplied = errors.New("token: genesis already applied")

// RewardPoolAccount is the reserved account ID that holds the genesis-allocated
// reward pool. Provider and buyer balances are seeded by moving native MATRIX
// out of this pool (see Treasury.FundFromRewardPool), which conserves total
// supply instead of minting new coins. It is a fixed, well-known, non-hex
// identifier so it can never collide with a real ed25519-derived account ID
// (those are 64 lowercase hex chars).
const RewardPoolAccount = "native/reward-pool"

// GenesisAllocation is a single initial balance assigned at genesis: amount
// native base units credited to Account.
type GenesisAllocation struct {
	Account string
	Amount  uint64
}

// Treasury manages native MATRIX issuance over an existing market.Ledger. It
// owns the persisted genesis marker and the cumulative-issued-supply counter and
// enforces the NativeMaxSupply cap. The reward pool it funds at genesis is the
// sanctioned source of provider/buyer balances.
//
// A Treasury is safe for concurrent use: all issuance mutations run inside
// market.Ledger.Atomically, which holds the ledger write lock across the
// supply-counter read, the cap check, the persist, and the balance change, so no
// concurrent ledger writer can interleave and break the invariant
// "issued_supply == sum of credited balances <= NativeMaxSupply".
type Treasury struct {
	ledger *market.Ledger
	store  *kv.Store
}

// NewTreasury builds a Treasury over the given ledger and the kv.Store that
// backs it (the same store the ledger and chain use). The store is needed
// directly for the genesis marker and issued-supply counter, which live outside
// the balance keyspace.
func NewTreasury(ledger *market.Ledger, store *kv.Store) *Treasury {
	return &Treasury{ledger: ledger, store: store}
}

// IssuedSupply returns the cumulative native base units issued so far (genesis
// plus later issuance). A fresh store reports zero.
func (t *Treasury) IssuedSupply() (uint64, error) {
	data, err := t.store.Get([]byte(issuedSupplyKey))
	if err != nil {
		return 0, fmt.Errorf("token: read issued supply: %w", err)
	}
	if data == nil {
		return 0, nil
	}
	if len(data) != 8 {
		return 0, fmt.Errorf("token: corrupt issued-supply counter: expected 8 bytes, got %d", len(data))
	}
	return binary.BigEndian.Uint64(data), nil
}

// GenesisApplied reports whether a genesis allocation has already been persisted
// for this store.
func (t *Treasury) GenesisApplied() (bool, error) {
	data, err := t.store.Get([]byte(genesisMarkerKey))
	if err != nil {
		return false, fmt.Errorf("token: read genesis marker: %w", err)
	}
	return data != nil, nil
}

// encodeUint64 encodes v as an 8-byte big-endian value, matching the ledger's
// balance encoding.
func encodeUint64(v uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, v)
	return buf
}

// ApplyGenesis credits the given initial allocations exactly once and records a
// persisted marker so a restart cannot double-credit. The allocations plus the
// reward pool are counted toward issued supply and must not exceed
// NativeMaxSupply (otherwise ErrSupplyCapExceeded is returned and NOTHING is
// written). rewardPool is the amount, in native base units, allocated to the
// reserved RewardPoolAccount; pass zero to allocate no reward pool.
//
// ApplyGenesis is idempotent: if genesis has already been applied it is a no-op
// and returns nil (not an error), so callers can invoke it unconditionally at
// startup. The marker check, the cap check, every credit, and the supply-counter
// write all happen inside a single ledger critical section, so a concurrent
// writer cannot interleave and no partial genesis can be observed.
func (t *Treasury) ApplyGenesis(allocations []GenesisAllocation, rewardPool uint64) error {
	return t.ledger.Atomically(func(ltx market.LedgerTx) error {
		applied, err := t.GenesisApplied()
		if err != nil {
			return err
		}
		if applied {
			// Already seeded: idempotent no-op so restart cannot double-credit.
			return nil
		}

		// Sum the requested issuance (allocations + reward pool) with overflow
		// protection, then enforce the cap before writing anything.
		total := uint64(0)
		add := func(x uint64) error {
			if total > NativeMaxSupply-x {
				return fmt.Errorf("token: genesis allocations sum to more than NativeMaxSupply (%d): %w", NativeMaxSupply, ErrSupplyCapExceeded)
			}
			total += x
			return nil
		}
		for _, a := range allocations {
			if err := add(a.Amount); err != nil {
				return err
			}
		}
		if err := add(rewardPool); err != nil {
			return err
		}

		// Genesis starts from zero issued supply by definition; total is already
		// checked <= NativeMaxSupply above.

		// Build a single atomic kv batch that credits every allocation, the reward
		// pool, the issued-supply counter, and the genesis marker. Either the whole
		// genesis lands or none of it does. Balances are credited by reading the
		// (zero, on a fresh store) balance and setting balance+amount, matching the
		// ledger's own encoding under the market/balance/<account> prefix.
		batch := t.store.NewBatch()
		defer batch.Close()

		// Deterministic order for reproducibility across nodes/restarts.
		sorted := make([]GenesisAllocation, len(allocations))
		copy(sorted, allocations)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Account < sorted[j].Account })

		credit := func(account string, amount uint64) error {
			if amount == 0 {
				return nil
			}
			cur, err := ltx.Balance(account)
			if err != nil {
				return err
			}
			// cur is 0 on a fresh store; guard against overflow regardless.
			if cur > NativeMaxSupply-amount {
				return fmt.Errorf("token: genesis credit to %q overflows: %w", account, ErrSupplyCapExceeded)
			}
			if err := batch.Set(market.BalanceKey(account), encodeUint64(cur+amount), nil); err != nil {
				return fmt.Errorf("token: stage genesis credit for %q: %w", account, err)
			}
			return nil
		}

		for _, a := range sorted {
			if a.Account == "" {
				return fmt.Errorf("token: genesis allocation has empty account: %w", ErrInvalidAccountID)
			}
			if err := credit(a.Account, a.Amount); err != nil {
				return err
			}
		}
		if err := credit(RewardPoolAccount, rewardPool); err != nil {
			return err
		}

		if err := batch.Set([]byte(issuedSupplyKey), encodeUint64(total), nil); err != nil {
			return fmt.Errorf("token: stage issued-supply counter: %w", err)
		}
		if err := batch.Set([]byte(genesisMarkerKey), genesisMarkerValue, nil); err != nil {
			return fmt.Errorf("token: stage genesis marker: %w", err)
		}
		if err := batch.Commit(pebble.Sync); err != nil {
			return fmt.Errorf("token: commit genesis: %w", err)
		}
		return nil
	})
}

// FundFromRewardPool moves amount native base units from the reserved reward
// pool to recipient. This is the sanctioned way to seed a provider or buyer
// balance: it CONSERVES total supply (no new coins are minted and issued supply
// is unchanged) because the coins already exist in the genesis-allocated pool.
// It returns market.ErrInsufficientFunds (matchable via errors.Is) if the pool
// balance is below amount, leaving all balances unchanged.
//
// The transfer runs inside the ledger critical section so a concurrent writer
// cannot drain the pool between the balance check and the move.
func (t *Treasury) FundFromRewardPool(recipient string, amount uint64) error {
	if recipient == "" {
		return fmt.Errorf("token: reward-pool recipient must not be empty: %w", ErrInvalidAccountID)
	}
	if recipient == RewardPoolAccount {
		return fmt.Errorf("token: cannot fund the reward pool from itself: %w", ErrInvalidAccountID)
	}
	return t.ledger.Atomically(func(ltx market.LedgerTx) error {
		if err := ltx.Transfer(RewardPoolAccount, recipient, amount); err != nil {
			return fmt.Errorf("token: fund %q from reward pool: %w", recipient, err)
		}
		return nil
	})
}

// Issue mints new native MATRIX to recipient, increasing cumulative issued
// supply. It enforces the NativeMaxSupply cap: if issued_supply + amount would
// exceed the cap it returns ErrSupplyCapExceeded and mints nothing. This is the
// only supply-increasing primitive besides genesis; provider earnings should
// normally come from FundFromRewardPool (which does NOT increase supply), and
// Issue exists for explicitly capped, supply-tracked issuance.
//
// The read-modify-write of the counter, the cap check, and the credit all run in
// one ledger critical section so concurrent issuance cannot race past the cap.
func (t *Treasury) Issue(recipient string, amount uint64) error {
	if recipient == "" {
		return fmt.Errorf("token: issuance recipient must not be empty: %w", ErrInvalidAccountID)
	}
	if amount == 0 {
		return nil
	}
	return t.ledger.Atomically(func(ltx market.LedgerTx) error {
		issued, err := t.IssuedSupply()
		if err != nil {
			return err
		}
		if issued > NativeMaxSupply-amount {
			return fmt.Errorf("token: issue %d would raise supply above cap %d (issued %d): %w",
				amount, NativeMaxSupply, issued, ErrSupplyCapExceeded)
		}

		cur, err := ltx.Balance(recipient)
		if err != nil {
			return err
		}

		batch := t.store.NewBatch()
		defer batch.Close()

		if err := batch.Set(market.BalanceKey(recipient), encodeUint64(cur+amount), nil); err != nil {
			return fmt.Errorf("token: stage issuance credit for %q: %w", recipient, err)
		}
		if err := batch.Set([]byte(issuedSupplyKey), encodeUint64(issued+amount), nil); err != nil {
			return fmt.Errorf("token: stage issued-supply counter: %w", err)
		}
		if err := batch.Commit(pebble.Sync); err != nil {
			return fmt.Errorf("token: commit issuance: %w", err)
		}
		return nil
	})
}
