package cli

import (
	"fmt"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"github.com/spf13/cobra"
)

// newFundCommand builds `matrix fund --account --amount`. It moves native MATRIX
// from the node's genesis-allocated reward pool into an account through the
// FundAccount RPC (Treasury.FundFromRewardPool server-side), then prints the
// account's new balance. This is the bootstrap-funding path that lets a fresh
// buyer obtain spendable MATRIX before submitting paid compute jobs; no coins
// are minted, they are moved from the reward pool.
func newFundCommand(opts *globalOptions) *cobra.Command {
	var (
		account string
		amount  uint64
	)
	cmd := &cobra.Command{
		Use:   "fund",
		Short: "Fund an account from the node's genesis reward pool",
		Long: `fund moves native MATRIX from the node's genesis-allocated reward pool
into an account so it has spendable balance for paid compute jobs.

It calls the market FundAccount RPC, which transfers reward-pool MATRIX through
the treasury (no coins are minted; the native supply cap is respected). The new
account balance is printed on success.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if account == "" {
				return fmt.Errorf("--account is required")
			}
			if amount == 0 {
				return fmt.Errorf("--amount must be greater than 0")
			}
			cc, err := dial(opts)
			if err != nil {
				return err
			}
			defer cc.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := cc.market.FundAccount(ctx, &marketv1.FundAccountRequest{
				Account: account,
				Amount:  amount,
			})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			return printBalance(cmd.OutOrStdout(), opts.JSON, resp.GetAccount(), resp.GetBalance())
		},
	}
	cmd.Flags().StringVar(&account, "account", "", "account ID to fund from the reward pool (required)")
	cmd.Flags().Uint64Var(&amount, "amount", 0, "amount in native base units to move from the reward pool (required, > 0)")
	return cmd
}
