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
//     header the node's admin.Authenticator reads when ACLs are enabled. Falls
//     back to MATRIX_ADMIN_API_KEY, which is what `matrixd -init` tells the
//     operator to export and what keeps the key out of argv and shell history
//   - --timeout: per-RPC timeout (default 10s)
//   - --json:    emit machine-readable JSON instead of human tables
//
// Wallets are ed25519 keypairs stored at ~/.matrix/wallet.json (0600),
// overridable via --wallet. Private keys are never printed or logged.
package cli

import (
	"os"
	"time"

	"github.com/ecirlabs/matrix-core/internal/version"
	"github.com/spf13/cobra"
)

// apiKeyEnv is the environment variable `matrixd -init` tells the operator to
// export. It said so before anything read it: the key was simply never sent,
// the node answered Unauthenticated, and nothing connected the two.
//
// A flag is also the wrong place for a secret on a shared box. It lands in
// argv, where `ps` shows it to every other user, and in shell history.
const apiKeyEnv = "MATRIX_ADMIN_API_KEY"

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
	pf.StringVar(&opts.APIKey, "api-key", "", "API key for nodes running with ACLs (sent as authorization metadata); defaults to $"+apiKeyEnv)
	pf.DurationVar(&opts.Timeout, "timeout", defaultTimeout, "per-RPC timeout")
	pf.BoolVar(&opts.JSON, "json", false, "emit machine-readable JSON output")

	// An explicit --api-key wins, including an explicit empty one: an operator
	// who passes --api-key="" is saying to send nothing, and silently
	// substituting the environment would make that unsayable. Checking Changed
	// rather than the value is what distinguishes the two.
	root.PersistentPreRunE = func(*cobra.Command, []string) error {
		if !pf.Changed("api-key") {
			if key := os.Getenv(apiKeyEnv); key != "" {
				// Through the flag rather than around it: the flag is bound to
				// opts.APIKey, so this fills the same field while leaving the
				// resolved value readable where every other setting is read.
				if err := pf.Set("api-key", key); err != nil {
					return err
				}
			}
		}
		return nil
	}

	root.AddCommand(
		newStatusCommand(opts),
		newHealthCommand(opts),
		newAttestCommand(opts),
		newProviderCommand(opts),
		newReceiptCommand(opts),
		newJobCommand(opts),
		newBalanceCommand(opts),
		newFundCommand(opts),
		newQuickstartCommand(opts),
		newInferenceCommand(opts),
		newAgentCommand(opts),
		newTxCommand(opts),
		newWalletCommand(opts),
		newStakeCommand(opts),
		newBridgeCommand(opts),
		newBudgetCommand(opts),
	)

	return root
}
