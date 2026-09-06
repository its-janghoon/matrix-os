package cli

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// walletFile is the on-disk representation of a wallet. Keys are stored as hex
// strings. The private key never leaves this file and is never printed by any
// command.
type walletFile struct {
	// PublicKey is the hex-encoded raw ed25519 public key. It doubles as the
	// account ID.
	PublicKey string `json:"public_key"`
	// PrivateKey is the hex-encoded raw ed25519 private key (64 bytes). Secret.
	PrivateKey string `json:"private_key"`
}

// defaultWalletPath returns the default wallet location, ~/.matrix/wallet.json.
func defaultWalletPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".matrix", "wallet.json"), nil
}

// resolveWalletPath returns the explicit path if set, else the default.
func resolveWalletPath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return defaultWalletPath()
}

// createWallet generates a fresh ed25519 account and writes it to path with
// 0600 permissions, creating the parent directory (0700) as needed. It refuses
// to overwrite an existing wallet so a key is never clobbered by accident.
func createWallet(path string) (*token.Account, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("wallet already exists at %s (refusing to overwrite)", path)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot stat %s: %w", path, err)
	}

	acct, err := token.GenerateAccount()
	if err != nil {
		return nil, err
	}

	wf := walletFile{
		PublicKey:  hex.EncodeToString(acct.PublicKey),
		PrivateKey: hex.EncodeToString(acct.PrivateKey),
	}
	data, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to encode wallet: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create wallet directory %s: %w", dir, err)
	}
	// Write with 0600 so the secret key is owner-only.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, fmt.Errorf("failed to write wallet %s: %w", path, err)
	}
	return acct, nil
}

// loadWallet reads and decodes a wallet file into an Account (including the
// private key needed for signing).
func loadWallet(path string) (*token.Account, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no wallet at %s (run `matrix wallet create` first)", path)
		}
		return nil, fmt.Errorf("failed to read wallet %s: %w", path, err)
	}
	var wf walletFile
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("failed to decode wallet %s: %w", path, err)
	}

	pubBytes, err := hex.DecodeString(wf.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("wallet %s has a malformed public key: %w", path, err)
	}
	privBytes, err := hex.DecodeString(wf.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("wallet %s has a malformed private key: %w", path, err)
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("wallet %s public key must be %d bytes, got %d", path, ed25519.PublicKeySize, len(pubBytes))
	}
	if len(privBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("wallet %s private key must be %d bytes, got %d", path, ed25519.PrivateKeySize, len(privBytes))
	}

	pub := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(pub, pubBytes)
	priv := make(ed25519.PrivateKey, ed25519.PrivateKeySize)
	copy(priv, privBytes)
	return &token.Account{PublicKey: pub, PrivateKey: priv}, nil
}
