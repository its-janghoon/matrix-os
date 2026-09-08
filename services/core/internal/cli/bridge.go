package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ecirlabs/matrix-core/internal/bridge"
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
	cmd.AddCommand(newAttestorNewCommand(opts))
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
