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

// createEncryptedWallet writes an encrypted keystore derived from a BIP-39
// recovery phrase, and returns the account and the phrase so the caller can show
// it once. It refuses to overwrite an existing file for the same reason
// createWallet does.
//
// This is what `matrix wallet create` does now. The plaintext form it replaces
// put the private key in a file at mode 0600 and nothing more: anyone who could
// read that file owned the account, and losing it lost the account outright
// because there was no phrase to write down.
func createEncryptedWallet(path, passphrase string) (*token.Account, string, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, "", fmt.Errorf("wallet already exists at %s (refusing to overwrite)", path)
	} else if !os.IsNotExist(err) {
		return nil, "", fmt.Errorf("cannot stat %s: %w", path, err)
	}

	phrase, err := token.NewMnemonic()
	if err != nil {
		return nil, "", err
	}
	acct, err := token.AccountFromMnemonic(phrase, "")
	if err != nil {
		return nil, "", err
	}
	if err := writeKeystore(path, acct, passphrase, true); err != nil {
		return nil, "", err
	}
	return acct, phrase, nil
}

// importEncryptedWallet writes an encrypted keystore for an account restored
// from a recovery phrase.
func importEncryptedWallet(path, phrase, passphrase string) (*token.Account, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("wallet already exists at %s (refusing to overwrite)", path)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot stat %s: %w", path, err)
	}
	acct, err := token.AccountFromMnemonic(phrase, "")
	if err != nil {
		return nil, err
	}
	if err := writeKeystore(path, acct, passphrase, true); err != nil {
		return nil, err
	}
	return acct, nil
}

func writeKeystore(path string, acct *token.Account, passphrase string, fromMnemonic bool) error {
	ks, err := token.EncryptKeystore(acct, passphrase, fromMnemonic)
	if err != nil {
		return err
	}
	data, err := token.MarshalKeystore(ks)
	if err != nil {
		return fmt.Errorf("failed to encode keystore: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("failed to create wallet directory %s: %w", filepath.Dir(path), err)
	}
	// Still 0600. The file is encrypted, and file permissions are the cheap
	// second lock rather than a replacement for the first.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("failed to write wallet %s: %w", path, err)
	}
	return nil
}

// walletAccountID reads the wallet's account id WITHOUT unlocking it.
//
// A keystore keeps the public key and account id in plaintext precisely so this
// is possible: printing your own address, or reading its balance, is not a
// reason to type a passphrase, and a tool that asks anyway teaches people to
// type it reflexively.
func walletAccountID(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no wallet at %s (run `matrix wallet create` first)", path)
		}
		return "", fmt.Errorf("failed to read wallet %s: %w", path, err)
	}
	if ks, ok := token.UnmarshalKeystore(data); ok {
		if ks.AccountID == "" {
			return "", fmt.Errorf("wallet %s does not record its account id", path)
		}
		return ks.AccountID, nil
	}
	// A legacy plaintext wallet: the account id is the public key it carries.
	var wf walletFile
	if err := json.Unmarshal(data, &wf); err != nil {
		return "", fmt.Errorf("failed to decode wallet %s: %w", path, err)
	}
	if wf.PublicKey == "" {
		return "", fmt.Errorf("wallet %s has no public key", path)
	}
	return wf.PublicKey, nil
}

// loadWallet reads a wallet file into an Account, accepting either form.
//
// A keystore needs a passphrase, supplied by the caller's prompt function. A
// LEGACY plaintext wallet is still read, without one: refusing it would strand
// every account created before this existed, and there is no migration a tool
// can perform on the user's behalf because it cannot invent a passphrase. The
// caller warns instead.
func loadWallet(path string, passphrase func() (string, error)) (*token.Account, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no wallet at %s (run `matrix wallet create` first)", path)
		}
		return nil, fmt.Errorf("failed to read wallet %s: %w", path, err)
	}

	if ks, ok := token.UnmarshalKeystore(data); ok {
		if passphrase == nil {
			return nil, fmt.Errorf("wallet %s is encrypted and no passphrase is available", path)
		}
		pass, err := passphrase()
		if err != nil {
			return nil, err
		}
		acct, err := token.DecryptKeystore(ks, pass)
		if err != nil {
			return nil, fmt.Errorf("failed to unlock %s: %w", path, err)
		}
		return acct, nil
	}

	// A legacy plaintext wallet. Warn every time it is USED to sign, not once:
	// the risk is not that the user has never been told, it is that the file is
	// still there. Stderr so it never contaminates --json output.
	fmt.Fprintf(os.Stderr,
		"warning: %s stores its private key in the clear. Anyone who can read that file owns "+
			"the account.\n         Move to an encrypted wallet: `matrix wallet import` with the "+
			"recovery phrase, or create a new\n         wallet and transfer the balance.\n", path)

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

	// Verify the key pair is self-consistent: the public key stored in the file
	// must be the one the private key actually derives. A corrupt or hand-edited
	// wallet (mismatched halves) is not a theft vector because the account id is
	// the public key and any transfer is verified against it, but it would fail
	// only later at signing/verification time. Fail fast here with a clear error.
	if !derived(priv).Equal(pub) {
		return nil, fmt.Errorf("wallet %s is corrupt: its private key does not match its public key", path)
	}
	return &token.Account{PublicKey: pub, PrivateKey: priv}, nil
}

// derived returns the ed25519 public key that priv derives.
func derived(priv ed25519.PrivateKey) ed25519.PublicKey {
	return priv.Public().(ed25519.PublicKey)
}
