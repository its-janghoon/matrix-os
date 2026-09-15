package node

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// walletAccounts is the node's honest inference Accounts resolver. Settling an
// inference job requires the buyer's private key to sign the consensus transfer,
// so the node resolves buyer signing keys in two ways, in order:
//
//  1. an in-memory set of accounts registered at runtime via Add (used by
//     in-process callers and tests that hold a *token.Account directly), and
//  2. the local wallet directory (~/.matrix by default): a wallet file whose
//     ed25519 public key matches the requested account id yields that account's
//     signing key.
//
// A wallet file comes in two shapes and BOTH are read, because `matrix wallet`
// writes the second and this resolver used to read only the first. The result
// was a node that ignored the wallet its own CLI had just written, reported
// "no signing account for buyer", and pointed at nothing: an unparseable file is
// indistinguishable here from a file for a different account.
//
//   - plaintext {public_key, private_key}, the older shape
//   - an encrypted token.Keystore, which `matrix wallet create` and `import`
//     write, unlocked with the passphrase in WalletPassphraseEnv
//
// A keystore carries its public key in the CLEAR, so the account id is matched
// before anything is decrypted. That is not a micro-optimisation: the KDF is
// scrypt and deliberately expensive, so decrypting every wallet in the directory
// on every lookup would put that cost in front of every settlement.
//
// This models the single-operator/dev deployment the quickstart uses: a node
// fulfilling inference on behalf of buyers whose keys it legitimately holds. It
// never fabricates custody, it can only resolve keys that already exist as an
// in-memory registration or an on-disk wallet under its directory. A multi-tenant
// deployment substitutes its own custodial resolver via the inference Service.
//
// It is safe for concurrent use.
// WalletPassphraseEnv is where the passphrase for an encrypted wallet keystore
// is read from. Env-only and never a config field, exactly as the attestor's
// MATRIX_ATTESTOR_PASSPHRASE is handled: a secret in the file is a secret in
// every backup of the file.
//
// A node with no wallet keystore never needs it, so an empty value is not an
// error here - it is only reported when a keystore was found whose id matched
// and could not be opened, which is the moment it is actually missing.
const WalletPassphraseEnv = "MATRIX_WALLET_PASSPHRASE"

type walletAccounts struct {
	// dir is the wallet directory scanned for *.json wallet files. Empty defaults
	// to ~/.matrix.
	dir string

	mu       sync.RWMutex
	inMemory map[string]*token.Account
}

// walletDiskFile mirrors the on-disk wallet layout written by the CLI
// (internal/cli/wallet.go): hex-encoded ed25519 key halves. It is duplicated
// here (rather than imported) because internal/cli imports nothing from
// internal/node and the format is a stable two-field JSON document.
type walletDiskFile struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

// newWalletAccounts builds a resolver over the default wallet directory
// (~/.matrix). If the home directory cannot be determined the disk lookup is
// simply skipped and only in-memory registrations resolve.
func newWalletAccounts() *walletAccounts {
	dir := ""
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, ".matrix")
	}
	return &walletAccounts{dir: dir, inMemory: make(map[string]*token.Account)}
}

// newWalletAccountsDir builds a resolver over an explicit wallet directory. It
// is used by tests to point the resolver at a temporary directory.
func newWalletAccountsDir(dir string) *walletAccounts {
	return &walletAccounts{dir: dir, inMemory: make(map[string]*token.Account)}
}

// Add registers an account's signing key in memory so the node can settle on its
// behalf without an on-disk wallet. A nil account is ignored.
func (w *walletAccounts) Add(acct *token.Account) {
	if acct == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.inMemory[acct.AccountID()] = acct
}

// Account resolves an account id to its signing token.Account, checking the
// in-memory registrations first and then scanning the wallet directory for a
// wallet whose public key equals id. It reports whether a signing account was
// found. It never returns an account whose key material fails to parse.
func (w *walletAccounts) Account(id string) (*token.Account, bool) {
	if id == "" {
		return nil, false
	}

	w.mu.RLock()
	acct, ok := w.inMemory[id]
	w.mu.RUnlock()
	if ok {
		return acct, true
	}

	if w.dir == "" {
		return nil, false
	}

	// The account id is the hex-encoded ed25519 public key, which is exactly the
	// PublicKey field a wallet file stores. Scan wallet files and match on it.
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return nil, false
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		found, ok := walletFileAccount(filepath.Join(w.dir, e.Name()), id)
		if !ok {
			continue
		}
		if found.AccountID() == id {
			// Cache the resolved account so repeated settlements do not re-scan.
			w.mu.Lock()
			w.inMemory[id] = found
			w.mu.Unlock()
			return found, true
		}
	}
	return nil, false
}

// walletFileAccount reads a wallet file in either shape and reconstructs its
// token.Account, reporting whether it yielded a well-formed ed25519 keypair.
//
// wantID is the account being looked for. It is used ONLY to decide whether an
// encrypted keystore is worth unlocking; the caller still checks the resulting
// account id, so a file that lies about its public key resolves to nothing.
func walletFileAccount(path, wantID string) (*token.Account, bool) {
	if acct, ok := loadWalletAccount(path); ok {
		return acct, true
	}
	return loadKeystoreAccount(path, wantID)
}

// loadKeystoreAccount unlocks an encrypted wallet keystore, but only when its
// clear-text public key is the account being asked for.
//
// Failures are silent, matching the plaintext reader: this runs over every file
// in a directory that may hold wallets for other accounts, an attestor keystore
// (which DecryptKeystore refuses by key type rather than mangling), and files
// that are not wallets at all. A missing passphrase is the one failure worth
// naming, and it is named where it becomes true - a keystore whose id matched
// and could not be opened.
func loadKeystoreAccount(path, wantID string) (*token.Account, bool) {
	if wantID == "" {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var ks token.Keystore
	if err := json.Unmarshal(data, &ks); err != nil {
		return nil, false
	}
	// Matched in the clear, before scrypt runs. An account id IS the hex public
	// key, so either field answers it; both are checked because a keystore
	// written by an older build may carry only one.
	if !strings.EqualFold(ks.PublicKey, wantID) && !strings.EqualFold(ks.AccountID, wantID) {
		return nil, false
	}

	pass := os.Getenv(WalletPassphraseEnv)
	if pass == "" {
		fmt.Printf("Inference: %s holds the wallet for %s, but %s is empty, so this node "+
			"cannot sign for that buyer. The passphrase is read from the environment so it "+
			"is not in the config file or its backups.\n", path, wantID, WalletPassphraseEnv)
		return nil, false
	}
	acct, err := token.DecryptKeystore(&ks, pass)
	if err != nil {
		fmt.Printf("Inference: %s holds the wallet for %s but did not unlock: %v\n",
			path, wantID, err)
		return nil, false
	}
	return acct, true
}

// loadWalletAccount reads a PLAINTEXT wallet file and reconstructs its
// token.Account, reporting whether it parsed into a well-formed ed25519 keypair.
func loadWalletAccount(path string) (*token.Account, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var wf walletDiskFile
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, false
	}
	pub, err := hex.DecodeString(wf.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, false
	}
	priv, err := hex.DecodeString(wf.PrivateKey)
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return nil, false
	}
	// Verify the key pair is self-consistent: the stored public key must be the
	// one the private key derives. A mismatched (corrupt or hand-edited) wallet is
	// rejected here rather than silently resolved and failing later at signature
	// verification time. The account id used for matching is the public key, so a
	// resolver must never surface an account whose signing key would produce a
	// signature that does not verify against that id.
	privKey := ed25519.PrivateKey(priv)
	if !privKey.Public().(ed25519.PublicKey).Equal(ed25519.PublicKey(pub)) {
		return nil, false
	}
	return &token.Account{
		PublicKey:  ed25519.PublicKey(pub),
		PrivateKey: privKey,
	}, true
}
