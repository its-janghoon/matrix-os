package cli

import (
	"fmt"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"github.com/spf13/cobra"
)

// newProviderCommand builds `matrix provider` with register/list subcommands.
func newProviderCommand(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Register and list compute providers",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newProviderRegisterCommand(opts),
		newProviderListCommand(opts),
	)
	return cmd
}

func newProviderRegisterCommand(opts *globalOptions) *cobra.Command {
	var (
		id       string
		capacity uint64
		price    uint64
		models   []string
	)
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Advertise local compute capacity on the order book",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id is required")
			}
			if capacity == 0 {
				return fmt.Errorf("--capacity must be greater than 0")
			}
			if price == 0 {
				return fmt.Errorf("--price must be greater than 0")
			}
			cc, err := dial(opts)
			if err != nil {
				return err
			}
			defer cc.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := cc.market.RegisterProvider(ctx, &marketv1.RegisterProviderRequest{
				Id:           id,
				Capacity:     capacity,
				PricePerUnit: price,
				Models:       models,
			})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			out := cmd.OutOrStdout()
			if opts.JSON {
				return printJSON(out, providerToRow(resp.GetProvider()))
			}
			return printProviders(out, false, []*marketv1.Provider{resp.GetProvider()})
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "provider ID (required, unique)")
	cmd.Flags().Uint64Var(&capacity, "capacity", 0, "total capacity in compute units (required, > 0)")
	cmd.Flags().Uint64Var(&price, "price", 0, "price per compute unit in credits (required, > 0)")
	cmd.Flags().StringSliceVar(&models, "models", nil,
		"model identifiers this provider serves, comma separated (optional; a provider with none is reachable by id only)")
	return cmd
}

func newProviderListCommand(opts *globalOptions) *cobra.Command {
	var includeRemote bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List providers on the order book",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := dial(opts)
			if err != nil {
				return err
			}
			defer cc.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := cc.market.ListProviders(ctx, &marketv1.ListProvidersRequest{IncludeRemote: includeRemote})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			return printProviders(cmd.OutOrStdout(), opts.JSON, resp.GetProviders())
		},
	}
	cmd.Flags().BoolVar(&includeRemote, "include-remote", false, "include remote P2P-discovered providers")
	return cmd
}
