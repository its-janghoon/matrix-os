package cli

import (
	"context"
	"fmt"
	"time"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// chainHashSize is the length of a chain link hash / genesis prev-hash.
const chainHashSize = 32

// newWalletCommand builds `matrix wallet` with create/show/balance/transfer.
func newWalletCommand(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wallet",
		Short: "Manage a local ed25519 wallet and sign native transfers",
		Long: `Manage a local ed25519 wallet used to sign native MATRIX transfers.

The wallet is stored at ~/.matrix/wallet.json (overridable with --wallet) with
0600 permissions, ENCRYPTED under a passphrase you choose. The private key is
never printed or logged.

"wallet create" also shows a 12-word BIP-39 recovery phrase, once. That phrase is the
only way to restore the account if the file is lost, and it is derived at the
standard SLIP-0010 ed25519 path (m/44'/9004'/0'/0'), so it also restores in
other wallets that implement it.

A passphrase is read from MATRIX_WALLET_PASSPHRASE when set, otherwise typed at
a prompt without being echoed. Commands that only need the public account id
(show, balance) do not ask for it at all.

Wallets created before encryption existed are still read as they are, with a
warning, because there is no migration a tool can do on your behalf: use
"wallet import" with the recovery phrase, or move value to a new wallet.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(
		newWalletCreateCommand(opts),
		newWalletImportCommand(opts),
		newWalletShowCommand(opts),
		newWalletBalanceCommand(opts),
		newWalletTransferCommand(opts),
	)
	return cmd
}

func newWalletCreateCommand(opts *globalOptions) *cobra.Command {
	var walletPath string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Generate a new encrypted wallet and show its recovery phrase",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveWalletPath(walletPath)
			if err != nil {
				return err
			}
			passphrase, err := newPassphrase(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			acct, phrase, err := createEncryptedWallet(path, passphrase)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if opts.JSON {
				// The phrase is in the JSON because a caller asking for machine
				// output has nowhere else to get it, and it exists exactly once.
				// A caller that pipes this to a log has published its key.
				return printJSON(out, struct {
					Path           string `json:"path"`
					AccountID      string `json:"account_id"`
					RecoveryPhrase string `json:"recovery_phrase"`
				}{Path: path, AccountID: acct.AccountID(), RecoveryPhrase: phrase})
			}
			fmt.Fprintf(out, "wallet created: %s\n", path)
			fmt.Fprintf(out, "account:        %s\n", acct.AccountID())
			fmt.Fprintf(out, "\nRecovery phrase (write this down; it is shown once and never again):\n\n  %s\n\n", phrase)
			fmt.Fprintf(out, "Anyone with that phrase owns this account. Anyone without it, including us,\n")
			fmt.Fprintf(out, "cannot restore it for you.\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&walletPath, "wallet", "", "wallet file path (default ~/.matrix/wallet.json)")
	return cmd
}

func newWalletImportCommand(opts *globalOptions) *cobra.Command {
	var (
		walletPath string
		phrase     string
	)
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Restore a wallet from its recovery phrase",
		Long: `import restores the account a 12- or 24-word BIP-39 phrase derives at
m/44'/9004'/0'/0', and writes it as an encrypted wallet.

Pass --mnemonic to supply the phrase, or leave it off to be asked. Being asked
is better: a phrase on the command line ends up in your shell history.

The phrase is checked against its BIP-39 checksum before anything is written, so
a mistyped word is a refusal rather than a different, empty account.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveWalletPath(walletPath)
			if err != nil {
				return err
			}
			if phrase == "" {
				phrase, err = readMnemonic(cmd.ErrOrStderr(), cmd.InOrStdin())
				if err != nil {
					return err
				}
			}
			if err := token.ValidateMnemonic(phrase); err != nil {
				return fmt.Errorf("%w: check the words and their order", err)
			}
			passphrase, err := newPassphrase(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			acct, err := importEncryptedWallet(path, phrase, passphrase)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if opts.JSON {
				return printJSON(out, struct {
					Path      string `json:"path"`
					AccountID string `json:"account_id"`
				}{Path: path, AccountID: acct.AccountID()})
			}
			fmt.Fprintf(out, "wallet restored: %s\n", path)
			fmt.Fprintf(out, "account:         %s\n", acct.AccountID())
			return nil
		},
	}
	cmd.Flags().StringVar(&walletPath, "wallet", "", "wallet file path (default ~/.matrix/wallet.json)")
	cmd.Flags().StringVar(&phrase, "mnemonic", "",
		"the recovery phrase (omit to be asked, which keeps it out of your shell history)")
	return cmd
}

func newWalletShowCommand(opts *globalOptions) *cobra.Command {
	var walletPath string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the wallet's account ID / public key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveWalletPath(walletPath)
			if err != nil {
				return err
			}
			// No unlock: the id is public, and asking for a passphrase to print
			// your own address teaches people to type it reflexively.
			accountID, err := walletAccountID(path)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if opts.JSON {
				return printJSON(out, struct {
					AccountID string `json:"account_id"`
					PublicKey string `json:"public_key"`
				}{AccountID: accountID, PublicKey: accountID})
			}
			fmt.Fprintf(out, "account:    %s\n", accountID)
			fmt.Fprintf(out, "public key: %s\n", accountID)
			return nil
		},
	}
	cmd.Flags().StringVar(&walletPath, "wallet", "", "wallet file path (default ~/.matrix/wallet.json)")
	return cmd
}

func newWalletBalanceCommand(opts *globalOptions) *cobra.Command {
	var (
		walletPath string
		account    string
	)
	cmd := &cobra.Command{
		Use:   "balance",
		Short: "Read the wallet's (or an explicit account's) balance",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			acctID := account
			if acctID == "" {
				path, err := resolveWalletPath(walletPath)
				if err != nil {
					return err
				}
				// A balance is a read of a public account; no unlock needed.
				acctID, err = walletAccountID(path)
				if err != nil {
					return err
				}
			}
			cc, err := dial(opts)
			if err != nil {
				return err
			}
			defer cc.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := cc.market.GetBalance(ctx, &marketv1.GetBalanceRequest{Account: acctID})
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			return printBalance(cmd.OutOrStdout(), opts.JSON, resp.GetAccount(), resp.GetBalance())
		},
	}
	cmd.Flags().StringVar(&walletPath, "wallet", "", "wallet file path (default ~/.matrix/wallet.json)")
	cmd.Flags().StringVar(&account, "account", "", "explicit account ID to query instead of the wallet")
	return cmd
}

func newWalletTransferCommand(opts *globalOptions) *cobra.Command {
	var (
		walletPath string
		to         string
		amount     uint64
	)
	cmd := &cobra.Command{
		Use:   "transfer",
		Short: "Sign and submit a native MATRIX transfer to a recipient",
		Long: `Build, sign, and submit a native MATRIX transfer.

The transfer settles through consensus: it is signed locally, submitted via
SubmitSignedTransfer, ordered into a committed block by a quorum, and applied by
every node, so all nodes agree on the resulting balances and the transfer pays
the protocol fee (default off) like every other committed transfer. The command
derives the sender's next transfer nonce from the consensus transaction history
over gRPC (a uniquifier; consensus dedups committed transfers for replay
protection), signs the canonical transaction payload with the wallet's ed25519
private key, and waits for the settlement to commit. The private key never
leaves the local wallet.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" {
				return fmt.Errorf("--to is required")
			}
			if amount == 0 {
				return fmt.Errorf("--amount must be greater than 0")
			}
			path, err := resolveWalletPath(walletPath)
			if err != nil {
				return err
			}
			acct, err := loadWallet(path, passphrasePrompt(cmd.ErrOrStderr(), "Passphrase for "+path))
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

			nonce, err := deriveNonce(ctx, cc.market, acct.AccountID())
			if err != nil {
				return mapErr(opts.Addr, err)
			}

			tx := &token.Transaction{
				From:      acct.PublicKey,
				To:        to,
				Amount:    amount,
				Nonce:     nonce,
				Timestamp: time.Now().UnixNano(),
				// prev_hash is unused for consensus ledger linkage; a stable zero
				// seed keeps the canonical signing bytes well-formed and matches
				// what consensus.SubmitTransfer signs server-side.
				PrevHash: make([]byte, chainHashSize),
			}
			if err := tx.Sign(acct.PrivateKey); err != nil {
				return fmt.Errorf("failed to sign transfer: %w", err)
			}

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
				return transferSubmitError(opts, tx, err)
			}
			return printTransaction(cmd.OutOrStdout(), opts.JSON, resp.GetTransaction())
		},
	}
	cmd.Flags().StringVar(&walletPath, "wallet", "", "wallet file path (default ~/.matrix/wallet.json)")
	cmd.Flags().StringVar(&to, "to", "", "recipient account ID (required)")
	cmd.Flags().Uint64Var(&amount, "amount", 0, "native MATRIX base units to transfer (required, > 0)")
	return cmd
}

// deriveNonce reads the consensus transaction history over gRPC to compute the
// sender's next transfer nonce (the count of the sender's prior committed
// transfers, since the nonce is a per-sender uniquifier). It pages through
// ListTransactions from index 0 so it works regardless of any server-side page
// cap.
//
// A count-derived nonce is monotonic per sender, which keeps otherwise-identical
// repeated transfers distinct.
//
// WHAT THIS COMMENT USED TO SAY, AND WHY IT WAS WRONG. It said "a rare racing
// transfer that reuses a nonce is simply a distinct transaction (the timestamp
// differs) and both commit independently". That was true of the code and it was
// a double payment: the count only advances when a transfer COMMITS, so a
// second transfer signed while the first is still pending reads the same nonce,
// and both used to commit and both moved money. Consensus now refuses a second,
// different transfer at a nonce the sender has spent or has pending
// (consensus.ErrNonceAlreadyUsed), so the reuse is an error instead of a second
// payment - which is what makes the deadline case below safe to retry.
func deriveNonce(ctx context.Context, client marketv1.MarketServiceClient, senderID string) (nonce uint64, err error) {
	var (
		start     uint64
		senderTxs uint64
		total     uint64
		seen      uint64
	)
	for {
		resp, err := client.ListTransactions(ctx, &marketv1.ListTransactionsRequest{StartIndex: start})
		if err != nil {
			return 0, err
		}
		total = resp.GetTotal()
		txs := resp.GetTransactions()
		if len(txs) == 0 {
			break
		}
		for _, t := range txs {
			if t.GetFrom() == senderID {
				senderTxs++
			}
			seen++
		}
		// Advance past the highest index returned.
		start = txs[len(txs)-1].GetIndex() + 1
		if seen >= total {
			break
		}
	}
	return senderTxs, nil
}

// transferSubmitError turns a SubmitSignedTransfer failure into a message that
// says what actually happened to the money.
//
// WHY THIS EXISTS. A DeadlineExceeded here does NOT mean the transfer failed. It
// means the client stopped waiting: the transfer is signed, submitted, and in
// the mempool, and it commits when the next block carrying it does. Reporting it
// as a bare "DeadlineExceeded: context deadline exceeded" made a committed
// transfer look failed, and the natural response - run it again - used to sign a
// SECOND transfer at the same nonce, because the nonce is derived from committed
// transfers and nothing had committed yet. Both then committed and the sender
// paid twice.
//
// Consensus now refuses the second transfer, so the double payment is gone. This
// removes the reason to attempt it: the message names the nonce, says the
// transfer may still commit, and says how to check rather than inviting a
// re-run.
func transferSubmitError(opts *globalOptions, tx *token.Transaction, err error) error {
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.DeadlineExceeded {
		return mapErr(opts.Addr, err)
	}
	return fmt.Errorf(`the transfer was submitted but did not commit within %s, so this command stopped waiting.
It was NOT rejected: it is signed and in the node's mempool, and it commits when a block carrying it does.

  from   %s
  to     %s
  amount %d
  nonce  %d

Do NOT run this command again to "retry" - that signs a different transfer, and
the node will refuse it because this nonce is already pending. Instead check
whether it committed:

  matrix tx list --addr %s

and if it is there, it is done. If the network is simply slower than the default
wait, use a longer --timeout`,
		effectiveTimeout(opts), tx.SenderID(), tx.To, tx.Amount, tx.Nonce, opts.Addr)
}

// effectiveTimeout reports the wait this invocation actually used, so the
// message quotes the real number rather than the default.
func effectiveTimeout(opts *globalOptions) time.Duration {
	if opts.Timeout > 0 {
		return opts.Timeout
	}
	return defaultTimeout
}
