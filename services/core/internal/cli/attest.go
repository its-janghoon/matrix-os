package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/ecirlabs/matrix-core/internal/marketexchange"
)

// Minting the one badge this marketplace has.
//
// WHAT IT IS FOR. A new network's directory is mostly strangers. The settled
// history that tells sellers apart does not exist yet and a stake proves capital
// rather than competence, so a buyer has no reason to send a first prompt to
// anyone. Somebody has to go first, and the honest way for the people running a
// network to do it is to run sellers themselves and say so.
//
// WHAT IT IS NOT. Not a rating, not an endorsement, not a claim that these
// sellers answer better. It says one checkable thing: the account this chain
// names as its maintainer states that it operates this node. A reader who does
// not trust that account learns nothing from it, and that is the correct
// outcome.
//
// WHY IT CANNOT BE FAKED. It is signed by the maintainer key, and every reader
// checks that signature against the maintainer THEIR OWN chain names - consensus
// state, agreed by every node, rotatable only by the current maintainer's own
// signature. A seller can put any bytes in its announcement; it cannot produce
// that signature.

func newAttestCommand(opts *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attest",
		Short: "Sign, as the maintainer, that this network operates a seller",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newAttestIssueCommand(opts), newAttestShowCommand(opts))
	return cmd
}

func newAttestIssueCommand(opts *globalOptions) *cobra.Command {
	var (
		nodeID     string
		providerID string
		operator   string
		validFor   time.Duration
		walletPath string
		out        string
	)
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Sign an operator attestation for one of this network's own sellers",
		Long: `issue signs the maintainer's statement that this network operates a seller.

It says exactly one thing, and a reader should be told no more than it:

    the account this chain names as its maintainer operates this node

Not that the seller is good, not that it is fast, not that anyone should prefer
it. Those are claims no signature can carry. What this does is let the people
running a network stand behind the capacity they run themselves, in a form
nobody else can forge - and let a buyer with nothing else to go on start there.

It is signed with the MAINTAINER's wallet, and every reader checks it against
the maintainer their own chain names. Signing with any other key produces an
attestation that verifies nowhere.

It EXPIRES, and cannot be dated further ahead than the protocol allows. A box
gets decommissioned, sold or repurposed, and a permanent badge on a machine
somebody else now owns is not recoverable. Re-signing is a scheduled chore.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if nodeID == "" {
				return fmt.Errorf("--node is required: an attestation names the node it is for, " +
					"so it cannot be lifted onto somebody else's listing")
			}
			if providerID == "" {
				return fmt.Errorf("--provider is required: an attestation covers one of a node's " +
					"sellers rather than everything it ever lists")
			}
			if operator == "" {
				return fmt.Errorf("--operator is required: a badge that cannot say WHO vouched is " +
					"a halo, and a reader deserves a name to judge")
			}
			if validFor <= 0 {
				return fmt.Errorf("--valid-for must be positive")
			}
			if validFor > marketexchange.MaxOperatorAttestationValidity {
				return fmt.Errorf("--valid-for is %s; the protocol refuses anything beyond %s, because "+
					"an attestation nobody has to renew outlives the arrangement it describes",
					validFor, marketexchange.MaxOperatorAttestationValidity)
			}

			acct, err := loadStakeWallet(cmd, walletPath)
			if err != nil {
				return err
			}

			now := time.Now().UTC()
			att := &marketexchange.OperatorAttestation{
				NodeID:     nodeID,
				ProviderID: providerID,
				Operator:   operator,
				IssuedAt:   now.UnixNano(),
				ExpiresAt:  now.Add(validFor).UnixNano(),
			}
			if err := att.Sign(acct); err != nil {
				return err
			}
			// Checked against its own signer before it is written. A verifier on a
			// chain naming a different maintainer will refuse it, and it is better
			// to hand back a file that is at least self-consistent.
			if err := att.VerifyFor(nodeID, providerID, acct.AccountID(), now); err != nil {
				return fmt.Errorf("the attestation this just signed does not verify: %w", err)
			}

			body, err := json.MarshalIndent(att, "", "  ")
			if err != nil {
				return err
			}
			body = append(body, '\n')

			if out == "" {
				_, err = cmd.OutOrStdout().Write(body)
				return err
			}
			if err := os.WriteFile(out, body, 0o600); err != nil {
				return fmt.Errorf("write attestation: %w", err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(),
				"Wrote an attestation for %s, valid until %s.\n"+
					"Signed by %s - it verifies only where the chain names that account as maintainer.\n"+
					"Point the seller's node at it with inference.backends[].attestation.\n",
				providerID, now.Add(validFor).Format(time.RFC3339), acct.AccountID())
			return nil
		},
	}
	cmd.Flags().StringVar(&nodeID, "node", "", "consensus id of the node that will announce this seller (required)")
	cmd.Flags().StringVar(&providerID, "provider", "", "payout account being vouched for (required)")
	cmd.Flags().StringVar(&operator, "operator", "", "human name of whoever runs it, shown to readers (required)")
	cmd.Flags().DurationVar(&validFor, "valid-for", 14*24*time.Hour, "how long it stays valid")
	cmd.Flags().StringVar(&walletPath, "wallet", "", "maintainer wallet file (default ~/.matrix/wallet.json)")
	cmd.Flags().StringVar(&out, "out", "", "write to this file instead of stdout")
	return cmd
}

func newAttestShowCommand(opts *globalOptions) *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Read an attestation and say what it claims and until when",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if path == "" {
				return fmt.Errorf("--file is required")
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var att marketexchange.OperatorAttestation
			if err := json.Unmarshal(body, &att); err != nil {
				return fmt.Errorf("not an attestation: %w", err)
			}

			expires := time.Unix(0, att.ExpiresAt).UTC()
			if opts.JSON {
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"node":     att.NodeID,
					"provider": att.ProviderID,
					"operator": att.Operator,
					"attestor": att.AttestorID(),
					"expires":  expires.Format(time.RFC3339),
					"expired":  !expires.After(time.Now().UTC()),
				})
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "operator:  %s\n", att.Operator)
			fmt.Fprintf(w, "node:      %s\n", att.NodeID)
			fmt.Fprintf(w, "provider:  %s\n", att.ProviderID)
			fmt.Fprintf(w, "signed by: %s\n", att.AttestorID())
			fmt.Fprintf(w, "expires:   %s%s\n", expires.Format(time.RFC3339),
				map[bool]string{true: " (EXPIRED)", false: ""}[!expires.After(time.Now().UTC())])
			fmt.Fprintln(w)
			fmt.Fprintln(w, "This says the signer operates that node. It says nothing about how well it")
			fmt.Fprintln(w, "answers, and it is worth nothing on a chain that names a different maintainer.")
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "file", "", "path to the attestation JSON (required)")
	return cmd
}
