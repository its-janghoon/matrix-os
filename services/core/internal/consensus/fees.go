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

	// MaxMaintainerShareBasisPoints caps the maintainer's cut of the fee at half
	// of it, in hundredths of a percent.
	//
	// The ceiling exists for the same reason MaxFeeBasisPoints does, and it is
	// set here rather than left to config because of what the fee is FOR. The fee
	// exists to pay for validating - it is what makes a bond worth posting by a
	// third party. A maintainer who took most of it would be funding themselves
	// out of the budget that buys the network its security, and every validator
	// would see their earnings fall without anything in the protocol saying why.
	// Half means the people doing the work always keep at least half of what the
	// work is paid.
	MaxMaintainerShareBasisPoints uint32 = 5_000

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
	// IsReservedRecipient, not a hand-kept list. This was the fourth divergent
	// copy of the same list: it named stake and set changes and not provider
	// registry changes or burn unlocks. Harmless today only because those carry
	// no value and a fee on zero is zero - but a list that is right by accident
	// is one edit away from being wrong.
	if IsReservedRecipient(to) {
		return false
	}
	if strings.HasPrefix(to, feeNamespace) {
		return false
	}
	return true
}

// feeNamespace is the reserved prefix for fee state.
const feeNamespace = "consensus/fees/"

// isAccountID reports whether id has the shape of an ed25519-derived account:
// exactly 64 lowercase hex characters. It is a shape check, not proof that
// anyone holds the key - but it is what separates a real account from a typo,
// and a fee paid every block into a typo is unrecoverable.
func isAccountID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// The maintainer share: a standing cut of the fee for whoever keeps the network
// running.
//
// WHAT IT IS FOR. A network has a person who starts it and then keeps operating
// and developing it, and that work does not stop at launch. The protocol fee
// already pays validators for validating. It pays nothing for maintenance, so
// the only ways a maintainer could fund themselves were to run validators (a
// share that DILUTES as the set grows, which is to say it shrinks exactly as the
// project succeeds) or to take a one-off genesis allocation (which funds a
// moment, not an ongoing obligation). This is the third: a fixed fraction of the
// fee, paid to one named account, that does not dilute.
//
// WHY IT IS A SHARE OF THE FEE AND NOT OF THE TRANSFER. Bounding it inside the
// fee means it inherits the fee's ceiling: at the maximum fee of 1% and the
// maximum share of half, a maintainer takes 0.5% of transferred value and can
// never take more, whatever anyone configures. A cut quoted against the transfer
// would need its own ceiling and a second place to reason about the worst case.
//
// WHY AN OPERATOR CANNOT QUIETLY ZERO IT. It is read from config, like the fee
// rate itself, and like the fee rate every node must agree on it: a node that
// set a different share would compute different balances from the same block and
// fork itself off the network. That is not an enforcement mechanism anyone
// built, it is a consequence of the fee being consensus arithmetic - but it is a
// real one, and it is stronger than a promise. The cost of not paying it is
// leaving.
//
// WHAT IT IS, PLAINLY. A tax on the people using the network, taken to fund the
// person maintaining it. That is a legitimate thing for a network to do and a
// dishonest thing to hide, so it defaults to zero, it is bounded in code, the
// node prints it at startup, and MaintainerShare exposes it for a client to
// read. Anyone can see what they are paying and to whom.

// checkMaintainerConfig validates the maintainer account and share before a node
// starts, rather than letting a bad one become a fee taken every block.
//
// Every failure here is silent in production if it is not caught here: an
// over-ceiling share quietly outbids the validators, a share with no account
// charges users and pays nobody, and a mistyped account pays into something
// nobody holds a key for - forever, with nothing to notice it by, because a fee
// arriving somewhere unspendable looks exactly like a fee arriving.
func checkMaintainerConfig(account string, bps uint32) error {
	if bps > MaxMaintainerShareBasisPoints {
		return fmt.Errorf("consensus: maintainer share of %d basis points exceeds the %d-basis-point "+
			"ceiling this build allows, which keeps at least half the fee with the validators earning it",
			bps, MaxMaintainerShareBasisPoints)
	}
	if bps == 0 {
		return nil
	}
	if account == "" {
		return fmt.Errorf("consensus: a maintainer share of %d basis points is configured but no "+
			"maintainer account is set, so there is nobody to pay it to", bps)
	}
	// A reserved id is a namespace this package spends FROM, not an account
	// anyone holds a key for.
	if IsReservedRecipient(account) || strings.HasPrefix(account, feeNamespace) ||
		account == rewardPoolAccount {
		return fmt.Errorf("consensus: maintainer account %q is a reserved id, not an account anyone "+
			"holds a key for", account)
	}
	if !isAccountID(account) {
		return fmt.Errorf("consensus: maintainer account %q is not an account id (64 lowercase hex "+
			"characters); a typo here pays the fee to an account nobody can spend from, every block, "+
			"with nothing to notice it by", account)
	}
	return nil
}

// takeMaintainerShareLocked moves the maintainer's cut out of the accrual
// account before validators are paid, returning what was taken.
//
// Determinism: accrued is committed state and the share is exact integer
// arithmetic, so every node takes the same amount at the same height. The
// remainder is what the pro-rata split then divides, so the maintainer's cut
// cannot be affected by how the validator split rounds.
func takeMaintainerShareLocked(ltx market.LedgerTx, accrued uint64, account string, bps uint32) (uint64, error) {
	if account == "" || bps == 0 || accrued == 0 {
		return 0, nil
	}
	// accrued <= the supply cap and bps <= 5000, so the product is at most
	// 5e21 - past a uint64. Divide first, exactly as FeeFor does, and carry the
	// remainder term separately so no ordering can overflow.
	whole := accrued / basisPointDivisor
	rest := accrued % basisPointDivisor
	share := whole*uint64(bps) + rest*uint64(bps)/basisPointDivisor
	if share == 0 {
		return 0, nil
	}
	if err := ltx.Transfer(feeAccrualAccount, account, share); err != nil {
		return 0, fmt.Errorf("consensus: pay maintainer share to %s: %w", account, err)
	}
	return share, nil
}

// distributeFeesLocked pays the maintainer share, then pays out the rest of the
// fee accrual account to the validator set pro rata by voting power, leaving the
// indivisible remainder for the next block. It runs inside the ledger critical
// section that applied the block's transfers, so a block's fees and its
// transfers land together or not at all.
//
// Determinism: the accrued balance, the set and every member's power are all
// committed state, and the shares are computed with exact integer arithmetic in
// a fixed (sorted) order. Every node computes the same payout.
func distributeFeesLocked(ltx market.LedgerTx, vs *ValidatorSet, maintainer string, maintainerBPS uint32) error {
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

	// The maintainer is paid first, out of the whole accrual, so their share does
	// not depend on the validator split's rounding.
	taken, err := takeMaintainerShareLocked(ltx, accrued, maintainer, maintainerBPS)
	if err != nil {
		return err
	}
	accrued -= taken
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

// MaintainerShare returns the account paid a standing cut of the protocol fee
// and that cut in basis points OF THE FEE. An empty account or a zero share
// means nobody is paid one.
//
// It is exported because a fee nobody can see is not a fee anyone agreed to: a
// client can read what it is paying and to whom.
func (e *Engine) MaintainerShare() (account string, basisPoints uint32) {
	return e.MaintainerAccountInForce(), e.maintainerShareBPS
}

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
