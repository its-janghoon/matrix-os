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
