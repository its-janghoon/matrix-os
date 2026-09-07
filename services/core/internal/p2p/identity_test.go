package p2p

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
)

// openStore opens a pebble store in a temp dir the test owns, and reopens the
// same path when called twice with the same dir - which is what a node restart
// looks like from this package's point of view.
func openStore(t *testing.T, dir string) *kv.Store {
	t.Helper()
	store, err := kv.New(kv.Config{Path: filepath.Join(dir, "kv")})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	return store
}

func peerIDOf(t *testing.T, priv libp2pcrypto.PrivKey) libp2ppeer.ID {
	t.Helper()
	id, err := libp2ppeer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatalf("IDFromPrivateKey: %v", err)
	}
	return id
}

// TestPeerKeySurvivesARestart is the regression guard for the defect two running
// nodes exposed: the peer id used to change on every start, so every other
// node's network.bootstrap_peers entry went stale the moment this node bounced
// and libp2p refused the dial with "all dials failed".
func TestPeerKeySurvivesARestart(t *testing.T) {
	dir := t.TempDir()

	first := openStore(t, dir)
	privA, err := LoadOrCreatePeerKey(first)
	if err != nil {
		t.Fatalf("first LoadOrCreatePeerKey: %v", err)
	}
	idA := peerIDOf(t, privA)
	if err := first.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// Reopen the same path, as a restarted node does.
	second := openStore(t, dir)
	defer func() { _ = second.Close() }()
	privB, err := LoadOrCreatePeerKey(second)
	if err != nil {
		t.Fatalf("second LoadOrCreatePeerKey: %v", err)
	}
	idB := peerIDOf(t, privB)

	if idA != idB {
		t.Fatalf("peer id changed across a restart: %s then %s", idA, idB)
	}
}

// TestPeerKeyDrivesTheHostIdentity proves the persisted key is what the running
// host actually presents. Persisting a key that New() then ignores would leave
// the original bug in place while this package's other test passed.
func TestPeerKeyDrivesTheHostIdentity(t *testing.T) {
	store := openStore(t, t.TempDir())
	defer func() { _ = store.Close() }()

	priv, err := LoadOrCreatePeerKey(store)
	if err != nil {
		t.Fatalf("LoadOrCreatePeerKey: %v", err)
	}
	want := peerIDOf(t, priv)

	h, err := New(context.Background(), &Config{ListenAddr: "127.0.0.1:0", Identity: priv})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = h.Close() }()

	if got := h.GetPeerID(); got != want {
		t.Fatalf("host peer id = %s, want the persisted key's %s", got, want)
	}
}

// TestNoStoreYieldsAnEphemeralKey pins the documented fallback: an in-process
// host with no store gets a fresh identity rather than an error or a shared one.
func TestNoStoreYieldsAnEphemeralKey(t *testing.T) {
	first, err := LoadOrCreatePeerKey(nil)
	if err != nil {
		t.Fatalf("LoadOrCreatePeerKey(nil): %v", err)
	}
	second, err := LoadOrCreatePeerKey(nil)
	if err != nil {
		t.Fatalf("LoadOrCreatePeerKey(nil): %v", err)
	}
	if peerIDOf(t, first) == peerIDOf(t, second) {
		t.Fatal("expected two ephemeral keys to differ")
	}
}
