package node

import (
	"github.com/ecirlabs/matrix-core/internal/openaiapi"
)

// What this node has heard of the rest of the market, in the shape the OpenAI
// door asks for.
//
// The adapter exists so openaiapi keeps depending on nothing but the market
// vocabulary. It converts rather than re-exports, which also makes the boundary
// the place to see exactly what the door is told: who, where, and on what terms,
// and nothing about gossip, signatures or peer ids.
type exchangeDirectory struct {
	node *Node
}

// RemoteSellers lists the sellers this node can describe but not fulfil.
//
// ListRemoteProviders has already dropped everything whose receipt TTL or quote
// validity has run out, so what comes back is what the node believes is sellable
// right now - the same set its own directory endpoint would show.
func (d exchangeDirectory) RemoteSellers() []openaiapi.RemoteSeller {
	if d.node == nil || d.node.exchange == nil {
		return nil
	}
	remotes := d.node.exchange.ListRemoteProviders()
	out := make([]openaiapi.RemoteSeller, 0, len(remotes))
	for _, rp := range remotes {
		out = append(out, openaiapi.RemoteSeller{
			ProviderID:   rp.ID,
			Endpoint:     rp.Endpoint,
			Models:       rp.Models,
			PricePerUnit: rp.PricePerUnit,
			Available:    rp.Available,
		})
	}
	return out
}
