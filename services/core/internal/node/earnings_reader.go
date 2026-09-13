package node

import "github.com/ecirlabs/matrix-core/internal/consensus"

// engineEarnings adapts the consensus engine to marketapi.EarningsReader.
//
// The adapter exists so the market API keeps not importing internal/consensus,
// the same reason the transfer settler is injected rather than referenced. It
// flattens the engine's typed record into scalars because that is the whole of
// what crosses the boundary, and a shared struct would put a consensus type in
// the market API's signature to save nothing.
type engineEarnings struct{ engine *consensus.Engine }

func (e engineEarnings) Earnings(account string) (received, payments, payers, firstHeight, lastHeight, indexedFrom uint64, err error) {
	if e.engine == nil {
		return 0, 0, 0, 0, 0, 0, nil
	}
	got, from, err := e.engine.Earnings(account)
	if err != nil {
		return 0, 0, 0, 0, 0, 0, err
	}
	return got.Received, got.Payments, got.Payers, got.FirstHeight, got.LastHeight, from, nil
}

// Bonded reports what an account has staked and when it could take it back.
//
// The withdrawal height is as much the point as the amount. A bond that can be
// pulled in the next block is capital a seller has committed to nothing, so a
// buyer weighing one needs to know how long it is actually committed for.
func (e engineEarnings) Bonded(account string) (amount, withdrawableAt uint64, err error) {
	if e.engine == nil {
		return 0, 0, nil
	}
	amount, err = e.engine.BondedStake(account)
	if err != nil {
		return 0, 0, err
	}
	if amount == 0 {
		return 0, 0, nil
	}
	at, _, err := e.engine.BondWithdrawableAt(account)
	if err != nil {
		return amount, 0, err
	}
	return amount, at, nil
}

// Maintainer returns the account the chain currently names as maintainer.
//
// From consensus state, not from config: the account is rotatable by a committed
// transaction, so a listing reading a startup value would go on vouching for a
// key the chain has moved on from - which is precisely the case where a stale
// badge does harm.
func (e engineEarnings) Maintainer() string {
	if e.engine == nil {
		return ""
	}
	return e.engine.MaintainerAccountInForce()
}
