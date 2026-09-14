package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"

	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// `matrix stake`: bonding, withdrawing, and seeing where a bond stands.
//
// WHY THIS EXISTS. A validator could bond - the node does it itself from
// stake.bond - and could not get the bond back. Engine.SubmitWithdrawBond was
// reachable from Go and from no RPC or command, and the obvious workaround did
// not work either: a withdrawal must carry NO amount, and `matrix wallet
// transfer` refuses --amount 0. So an operator could put a million MATRIX at
// risk and had no way to ever take it out, which on a network anyone may join is
// a reason not to join.
//
// Nothing new was needed on the wire. A stake operation is a transfer to a
// reserved recipient, so these commands sign exactly what `wallet transfer`
// signs and submit it through the same SubmitSignedTransfer; and a bond is a
// balance in a reserved account, so `status` is a balance read. The gap was
// never a missing mechanism, only a missing door.

func newStakeCommand(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stake",
		Short: "Bond native MATRIX as a validator, withdraw it, or see where it stands",
		Long: `Manage this wallet's bonded stake.

Voting power is bonded stake, so a quorum costs two thirds of everything bonded
however many identities it is spread across, and a provable offence is answered
by taking the bond. Bonding is open to anyone: it is putting your own coins
somewhere you cannot spend them from, which needs no permission.

Withdrawing is gated on being out of the validator set for the unbonding period.
That delay is the whole reason a bond deters anything - without it a validator
could equivocate, be ejected, and withdraw before the evidence committed.`,
	}
	cmd.AddCommand(
		newStakeBondCommand(opts),
		newStakeWithdrawCommand(opts),
		newStakeStatusCommand(opts),
	)
	return cmd
}

func newStakeBondCommand(opts *globalOptions) *cobra.Command {
	var (
		walletPath string
		amount     uint64
	)
	cmd := &cobra.Command{
		Use:   "bond",
		Short: "Lock native MATRIX from this wallet as bonded stake",
		Long: `Move native MATRIX from this wallet's spendable balance into its bond.

The bond is an ordinary balance in a reserved account the wallet cannot spend
from, so the coins leave circulation without any second accounting system and
total supply is conserved by construction.

Bonding is what admission to the validator set costs. A node configured with
stake.bond tops its own bond up from the account it holds; this command is for
bonding from a wallet you hold yourself instead.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if amount == 0 {
				return fmt.Errorf("--amount must be greater than 0")
			}
			acct, err := loadStakeWallet(cmd, walletPath)
			if err != nil {
				return err
			}
			return submitStakeOperation(cmd, opts, acct,
				consensus.BondAccount(acct.AccountID()), amount)
		},
	}
	cmd.Flags().StringVar(&walletPath, "wallet", "", "wallet file path (default ~/.matrix/wallet.json)")
	cmd.Flags().Uint64Var(&amount, "amount", 0, "native MATRIX base units to bond (required, > 0)")
	return cmd
}

func newStakeWithdrawCommand(opts *globalOptions) *cobra.Command {
	var walletPath string
	cmd := &cobra.Command{
		Use:   "withdraw",
		Short: "Return this wallet's whole bond to its spendable balance",
		Long: `Ask for the whole bond back.

It carries no amount: a withdrawal returns everything, because a partial one
would let a validator shed stake down to nothing while still voting with the
power its earlier bond bought.

It is valid only once the account is OUT of the validator set and its unbonding
period has elapsed. Submitted earlier, the transaction may sit in the mempool
until then and land by itself, which is the intended behaviour rather than a
failure - leave the set first (participate_in_open_set: false, or
` + "`matrix stake withdraw`" + ` after the node has exited) and the withdrawal
commits when the clock allows it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			acct, err := loadStakeWallet(cmd, walletPath)
			if err != nil {
				return err
			}
			// Zero on purpose. The amount is not the caller's to choose, and
			// consensus refuses a withdrawal that carries one.
			return submitStakeOperation(cmd, opts, acct,
				consensus.WithdrawRecipient(acct.AccountID()), 0)
		},
	}
	cmd.Flags().StringVar(&walletPath, "wallet", "", "wallet file path (default ~/.matrix/wallet.json)")
	return cmd
}

func newStakeStatusCommand(opts *globalOptions) *cobra.Command {
	var (
		walletPath string
		account    string
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show an account's bonded and spendable native MATRIX",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := account
			if id == "" {
				acct, err := loadStakeWallet(cmd, walletPath)
				if err != nil {
					return err
				}
				id = acct.AccountID()
			}

			cc, err := dial(opts)
			if err != nil {
				return err
			}
			defer cc.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			// A bond IS a balance in a reserved account, so both numbers come from
			// the same read and no separate stake RPC is needed.
			spendable, err := readBalance(ctx, cc.market, id)
			if err != nil {
				return mapErr(opts.Addr, err)
			}
			bonded, err := readBalance(ctx, cc.market, consensus.BondAccount(id))
			if err != nil {
				return mapErr(opts.Addr, err)
			}

			if opts.JSON {
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"account":   id,
					"spendable": spendable,
					"bonded":    bonded,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "account:   %s\n", id)
			fmt.Fprintf(cmd.OutOrStdout(), "spendable: %d native base units\n", spendable)
			fmt.Fprintf(cmd.OutOrStdout(), "bonded:    %d native base units\n", bonded)
			return nil
		},
	}
	cmd.Flags().StringVar(&walletPath, "wallet", "", "wallet file path (default ~/.matrix/wallet.json)")
	cmd.Flags().StringVar(&account, "account", "", "explicit account ID to query instead of the wallet")
	return cmd
}

// loadStakeWallet resolves and unlocks the wallet these commands sign with.
func loadStakeWallet(cmd *cobra.Command, walletPath string) (*token.Account, error) {
	path, err := resolveWalletPath(walletPath)
	if err != nil {
		return nil, err
	}
	return loadWallet(path, passphrasePrompt(cmd.ErrOrStderr(), "Passphrase for "+path))
}

// submitStakeOperation signs a transfer to a reserved stake recipient and
// submits it the same way an ordinary transfer goes, because that is exactly
// what it is.
func submitStakeOperation(cmd *cobra.Command, opts *globalOptions, acct *token.Account, recipient string, amount uint64) error {
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
		To:        recipient,
		Amount:    amount,
		Nonce:     nonce,
		Timestamp: time.Now().UnixNano(),
		PrevHash:  make([]byte, chainHashSize),
	}
	if err := tx.Sign(acct.PrivateKey); err != nil {
		return fmt.Errorf("failed to sign the stake operation: %w", err)
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
}

// readBalance reads one account's native balance.
func readBalance(ctx context.Context, client marketv1.MarketServiceClient, account string) (uint64, error) {
	resp, err := client.GetBalance(ctx, &marketv1.GetBalanceRequest{Account: account})
	if err != nil {
		return 0, err
	}
	return resp.GetBalance(), nil
}
