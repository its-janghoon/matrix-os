package consensus

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Bonded stake: what a validator has at risk.
//
// Membership was previously agreement between operators and nothing else. A
// validator caught equivocating lost its place and nothing more, so the
// deterrent was reputational - fine among operators who know each other, and
// not fine for a set anyone may join. Worse, a quorum measured in HEADS can be
// bought for the price of N identities, and identities are free.
//
// A bond fixes both. Voting power is bonded stake, so a quorum costs two thirds
// of everything bonded however many identities it is spread across; and an
// offence that is provable is answered by taking the bond, so misbehaving has a
// price denominated in the same coin the network settles in.
//
// WHERE THE COINS LIVE. A bond is an ordinary balance in a reserved account,
// "consensus/stake/<account id>", and bonding is an ordinary transfer into it.
// That is deliberate: the market ledger already moves credits atomically with
// an affordability check, and reusing it means bonded coins leave the spendable
// balance, total supply is conserved by construction, and there is no second
// accounting system to keep consistent with the first. The reserved account has
// no key, so nothing can spend from it except the rules in this file.
//
// THE THREE OPERATIONS, each a transaction to a reserved recipient:
//
//	consensus/stake/bond/<account id>       lock the amount sent into the bond
//	consensus/stake/withdraw/<account id>   return the bond to its owner
//	consensus/stake/slash/<account id>      take the bond (see below)
//
// Bonding is open to anyone: it is putting your own coins somewhere you cannot
// spend them from, which needs no permission. Withdrawing is gated on being out
// of the validator set for UnbondingPeriod blocks, which is what stops a
// validator equivocating and pulling its bond out before the evidence lands.
// Slashing is not a transaction anyone submits; it rides in as a set change,
// because deciding that an offence happened is the network's decision and not
// one node's (see SetChangeSlash).

const (
	stakePrefix = "consensus/stake/"
	// bondPrefix + <id> is BOTH the recipient of a bonding transfer and the
	// account that holds the bond, so a bond is exactly a balance.
	bondPrefix     = stakePrefix + "bond/"
	withdrawPrefix = stakePrefix + "withdraw/"
	slashPrefix    = stakePrefix + "slash/"

	// leftSetKeyPrefix + <id> records the height at which an account stopped
	// being a validator, which is when its unbonding clock starts.
	leftSetKeyPrefix = "consensus/stake/left/"
	// bondedAtKeyPrefix + <id> records the height an account's CURRENT bond was
	// first posted, which is what the minimum residency below is measured from.
	bondedAtKeyPrefix = "consensus/stake/bonded-at/"

	// DefaultMinBond is the stake an account must have bonded before it may be
	// admitted to the validator set, in native base units. It is deliberately
	// not zero-by-default: a validator with nothing at risk is what stake
	// exists to stop.
	//
	// 1e15 base units is 1,000,000 whole MATRIX at 9 decimals, one thousandth of
	// the supply cap. A network that wants a different floor sets one; a network
	// that wants none sets zero and gets the unstaked behaviour back.
	DefaultMinBond uint64 = 1e15

	// DefaultUnbondingPeriod is how many blocks after leaving the validator set
	// an account must wait before it may withdraw its bond.
	//
	// The delay is the whole reason a bond deters anything. Without it a
	// validator equivocates, is ejected, and withdraws before the network has
	// committed the slash - and the bond it was supposed to lose is already
	// spent. The period has to be long enough for evidence to be gossiped,
	// voted on and committed, which is a few epochs.
	DefaultUnbondingPeriod uint64 = 1000
)

// ErrInsufficientBond is returned when an account is admitted to the validator
// set without the minimum bond.
var ErrInsufficientBond = errors.New("consensus: bonded stake below the minimum")

// ErrBondLocked is returned when a withdrawal is refused because the account is
// still a validator or still inside its unbonding period.
var ErrBondLocked = errors.New("consensus: bond is not withdrawable yet")

// BondAccount returns the reserved account that holds an account's bond. Its
// balance IS the bond.
func BondAccount(id string) string { return bondPrefix + id }

// WithdrawRecipient returns the transaction recipient that withdraws a bond.
func WithdrawRecipient(id string) string { return withdrawPrefix + id }

// SlashRecipient returns the reserved recipient naming a slashed account. It is
// not a transaction anyone submits; it is how a slash set change is encoded.
func SlashRecipient(id string) string { return slashPrefix + id }

// IsStakeRecipient reports whether a recipient is one of the reserved stake
// accounts, so the apply path can tell a stake operation from a transfer.
func IsStakeRecipient(to string) bool { return strings.HasPrefix(to, stakePrefix) }

// StakeOp is what a stake transaction does.
type StakeOp string

const (
	StakeOpBond     StakeOp = "bond"
	StakeOpWithdraw StakeOp = "withdraw"
)

// StakeRequest is a decoded stake transaction.
type StakeRequest struct {
	Op StakeOp
	// Account is the id whose bond the request concerns.
	Account string
}

// ParseStakeRecipient decodes a stake transaction's recipient. It rejects
// anything malformed, so a transfer to a lookalike account cannot be mistaken
// for a stake operation.
func ParseStakeRecipient(to string) (StakeRequest, error) {
	switch {
	case strings.HasPrefix(to, bondPrefix):
		id := strings.TrimPrefix(to, bondPrefix)
		if err := checkAccountID(id); err != nil {
			return StakeRequest{}, fmt.Errorf("bond target: %w", err)
		}
		return StakeRequest{Op: StakeOpBond, Account: id}, nil
	case strings.HasPrefix(to, withdrawPrefix):
		id := strings.TrimPrefix(to, withdrawPrefix)
		if err := checkAccountID(id); err != nil {
			return StakeRequest{}, fmt.Errorf("withdraw target: %w", err)
		}
		return StakeRequest{Op: StakeOpWithdraw, Account: id}, nil
	default:
		return StakeRequest{}, fmt.Errorf("%w: %q is not a stake operation", ErrInvalidMessage, to)
	}
}

// checkAccountID requires a 64-hex-character account id, the form every
// ed25519-derived id takes.
func checkAccountID(id string) error {
	// Both account kinds, because a reserved recipient names an ACCOUNT and there
	// are two kinds of those. Accepting only the ed25519 form meant a wallet-held
	// account could hold a balance and be paid, but could not bond, withdraw or
	// leave the set - its own id was refused as malformed by the parser for the
	// operation it was trying to perform.
	if token.IsEthAccountID(id) {
		if _, err := token.ParseEthAccountID(id); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidMessage, err)
		}
		// Lowercase because EthAccountID produces one form per address, and two
		// spellings of one account would be two bond accounts.
		if id != strings.ToLower(id) {
			return fmt.Errorf("%w: an ethereum account id must be lowercase", ErrInvalidMessage)
		}
		return nil
	}
	if len(id) != 64 {
		return fmt.Errorf("%w: must be a 64-hex-character account id or an eth:0x... address, got %d characters",
			ErrInvalidMessage, len(id))
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("%w: must be lowercase hex", ErrInvalidMessage)
		}
	}
	return nil
}

// StakeLedger reads and writes bonded stake over the market ledger that holds
// every other balance, plus the small amount of extra state a bond needs: when
// an account left the validator set.
//
// It holds no balances of its own. A bond is a balance in a reserved account,
// so "how much is bonded" is a ledger read and every movement is a ledger
// transfer - which is what makes bonding atomic and supply-conserving without
// any new accounting.
type StakeLedger struct {
	ledger *market.Ledger
	store  *kv.Store
}

// NewStakeLedger builds a StakeLedger over the ledger holding balances and the
// kv store backing it.
func NewStakeLedger(ledger *market.Ledger, store *kv.Store) *StakeLedger {
	return &StakeLedger{ledger: ledger, store: store}
}

// Bonded returns the stake bonded by an account.
func (s *StakeLedger) Bonded(id string) (uint64, error) {
	if s == nil || s.ledger == nil {
		return 0, nil
	}
	return s.ledger.Balance(BondAccount(id))
}

// BondedFor returns the stake bonded by each of the given accounts, which is
// the map a validator set is weighted by.
func (s *StakeLedger) BondedFor(ids []string) (map[string]uint64, error) {
	out := make(map[string]uint64, len(ids))
	if s == nil || s.ledger == nil {
		return out, nil
	}
	for _, id := range ids {
		bonded, err := s.ledger.Balance(BondAccount(id))
		if err != nil {
			return nil, fmt.Errorf("consensus: read bond of %s: %w", id, err)
		}
		out[id] = bonded
	}
	return out, nil
}

// leftKey is the kv key holding when an account left the validator set.
func leftKey(id string) []byte { return []byte(leftSetKeyPrefix + id) }

func bondedAtKey(id string) []byte { return []byte(bondedAtKeyPrefix + id) }

// A bond that can be pulled the instant it is posted is not a stake in anything.
//
// WHY THIS EXISTS. An account that was never a validator could withdraw
// immediately: the unbonding delay ran from the height it LEFT the validator
// set, and an account that was never in it has no such height, so nothing
// gated the withdrawal at all. That is right for what the delay was built for -
// covering the window in which a departed validator could still be slashed -
// and wrong the moment a bond means anything else.
//
// It matters because a bond is what makes a marketplace listing cost something.
// Without residency, a seller bonds to clear a buyer's floor, is listed, takes
// the business, and withdraws in the next block - the deposit was a formality
// that never had capital behind it for longer than it took to read. With it, a
// listing is capital committed for a period, which is the whole of what a bond
// can honestly claim to be.
//
// It is deliberately NOT provider-specific. "Money posted here stays posted for
// a while" is a property of a bond, and a rule that applied only to accounts
// someone had labelled a provider would be a rule an attacker opts out of by
// not carrying the label.

// RecordBonded notes the height an account's bond was first posted. Recording it
// again for an account that already has one is a no-op: topping up a bond must
// not restart the clock, or a seller could hold a withdrawal open indefinitely
// by adding a single base unit, and it must not shorten it either.
func (s *StakeLedger) RecordBonded(id string, height uint64) error {
	if s == nil || s.store == nil {
		return nil
	}
	existing, err := s.store.Get(bondedAtKey(id))
	if err != nil {
		return fmt.Errorf("consensus: read bond age of %s: %w", id, err)
	}
	if existing != nil {
		return nil
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, height)
	if err := s.store.Put(bondedAtKey(id), buf); err != nil {
		return fmt.Errorf("consensus: record bond age of %s: %w", id, err)
	}
	return nil
}

// ClearBonded forgets an account's bond age, which is what withdrawing means:
// there is no bond, so no clock is running. A later bond starts a fresh one.
func (s *StakeLedger) ClearBonded(id string) error {
	if s == nil || s.store == nil {
		return nil
	}
	if err := s.store.Delete(bondedAtKey(id)); err != nil {
		return fmt.Errorf("consensus: clear bond age of %s: %w", id, err)
	}
	return nil
}

// BondedAt returns the height an account's current bond was posted, and whether
// one is recorded.
func (s *StakeLedger) BondedAt(id string) (uint64, bool, error) {
	if s == nil || s.store == nil {
		return 0, false, nil
	}
	body, err := s.store.Get(bondedAtKey(id))
	if err != nil {
		return 0, false, fmt.Errorf("consensus: read bond age of %s: %w", id, err)
	}
	if body == nil {
		return 0, false, nil
	}
	if len(body) != 8 {
		return 0, false, fmt.Errorf("consensus: corrupt bond age for %s: %d bytes", id, len(body))
	}
	return binary.BigEndian.Uint64(body), true, nil
}

// RecordLeftSet notes that an account stopped being a validator at height,
// starting its unbonding clock. Recording it again for the same account
// overwrites the earlier height, which is correct: an account that rejoined and
// left again must serve the delay from the LATER departure.
func (s *StakeLedger) RecordLeftSet(id string, height uint64) error {
	if s == nil || s.store == nil {
		return nil
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, height)
	if err := s.store.Put(leftKey(id), buf); err != nil {
		return fmt.Errorf("consensus: record departure of %s: %w", id, err)
	}
	return nil
}

// ClearLeftSet forgets an account's departure height, which is what admitting it
// again means: it is a validator, so no unbonding clock is running.
func (s *StakeLedger) ClearLeftSet(id string) error {
	if s == nil || s.store == nil {
		return nil
	}
	if err := s.store.Delete(leftKey(id)); err != nil {
		return fmt.Errorf("consensus: clear departure of %s: %w", id, err)
	}
	return nil
}

// LeftSetAt returns the height at which an account left the validator set, and
// whether such a height is recorded. No record means the account has not been a
// validator on this chain, so nothing gates its withdrawal.
func (s *StakeLedger) LeftSetAt(id string) (uint64, bool, error) {
	if s == nil || s.store == nil {
		return 0, false, nil
	}
	body, err := s.store.Get(leftKey(id))
	if err != nil {
		return 0, false, fmt.Errorf("consensus: read departure of %s: %w", id, err)
	}
	if body == nil {
		return 0, false, nil
	}
	if len(body) != 8 {
		return 0, false, fmt.Errorf("consensus: corrupt departure record for %s: %d bytes", id, len(body))
	}
	return binary.BigEndian.Uint64(body), true, nil
}

// WithdrawableAt returns the height from which an account may withdraw its
// bond, and whether the account is currently blocked from withdrawing at all
// (which is the case while it is still a validator).
//
// A validator may not withdraw at any height: it has to leave the set first,
// and the delay runs from the height it left.
func (s *StakeLedger) WithdrawableAt(id string, vs *ValidatorSet, unbonding, residency uint64) (uint64, bool, error) {
	if vs != nil && vs.Contains(id) {
		return 0, false, nil
	}

	// The minimum a bond stays posted, whoever posted it. Before this, an account
	// that had never been a validator could withdraw the instant it bonded,
	// because the only clock was the one measuring a departure from the validator
	// set - and it had never made one.
	var earliest uint64
	if residency > 0 {
		at, bonded, err := s.BondedAt(id)
		if err != nil {
			return 0, false, err
		}
		if bonded {
			earliest = at + residency
		}
	}

	left, ok, err := s.LeftSetAt(id)
	if err != nil {
		return 0, false, err
	}
	if ok {
		// A departed validator waits out its unbonding delay as well. Whichever
		// clock runs longer is the one that governs: both exist to keep capital
		// in place, and satisfying one early does not excuse the other.
		if after := left + unbonding; after > earliest {
			earliest = after
		}
	}
	return earliest, true, nil
}

// Slash moves an account's whole bond to the reserved reward pool.
//
// To the pool rather than out of existence: destroying it would mean the
// circulating supply no longer matches the issued-supply counter the treasury
// keeps, and reconciling those is a worse problem than deciding where slashed
// coins go. The pool is where provider and buyer funding already comes from, so
// a slashed bond ends up paying the honest participants - which is the right
// place for it.
//
// It returns the amount taken, which is zero for an account with no bond.
func (s *StakeLedger) Slash(id string) (uint64, error) {
	if s == nil || s.ledger == nil {
		return 0, nil
	}
	var taken uint64
	err := s.ledger.Atomically(func(ltx market.LedgerTx) error {
		bonded, err := ltx.Balance(BondAccount(id))
		if err != nil {
			return err
		}
		if bonded == 0 {
			return nil
		}
		if err := ltx.Transfer(BondAccount(id), rewardPoolAccount, bonded); err != nil {
			return err
		}
		taken = bonded
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("consensus: slash %s: %w", id, err)
	}
	return taken, nil
}

// rewardPoolAccount is token.RewardPoolAccount, spelled out because
// internal/token imports internal/market and this package must not pull the
// treasury in just to name an account. A test asserts the two agree.
const rewardPoolAccount = "native/reward-pool"

// Withdraw returns an account's whole bond to it, having checked that it may.
func (s *StakeLedger) Withdraw(id string, vs *ValidatorSet, unbonding, residency, height uint64) (uint64, error) {
	at, allowed, err := s.WithdrawableAt(id, vs, unbonding, residency)
	if err != nil {
		return 0, err
	}
	if !allowed {
		return 0, fmt.Errorf("%w: %s is still a validator", ErrBondLocked, id)
	}
	if height < at {
		return 0, fmt.Errorf("%w: %s may withdraw from height %d, current height %d", ErrBondLocked, id, at, height)
	}

	var returned uint64
	err = s.ledger.Atomically(func(ltx market.LedgerTx) error {
		bonded, err := ltx.Balance(BondAccount(id))
		if err != nil {
			return err
		}
		if bonded == 0 {
			return nil
		}
		if err := ltx.Transfer(BondAccount(id), id, bonded); err != nil {
			return err
		}
		returned = bonded
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("consensus: withdraw bond of %s: %w", id, err)
	}
	return returned, nil
}
