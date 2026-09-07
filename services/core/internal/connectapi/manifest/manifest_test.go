package manifest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/connectapi/manifest"
	inferencev1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/inference/v1"
	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
)

// manifestPath is the checked-in copy the TypeScript SDK reads.
const manifestPath = "../../../../../packages/sdk/src/rpc-manifest.json"

// TestCheckedInManifestIsCurrent fails when a proto has gained or lost a method
// and the SDK's copy of the served surface was not regenerated.
//
// Without this, a new RPC is invisible to every non-Go client until somebody
// notices - the exact way the console ended up with a client for a protocol the
// daemon did not serve.
func TestCheckedInManifestIsCurrent(t *testing.T) {
	want, err := manifest.JSON(manifest.Build(
		&marketv1.MarketService_ServiceDesc,
		&inferencev1.InferenceService_ServiceDesc,
	))
	if err != nil {
		t.Fatalf("render manifest: %v", err)
	}

	got, err := os.ReadFile(filepath.Clean(manifestPath))
	if err != nil {
		t.Fatalf("read %s: %v", manifestPath, err)
	}

	if string(got) != string(want) {
		t.Fatalf("packages/sdk/src/rpc-manifest.json is stale.\nRegenerate it:\n"+
			"  cd services/core && go run ./cmd/rpcmanifest > ../../packages/sdk/src/rpc-manifest.json\n\n"+
			"have:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildIsSortedAndStable(t *testing.T) {
	m := manifest.Build(&marketv1.MarketService_ServiceDesc, &inferencev1.InferenceService_ServiceDesc)
	if len(m.Services) != 2 {
		t.Fatalf("got %d services, want 2", len(m.Services))
	}
	// Services and methods are sorted, so the JSON does not churn between runs
	// and a diff means a real change.
	if m.Services[0].Name > m.Services[1].Name {
		t.Fatal("services are not sorted by name")
	}
	for _, s := range m.Services {
		for i := 1; i < len(s.Methods); i++ {
			if s.Methods[i-1] > s.Methods[i] {
				t.Fatalf("methods of %s are not sorted", s.Name)
			}
		}
	}
}
