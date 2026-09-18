package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	inferencev1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/inference/v1"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ecirlabs/matrix-core/internal/inference"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// defaultInferenceAddr is the node's default inference gRPC endpoint
// (node Inference.Addr). It is distinct from the market endpoint so the
// inference commands reach the InferenceService server.
const defaultInferenceAddr = "127.0.0.1:9092"

// defaultInferenceTimeout is how long `inference submit` waits when the caller
// says nothing, replacing the global per-RPC default for this one command.
//
// It matches the request_timeout a provider is shown advertising in the GPU
// provider overlay, because that is the side that knows how long its model
// takes and the buyer's deadline must not undercut it. A 27B reasoning model
// answering one question spends minutes producing working before a single
// completion token exists.
//
// It is a default and not a floor: --timeout still wins, in both directions.
const defaultInferenceTimeout = 10 * time.Minute

// inferenceTimeout resolves the deadline a run gets. An explicit --timeout is
// obeyed exactly, including a short one: a caller who says 5s is saying they
// would rather fail fast than wait, and silently lengthening it would make that
// unsayable. Only silence is replaced.
func inferenceTimeout(explicit bool, global time.Duration) time.Duration {
	if explicit {
		return global
	}
	return defaultInferenceTimeout
}

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
		newInferenceRecoverCommand(opts, &inferenceAddr),
	)
	return cmd
}

func newInferenceSubmitCommand(opts *globalOptions, inferenceAddr *string) *cobra.Command {
	var (
		buyer        string
		provider     string
		prompt       string
		model        string
		units        uint64
		fulfill      bool
		clientSigned bool
		escrowed     bool
		walletPath   string
		progress     bool
	)
	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit an inference job to a provider and run it",
		Long: `submit reserves capacity for an inference job on a provider and, by
default, immediately fulfills it so the completion and settled units are
returned in one command (reserve -> fulfill -> settle -> completed).

Pass --fulfill=false to only reserve the job (leaving it PENDING) and fulfill it
later via a separate call.

By default the NODE signs the payment, with a key it already holds for the buyer.
Pass --client-signed to pay with the local wallet's key instead: the node runs
the model, returns the exact transfer to sign and withholds the completion, this
command signs it, and the node settles and hands the completion over. That is
the path a public endpoint or a dApp uses, where the node holding your key would
make its operator a custodian of your balance.

Pass --escrowed to keep your key AND watch the answer arrive. The wallet funds
the reservation - units x price - before the model starts, so the provider
already holds the most the job can cost and the completion has nothing left to
withhold: it streams to stdout as it is produced. The settlement then names what
it really cost and consensus returns the change. Interrupting settles for what
arrived rather than forfeiting the reservation. It needs a node whose chain has
activated the inference-escrow rules.`,
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

			// Running a model is not a read, and the global default is sized for
			// reads. A buyer who says nothing about timeouts gets the inference
			// default here, and one who passes --timeout still wins.
			//
			// WHY THIS EXISTS. The buyer's deadline caps the whole call, including
			// the provider's own request_timeout - so the 60s that is generous for
			// a balance lookup silently overrode a provider advertising 10m, and a
			// reasoning model on a real GPU exceeded it every time. It surfaced as
			// "read provider stream: context deadline exceeded", which names the
			// provider's stream and reads as the provider's fault, when the limit
			// came from this side.
			opts.Timeout = inferenceTimeout(
				cmd.Root().PersistentFlags().Changed("timeout"), opts.Timeout)
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			if escrowed && clientSigned {
				return fmt.Errorf("--escrowed and --client-signed are two different ways to pay " +
					"without handing over a key; pick one")
			}
			if escrowed {
				job, err := runEscrowed(ctx, ic, *inferenceAddr, escrowedInput{
					buyer: buyer, provider: provider, model: model, prompt: prompt,
					units: units, walletPath: walletPath,
					out: cmd.OutOrStdout(), errOut: cmd.ErrOrStderr(),
				})
				if err != nil {
					return err
				}
				return printInferenceJob(cmd.OutOrStdout(), opts.JSON, job)
			}
			if clientSigned {
				job, err := runClientSigned(ctx, ic, *inferenceAddr, clientSignedInput{
					progress: progress,
					buyer:    buyer, provider: provider, model: model, prompt: prompt,
					units: units, walletPath: walletPath,
				})
				if err != nil {
					return err
				}
				return printInferenceJob(cmd.OutOrStdout(), opts.JSON, job)
			}

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
	cmd.Flags().BoolVar(&progress, "progress", true,
		"report how far the run has got while it works (client-signed only; no completion text travels)")
	cmd.Flags().BoolVar(&clientSigned, "client-signed", false,
		"pay with the local wallet's key instead of letting the node sign for you")
	cmd.Flags().BoolVar(&escrowed, "escrowed", false,
		"fund the reservation up front and stream the answer, paying with the local wallet's key")
	cmd.Flags().StringVar(&walletPath, "wallet", "",
		"wallet file to sign with when --client-signed is set (default ~/.matrix/wallet.json)")
	return cmd
}

// clientSignedInput is what the client-signed run needs, gathered so the flow
// below reads as the three steps it is.
type clientSignedInput struct {
	buyer, provider, model, prompt string
	units                          uint64
	walletPath                     string
	// progress asks the node to report how far the run has got while it works.
	// No completion text comes back on that stream - the node withholds it until
	// the payment is signed - so this changes what the wait LOOKS like and
	// nothing about what is delivered or charged.
	progress bool
}

// runClientSigned drives the path where the node holds no key: run, sign the
// invoice with the local wallet, settle.
//
// The wallet must be the buyer's own. Signing with a different key would produce
// a transfer that verifies and still be refused, because the node checks the
// signer against the buyer the job was submitted for - so this checks it here
// and says so plainly rather than letting the node answer with a mismatch.
func runClientSigned(ctx context.Context, ic *inferenceConn, addr string, in clientSignedInput) (*inferencev1.InferenceJob, error) {
	path, err := resolveWalletPath(in.walletPath)
	if err != nil {
		return nil, err
	}
	acct, err := loadWallet(path, passphrasePrompt(os.Stderr, "Passphrase for "+path))
	if err != nil {
		return nil, err
	}
	if acct.AccountID() != in.buyer {
		return nil, fmt.Errorf("the wallet at %s is account %s, but --buyer is %s: "+
			"--client-signed pays with the wallet's own key, so they must match",
			path, acct.AccountID(), in.buyer)
	}

	// Authorise the run before any work happens.
	//
	// WHY THIS IS NOT OPTIONAL. A node that serves this method without an API key
	// - which is what a public endpoint and a browser both need, since a page
	// cannot hold a key - has nothing but this signature to tell a real buyer
	// from a string. Without it anyone could name somebody else's funded account,
	// have a provider do the work, and never sign for it: the victim's balance is
	// untouched, and the PROVIDER works for free with its capacity held until the
	// payment request expires.
	//
	// It was missing here, which meant --client-signed only worked against a node
	// with signed_writes OFF - a node that takes `buyer` on trust, which is
	// exactly the configuration a public one must not run. So the path that
	// exists to avoid handing a node your key could not be used against any node
	// configured for buyers who do not.
	request := inference.InferenceRequest{Prompt: in.prompt, Model: in.model}
	auth := &inference.RunAuthorization{
		PublicKey: acct.PublicKey,
		Provider:  in.provider,
		Model:     in.model,
		Timestamp: time.Now().UTC().UnixNano(),
	}
	if err := auth.Sign(request, acct.PrivateKey); err != nil {
		return nil, fmt.Errorf("sign the run authorization: %w", err)
	}

	run := &inferencev1.RunInferenceJobRequest{
		Buyer:         in.buyer,
		Provider:      in.provider,
		Model:         in.model,
		Prompt:        in.prompt,
		UnitsEstimate: in.units,
		Authorization: &inferencev1.RunAuthorization{
			PublicKey: auth.PublicKey,
			Timestamp: auth.Timestamp,
			Signature: auth.Signature,
		},
	}

	pay, err := runShowingProgress(ctx, ic, addr, run, in.progress)
	if err != nil {
		return nil, err
	}

	// Sign exactly what was invoiced. Any drift in these fields is refused by the
	// node as a payment mismatch, which is the check that stops a buyer from
	// paying one base unit to an account they control.
	tx := &token.Transaction{
		From:      acct.PublicKey,
		To:        pay.GetTo(),
		Amount:    pay.GetAmount(),
		Nonce:     pay.GetNonce(),
		Timestamp: pay.GetTimestamp(),
		PrevHash:  pay.GetPrevHash(),
	}
	if err := tx.Sign(acct.PrivateKey); err != nil {
		return nil, fmt.Errorf("sign the payment for job %s: %w", pay.GetJobId(), err)
	}

	settled, err := ic.inference.SettleInferenceJob(ctx, &inferencev1.SettleInferenceJobRequest{
		Id:            pay.GetJobId(),
		FromPublicKey: acct.PublicKey,
		To:            tx.To,
		Amount:        tx.Amount,
		Nonce:         tx.Nonce,
		Timestamp:     tx.Timestamp,
		PrevHash:      tx.PrevHash,
		Signature:     tx.Signature,
	})
	if err != nil {
		return nil, mapErr(addr, err)
	}
	return settled.GetJob(), nil
}

// runShowingProgress performs the run, reporting how far it has got when asked.
//
// THE WAIT IS THE PROBLEM IT SOLVES, and the wait is real: the node reserves
// capacity, runs the whole model, and computes the charge before it returns
// anything at all. A reasoning model can spend a minute on working nobody is
// allowed to see yet, and from here that is indistinguishable from a node that
// has died - so the honest thing is to say how far it has got.
//
// No completion text arrives on this stream, by construction rather than by
// omission: the node withholds it until the payment is signed, and that
// withholding is the only enforcement on this path.
//
// Falling back is deliberate. A node too old to serve the progress stream still
// serves the plain run, and a buyer should get their answer from it rather than
// an error about a nicety.
func runShowingProgress(
	ctx context.Context,
	ic *inferenceConn,
	addr string,
	run *inferencev1.RunInferenceJobRequest,
	show bool,
) (*inferencev1.PaymentRequest, error) {
	if show {
		pay, err := streamRunProgress(ctx, ic, run)
		if err == nil {
			return pay, nil
		}
		if status.Code(err) != codes.Unimplemented {
			return nil, mapErr(addr, err)
		}
		fmt.Fprintln(os.Stderr, "  (this node does not report progress; waiting)")
	}

	runResp, err := ic.inference.RunInferenceJob(ctx, run)
	if err != nil {
		return nil, mapErr(addr, err)
	}
	pay := runResp.GetPayment()
	if pay == nil {
		return nil, fmt.Errorf("the node ran the job but returned no payment request")
	}
	return pay, nil
}

// streamRunProgress consumes the progress stream, redrawing one line, and
// returns the payment the final message carries.
func streamRunProgress(
	ctx context.Context,
	ic *inferenceConn,
	run *inferencev1.RunInferenceJobRequest,
) (*inferencev1.PaymentRequest, error) {
	stream, err := ic.inference.RunInferenceJobProgress(ctx,
		&inferencev1.RunInferenceJobProgressRequest{Run: run})
	if err != nil {
		return nil, err
	}

	// Progress goes to stderr, so piping stdout to a file still gets exactly the
	// job and nothing else.
	for {
		msg, err := stream.Recv()
		if err != nil {
			return nil, err
		}
		if result := msg.GetResult(); result != nil {
			if msg.GetStreamedOneShot() {
				fmt.Fprintf(os.Stderr, "\r  this provider cannot report progress, so the wait was opaque\n")
			} else {
				fmt.Fprintf(os.Stderr, "\r  %d tokens produced\n", msg.GetTokensSoFar())
			}
			pay := result.GetPayment()
			if pay == nil {
				return nil, fmt.Errorf("the node ran the job but returned no payment request")
			}
			return pay, nil
		}
		// \r and no newline: one line that counts up rather than a wall of them.
		fmt.Fprintf(os.Stderr, "\r  %d tokens...", msg.GetTokensSoFar())
	}
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
	// Reasoning is a reasoning model's working, empty for a model with none. It
	// is billed and therefore delivered; `matrix receipt verify --reasoning`
	// reads it back into the digest.
	Reasoning string `json:"reasoning,omitempty"`
	// Receipt is the serving node's signed account of what it charged, as the
	// exact bytes it signed. Carried verbatim because re-encoding it would
	// invalidate the signature, and because the buyer keeping those bytes is the
	// whole of what makes it evidence. `matrix receipt verify` reads it back.
	Receipt json.RawMessage `json:"receipt,omitempty"`
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
		Reasoning:  j.GetReasoning(),
		Receipt:    j.GetReceipt(),
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
	if r.Reasoning != "" {
		// Before the completion, which is the order it was produced in and the
		// order a reader wants it: the working, then the answer. It is printed
		// because it is billed - see the proto field's note.
		fmt.Fprintf(w, "reasoning:  %s\n", r.Reasoning)
	}
	fmt.Fprintf(w, "completion: %s\n", r.Completion)
	if len(r.Receipt) > 0 {
		// Printed in full rather than summarised: it is a signed document, and
		// what makes it worth anything is that the buyer keeps the exact bytes.
		// `matrix receipt verify` reads this back.
		fmt.Fprintf(w, "receipt:    %s\n", r.Receipt)
	}
	return nil
}
