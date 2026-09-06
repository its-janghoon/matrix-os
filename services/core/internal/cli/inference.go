package cli

import (
	"fmt"
	"io"

	inferencev1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/inference/v1"
	"github.com/spf13/cobra"
)

// defaultInferenceAddr is the node's default inference gRPC endpoint
// (node Inference.Addr). It is distinct from the market endpoint so the
// inference commands reach the InferenceService server.
const defaultInferenceAddr = "127.0.0.1:9092"

// newInferenceCommand builds `matrix inference` with submit/get. It drives the
// node's matrix.inference.v1.InferenceService: a buyer submits an inference job
// to an inference-capable provider, the provider fulfills it on its registered
// backend (the node registers a GPU-free echo backend for its demo provider by
// default), and the computed units settle buyer -> provider through the same
// consensus-backed token settlement the compute marketplace uses.
//
// Because the inference server listens on its own address (default
// 127.0.0.1:9092), the inference subcommands accept a dedicated
// --inference-addr flag rather than the global --addr (which targets the market
// API on 9091).
func newInferenceCommand(opts *globalOptions) *cobra.Command {
	var inferenceAddr string
	cmd := &cobra.Command{
		Use:   "inference",
		Short: "Submit and inspect LLM inference jobs",
		Long: `inference drives a node's matrix.inference.v1 InferenceService.

A buyer submits an inference job to an inference-capable provider; the provider
fulfills it on its registered backend and the computed units settle buyer ->
provider through the consensus-backed token settlement. A freshly-initialized
node registers a GPU-free echo backend for its demo provider, so inference runs
end to end locally without a GPU.

The inference API listens on its own address (default 127.0.0.1:9092), set with
--inference-addr, distinct from the market API targeted by the global --addr.`,
		Args: cobra.NoArgs,
	}
	cmd.PersistentFlags().StringVar(&inferenceAddr, "inference-addr", defaultInferenceAddr,
		"node inference gRPC endpoint host:port")
	cmd.AddCommand(
		newInferenceSubmitCommand(opts, &inferenceAddr),
		newInferenceGetCommand(opts, &inferenceAddr),
	)
	return cmd
}

func newInferenceSubmitCommand(opts *globalOptions, inferenceAddr *string) *cobra.Command {
	var (
		buyer    string
		provider string
		prompt   string
		model    string
		units    uint64
		fulfill  bool
	)
	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit an inference job to a provider and run it",
		Long: `submit reserves capacity for an inference job on a provider and, by
default, immediately fulfills it so the completion and settled units are
returned in one command (reserve -> fulfill -> settle -> completed).

Pass --fulfill=false to only reserve the job (leaving it PENDING) and fulfill it
later via a separate call.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if buyer == "" {
				return fmt.Errorf("--buyer is required")
			}
			if provider == "" {
				return fmt.Errorf("--provider is required")
			}
			if prompt == "" {
				return fmt.Errorf("--prompt is required")
			}
			ic, err := dialInference(*inferenceAddr, opts.APIKey)
			if err != nil {
				return err
			}
			defer ic.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			subResp, err := ic.inference.SubmitInferenceJob(ctx, &inferencev1.SubmitInferenceJobRequest{
				Buyer:         buyer,
				Provider:      provider,
				Model:         model,
				Prompt:        prompt,
				UnitsEstimate: units,
			})
			if err != nil {
				return mapErr(*inferenceAddr, err)
			}
			job := subResp.GetJob()

			if fulfill {
				fulResp, err := ic.inference.FulfillInferenceJob(ctx, &inferencev1.FulfillInferenceJobRequest{
					Id: job.GetId(),
				})
				if err != nil {
					return mapErr(*inferenceAddr, err)
				}
				job = fulResp.GetJob()
			}
			return printInferenceJob(cmd.OutOrStdout(), opts.JSON, job)
		},
	}
	cmd.Flags().StringVar(&buyer, "buyer", "", "buyer account ID (required)")
	cmd.Flags().StringVar(&provider, "provider", "", "inference-capable provider ID (required)")
	cmd.Flags().StringVar(&prompt, "prompt", "", "prompt to run (required)")
	cmd.Flags().StringVar(&model, "model", "", "optional model identifier")
	cmd.Flags().Uint64Var(&units, "units", 0, "upfront compute units to reserve (0 defaults to 1)")
	cmd.Flags().BoolVar(&fulfill, "fulfill", true, "run and settle the job immediately after reserving it")
	return cmd
}

func newInferenceGetCommand(opts *globalOptions, inferenceAddr *string) *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Fetch a single inference job by ID",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id is required")
			}
			ic, err := dialInference(*inferenceAddr, opts.APIKey)
			if err != nil {
				return err
			}
			defer ic.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := ic.inference.GetInferenceJob(ctx, &inferencev1.GetInferenceJobRequest{Id: id})
			if err != nil {
				return mapErr(*inferenceAddr, err)
			}
			return printInferenceJob(cmd.OutOrStdout(), opts.JSON, resp.GetJob())
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "inference job ID (required)")
	return cmd
}

// inferenceJobRow is the flattened, presentation-friendly view of an inference
// job for JSON and table output.
type inferenceJobRow struct {
	ID         string `json:"id"`
	Buyer      string `json:"buyer"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Status     string `json:"status"`
	Units      uint64 `json:"units"`
	Completion string `json:"completion"`
}

// inferenceStatusString renders an inference job status enum as a lowercase
// human string.
func inferenceStatusString(s inferencev1.InferenceJobStatus) string {
	switch s {
	case inferencev1.InferenceJobStatus_INFERENCE_JOB_STATUS_PENDING:
		return "pending"
	case inferencev1.InferenceJobStatus_INFERENCE_JOB_STATUS_RUNNING:
		return "running"
	case inferencev1.InferenceJobStatus_INFERENCE_JOB_STATUS_SETTLING:
		return "settling"
	case inferencev1.InferenceJobStatus_INFERENCE_JOB_STATUS_COMPLETED:
		return "completed"
	case inferencev1.InferenceJobStatus_INFERENCE_JOB_STATUS_FAILED:
		return "failed"
	default:
		return "unspecified"
	}
}

// printInferenceJob renders a single inference job as JSON or key/value lines.
func printInferenceJob(w io.Writer, asJSON bool, j *inferencev1.InferenceJob) error {
	r := inferenceJobRow{
		ID:         j.GetId(),
		Buyer:      j.GetBuyer(),
		Provider:   j.GetProvider(),
		Model:      j.GetModel(),
		Status:     inferenceStatusString(j.GetStatus()),
		Units:      j.GetUnits(),
		Completion: j.GetCompletion(),
	}
	if asJSON {
		return printJSON(w, r)
	}
	fmt.Fprintf(w, "id:         %s\n", r.ID)
	fmt.Fprintf(w, "buyer:      %s\n", r.Buyer)
	fmt.Fprintf(w, "provider:   %s\n", r.Provider)
	fmt.Fprintf(w, "model:      %s\n", r.Model)
	fmt.Fprintf(w, "status:     %s\n", r.Status)
	fmt.Fprintf(w, "units:      %d\n", r.Units)
	fmt.Fprintf(w, "completion: %s\n", r.Completion)
	return nil
}
