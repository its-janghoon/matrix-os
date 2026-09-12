package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ecirlabs/matrix-core/internal/inference"
)

// Checking a seller's signed account of what it charged.
//
// This command talks to nothing. No node, no chain, no account - it reads a
// receipt and the text of the exchange and tells you whether the signature
// holds. That is the point of it: a receipt is only useful as evidence if the
// person you show it to can check it without asking the seller, or us, for
// anything.

func newReceiptCommand(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "receipt",
		Short: "Verify a provider's signed account of what it charged",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newReceiptVerifyCommand(opts))
	return cmd
}

func newReceiptVerifyCommand(opts *globalOptions) *cobra.Command {
	var (
		path       string
		promptPath string
		outputPath string
		buyer      string
	)
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check a receipt's signature, arithmetic, and that it covers this exchange",
		Long: `verify reads a receipt and reports whether it holds up.

It contacts nothing. A receipt is evidence only if a third party can check it
holding the document and the text of the exchange and nothing else - no node, no
chain, no account - so that is all this needs.

Three things are checked, and they answer different questions:

  SIGNATURE   the node named on the receipt is the one that signed it, so the
              claims are attributable and cannot later be disowned
  ARITHMETIC  the total follows from the units and the price, which is the
              simplest overcharge there is and the one a signed document nobody
              read closely would hide
  EXCHANGE    with --prompt and --completion, that this receipt was issued over
              THAT request and no other, so it cannot be a copy of an honest one

What it cannot tell you is whether the seller told the truth. No signature can.
If the receipt says llama-3.3-70b and the answer reads like a much smaller model,
this command will confirm the seller signed that claim - which is the thing you
need in order to take it anywhere.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if path == "" {
				return fmt.Errorf("--receipt is required")
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read receipt: %w", err)
			}
			receipt, err := inference.ParseReceipt(body)
			if err != nil {
				return err
			}

			if err := receipt.Verify(); err != nil {
				return err
			}

			// Bound to an exchange only when the caller supplies one. Without it
			// the receipt is a valid signed claim about SOME request, which is
			// worth saying plainly rather than implying more.
			boundToExchange := false
			if promptPath != "" || outputPath != "" {
				if promptPath == "" || outputPath == "" {
					return fmt.Errorf("--prompt and --completion go together: " +
						"binding a receipt to an exchange needs both halves of it")
				}
				prompt, err := os.ReadFile(promptPath)
				if err != nil {
					return fmt.Errorf("read prompt: %w", err)
				}
				completion, err := os.ReadFile(outputPath)
				if err != nil {
					return fmt.Errorf("read completion: %w", err)
				}
				req := inference.InferenceRequest{Prompt: string(prompt), Model: receipt.Model}
				if err := receipt.VerifyFor(buyer, req, string(completion)); err != nil {
					return err
				}
				boundToExchange = true
			} else if buyer != "" && receipt.Buyer != buyer {
				return fmt.Errorf("receipt is for buyer %s, not %s", receipt.Buyer, buyer)
			}

			if opts.JSON {
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"valid":             true,
					"bound_to_exchange": boundToExchange,
					"receipt":           json.RawMessage(body),
				})
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "signature:  valid, signed by node %s\n", receipt.NodeID)
			fmt.Fprintf(out, "arithmetic: %d units at %d each is %d, as charged\n",
				receipt.Units, receipt.PricePerUnit, receipt.Total)
			if boundToExchange {
				fmt.Fprintln(out, "exchange:   this receipt was issued over the prompt and completion given")
			} else {
				fmt.Fprintln(out, "exchange:   NOT CHECKED - pass --prompt and --completion to bind this")
				fmt.Fprintln(out, "            receipt to one request. Without it, it is a valid claim")
				fmt.Fprintln(out, "            about some exchange, not proof it was yours.")
			}
			fmt.Fprintln(out)
			fmt.Fprintf(out, "the seller signed this claim:\n")
			fmt.Fprintf(out, "  model served:     %s\n", receipt.Model)
			fmt.Fprintf(out, "  tokens reported:  %d prompt + %d completion = %d\n",
				receipt.PromptTokens, receipt.CompletionTokens, receipt.TotalTokens)
			fmt.Fprintf(out, "  units billed:     %d\n", receipt.Units)
			fmt.Fprintf(out, "  paid to:          %s\n", receipt.Provider)
			fmt.Fprintln(out)
			fmt.Fprintln(out, "A valid signature is not proof the claim is true. It is proof the seller")
			fmt.Fprintln(out, "made it, which is what you need to take it anywhere.")
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "receipt", "", "path to the receipt JSON (required)")
	cmd.Flags().StringVar(&promptPath, "prompt", "", "file holding the prompt that was sent")
	cmd.Flags().StringVar(&outputPath, "completion", "", "file holding the completion that came back")
	cmd.Flags().StringVar(&buyer, "buyer", "", "account the receipt must name as the buyer")
	return cmd
}
