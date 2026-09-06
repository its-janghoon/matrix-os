package node

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
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
// This models the single-operator/dev deployment the quickstart uses: a node
// fulfilling inference on behalf of buyers whose keys it legitimately holds. It
// never fabricates custody, it can only resolve keys that already exist as an
// in-memory registration or an on-disk wallet under its directory. A multi-tenant
// deployment substitutes its own custodial resolver via the inference Service.
//
// It is safe for concurrent use.
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
		found, ok := loadWalletAccount(filepath.Join(w.dir, e.Name()))
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

// loadWalletAccount reads a wallet file and reconstructs its token.Account,
// reporting whether it parsed into a well-formed ed25519 keypair.
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
	return &token.Account{
		PublicKey:  ed25519.PublicKey(pub),
		PrivateKey: ed25519.PrivateKey(priv),
	}, true
}
