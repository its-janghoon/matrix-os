package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// newStatusCommand builds `matrix status`, an alias of the health check that
// also echoes the configured endpoint.
func newStatusCommand(opts *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show node connectivity and serving status",
		Long:  "Dial the node's market gRPC endpoint and report its health-check serving status.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHealth(cmd, opts)
		},
	}
}

// newHealthCommand builds `matrix health`, which calls the gRPC health Check.
func newHealthCommand(opts *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Check the node's gRPC health status",
		Long:  "Call the standard gRPC health Check on the node's market server and print SERVING/NOT_SERVING.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHealth(cmd, opts)
		},
	}
}

// runHealth performs the health Check and prints the result.
func runHealth(cmd *cobra.Command, opts *globalOptions) error {
	cc, err := dial(opts)
	if err != nil {
		return err
	}
	defer cc.Close()

	ctx, cancel := callContext(cmd.Context(), opts)
	defer cancel()

	resp, err := cc.health.Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return mapErr(opts.Addr, err)
	}

	statusName := resp.GetStatus().String()
	serving := statusName == healthpb.HealthCheckResponse_SERVING.String()

	out := cmd.OutOrStdout()
	if opts.JSON {
		return printJSON(out, struct {
			Endpoint string `json:"endpoint"`
			Status   string `json:"status"`
			Serving  bool   `json:"serving"`
		}{Endpoint: opts.Addr, Status: statusName, Serving: serving})
	}
	fmt.Fprintf(out, "endpoint: %s\n", opts.Addr)
	fmt.Fprintf(out, "status:   %s\n", statusName)
	return nil
}
