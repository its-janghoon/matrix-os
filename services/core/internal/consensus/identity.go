package consensus

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// validatorKeyKey is where a node's persisted consensus validator keypair lives
// in the shared kv.Store, under the consensus/* namespace.
const validatorKeyKey = "consensus/identity/validator_key"

// persistedKey is the JSON envelope for a stored ed25519 keypair.
type persistedKey struct {
	PublicKey  []byte `json:"public_key"`
	PrivateKey []byte `json:"private_key"`
}

// LoadOrCreateValidatorAccount returns this node's stable consensus validator
// identity, generating and persisting a fresh ed25519 keypair the first time and
// reloading it on subsequent starts. Persisting the key under consensus/* (in
// the same shared store as the committed chain) means a node keeps the same
// validator identity across restarts, which is required for the fixed validator
// set and round-robin leader schedule to remain stable.
func LoadOrCreateValidatorAccount(store *kv.Store) (*token.Account, error) {
	data, err := store.Get([]byte(validatorKeyKey))
	if err != nil {
		return nil, fmt.Errorf("consensus: read validator key: %w", err)
	}
	if data != nil {
		var pk persistedKey
		if err := json.Unmarshal(data, &pk); err != nil {
			return nil, fmt.Errorf("consensus: decode validator key: %w", err)
		}
		if len(pk.PublicKey) != ed25519.PublicKeySize || len(pk.PrivateKey) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("consensus: corrupt persisted validator key")
		}
		pub := make(ed25519.PublicKey, ed25519.PublicKeySize)
		copy(pub, pk.PublicKey)
		priv := make(ed25519.PrivateKey, ed25519.PrivateKeySize)
		copy(priv, pk.PrivateKey)
		return &token.Account{PublicKey: pub, PrivateKey: priv}, nil
	}

	acct, err := token.GenerateAccount()
	if err != nil {
		return nil, err
	}
	enc, err := json.Marshal(persistedKey{
		PublicKey:  token.MarshalPublicKey(acct.PublicKey),
		PrivateKey: append([]byte(nil), acct.PrivateKey...),
	})
	if err != nil {
		return nil, fmt.Errorf("consensus: encode validator key: %w", err)
	}
	if err := store.Put([]byte(validatorKeyKey), enc); err != nil {
		return nil, fmt.Errorf("consensus: persist validator key: %w", err)
	}
	return acct, nil
}

// ValidatorSetFromConfig builds the fixed validator set from a list of
// hex-encoded validator account IDs (public keys) plus this node's own public
// key (self), so a node is always a member of its own validator set. Duplicate
// keys are collapsed by NewValidatorSet. When ids is empty the set is just self,
// which yields a functioning single-validator consensus (quorum 1) suitable for
// a solo/dev node. Every node given the same ids derives the identical set and
// leader schedule.
func ValidatorSetFromConfig(self ed25519.PublicKey, ids []string) (*ValidatorSet, error) {
	keys := []ed25519.PublicKey{self}
	for _, id := range ids {
		if id == "" {
			continue
		}
		pub, err := token.ParsePublicKeyHex(id)
		if err != nil {
			return nil, fmt.Errorf("consensus: invalid validator id %q: %w", id, err)
		}
		keys = append(keys, pub)
	}
	return NewValidatorSet(keys)
}
