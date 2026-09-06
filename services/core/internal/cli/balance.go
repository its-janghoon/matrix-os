package cli

import (
	"fmt"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"github.com/spf13/cobra"
)

// newBalanceCommand builds `matrix balance --account`.
func newBalanceCommand(opts *globalOptions) *cobra.Command {
	var account string
	cmd := &cobra.Command{
		Use:   "balance",
		Short: "Read a compute-credit balance for an account",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if account == "" {
				return fmt.Errorf("--account is required")
			}
			cc, err := dial(opts)
			if err != nil {
				return err
			}
			defer cc.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := cc.market.GetBalance(ctx, &marketv1.GetBalanceRequest{Account: account})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			return printBalance(cmd.OutOrStdout(), opts.JSON, resp.GetAccount(), resp.GetBalance())
		},
	}
	cmd.Flags().StringVar(&account, "account", "", "account ID to read the balance for (required)")
	return cmd
}

// printBalance renders an account balance as JSON or a line.
func printBalance(w interface {
	Write([]byte) (int, error)
}, asJSON bool, account string, balance uint64) error {
	if asJSON {
		return printJSON(w, struct {
			Account string `json:"account"`
			Balance uint64 `json:"balance"`
		}{Account: account, Balance: balance})
	}
	_, err := fmt.Fprintf(w, "%s\t%d\n", account, balance)
	return err
}
