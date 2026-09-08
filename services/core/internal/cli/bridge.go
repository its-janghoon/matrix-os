package cli

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"github.com/spf13/cobra"

	"github.com/ecirlabs/matrix-core/internal/bridge"
	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// lockIDLen is the byte length of a lock id, which is a sha256 digest. It is
// checked before the RPC so a truncated paste is named here rather than coming
// back as a not_found the operator reads as "the lock did not commit".
const lockIDLen = 32

// `matrix bridge` groups the operator commands for the lock-and-mint bridge.
//
// It is the operator's whole side of the on-ramp: generate this validator's
// attestor key, lock native for an Ethereum address, and collect the validators'
// signatures for that lock. The last step is the mint, which happens on
// Ethereum and is contracts/scripts/bridge-mint.ts.
//
// The attestor command exists because the key had nowhere to come from. The node
// held no attestor at all, and the only signer in the tree was
// cmd/bridge-attest's deterministic test seed - which is documented as never for
// real funds, and is not a key an operator can be handed.
func newBridgeCommand(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bridge",
		Short: "Operator commands for the lock-and-mint bridge",
	}
	cmd.AddCommand(newAttestorNewCommand(opts), newBridgeLockCommand(opts),
		newBridgeAttestationCommand(opts), newBridgeReconcileCommand(opts))
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

The passphrase is read from MATRIX_ATTESTOR_PASSPHRASE - the same variable the
NODE uses to unlock the file - falling back to MATRIX_WALLET_PASSPHRASE, and
otherwise typed at the prompt. There is no way to recover this key: back up the
file.`,
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

			ask := attestorPassphrasePrompt(cmd.OutOrStdout(), "New attestor passphrase: ")
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

// attestationOut is the wire shape written to stdout: exactly the field names
// GetLockAttestation returns over the Connect HTTP surface, because that is what
// contracts/scripts/bridge-mint.ts reads. Two encodings of the same thing would
// be one more place for the mint to fail with a bare revert.
type attestationOut struct {
	Recipient    string `json:"recipient"`
	ERC20Amount  string `json:"erc20Amount"`
	LockID       string `json:"lockId"`
	Signature    string `json:"signature"`
	Attestor     string `json:"attestor"`
	NativeAmount string `json:"nativeAmount"`
	Validator    string `json:"validator"`
}

func newBridgeAttestationCommand(opts *globalOptions) *cobra.Command {
	var (
		lockID     string
		validators []string
	)
	cmd := &cobra.Command{
		Use:   "attestation",
		Short: "Collect validators' mint authorizations for a committed lock",
		Long: `attestation gathers one signature per validator for a lock the chain has
already committed, and prints them as the JSON array the mint step reads.

This is the middle step of the on-ramp, and doing it by hand is where a mint
goes wrong. Each validator holds its own secp256k1 key and signs the same digest
independently, so an m-of-n mint needs m of these collected from m different
nodes; there is no gossip of partial signatures, deliberately, because gossiping
them would be a second consensus for something the contract already checks.

Pass --validator once per node. With none given it asks the single node --addr
points at, which is a 1-of-1 bridge or a rehearsal.

It refuses to print a set whose members disagree about the lock. Two validators
naming different recipients or amounts for one lock id means they are not on the
same chain, and submitting it anyway would spend gas to be told the same thing
by a revert.

A lock that is not yet committed is a not_found, which is the honest answer:
there is nothing to sign for until a block carries it.

  matrix bridge attestation --lock-id 0x5b5a... > atts.json
  CONTRACT=0x... ATTESTATIONS=./atts.json \
    npx hardhat run scripts/bridge-mint.ts --network sepolia`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id := strings.TrimPrefix(strings.TrimPrefix(lockID, "0x"), "0X")
			raw, err := hex.DecodeString(id)
			if err != nil {
				return fmt.Errorf("--lock-id must be hex: %w", err)
			}
			if len(raw) != lockIDLen {
				return fmt.Errorf("--lock-id must be %d bytes, got %d; the id is the one "+
					"`matrix bridge lock` printed", lockIDLen, len(raw))
			}
			targets := validators
			if len(targets) == 0 {
				targets = []string{opts.Addr}
			}

			// Progress goes to stderr so stdout stays a clean JSON array that can
			// be redirected straight into a file.
			errW := cmd.ErrOrStderr()
			out := make([]attestationOut, 0, len(targets))
			for _, target := range targets {
				per := *opts
				per.Addr = target
				cc, err := dial(&per)
				if err != nil {
					return err
				}
				ctx, cancel := callContext(cmd.Context(), &per)
				resp, err := cc.market.GetLockAttestation(ctx, &marketv1.GetLockAttestationRequest{
					LockId: id,
				})
				cancel()
				_ = cc.Close()
				if err != nil {
					return fmt.Errorf("validator %s: %w", target, mapErr(target, err))
				}
				out = append(out, attestationOut{
					Recipient:    resp.GetRecipient(),
					ERC20Amount:  resp.GetErc20Amount(),
					LockID:       resp.GetLockId(),
					Signature:    resp.GetSignature(),
					Attestor:     resp.GetAttestor(),
					NativeAmount: strconv.FormatUint(resp.GetNativeAmount(), 10),
					Validator:    target,
				})
				fmt.Fprintf(errW, "%s signed as %s\n", target, resp.GetAttestor())
			}
			if err := checkAttestationsAgree(out); err != nil {
				return err
			}

			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if err := enc.Encode(out); err != nil {
				return err
			}
			fmt.Fprintf(errW, "\n%d attestation(s). Pass them to the mint step; the contract "+
				"needs its threshold of DISTINCT registered attestors.\n", len(out))
			return nil
		},
	}
	cmd.Flags().StringVar(&lockID, "lock-id", "", "the lock id `matrix bridge lock` printed")
	cmd.Flags().StringArrayVar(&validators, "validator", nil,
		"a validator's market endpoint host:port; repeat once per validator (default: --addr)")
	_ = cmd.MarkFlagRequired("lock-id")
	return cmd
}

// checkAttestationsAgree refuses a set whose members describe different locks.
//
// Disagreement is not a signature problem to be sorted out on chain: it means
// two validators applied different state for one lock id, which is a fork or a
// node pointed at the wrong network. The mint would revert, but only after the
// gas, and the revert would say "threshold not met" rather than what is wrong.
func checkAttestationsAgree(atts []attestationOut) error {
	if len(atts) < 2 {
		return nil
	}
	first := atts[0]
	for _, a := range atts[1:] {
		switch {
		case !strings.EqualFold(a.Recipient, first.Recipient):
			return fmt.Errorf("validator %s says lock %s mints to %s, but %s says %s: the "+
				"validators are not on the same chain",
				a.Validator, first.LockID, a.Recipient, first.Validator, first.Recipient)
		case a.ERC20Amount != first.ERC20Amount:
			return fmt.Errorf("validator %s says lock %s is worth %s, but %s says %s: the "+
				"validators are not on the same chain",
				a.Validator, first.LockID, a.ERC20Amount, first.Validator, first.ERC20Amount)
		case strings.EqualFold(a.Attestor, first.Attestor):
			return fmt.Errorf("validators %s and %s both signed as %s. The contract counts "+
				"DISTINCT signers, so two answers from one key are one signature, not two - "+
				"check that --validator names different nodes",
				first.Validator, a.Validator, a.Attestor)
		}
	}
	return nil
}

func newBridgeReconcileCommand(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Check that escrowed native still backs the wrapped supply 1:1",
		Long: `reconcile reads the node's bridge accounting and its actual escrow balance,
and reports what the Ethereum contract's total supply must be if the bridge is
correctly backed.

This is the invariant the whole bridge rests on: every wMATRIX in existence is
matched by native MATRIX that cannot move until it is burned back. The node
refuses to return a snapshot at all when its own accounting and its escrow
balance disagree, so an error here is not a reporting problem - it means the two
halves have diverged and something has minted or released outside the rules.

The number to compare against the chain is OUTSTANDING (erc20): read
totalSupply() on the WrappedMatrix contract and require the two to be equal. They
are allowed to differ only while a lock is committed and its mint has not been
broadcast yet, and then only in one direction - escrow ahead of supply, never
behind.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cc, err := dial(opts)
			if err != nil {
				return err
			}
			defer cc.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := cc.market.GetBridgeReconciliation(ctx,
				&marketv1.GetBridgeReconciliationRequest{})
			if err != nil {
				return mapErr(opts.Addr, err)
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Locked (native, cumulative):    %d\n", resp.GetLockedNative())
			fmt.Fprintf(w, "Unlocked (native, cumulative):  %d\n", resp.GetUnlockedNative())
			fmt.Fprintf(w, "Outstanding (native):           %d\n", resp.GetOutstandingNative())
			fmt.Fprintf(w, "Escrow balance (native):        %d\n", resp.GetEscrowBalance())
			fmt.Fprintf(w, "Outstanding (erc20, 18dp):      %s\n", resp.GetOutstandingErc20())
			fmt.Fprintf(w, "\nThe contract's totalSupply() must equal the erc20 figure above. "+
				"Escrow may lead it by a lock whose mint has not been broadcast; it must "+
				"never lag it.\n")
			return nil
		},
	}
	return cmd
}
