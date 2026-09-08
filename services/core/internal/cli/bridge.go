package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"github.com/spf13/cobra"

	"github.com/ecirlabs/matrix-core/internal/bridge"
	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// `matrix bridge` groups the operator commands for the lock-and-mint bridge.
//
// Today that is one command: generating this validator's attestor key. It exists
// because the key had nowhere to come from. The node held no attestor at all,
// and the only signer in the tree was cmd/bridge-attest's deterministic test
// seed - which is documented as never for real funds, and is not a key an
// operator can be handed.
func newBridgeCommand(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bridge",
		Short: "Operator commands for the lock-and-mint bridge",
	}
	cmd.AddCommand(newAttestorNewCommand(opts), newBridgeLockCommand(opts))
	return cmd
}

func newAttestorNewCommand(opts *globalOptions) *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "attestor-new",
		Short: "Generate this validator's encrypted bridge attestor key",
		Long: `attestor-new generates a fresh secp256k1 attestor key and writes it as an
encrypted keystore, the same scrypt-and-AES-GCM format a wallet key uses.

The key is unilateral authority to sign mint authorizations against escrowed
native MATRIX, which is why it is a keystore rather than a hex string in a config
file. Point bridge.attestor_keystore at the file and supply the passphrase in
MATRIX_ATTESTOR_PASSPHRASE; the node refuses to start if it cannot unlock it.

The printed ADDRESS is what goes in the WrappedMatrix contract's registered
attestor set. A node whose address is not registered signs attestations the
contract rejects, and on a threshold bridge that looks like mints simply never
reaching quorum.

A passphrase is read from MATRIX_WALLET_PASSPHRASE when set, otherwise typed at
the prompt. There is no way to recover this key: back up the file.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if out == "" {
				return fmt.Errorf("--out is required: name the file to write the keystore to")
			}
			// Refuse to overwrite. Silently replacing an attestor key would strand
			// every lock this validator had already attested to and leave its
			// registered address signing nothing.
			if _, err := os.Stat(out); err == nil {
				return fmt.Errorf("%s already exists; refusing to overwrite an attestor key", out)
			}

			ask := passphrasePrompt(cmd.OutOrStdout(), "New attestor passphrase: ")
			pass, err := ask()
			if err != nil {
				return err
			}
			ks, att, err := bridge.NewAttestorKeystore(pass)
			if err != nil {
				return err
			}
			body, err := token.MarshalKeystore(ks)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
				return fmt.Errorf("create keystore directory: %w", err)
			}
			// 0600: the file is a minting key, encrypted or not.
			if err := os.WriteFile(out, body, 0o600); err != nil {
				return fmt.Errorf("write keystore: %w", err)
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Attestor keystore written to %s\n", out)
			fmt.Fprintf(w, "  address: %s\n", att.AddressHex())
			fmt.Fprintf(w, "\nNext:\n")
			fmt.Fprintf(w, "  1. Register %s in the WrappedMatrix attestor set (ATTESTORS on deploy).\n",
				att.AddressHex())
			fmt.Fprintf(w, "  2. Set bridge.attestor_keystore: %s in the node config.\n", out)
			fmt.Fprintf(w, "  3. Export MATRIX_ATTESTOR_PASSPHRASE before starting the node.\n")
			fmt.Fprintf(w, "\nBack up this file. The key cannot be recovered from anything else.\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "path to write the encrypted attestor keystore to")
	return cmd
}

// newBridgeLockCommand builds `matrix bridge lock`.
//
// It exists because locking had no front door. The operation is an ordinary
// signed transfer whose RECIPIENT encodes the intent, so without a command an
// operator has to hand-assemble `bridge/lock/<address>` - and getting that
// string wrong does not produce an error, it produces a transfer to a different
// reserved namespace or to an account id nobody holds.
//
// It prints the LOCK ID, which is the thing the next step needs: the id is
// derived from this transaction (nonce, sender, recipient, amount), and a client
// that recomputes it differently asks every validator about a lock that does not
// exist.
func newBridgeLockCommand(opts *globalOptions) *cobra.Command {
	var (
		walletPath string
		toHex      string
		amount     uint64
	)
	cmd := &cobra.Command{
		Use:   "lock",
		Short: "Lock native MATRIX for an Ethereum address, to be minted as wMATRIX",
		Long: `lock moves native MATRIX into the bridge escrow so wMATRIX can be minted
against it on Ethereum.

It is a signed transfer to a reserved recipient, so it is ordered by consensus
like any other: every node applies the same escrow move from the same committed
block. The escrow receives the FULL amount - a lock pays no protocol fee, because
the wrapped supply minted against it is computed from what was locked, and a fee
would mint more wrapped than the escrow holds.

The printed lock id is what the next step needs. Ask a threshold of validators
for their signatures over it (each node's GetLockAttestation returns one), then
pass the collected set to WrappedMatrix.mint.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			addr, err := bridge.ParseAddress(toHex)
			if err != nil {
				return fmt.Errorf("--to must be a 0x ethereum address: %w", err)
			}
			if amount == 0 {
				return fmt.Errorf("--amount must be greater than zero; a zero lock would mint " +
					"nothing and still consume a lock id")
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
				To:        consensus.BridgeLockRecipient(addr),
				Amount:    amount,
				Nonce:     nonce,
				Timestamp: time.Now().UnixNano(),
				PrevHash:  make([]byte, chainHashSize),
			}
			if err := tx.Sign(acct.PrivateKey); err != nil {
				return fmt.Errorf("failed to sign lock: %w", err)
			}
			if _, err := cc.market.SubmitSignedTransfer(ctx, &marketv1.SubmitSignedTransferRequest{
				FromPublicKey: token.MarshalPublicKey(acct.PublicKey),
				To:            tx.To,
				Amount:        tx.Amount,
				Nonce:         tx.Nonce,
				PrevHash:      tx.PrevHash,
				Signature:     tx.Signature,
				Timestamp:     tx.Timestamp,
			}); err != nil {
				return transferSubmitError(opts, tx, err)
			}

			// Derived locally from the same fields the chain uses, so the operator
			// has the id without a second round trip - and if the two ever
			// disagreed, the attestation request would fail loudly rather than
			// silently attesting to nothing.
			id := consensus.DeriveLockID(tx.Nonce, acct.AccountID(), addr, amount)

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Locked %d native base units for %s\n", amount, addr.Hex())
			fmt.Fprintf(w, "  lock id: 0x%x\n", id)
			fmt.Fprintf(w, "\nNext: collect a threshold of attestations, one per validator, then mint.\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&walletPath, "wallet", "", "wallet keystore path (default ~/.matrix/wallet.json)")
	cmd.Flags().StringVar(&toHex, "to", "", "ethereum address to mint the wrapped tokens to")
	cmd.Flags().Uint64Var(&amount, "amount", 0, "native base units to lock (9 decimals)")
	_ = cmd.MarkFlagRequired("to")
	_ = cmd.MarkFlagRequired("amount")
	return cmd
}
