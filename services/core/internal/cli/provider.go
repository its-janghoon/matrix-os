package cli

import (
	"fmt"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"github.com/spf13/cobra"
)

// newProviderCommand builds `matrix provider` with register/quote-update/list subcommands.
func newProviderCommand(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Register, safely refresh, and list compute providers",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newProviderRegisterCommand(opts),
		newProviderQuoteUpdateCommand(opts),
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
		Short: "Advertise capacity with a manual MATRIX quote",
		Long: `register advertises local compute capacity with a final per-unit quote in
native MATRIX base units. It is not a USD or stablecoin peg. The marketplace
assigns quote identity and validity metadata, and jobs snapshot the accepted
quote so re-registering a provider cannot reprice existing reservations.

Use matrix provider quote-update to refresh an existing provider. Do not
re-register to refresh: registration resets available capacity, while
quote-update preserves capacity, active reservations, and models. Registration
and list output include quote metadata, and job output includes the immutable
reservation snapshot.`,
		Args: cobra.NoArgs,
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
	cmd.Flags().Uint64Var(&price, "price", 0, "final manual quote per compute unit in native MATRIX base units (required, > 0)")
	cmd.Flags().StringSliceVar(&models, "models", nil,
		"model identifiers this provider serves, comma separated (optional; a provider with none is reachable by id only)")
	return cmd
}

func newProviderQuoteUpdateCommand(opts *globalOptions) *cobra.Command {
	var (
		id    string
		price uint64
	)
	cmd := &cobra.Command{
		Use:   "quote-update",
		Short: "Safely refresh an existing provider quote",
		Long: `quote-update refreshes an existing provider's manual MATRIX quote for
future reservations. It preserves total and available capacity, active
reservations, and advertised models; do not use provider register as a refresh
path. The market advances quote identity/version, records the current observation
time, and applies the default expiry. Run or schedule this before the quote
expires.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id is required")
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

			resp, err := cc.market.UpdateProviderQuote(ctx, &marketv1.UpdateProviderQuoteRequest{
				Id:           id,
				PricePerUnit: price,
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
	cmd.Flags().StringVar(&id, "id", "", "existing provider ID (required)")
	cmd.Flags().Uint64Var(&price, "price", 0, "new final quote per compute unit in native MATRIX base units (required, > 0)")
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
