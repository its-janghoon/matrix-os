package cli

import (
	"context"
	"fmt"
	"time"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"github.com/spf13/cobra"

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
0600 permissions. The private key is never printed or logged.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(
		newWalletCreateCommand(opts),
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
		Short: "Generate a new ed25519 wallet",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveWalletPath(walletPath)
			if err != nil {
				return err
			}
			acct, err := createWallet(path)
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
			fmt.Fprintf(out, "wallet created: %s\n", path)
			fmt.Fprintf(out, "account:        %s\n", acct.AccountID())
			return nil
		},
	}
	cmd.Flags().StringVar(&walletPath, "wallet", "", "wallet file path (default ~/.matrix/wallet.json)")
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
			acct, err := loadWallet(path)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if opts.JSON {
				return printJSON(out, struct {
					AccountID string `json:"account_id"`
					PublicKey string `json:"public_key"`
				}{AccountID: acct.AccountID(), PublicKey: acct.AccountID()})
			}
			fmt.Fprintf(out, "account:    %s\n", acct.AccountID())
			fmt.Fprintf(out, "public key: %s\n", acct.AccountID())
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
				acct, err := loadWallet(path)
				if err != nil {
					return err
				}
				acctID = acct.AccountID()
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
			acct, err := loadWallet(path)
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
				return mapErr(opts.Addr, err)
			}
			return printTransaction(cmd.OutOrStdout(), opts.JSON, resp.GetTransaction())
		},
	}
	cmd.Flags().StringVar(&walletPath, "wallet", "", "wallet file path (default ~/.matrix/wallet.json)")
	cmd.Flags().StringVar(&to, "to", "", "recipient account ID (required)")
	cmd.Flags().Uint64Var(&amount, "amount", 0, "amount in credits (required, > 0)")
	return cmd
}

// deriveNonce reads the consensus transaction history over gRPC to compute the
// sender's next transfer nonce (the count of the sender's prior committed
// transfers, since the nonce is a per-sender uniquifier). It pages through
// ListTransactions from index 0 so it works regardless of any server-side page
// cap.
//
// A signed transfer settles through consensus now, where replay protection
// comes from the engine's committed-transaction dedup set rather than a strict
// monotonic per-sender nonce. A count-derived nonce is still monotonic per
// sender, which keeps otherwise-identical repeated transfers distinct; a rare
// racing transfer that reuses a nonce is simply a distinct transaction (the
// timestamp differs) and both commit independently.
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
