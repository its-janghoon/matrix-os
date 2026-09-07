package p2p

import (
	"crypto/rand"
	"fmt"

	"github.com/ecirlabs/matrix-core/internal/kv"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
)

// peerKeyKey is where a node's persisted libp2p keypair lives in the shared
// kv.Store, alongside the consensus identity under its own namespace.
const peerKeyKey = "p2p/identity/peer_key"

// LoadOrCreatePeerKey returns this node's stable libp2p identity, generating and
// persisting one the first time and reloading it afterwards.
//
// WHY THIS EXISTS. libp2p.New with no Identity option mints a fresh keypair on
// every start, so the node's PEER ID changed every time it restarted. The
// consensus identity was already persisted, so the effect was subtle and
// specific: a node kept its validator identity across a restart and lost its
// network identity, which means every OTHER node's
// `network.bootstrap_peers` entry - the documented
// /ip4/<host>/tcp/<port>/p2p/<peer id> form - went stale the moment the peer
// bounced. libp2p verifies the peer id it dialed, correctly refusing to talk to
// a host presenting a different identity, so the symptom is "all dials failed"
// with no hint that the id is simply out of date.
//
// A single node never notices. This was found by running two of them.
func LoadOrCreatePeerKey(store *kv.Store) (libp2pcrypto.PrivKey, error) {
	if store == nil {
		// No store, so no stable identity is possible. Generating an ephemeral
		// one is the honest behaviour for an in-process or test host, and the
		// caller that wants stability passes a store.
		priv, _, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("p2p: generate peer key: %w", err)
		}
		return priv, nil
	}

	data, err := store.Get([]byte(peerKeyKey))
	if err != nil {
		return nil, fmt.Errorf("p2p: read peer key: %w", err)
	}
	if data != nil {
		priv, err := libp2pcrypto.UnmarshalPrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("p2p: decode peer key: %w", err)
		}
		return priv, nil
	}

	// ed25519 to match the rest of this system's identities, and because it is
	// what libp2p's own default would pick.
	priv, _, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("p2p: generate peer key: %w", err)
	}
	enc, err := libp2pcrypto.MarshalPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("p2p: encode peer key: %w", err)
	}
	if err := store.Put([]byte(peerKeyKey), enc); err != nil {
		return nil, fmt.Errorf("p2p: persist peer key: %w", err)
	}
	return priv, nil
}
