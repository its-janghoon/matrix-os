package cli

import (
	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"github.com/spf13/cobra"
)

// newTxCommand builds `matrix tx` with get/list subcommands over the chain.
func newTxCommand(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tx",
		Short: "Inspect committed transfers in the consensus transaction history",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newTxGetCommand(opts),
		newTxListCommand(opts),
	)
	return cmd
}

func newTxGetCommand(opts *globalOptions) *cobra.Command {
	var index uint64
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Read a single committed transfer by its history index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := dial(opts)
			if err != nil {
				return err
			}
			defer cc.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := cc.market.GetTransaction(ctx, &marketv1.GetTransactionRequest{Index: index})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			return printTransaction(cmd.OutOrStdout(), opts.JSON, resp.GetTransaction())
		},
	}
	cmd.Flags().Uint64Var(&index, "index", 0, "zero-based index in the consensus transaction history")
	return cmd
}

func newTxListCommand(opts *globalOptions) *cobra.Command {
	var (
		start uint64
		limit uint64
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List committed transfers in ascending index (commit) order",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := dial(opts)
			if err != nil {
				return err
			}
			defer cc.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := cc.market.ListTransactions(ctx, &marketv1.ListTransactionsRequest{
				StartIndex: start,
				Limit:      limit,
			})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			return printTransactions(cmd.OutOrStdout(), opts.JSON, resp.GetTransactions(), resp.GetTotal())
		},
	}
	cmd.Flags().Uint64Var(&start, "start", 0, "start index (inclusive)")
	cmd.Flags().Uint64Var(&limit, "limit", 0, "maximum number of transfers to return (0 = no limit)")
	return cmd
}
