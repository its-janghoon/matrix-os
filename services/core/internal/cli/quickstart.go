package cli

import (
	"fmt"
	"io"
	"os"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"github.com/spf13/cobra"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// Quickstart defaults. They are intentionally small, self-explanatory values so
// the demo loop is cheap and the arithmetic (units * price) is obvious in the
// printed summary.
const (
	// qsFundAmount is the native base units moved from the reward pool into the
	// buyer wallet at the start of the quickstart.
	qsFundAmount = 1_000_000
	// qsProviderID is the demo provider registered by the quickstart.
	qsProviderID = "quickstart-provider"
	// qsCapacity is the demo provider's advertised capacity in compute units.
	qsCapacity = 100
	// qsPrice is the demo provider's price per compute unit in native base units.
	qsPrice = 5
	// qsUnits is the number of compute units the demo job reserves.
	qsUnits = 10
)

// newQuickstartCommand builds `matrix quickstart`, a one-command zero-to-first-job
// flow against a local dev node started with `matrixd --init && matrixd`.
//
// It drives the node through the whole economic loop with sensible defaults so a
// brand-new user can copy one command and see native MATRIX actually move:
//
//  1. ensure a local wallet exists (reuses the ~/.matrix/wallet.json helpers;
//     creates one if absent) - this is the buyer account;
//  2. fund that wallet from the node's genesis reward pool via FundAccount
//     (the FEAT-002 path; no coins are minted);
//  3. register a demo compute provider on the order book;
//  4. submit a demo job (buyer buys qsUnits units at the provider's price);
//  5. complete/settle the job, transferring native MATRIX buyer -> provider;
//  6. print each step and the resulting balances, ending with a summary.
//
// Every step prints as it runs so the output doubles as a readable transcript.
// It reuses the same market RPCs the individual subcommands use, so what it
// demonstrates is exactly what `matrix fund`, `matrix provider register`,
// `matrix job submit`, and `matrix job complete` do.
func newQuickstartCommand(opts *globalOptions) *cobra.Command {
	var (
		walletPath string
		providerID string
		fundAmount uint64
		capacity   uint64
		price      uint64
		units      uint64
	)
	cmd := &cobra.Command{
		Use:   "quickstart",
		Short: "Zero-to-first-job demo loop against a local node",
		Long: `quickstart takes a fresh user from zero to a funded account and a first
completed compute job against a local node in one command.

Start a dev node first:

    matrixd --init && matrixd

then, in another shell:

    matrix quickstart

It will (1) create a local wallet if you do not have one, (2) fund it from the
node's genesis reward pool, (3) register a demo provider, (4) submit a demo job,
and (5) complete it so native MATRIX moves buyer -> provider, printing balances
at each step. Nothing is minted; funding moves reward-pool MATRIX through the
treasury. Re-running is safe: the wallet is reused and the provider re-registered.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			// 1. Ensure a local wallet (the buyer). Create one on first run,
			// otherwise reuse the existing key so re-running is idempotent.
			path, err := resolveWalletPath(walletPath)
			if err != nil {
				return err
			}
			// Whether to create is decided by the file's ABSENCE, not by a failed
			// load. Falling through on any error meant a wrong passphrase tried to
			// create a wallet, failed because the file existed, and reported
			// "could not load or create" - which sends you looking for a missing
			// file rather than at what you typed.
			var acct *token.Account
			if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
				passphrase, perr := newPassphrase(cmd.ErrOrStderr())
				if perr != nil {
					return perr
				}
				created, phrase, cerr := createEncryptedWallet(path, passphrase)
				if cerr != nil {
					return fmt.Errorf("could not create a wallet at %s: %w", path, cerr)
				}
				acct = created
				fmt.Fprintf(out, "1. wallet created at %s\n", path)
				fmt.Fprintf(out, "   recovery phrase (write it down, shown once): %s\n", phrase)
			} else {
				loaded, lerr := loadWallet(path, passphrasePrompt(cmd.ErrOrStderr(), "Passphrase for "+path))
				if lerr != nil {
					return lerr
				}
				acct = loaded
				fmt.Fprintf(out, "1. using existing wallet at %s\n", path)
			}
			buyer := acct.AccountID()
			fmt.Fprintf(out, "   buyer account: %s\n\n", buyer)

			cc, err := dial(opts)
			if err != nil {
				return err
			}
			defer cc.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			// 2. Fund the buyer from the genesis reward pool.
			fundResp, err := cc.market.FundAccount(ctx, &marketv1.FundAccountRequest{
				Account: buyer,
				Amount:  fundAmount,
			})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			fmt.Fprintf(out, "2. funded buyer with %d from the reward pool\n", fundAmount)
			fmt.Fprintf(out, "   buyer balance: %d\n\n", fundResp.GetBalance())

			// 3. Register a demo provider.
			provResp, err := cc.market.RegisterProvider(ctx, &marketv1.RegisterProviderRequest{
				Id:           providerID,
				Capacity:     capacity,
				PricePerUnit: price,
			})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			fmt.Fprintf(out, "3. registered provider %q (capacity %d, price %d/unit)\n\n",
				providerID, provResp.GetProvider().GetCapacity(), provResp.GetProvider().GetPricePerUnit())

			// 4. Submit a demo job: buyer buys `units` units from the provider.
			submitResp, err := cc.market.SubmitJob(ctx, &marketv1.SubmitJobRequest{
				Buyer:    buyer,
				Provider: providerID,
				Units:    units,
			})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			job := submitResp.GetJob()
			fmt.Fprintf(out, "4. submitted job %s: %d units, snapshotted total price %d (status %s)\n\n",
				job.GetId(), job.GetUnits(), job.GetPrice(), jobStatusString(job.GetStatus()))

			// 5. Complete/settle the job, transferring native MATRIX buyer -> provider.
			completeResp, err := cc.market.CompleteJob(ctx, &marketv1.CompleteJobRequest{Id: job.GetId()})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			done := completeResp.GetJob()
			fmt.Fprintf(out, "5. completed job %s (status %s)\n\n", done.GetId(), jobStatusString(done.GetStatus()))

			// Final balances, read back from the ledger to prove the settlement.
			buyerBal, err := cc.market.GetBalance(ctx, &marketv1.GetBalanceRequest{Account: buyer})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			provBal, err := cc.market.GetBalance(ctx, &marketv1.GetBalanceRequest{Account: providerID})
			if err != nil {
				return mapErr(opts.Addr, err)
			}

			return printQuickstartSummary(out, opts.JSON, quickstartResult{
				Buyer:           buyer,
				Provider:        providerID,
				Funded:          fundAmount,
				JobID:           done.GetId(),
				JobPrice:        done.GetPrice(),
				BuyerBalance:    buyerBal.GetBalance(),
				ProviderBalance: provBal.GetBalance(),
			})
		},
	}
	cmd.Flags().StringVar(&walletPath, "wallet", "", "wallet file path (default ~/.matrix/wallet.json)")
	cmd.Flags().StringVar(&providerID, "provider", qsProviderID, "demo provider ID to register")
	cmd.Flags().Uint64Var(&fundAmount, "fund", qsFundAmount, "native base units to fund the buyer from the reward pool")
	cmd.Flags().Uint64Var(&capacity, "capacity", qsCapacity, "demo provider capacity in compute units")
	cmd.Flags().Uint64Var(&price, "price", qsPrice, "demo provider price per compute unit")
	cmd.Flags().Uint64Var(&units, "units", qsUnits, "compute units the demo job reserves")
	return cmd
}

// quickstartResult is the machine-readable summary of a quickstart run.
type quickstartResult struct {
	Buyer           string `json:"buyer"`
	Provider        string `json:"provider"`
	Funded          uint64 `json:"funded"`
	JobID           string `json:"job_id"`
	JobPrice        uint64 `json:"job_price"`
	BuyerBalance    uint64 `json:"buyer_balance"`
	ProviderBalance uint64 `json:"provider_balance"`
}

// printQuickstartSummary prints the closing summary of a quickstart run as JSON
// or human-readable lines.
func printQuickstartSummary(w io.Writer, asJSON bool, r quickstartResult) error {
	if asJSON {
		return printJSON(w, r)
	}
	fmt.Fprintf(w, "done. native MATRIX moved buyer -> provider:\n")
	fmt.Fprintf(w, "  buyer    %s\t%d\n", r.Buyer, r.BuyerBalance)
	fmt.Fprintf(w, "  provider %s\t%d\n", r.Provider, r.ProviderBalance)
	fmt.Fprintf(w, "  job %s settled for %d\n", r.JobID, r.JobPrice)
	return nil
}
