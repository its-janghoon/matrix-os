package cli

import (
	"fmt"
	"strings"
	"time"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"github.com/spf13/cobra"
)

// newProviderCommand builds `matrix provider` with register/quote-update/list subcommands.
func newProviderCommand(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Register, refresh, and list compute providers, and browse the network directory",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newProviderRegisterCommand(opts),
		newProviderQuoteUpdateCommand(opts),
		newProviderListCommand(opts),
		newProviderDirectoryCmd(opts),
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

// newProviderDirectoryCmd is the buyer's view: who on this network is selling a
// model, at what price, and at what address.
//
// It exists because discovery had no consumer. Nodes gossiped provider
// announcements and kept a registry of what they heard, and the only way to read
// that registry was `provider list --include-remote`, which printed the order
// book's thirteen columns and - until the endpoint was carried - no address to
// connect to. So a buyer's actual question, "where do I send this prompt", had no
// command that answered it and was answered by asking a person.
func newProviderDirectoryCmd(opts *globalOptions) *cobra.Command {
	var (
		model   string
		minBond uint64
	)
	cmd := &cobra.Command{
		Use:   "directory",
		Short: "List providers announced on the network, with the address to reach them",
		Long: `directory shows what this node has heard other nodes announce: the models
they serve, their price, and the endpoint a buyer connects to.

SEEN FOR and HEARD are what THIS node observed - how long it has been hearing a
seller and how many announcements it accepted. They are not reported by the
seller: an announcement carries no self-declared uptime, latency or throughput,
because a number a seller publishes about its own reliability costs nothing to
inflate. A long history means this node watched that seller keep announcing, which
is evidence of presence and not a promise of service.

BONDED is capital the seller has staked, also read from the chain. It does not
make anyone honest and cannot be slashed for bad service: no protocol can judge
whether a completion was really the model advertised, a buyer-complaint slash
would be a weapon competitors point at each other, and validators voting on
service quality is not something consensus can do. What a bond does is make a
LISTING cost money, which is what stops one attacker from filling this table with
cheap fake sellers - and that sybil is what turns every other fraud from a scam
into an industry. Use --min-bond to refuse sellers who have staked nothing.

The endpoint is signed by the announcing node, so a relaying peer cannot redirect
traffic to a host of its choosing. What none of this tells you is whether the
answers are any good. Nothing on a chain can. Judge that yourself, on a small
job, before sending a large one.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := dial(opts)
			if err != nil {
				return err
			}
			defer cc.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := cc.market.ListProviders(ctx, &marketv1.ListProvidersRequest{IncludeRemote: true})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			listed := make([]*marketv1.Provider, 0, len(resp.GetProviders()))
			for _, p := range resp.GetProviders() {
				if p.GetOrigin() != marketv1.ProviderOrigin_PROVIDER_ORIGIN_REMOTE {
					// This node's own listings are not a directory entry: the caller
					// is the one running them and `provider list` shows them in full.
					continue
				}
				if model != "" && !servesModel(p, model) {
					continue
				}
				if p.GetBonded() < minBond {
					continue
				}
				listed = append(listed, p)
			}
			return printDirectory(cmd.OutOrStdout(), opts.JSON, listed, time.Now().UTC())
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "only sellers advertising this model")
	cmd.Flags().Uint64Var(&minBond, "min-bond", 0,
		"only sellers with at least this much staked, in native base units")
	return cmd
}

// servesModel reports whether a provider advertises model, matched the way the
// order book normalises names: case-insensitively.
func servesModel(p *marketv1.Provider, model string) bool {
	want := strings.ToLower(strings.TrimSpace(model))
	for _, m := range p.GetModels() {
		if strings.ToLower(m) == want {
			return true
		}
	}
	return false
}
