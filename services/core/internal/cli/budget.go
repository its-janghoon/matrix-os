package cli

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"github.com/spf13/cobra"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// Operating a spend budget from a terminal.
//
// A budget is what lets a key that holds nothing spend a bounded amount of
// somebody else's money: the owner signs ONE transfer into an account whose name
// carries the terms, and a delegate draws on it until it runs out or expires.
// The browser does this so a reader is not asked to approve every message; a
// script does it so a long-running agent needs no wallet passphrase in its
// environment, only a delegate key that can spend a day's budget on inference.
//
// The terms are the account's name, which has one consequence worth saying out
// loud in the help: LOSE THE NAME AND YOU CANNOT CLOSE THE BUDGET. It is
// recoverable - the deposit is in the owner's own transaction history - but it
// is not memorable, so `open` prints it and says to keep it.
func newBudgetCommand(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "budget",
		Short: "Open, inspect and close a spend budget a delegate key can draw on",
	}
	cmd.AddCommand(
		newBudgetOpenCommand(opts),
		newBudgetShowCommand(opts),
		newBudgetCloseCommand(opts),
	)
	return cmd
}

// budgetTerms is the flag set shared by open and close, so the same budget is
// spelled the same way by both.
type budgetTerms struct {
	delegate  string
	perJobCap uint64
	maxPrice  uint64
	ttl       time.Duration
	nonce     uint64
}

func (b *budgetTerms) bind(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&b.delegate, "delegate", "", "the delegate's account id: a 64-hex ed25519 public key")
	f.Uint64Var(&b.perJobCap, "per-job-cap", 0, "the most any single draw may take, in base units (required)")
	f.Uint64Var(&b.maxPrice, "max-price-per-unit", 0, "refuse a seller dearer than this, in base units (required)")
	f.DurationVar(&b.ttl, "ttl", 24*time.Hour, "how long the budget may be drawn on")
	f.Uint64Var(&b.nonce, "budget-nonce", 0, "distinguishes two budgets with otherwise identical terms")
}

func newBudgetOpenCommand(opts *globalOptions) *cobra.Command {
	var (
		walletPath string
		amount     uint64
		terms      budgetTerms
	)
	cmd := &cobra.Command{
		Use:   "open",
		Short: "Fund a budget a delegate key may spend on inference",
		Long: `open signs ONE transfer into a budget account and prints its name.

The budget account's NAME carries the terms: who owns it, which key may draw on
it, the most a single draw may take, the price ceiling, and when it dies. That
is why one signature is enough - a transfer signs its recipient, so signing the
deposit is signing the terms.

KEEP THE NAME IT PRINTS. Closing the budget means naming it, and it is not
memorable. It is recoverable from the owner's own transaction history, where the
deposit appears like any other payment, but that is a search and this is a
copy-paste.

What a delegate can do with the key is bounded by what is in that name, and by
the balance: it may pay sellers for inference, up to the per-job cap, until the
expiry, and it may not move the money anywhere else. What it CANNOT do is what
makes this different from handing someone a wallet.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if amount == 0 {
				return fmt.Errorf("--amount must be greater than 0")
			}
			if terms.delegate == "" {
				return fmt.Errorf("--delegate is required: the account id of the key that may draw")
			}
			if _, err := token.ParsePublicKeyHex(terms.delegate); err != nil {
				return fmt.Errorf("--delegate %q is not an account id: %w", terms.delegate, err)
			}
			if terms.perJobCap == 0 || terms.maxPrice == 0 {
				return fmt.Errorf("--per-job-cap and --max-price-per-unit are both required; a bound " +
					"left unset would be a budget with no bound, which is a blank cheque written by accident")
			}
			if terms.ttl <= 0 {
				return fmt.Errorf("--ttl must be positive")
			}

			path, err := resolveWalletPath(walletPath)
			if err != nil {
				return err
			}
			acct, err := loadWallet(path, passphrasePrompt(cmd.ErrOrStderr(), "Passphrase for "+path))
			if err != nil {
				return err
			}

			budget := token.SpendEscrow{
				Buyer:           acct.AccountID(),
				Delegate:        terms.delegate,
				PerJobCap:       terms.perJobCap,
				MaxPricePerUnit: terms.maxPrice,
				Expiry:          time.Now().Add(terms.ttl).Unix(),
				Nonce:           terms.nonce,
			}
			if _, err := token.ParseSpendEscrow(budget.Account()); err != nil {
				return fmt.Errorf("these terms do not make a valid budget: %w", err)
			}

			tx, err := submitBudgetTransfer(cmd, opts, acct, budget.Account(), amount)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if opts.JSON {
				return printJSON(out, struct {
					Account string `json:"account"`
					Amount  uint64 `json:"amount"`
					Expires string `json:"expires"`
					TxIndex uint64 `json:"tx_index"`
				}{budget.Account(), amount, time.Unix(budget.Expiry, 0).UTC().Format(time.RFC3339), tx.GetIndex()})
			}
			fmt.Fprintf(out, "budget:  %s\n", budget.Account())
			fmt.Fprintf(out, "funded:  %d base units\n", amount)
			fmt.Fprintf(out, "expires: %s\n", time.Unix(budget.Expiry, 0).UTC().Format(time.RFC3339))
			fmt.Fprintf(out, "\nKeep the budget line. Closing it means naming it.\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&walletPath, "wallet", "", "wallet file path (default ~/.matrix/wallet.json)")
	cmd.Flags().Uint64Var(&amount, "amount", 0, "base units to put in the budget (required, > 0)")
	terms.bind(cmd)
	return cmd
}

func newBudgetShowCommand(opts *globalOptions) *cobra.Command {
	var account string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Read what is left in a budget and the terms in its name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if account == "" {
				return fmt.Errorf("--account is required")
			}
			terms, err := token.ParseSpendEscrow(account)
			if err != nil {
				return err
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
			expiry := time.Unix(terms.Expiry, 0).UTC()

			out := cmd.OutOrStdout()
			if opts.JSON {
				return printJSON(out, struct {
					Account         string `json:"account"`
					Remaining       uint64 `json:"remaining"`
					Buyer           string `json:"buyer"`
					Delegate        string `json:"delegate"`
					PerJobCap       uint64 `json:"per_job_cap"`
					MaxPricePerUnit uint64 `json:"max_price_per_unit"`
					Expires         string `json:"expires"`
					Expired         bool   `json:"expired"`
				}{account, resp.GetBalance(), terms.Buyer, terms.Delegate, terms.PerJobCap,
					terms.MaxPricePerUnit, expiry.Format(time.RFC3339), !time.Now().Before(expiry)})
			}
			fmt.Fprintf(out, "remaining:    %d base units\n", resp.GetBalance())
			fmt.Fprintf(out, "owner:        %s\n", terms.Buyer)
			fmt.Fprintf(out, "delegate:     %s\n", terms.Delegate)
			fmt.Fprintf(out, "per-job cap:  %d\n", terms.PerJobCap)
			fmt.Fprintf(out, "price ceiling %d per unit\n", terms.MaxPricePerUnit)
			fmt.Fprintf(out, "expires:      %s", expiry.Format(time.RFC3339))
			if !time.Now().Before(expiry) {
				// Said rather than inferred from a date in the past: an expired
				// budget still holds its balance, and the thing to do about it
				// is close it.
				fmt.Fprintf(out, "  (EXPIRED - nothing can be drawn; close it to get the rest back)")
			}
			fmt.Fprintln(out)
			return nil
		},
	}
	cmd.Flags().StringVar(&account, "account", "", "the budget account, as `budget open` printed it (required)")
	return cmd
}

func newBudgetCloseCommand(opts *globalOptions) *cobra.Command {
	var (
		walletPath string
		account    string
	)
	cmd := &cobra.Command{
		Use:   "close",
		Short: "Return whatever is left in a budget to its owner",
		Long: `close returns the budget's remaining balance to the account that opened it.

Before the expiry only the owner may close a budget, and that is what revoking a
delegate IS: the key keeps working until the money is gone. At and after the
expiry anyone may close it, because by then it is not a decision - the money is
owed back - and that is what stops a balance being stranded by an owner who lost
interest or lost their key.

It carries no amount. The amount is the whole remaining balance and is not the
caller's to choose, exactly as a bond withdrawal's is not.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if account == "" {
				return fmt.Errorf("--account is required")
			}
			budget, err := token.ParseSpendEscrow(account)
			if err != nil {
				return err
			}
			path, err := resolveWalletPath(walletPath)
			if err != nil {
				return err
			}
			acct, err := loadWallet(path, passphrasePrompt(cmd.ErrOrStderr(), "Passphrase for "+path))
			if err != nil {
				return err
			}
			// Said here rather than left to a refusal from the node, because the
			// node's refusal is a skipped transaction that reads like nothing
			// happening.
			if acct.AccountID() != budget.Buyer && time.Now().Before(time.Unix(budget.Expiry, 0)) {
				return fmt.Errorf("this budget belongs to %s and does not expire until %s; "+
					"only its owner may close it before then",
					budget.Buyer, time.Unix(budget.Expiry, 0).UTC().Format(time.RFC3339))
			}

			tx, err := submitBudgetTransfer(cmd, opts, acct, budget.CloseRecipient(), 0)
			if err != nil {
				return err
			}
			return printTransaction(cmd.OutOrStdout(), opts.JSON, tx)
		},
	}
	cmd.Flags().StringVar(&walletPath, "wallet", "", "wallet file path (default ~/.matrix/wallet.json)")
	cmd.Flags().StringVar(&account, "account", "", "the budget account to close (required)")
	return cmd
}

// submitBudgetTransfer signs and submits one of the budget operations.
//
// The nonce is RANDOM rather than counted, for the reason the bridge lock's is:
// a budget operation is a reserved recipient, so it does not appear in the
// counted transfer history the ordinary nonce derivation reads, and a counter
// that never advanced would sign every operation at the same nonce. Consensus
// holds these to the sender-nonce uniqueness rule, so a collision is a refusal
// rather than a double spend - but a random 64-bit value makes one vanishingly
// unlikely in the first place.
func submitBudgetTransfer(cmd *cobra.Command, opts *globalOptions, acct *token.Account, to string, amount uint64) (*marketv1.Transaction, error) {
	nonce, err := randomNonce()
	if err != nil {
		return nil, err
	}
	tx := &token.Transaction{
		From:      acct.PublicKey,
		To:        to,
		Amount:    amount,
		Nonce:     nonce,
		Timestamp: time.Now().UnixNano(),
		PrevHash:  make([]byte, chainHashSize),
	}
	if err := tx.Sign(acct.PrivateKey); err != nil {
		return nil, fmt.Errorf("failed to sign: %w", err)
	}

	cc, err := dial(opts)
	if err != nil {
		return nil, err
	}
	defer cc.Close()
	ctx, cancel := callContext(cmd.Context(), opts)
	defer cancel()

	resp, err := cc.market.SubmitSignedTransfer(ctx, &marketv1.SubmitSignedTransferRequest{
		FromPublicKey: token.MarshalPublicKey(acct.PublicKey),
		To:            tx.To,
		Amount:        tx.Amount,
		Nonce:         tx.Nonce,
		PrevHash:      tx.PrevHash,
		Signature:     tx.Signature,
		Timestamp:     tx.Timestamp,
	})
	if err != nil {
		return nil, mapErr(opts.Addr, err)
	}
	return resp.GetTransaction(), nil
}

func randomNonce() (uint64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, fmt.Errorf("could not read a random nonce: %w", err)
	}
	return binary.BigEndian.Uint64(b[:]), nil
}
