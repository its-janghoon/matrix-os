package node

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/p2p"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"gopkg.in/yaml.v3"
)

// Identities are the two stable node identities operators need before they can
// finish a multi-validator genesis config.
type Identities struct {
	ConsensusID string `json:"consensus_id"`
	PeerID      string `json:"peer_id"`
}

// InitializeIdentities creates or reloads the stable consensus and libp2p keys
// in the configured store without starting network services or applying
// genesis. A multi-validator launch needs every node's IDs before the identical
// validator set and bootstrap peer lists can be frozen, so starting the full
// node to discover them would apply an incomplete genesis irreversibly.
func InitializeIdentities(configPath string) (Identities, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return Identities{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Identities{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Storage.Path == "" {
		cfg.Storage.Path = "./data"
	}

	store, err := kv.New(kv.Config{Path: cfg.Storage.Path})
	if err != nil {
		return Identities{}, fmt.Errorf("open identity store: %w", err)
	}
	defer func() { _ = store.Close() }()

	validator, err := consensus.LoadOrCreateValidatorAccount(store)
	if err != nil {
		return Identities{}, err
	}
	peerKey, err := p2p.LoadOrCreatePeerKey(store)
	if err != nil {
		return Identities{}, err
	}
	peerID, err := libp2ppeer.IDFromPrivateKey(peerKey)
	if err != nil {
		return Identities{}, fmt.Errorf("derive peer id: %w", err)
	}
	return Identities{
		ConsensusID: validator.AccountID(),
		PeerID:      peerID.String(),
	}, nil
}

// MarshalIdentities returns stable machine-readable output for launch scripts.
func MarshalIdentities(ids Identities) ([]byte, error) {
	return json.MarshalIndent(ids, "", "  ")
}
