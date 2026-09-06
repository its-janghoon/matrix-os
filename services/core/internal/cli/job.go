package cli

import (
	"fmt"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"github.com/spf13/cobra"
)

// newJobCommand builds `matrix job` with submit/get/list/complete/cancel.
func newJobCommand(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Submit, inspect, and manage compute jobs",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newJobSubmitCommand(opts),
		newJobGetCommand(opts),
		newJobListCommand(opts),
		newJobCompleteCommand(opts),
		newJobCancelCommand(opts),
	)
	return cmd
}

func newJobSubmitCommand(opts *globalOptions) *cobra.Command {
	var (
		buyer    string
		provider string
		units    uint64
	)
	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Reserve provider capacity for a paid compute job",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if buyer == "" {
				return fmt.Errorf("--buyer is required")
			}
			if provider == "" {
				return fmt.Errorf("--provider is required")
			}
			if units == 0 {
				return fmt.Errorf("--units must be greater than 0")
			}
			cc, err := dial(opts)
			if err != nil {
				return err
			}
			defer cc.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := cc.market.SubmitJob(ctx, &marketv1.SubmitJobRequest{
				Buyer:    buyer,
				Provider: provider,
				Units:    units,
			})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			return printJob(cmd.OutOrStdout(), opts.JSON, resp.GetJob())
		},
	}
	cmd.Flags().StringVar(&buyer, "buyer", "", "buyer account ID (required)")
	cmd.Flags().StringVar(&provider, "provider", "", "target provider ID (required)")
	cmd.Flags().Uint64Var(&units, "units", 0, "compute units to reserve (required, > 0)")
	return cmd
}

func newJobGetCommand(opts *globalOptions) *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Fetch a single job by ID",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id is required")
			}
			cc, err := dial(opts)
			if err != nil {
				return err
			}
			defer cc.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := cc.market.GetJob(ctx, &marketv1.GetJobRequest{Id: id})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			return printJob(cmd.OutOrStdout(), opts.JSON, resp.GetJob())
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "job ID (required)")
	return cmd
}

func newJobListCommand(opts *globalOptions) *cobra.Command {
	var buyer string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List jobs in chronological order",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := dial(opts)
			if err != nil {
				return err
			}
			defer cc.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := cc.market.ListJobs(ctx, &marketv1.ListJobsRequest{Buyer: buyer})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			return printJobs(cmd.OutOrStdout(), opts.JSON, resp.GetJobs())
		},
	}
	cmd.Flags().StringVar(&buyer, "buyer", "", "optional buyer account ID filter")
	return cmd
}

func newJobCompleteCommand(opts *globalOptions) *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "complete",
		Short: "Settle a job, transferring credits buyer -> provider",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id is required")
			}
			cc, err := dial(opts)
			if err != nil {
				return err
			}
			defer cc.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := cc.market.CompleteJob(ctx, &marketv1.CompleteJobRequest{Id: id})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			return printJob(cmd.OutOrStdout(), opts.JSON, resp.GetJob())
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "job ID (required)")
	return cmd
}

func newJobCancelCommand(opts *globalOptions) *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "cancel",
		Short: "Cancel a job and return reserved capacity",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id is required")
			}
			cc, err := dial(opts)
			if err != nil {
				return err
			}
			defer cc.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := cc.market.CancelJob(ctx, &marketv1.CancelJobRequest{Id: id})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			return printJob(cmd.OutOrStdout(), opts.JSON, resp.GetJob())
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "job ID (required)")
	return cmd
}
