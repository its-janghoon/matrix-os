package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	inferencev1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/inference/v1"

	"github.com/ecirlabs/matrix-core/internal/inference"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// The ESCROWED path from the terminal: the buyer keeps their key AND watches the
// answer arrive.
//
// The other two each give one of those up. On the node-signed path the operator
// holds a key that can move everything in the account. On --client-signed the
// buyer keeps their key and waits in silence, because the charge is unknowable
// until the work is done - so the node runs first, invoices second, and
// WITHHOLDS the completion until the invoice is signed. That withholding is the
// only enforcement there, which is why that path cannot stream.
//
// Here the amount is still unknowable but the RESERVATION is not. The buyer
// funds `units x price` before the model starts, so the provider already holds
// the most the job can cost and the text is not leverage any more. It streams,
// and consensus returns the change when the settlement names the actual.

// escrowedInput is what a run down the escrowed path needs.
type escrowedInput struct {
	buyer, provider, model, prompt string
	units                          uint64
	walletPath                     string
	// out receives the COMPLETION and nothing else, so piping this command into
	// a file gets the answer; progress and warnings go to errOut. Both are
	// passed in rather than taken from os, because a stream written straight to
	// the process's stdout cannot be captured by a test - which would leave the
	// one path that streams as the one path with no test of what it streams.
	out    io.Writer
	errOut io.Writer
}

// runEscrowed drives reserve -> fund -> stream -> settle.
//
// WHAT CTRL-C DOES, and why it is not just stopping. The settlement arrives on
// the stream's LAST message, so a buyer who interrupts never receives one - and
// with nothing to sign, the provider claims the WHOLE reservation at its expiry
// rather than the fraction the run cost. Interrupting would be the most
// expensive thing this command can do and would look like the cheapest. So an
// interrupted run asks the node for the settlement of the job it is no longer
// streaming, and pays for what arrived.
func runEscrowed(ctx context.Context, ic *inferenceConn, addr string, in escrowedInput) (*inferencev1.InferenceJob, error) {
	if in.out == nil {
		in.out = os.Stdout
	}
	if in.errOut == nil {
		in.errOut = os.Stderr
	}
	path, err := resolveWalletPath(in.walletPath)
	if err != nil {
		return nil, err
	}
	acct, err := loadWallet(path, passphrasePrompt(in.errOut, "Passphrase for "+path))
	if err != nil {
		return nil, err
	}
	if acct.AccountID() != in.buyer {
		return nil, fmt.Errorf("the wallet at %s is account %s, but --buyer is %s: "+
			"--escrowed pays with the wallet's own key, so they must match",
			path, acct.AccountID(), in.buyer)
	}

	request := inference.InferenceRequest{Prompt: in.prompt, Model: in.model}
	auth := &inference.RunAuthorization{
		PublicKey: acct.PublicKey,
		Provider:  in.provider,
		Model:     in.model,
		Timestamp: time.Now().UTC().UnixNano(),
	}
	if err := auth.Sign(request, acct.PrivateKey); err != nil {
		return nil, fmt.Errorf("sign the run authorization: %w", err)
	}
	wireAuth := &inferencev1.RunAuthorization{
		PublicKey: auth.PublicKey,
		Timestamp: auth.Timestamp,
		Signature: auth.Signature,
	}

	// 1. RESERVE. Nothing has run and nothing is charged.
	reserved, err := ic.inference.ReserveInferenceEscrow(ctx, &inferencev1.ReserveInferenceEscrowRequest{
		Buyer:         in.buyer,
		Provider:      in.provider,
		Model:         in.model,
		Prompt:        in.prompt,
		UnitsEstimate: in.units,
		Authorization: wireAuth,
	})
	if err != nil {
		return nil, mapErr(addr, err)
	}
	deposit := reserved.GetPayment()
	jobID := deposit.GetJobId()
	perUnit := reserved.GetPricePerUnit()

	// The arithmetic behind the deposit, checked before it is funded. It is the
	// one number here the node cannot be wrong about by accident, and a buyer who
	// signs it unchecked has agreed to whatever it said.
	if units, price := reserved.GetUnitsReserved(), perUnit; units != 0 && price != 0 {
		if want := units * price; want != deposit.GetAmount() {
			return nil, fmt.Errorf("the node asks %d to reserve %d units at %d each, which should be %d",
				deposit.GetAmount(), units, price, want)
		}
	}
	fmt.Fprintf(in.errOut, "  reserving %d units at %d each = %d, claimable by the provider after %s\n",
		reserved.GetUnitsReserved(), perUnit, deposit.GetAmount(),
		time.Unix(reserved.GetClaimableAt(), 0).UTC().Format(time.RFC3339))

	// 2. FUND, and wait for it to commit AND apply. A deposit in the mempool is
	// not money in escrow, and the node refuses to stream against one.
	depositTx, err := signAs(acct, deposit)
	if err != nil {
		return nil, err
	}
	if _, err := ic.inference.FundInferenceEscrow(ctx, &inferencev1.FundInferenceEscrowRequest{
		Id:            jobID,
		FromPublicKey: acct.PublicKey,
		To:            depositTx.To,
		Amount:        depositTx.Amount,
		Nonce:         depositTx.Nonce,
		Timestamp:     depositTx.Timestamp,
		PrevHash:      depositTx.PrevHash,
		Signature:     depositTx.Signature,
	}); err != nil {
		return nil, mapErr(addr, err)
	}

	// 3. STREAM. The completion goes to STDOUT as it arrives, so piping this
	// command into a file gets the answer and nothing else; everything the
	// operator is being told goes to stderr.
	settlement, completion, cutShort, err := streamEscrowed(ctx, ic, jobID, wireAuth, in.out, in.errOut)
	if err != nil {
		return nil, mapErr(addr, err)
	}

	// 4. SETTLE - but check the bill first. The node computed it and the node is
	// the seller's; a signature given to a number this side never checked is the
	// leverage this whole path exists to create, handed straight back.
	if err := checkEscrowBill(request, completion, settlement.GetAmount(), deposit.GetAmount(), perUnit); err != nil {
		return nil, fmt.Errorf("%w\nThe reservation is the provider's to claim after %s if this is "+
			"not settled before then", err, time.Unix(reserved.GetClaimableAt(), 0).UTC().Format(time.RFC3339))
	}

	settleTx, err := signAs(acct, settlement)
	if err != nil {
		return nil, err
	}
	// A FRESH context: an interrupted run reaches here with ctx already dead, and
	// settling is exactly what must still happen when it is.
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), DefaultSettleTimeout)
	defer cancel()
	done, err := ic.inference.SettleEscrowedInferenceJob(settleCtx, &inferencev1.SettleEscrowedInferenceJobRequest{
		Id:            jobID,
		FromPublicKey: acct.PublicKey,
		To:            settleTx.To,
		Amount:        settleTx.Amount,
		Nonce:         settleTx.Nonce,
		Timestamp:     settleTx.Timestamp,
		PrevHash:      settleTx.PrevHash,
		Signature:     settleTx.Signature,
	})
	if err != nil {
		return nil, mapErr(addr, err)
	}
	if cutShort {
		fmt.Fprintf(in.errOut, "\n  stopped early: charged %d of the %d reserved, the rest is returned\n",
			settlement.GetAmount(), deposit.GetAmount())
	}
	return done.GetJob(), nil
}

// DefaultSettleTimeout is how long settling gets after the stream has ended.
//
// It is separate from the run's deadline because it outlives it: an interrupted
// run settles on a context the interrupt already cancelled, and inheriting that
// deadline would abandon the reservation at the moment it most needs paying.
const DefaultSettleTimeout = 2 * time.Minute

// streamEscrowed reads the answer as it is produced and returns the settlement.
//
// An interrupted or broken stream is RECOVERED rather than failed: the job is
// funded, so the question left is what fraction of the reservation is owed, and
// abandoning it answers "all of it".
func streamEscrowed(
	ctx context.Context,
	ic *inferenceConn,
	jobID string,
	auth *inferencev1.RunAuthorization,
	out, errOut io.Writer,
) (*inferencev1.PaymentRequest, string, bool, error) {
	var completion string
	stream, err := ic.inference.StreamEscrowedInferenceJob(ctx,
		&inferencev1.StreamEscrowedInferenceJobRequest{Id: jobID, Authorization: auth})
	if err != nil {
		return recoverEscrow(ctx, ic, jobID, auth, completion, errOut)
	}
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return recoverEscrow(ctx, ic, jobID, auth, completion, errOut)
		}
		if delta := msg.GetDelta(); delta != "" {
			completion += delta
			fmt.Fprint(out, delta)
		}
		if pay := msg.GetPayment(); pay != nil {
			fmt.Fprintln(out)
			// The final frame is sent even when the run was cut short, because
			// the settlement is on it. So a cut-short run that this side is
			// still connected for - the provider dropped, not us - arrives here
			// rather than down the recovery path.
			return pay, completion, msg.GetCutShort(), nil
		}
	}
	// The stream ended without the settlement on it, which is the same position
	// an interrupt leaves us in.
	return recoverEscrow(ctx, ic, jobID, auth, completion, errOut)
}

// recoverEscrow asks the node for the settlement of a job we are no longer
// streaming.
func recoverEscrow(
	ctx context.Context,
	ic *inferenceConn,
	jobID string,
	auth *inferencev1.RunAuthorization,
	seen string,
	errOut io.Writer,
) (*inferencev1.PaymentRequest, string, bool, error) {
	fmt.Fprintln(errOut, "\n  the stream ended early; recovering the settlement so the whole "+
		"reservation is not forfeited")
	// Fresh, for the reason DefaultSettleTimeout gives: the usual way to get here
	// is an interrupt, which cancelled ctx.
	recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), DefaultSettleTimeout)
	defer cancel()
	resp, err := ic.inference.RecoverEscrowedInferenceJob(recCtx,
		&inferencev1.RecoverEscrowedInferenceJobRequest{Id: jobID, Authorization: auth})
	if err != nil {
		return nil, seen, false, err
	}
	completion := seen
	// The node's copy only when nothing was seen here: a bill is checked against
	// the text that actually arrived, and preferring the seller's transcript over
	// our own would check the bill against the seller's own account of it.
	if completion == "" {
		completion = resp.GetJob().GetCompletion()
	}
	return resp.GetPayment(), completion, resp.GetCutShort(), nil
}

// checkEscrowBill refuses a settlement the answer cannot account for.
//
// The same arithmetic the node used, run on this side. MaxUnitsFor is a
// deliberately loose upper bound from the text that crossed the wire - it cannot
// know the provider's tokeniser and does not need to, because the overcharge it
// exists to catch is two orders of magnitude and not a few percent. Running it
// here is what makes the buyer's signature mean something: the node's own copy
// runs on the SELLER's machine.
func checkEscrowBill(req inference.InferenceRequest, completion string, amount, reserved, pricePerUnit uint64) error {
	if amount == 0 {
		return fmt.Errorf("the node asks to settle nothing, which consensus refuses")
	}
	if amount > reserved {
		return fmt.Errorf("the node asks %d, more than the %d reserved", amount, reserved)
	}
	if pricePerUnit == 0 {
		// No price came back, so there is nothing to convert the amount into
		// units with. Refusing on a number we do not have would strand a buyer
		// who owes a real bill; the reservation still bounds it.
		return nil
	}
	ceiling := inference.MaxUnitsFor(req, completion, "")
	if most := ceiling * pricePerUnit; amount > most {
		return fmt.Errorf("the node asks %d, more than the %d this answer can honestly have cost "+
			"(%d units at %d each)", amount, most, ceiling, pricePerUnit)
	}
	return nil
}

// signAs signs exactly the transfer the node asked for.
//
// Every field, transcribed once: the signature covers all of them, so a
// transcription that dropped one would verify against different bytes than were
// signed and the failure would read as a bad key rather than as a bug.
func signAs(acct *token.Account, pr *inferencev1.PaymentRequest) (*token.Transaction, error) {
	tx := &token.Transaction{
		From:      acct.PublicKey,
		To:        pr.GetTo(),
		Amount:    pr.GetAmount(),
		Nonce:     pr.GetNonce(),
		Timestamp: pr.GetTimestamp(),
		PrevHash:  pr.GetPrevHash(),
	}
	if err := tx.Sign(acct.PrivateKey); err != nil {
		return nil, fmt.Errorf("sign the transfer for job %s: %w", pr.GetJobId(), err)
	}
	return tx, nil
}
