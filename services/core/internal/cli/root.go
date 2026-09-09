// Package cli implements the `matrix` command-line operations tool. It builds a
// cobra command tree that drives a running Matrix OS node over its gRPC market
// API (matrix.market.v1.MarketService, served on the node's Market.Addr, default
// 127.0.0.1:9091). The command logic lives here (rather than in package main) so
// it is unit-testable: NewRootCommand returns a fully-wired *cobra.Command whose
// subcommands can be exercised in-process against an in-memory MarketService.
//
// Global flags configured on the root command:
//   - --addr:    node market gRPC endpoint (default 127.0.0.1:9091)
//   - --api-key: optional API key, attached as the "authorization" gRPC metadata
//     header the node's admin.Authenticator reads when ACLs are enabled
//   - --timeout: per-RPC timeout (default 10s)
//   - --json:    emit machine-readable JSON instead of human tables
//
// Wallets are ed25519 keypairs stored at ~/.matrix/wallet.json (0600),
// overridable via --wallet. Private keys are never printed or logged.
package cli

import (
	"time"

	"github.com/ecirlabs/matrix-core/internal/version"
	"github.com/spf13/cobra"
)

// globalOptions holds the values of the root command's persistent flags. A
// single pointer is shared with every subcommand so they read a consistent
// configuration after flag parsing.
type globalOptions struct {
	// Addr is the node's market gRPC endpoint.
	Addr string
	// APIKey, when set, is attached as the "authorization" metadata header.
	APIKey string
	// Timeout bounds each RPC.
	Timeout time.Duration
	// JSON selects machine-readable output where practical.
	JSON bool
}

// defaultAddr is the node's default market gRPC endpoint (Market.Addr).
const defaultAddr = "127.0.0.1:9091"

// NewRootCommand builds the root `matrix` command with all subcommands and
// persistent global flags wired to a shared globalOptions. It is the single
// entry point used by both cmd/matrix/main.go and the tests.
func NewRootCommand() *cobra.Command {
	opts := &globalOptions{}

	root := &cobra.Command{
		Use:   "matrix",
		Short: "Operate a Matrix OS node over its gRPC market API",
		Long: `matrix is the command-line operations tool for a Matrix OS node.

It drives a running node over its gRPC market API (matrix.market.v1.MarketService,
served on the node's market port, default 127.0.0.1:9091): check node health,
manage compute providers and jobs, read balances and committed consensus transfer
history, operate the Base bridge, and manage an ed25519 wallet that signs native
MATRIX transfers locally.

Point it at a node with --addr and, if the node runs with ACLs, --api-key.`,
		SilenceUsage:  true,
		SilenceErrors: false,
		// Setting Version is what makes cobra provide `--version`. The docs
		// told users to run it long before the flag existed.
		Version: version.String(),
	}

	pf := root.PersistentFlags()
	pf.StringVar(&opts.Addr, "addr", defaultAddr, "node market gRPC endpoint host:port")
	pf.StringVar(&opts.APIKey, "api-key", "", "API key for nodes running with ACLs (sent as authorization metadata)")
	pf.DurationVar(&opts.Timeout, "timeout", defaultTimeout, "per-RPC timeout")
	pf.BoolVar(&opts.JSON, "json", false, "emit machine-readable JSON output")

	root.AddCommand(
		newStatusCommand(opts),
		newHealthCommand(opts),
		newProviderCommand(opts),
		newJobCommand(opts),
		newBalanceCommand(opts),
		newFundCommand(opts),
		newQuickstartCommand(opts),
		newInferenceCommand(opts),
		newAgentCommand(opts),
		newTxCommand(opts),
		newWalletCommand(opts),
		newBridgeCommand(opts),
	)

	return root
}
