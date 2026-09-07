package consensus

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// The protocol fee: what validating is paid for.
//
// Bonded stake gave a validator something to lose. It gave it nothing to earn,
// so a bond was a pure cost and no rational third party would post one. This is
// the other half: a small cut of every value transfer a committed block carries,
// paid to the validator set.
//
// IT IS A TRANSACTION FEE, and this file calls it that rather than a
// "settlement fee", because consensus cannot tell a marketplace settlement from
// any other transfer - both are a signed transfer of native MATRIX. Marketplace
// settlement pays it because settlement IS a transfer. Naming it after the one
// case it was designed for would misdescribe the other cases it also applies to.
//
// TAKEN FROM THE AMOUNT, not added to it. A recipient of a transfer of A
// receives A minus the fee. The alternative - debiting sender + fee - would make
// a transfer the sender could afford at proposal time unaffordable at apply
// time, so a job priced at exactly the buyer's balance would commit and then be
// skipped. Providers price for the cut, the way they would on any marketplace
// that takes one.
//
// WHO IS PAID, and why it is not who voted. The obvious answer - split it among
// the validators whose precommits carried the block - is not available, because
// the certificate a node observes is node-specific: one node may have seen three
// precommits and another four, and a fee split that depended on that would give
// two honest nodes different balances from the same block. That is a fork. The
// set IN FORCE at that height is committed state that every node agrees on, so
// the fee is split across it pro rata by voting power.
//
// The tradeoff in that choice is real and worth stating: it pays for stake
// rather than for participation, so a validator that never votes still earns.
// The answer to a validator that does not participate is to remove it, which the
// set-change machinery already does; the answer is not to make the fee split
// depend on state the nodes do not share.

const (
	// MaxFeeBasisPoints is the hard ceiling on the protocol fee, in hundredths
	// of a percent. One percent.
	//
	// It is a constant rather than a config bound because a fee is the one knob
	// whose abuse looks exactly like normal operation: an operator (or a bad
	// config push) that set it to 100% would take every transfer in full, and no
	// participant would see it coming. A ceiling in code means the worst a
	// misconfiguration can do is charge one percent.
	MaxFeeBasisPoints uint32 = 100

	// basisPointDivisor is 100%, in basis points.
	basisPointDivisor uint64 = 10_000

	// feeAccrualAccount holds fees taken but not yet distributed.
	//
	// It exists so the remainder of an uneven split is not lost and not given to
	// anyone in particular: each block distributes what divides evenly by voting
	// power and leaves the dust here, where the next block's distribution picks
	// it up. Over time every base unit reaches a validator, and no rounding rule
	// has to favour a particular member.
	feeAccrualAccount = "consensus/fees/accrued"
)

// FeeFor returns the protocol fee on an amount at the given rate, in native
// base units.
//
// The arithmetic divides before multiplying because it must not overflow: the
// native supply cap is 1e18 base units and 1e18 * 100 is larger than a uint64
// holds, so the naive amount*bps/10000 would wrap on a large transfer - and a
// wrapped fee would be a wrong balance on every node rather than an error
// anywhere. Splitting the amount into whole divisors plus a remainder keeps both
// products small: (1e18/10000)*100 is 1e16, and the remainder term is under
// 10000*100.
func FeeFor(amount uint64, basisPoints uint32) uint64 {
	if amount == 0 || basisPoints == 0 {
		return 0
	}
	bps := uint64(basisPoints)
	fee := (amount/basisPointDivisor)*bps + (amount%basisPointDivisor)*bps/basisPointDivisor
	// A fee can never exceed the amount it is taken from. Unreachable while
	// basisPoints <= MaxFeeBasisPoints, asserted because the alternative is a
	// negative transfer.
	if fee > amount {
		return amount
	}
	return fee
}

// paysFee reports whether a transaction's recipient is an ordinary account, and
// so whether the transfer to it is charged.
//
// Protocol operations are not charged. Bonding moves an account's own coins into
// its own bond, and taxing that would make posting stake lossy for no reason; a
// set change carries no value at all; and the fee accrual account is where the
// fee itself goes. Reserved recipients are recognisable by their namespace,
// which is why every one of them lives under a prefix rather than looking like
// an account id.
func paysFee(to string) bool {
	if to == "" {
		return false
	}
	if IsStakeRecipient(to) || IsSetChangeRecipient(to) {
		return false
	}
	if strings.HasPrefix(to, feeNamespace) {
		return false
	}
	return true
}

// feeNamespace is the reserved prefix for fee state.
const feeNamespace = "consensus/fees/"

// distributeFeesLocked pays out everything in the fee accrual account to the
// validator set, pro rata by voting power, and leaves the indivisible remainder
// for the next block. It runs inside the ledger critical section that applied
// the block's transfers, so a block's fees and its transfers land together or
// not at all.
//
// Determinism: the accrued balance, the set and every member's power are all
// committed state, and the shares are computed with exact integer arithmetic in
// a fixed (sorted) order. Every node computes the same payout.
func distributeFeesLocked(ltx market.LedgerTx, vs *ValidatorSet) error {
	if vs == nil || vs.Len() == 0 {
		return nil
	}
	accrued, err := ltx.Balance(feeAccrualAccount)
	if err != nil {
		return fmt.Errorf("consensus: read accrued fees: %w", err)
	}
	if accrued == 0 {
		return nil
	}
	total := vs.TotalPower()
	if total == 0 {
		return nil
	}

	// big.Int rather than the divide-first trick used above: here BOTH factors
	// can be near the supply cap (accrued fees and a validator's bonded stake),
	// so there is no ordering of a uint64 multiply and divide that cannot
	// overflow. This runs once per block over at most MaxValidators members.
	accruedBig := new(big.Int).SetUint64(accrued)
	totalBig := new(big.Int).SetUint64(total)
	share := new(big.Int)

	for _, id := range vs.IDs() {
		power := vs.Power(id)
		if power == 0 {
			continue
		}
		share.SetUint64(power)
		share.Mul(share, accruedBig)
		share.Div(share, totalBig)
		if share.Sign() == 0 {
			continue
		}
		if !share.IsUint64() {
			// Unreachable: share <= accrued, which came out of a uint64 balance.
			return fmt.Errorf("consensus: fee share for %s does not fit a uint64", id)
		}
		if err := ltx.Transfer(feeAccrualAccount, id, share.Uint64()); err != nil {
			return fmt.Errorf("consensus: pay fee share to %s: %w", id, err)
		}
	}
	return nil
}

// AccruedFees returns what has been taken in fees and not yet distributed. It is
// the dust of uneven splits, so on a live chain it is small and non-zero.
func (e *Engine) AccruedFees() (uint64, error) {
	if e.ledger == nil {
		return 0, nil
	}
	return e.ledger.Balance(feeAccrualAccount)
}

// FeeBasisPoints is the protocol fee rate in force, in hundredths of a percent.
// Zero means transfers are not charged.
func (e *Engine) FeeBasisPoints() uint32 { return e.feeBasisPoints }

// FeeAccrualAccount is the reserved account holding undistributed fees. It is
// exported so an operator can read its balance like any other.
func FeeAccrualAccount() string { return feeAccrualAccount }

// assert the reserved accounts this package names do not collide with a real
// ed25519-derived account id, which is 64 lowercase hex characters.
var _ = func() struct{} {
	for _, reserved := range []string{feeAccrualAccount, rewardPoolAccount} {
		if len(reserved) == 64 && !strings.ContainsAny(reserved, "/") {
			panic("consensus: reserved account " + reserved + " could collide with a real account id")
		}
	}
	_ = token.AccountIDFromPublicKey
	return struct{}{}
}()
