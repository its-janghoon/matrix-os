// Command rpcmanifest writes the served RPC surface as JSON.
//
// Usage:
//
//	go run ./cmd/rpcmanifest > ../../packages/sdk/src/rpc-manifest.json
//
// The SDK checks itself against that file, so a method added to a proto shows
// up as a failing SDK test rather than as a client that silently cannot call it.
package main

import (
	"fmt"
	"os"

	"github.com/ecirlabs/matrix-core/internal/connectapi/manifest"
	agentv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/agent/v1"
	inferencev1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/inference/v1"
	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
)

func main() {
	out, err := manifest.JSON(manifest.Build(
		&marketv1.MarketService_ServiceDesc,
		&inferencev1.InferenceService_ServiceDesc,
		&agentv1.AgentService_ServiceDesc,
	))
	if err != nil {
		fmt.Fprintf(os.Stderr, "rpcmanifest: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintf(os.Stderr, "rpcmanifest: %v\n", err)
		os.Exit(1)
	}
}
