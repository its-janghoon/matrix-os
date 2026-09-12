package consensus

import "github.com/ecirlabs/matrix-core/internal/token"

// Treasury allocation: the genesis reward pool holds essentially the entire
// native supply and a multi-validator network has no path out of it. FundAccount
// is refused because it writes one node's ledger and would fork the set, and
// launch provider emission is zero. The result is a coin that nobody can obtain,
// price, or spend: no market, no buyer balance, no provider proceeds. A chain
// whose unit of account is unreachable is not launched, it is only running.
//
// This moves a fixed, published amount to one named account so the coin has a
// float. It is NOT a mint. The units already exist in the pool, issued supply is
// unchanged, and NativeMaxSupply is untouched.
//
// The constants stay in consensus code for the reason the launch repair
// constants do: a configurable reward-pool withdrawal is an administrative
// mint-like power, and an operator who can set the recipient and the amount can
// take the whole pool. This operation is valid only for this account, this exact
// amount, and one exact pre-transfer pool balance. Changing any of them takes a
// released binary that every validator must independently run.
//
// The recipient is deliberately the same account the founder ceremony used. It
// is not a separate "treasury" identity, because a separate identity controlled
// by the same person would only hide the linkage rather than divide the holding.
// The vested 50,000,000 wMATRIX allocation is enforced on Base by the vesting
// contract, not by which native account this lands in, so mixing here removes
// nothing and discloses more.
const (
	treasuryAllocationRecipient = "consensus/treasury-allocation/public-float-v1"

	// treasuryAllocationAccount is launchRepairFounder on purpose; see above. It
	// is written as a reference rather than a second literal so the founder
	// account ID has exactly one definition in this package.
	treasuryAllocationAccount = launchRepairFounder

	// 1,000,000 MATRIX. Sized against the wrapped mint cap, not against total
	// supply: 50,000,000 of the immutable 60,000,000 cap is already spent on the
	// founder vault, so 10,000,000 is every token that can ever reach Base
	// without a new deployment. Taking 10% of that headroom leaves 9,000,000 for
	// the people who actually use the network.
	//
	// Written as whole MATRIX times token.NativeUnit rather than as a bare literal
	// so a decimal-place mistake is a compile-time expression, not a silent
	// thousandfold error in an operation that runs exactly once.
	treasuryAllocationAmount uint64 = 1_000_000 * token.NativeUnit

	// treasuryAllocationPoolBefore is the exact reward-pool balance this
	// operation requires, and it is what makes the operation single-use.
	//
	// WHY THE POOL AND NOT THE DESTINATION. Both launch repairs gate on the
	// DESTINATION balance, which is sound for a repair because the destination is
	// a vault-backing account that is not expected to spend back down to its
	// pre-repair value. It is NOT sound here. This allocation exists to be spent:
	// the whole point is to bridge it, so the destination returns to its
	// pre-allocation balance of zero within a few blocks and a destination guard
	// would re-arm, letting the pool be drained one million MATRIX at a time.
	//
	// The reward pool is monotonically non-increasing. Nothing credits it:
	// FundFromRewardPool refuses the pool as its own recipient,
	// IsReservedRecipient makes it unreachable as a transfer target, and launch
	// emission is zero. So once this transfer lands the precondition can never
	// hold again, whatever the recipient later does with the funds.
	//
	// It also fails CLOSED. If any other withdrawal moves the pool first this
	// value stops matching and the allocation becomes impossible rather than
	// repeatable, which is the correct direction for the mistake to point. The
	// fix would be to re-cut the constant against the observed balance and
	// re-release, under the same review as any other consensus change.
	treasuryAllocationPoolBefore uint64 = 946_999_999_999_999_900
)
