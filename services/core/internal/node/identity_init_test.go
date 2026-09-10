package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

func TestInitializeIdentitiesDoesNotApplyGenesis(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dataPath := filepath.Join(dir, "data")
	if err := os.WriteFile(configPath, []byte("storage:\n  path: "+dataPath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := InitializeIdentities(configPath)
	if err != nil {
		t.Fatalf("InitializeIdentities: %v", err)
	}
	if len(first.ConsensusID) != 64 || first.PeerID == "" {
		t.Fatalf("invalid identities: %+v", first)
	}
	second, err := InitializeIdentities(configPath)
	if err != nil {
		t.Fatalf("InitializeIdentities again: %v", err)
	}
	if second != first {
		t.Fatalf("identities changed: first %+v, second %+v", first, second)
	}

	store, err := kv.New(kv.Config{Path: dataPath})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := token.NewTreasury(mkt.Ledger(), store).GenesisApplied()
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("identity initialization applied genesis")
	}
}

func TestMarshalIdentitiesUsesStableFieldNames(t *testing.T) {
	out, err := MarshalIdentities(Identities{ConsensusID: "validator", PeerID: "peer"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["consensus_id"] != "validator" || got["peer_id"] != "peer" {
		t.Fatalf("unexpected JSON: %s", out)
	}
}
