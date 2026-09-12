package node

import (
	"context"
	"fmt"
	"time"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/marketexchange"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Publishing this node's providers to the directory.
//
// THE GAP THIS CLOSES. The exchange could sign and publish an announcement, and
// every node subscribed to the topic and kept a registry of what it heard - and
// nothing anywhere called the publish path. Only tests did. So on a real network
// the registry was empty on every node, `--include-remote` listed nothing, and a
// buyer had exactly one way to find a provider: be told its address by a person.
//
// Discovery that is wired and never invoked is worse than none, because reading
// the code says the marketplace has it.
//
// WHAT IS ANNOUNCED. Only providers this node actually serves, with the terms its
// own order book holds, on the interval below. Re-announcing rather than
// announcing once is what makes the directory self-healing in both directions: a
// node that joins late hears everyone within one interval, and a node that stops
// - crashed, unplugged, or deliberately - falls out of every other node's
// registry when its receipts go stale, without needing to say goodbye.
//
// WHAT IS NOT ANNOUNCED. A suspended provider. The health probe suspends a
// backend whose model server stopped answering, and continuing to advertise it
// would fill the directory with addresses that take reservations and cannot
// serve them. Falling silent is the honest signal, and it is the same signal a
// dead node sends.

// DefaultAnnounceInterval is how often this node republishes its providers.
//
// It is a third of marketexchange.DefaultProviderTTL, so a listener has to miss
// three consecutive announcements before dropping a provider that is in fact
// healthy. Gossip is best-effort and one lost message is ordinary; three in a row
// is a signal. At one-half the TTL a single drop would already halve the margin,
// and at one-tenth the network would carry ten times the traffic to learn the
// same thing.
const DefaultAnnounceInterval = marketexchange.DefaultProviderTTL / 3

// announceLoop republishes this node's providers until ctx is cancelled.
//
// It announces once immediately rather than waiting out the first interval: a
// node that has just started is exactly when its operator is watching to see
// whether it appeared, and a minute of apparent silence reads as a failure.
func (n *Node) announceLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultAnnounceInterval
	}
	n.announceOnce(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.announceOnce(ctx)
		}
	}
}

// announceOnce publishes every local provider that is currently sellable.
func (n *Node) announceOnce(ctx context.Context) {
	if n.exchange == nil || n.market == nil {
		return
	}
	node := n.announcingAccount()
	if node == nil {
		// Nothing to sign with. A node with no consensus identity is not one a
		// buyer could settle with either, so silence is correct rather than a
		// degraded announcement.
		return
	}
	for _, p := range n.market.ListProviders() {
		if !announceable(p) {
			continue
		}
		if err := n.exchange.AnnounceProvider(ctx, node, p); err != nil {
			// One failed publish is not worth a line per provider per interval on
			// a network partition; the next tick retries and the registry's TTL
			// already expresses the outcome to everyone listening.
			continue
		}
	}
}

// announceable reports whether a provider should be in the directory right now.
//
// A suspended one should not: the health probe suspends a backend whose model
// server stopped answering, and an address that takes reservations it cannot
// serve is worse for a buyer than an address that is not listed. Nor should one
// with no capacity left, which would win a routing decision and then refuse it.
func announceable(p market.Provider) bool {
	return !p.Suspended && p.Available > 0
}

// announcingAccount returns the key this node signs announcements with.
//
// It is the consensus identity, which is the one account a node always has and
// the one that already identifies it to every other node. Deliberately not the
// provider's payout account: that may be a wallet address with no ed25519 key
// here, and signing with it would mean the node custodied a seller's key to do
// something that needs no custody.
func (n *Node) announcingAccount() *token.Account {
	return n.consensusAccount
}

// marketEndpoint is the address this node publishes for buyers to reach.
//
// It is configured rather than derived, and that is not laziness. A node cannot
// see its own public address: on any cloud instance the reachable name belongs to
// a load balancer or a NAT, the interface holds a private one, and a node that
// guessed would advertise an address nobody outside can dial - which is worse
// than advertising none, because a buyer would try it.
func marketEndpoint(cfg *Config) (string, error) {
	endpoint := cfg.Market.Endpoint
	if endpoint == "" {
		return "", nil
	}
	if err := marketexchange.ValidateEndpoint(endpoint); err != nil {
		return "", fmt.Errorf("market.endpoint: %w", err)
	}
	return endpoint, nil
}
