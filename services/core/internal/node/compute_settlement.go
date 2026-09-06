package node

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// This file adds the consensus-backed compute-settlement path for the compute
// marketplace, unifying it with the inference marketplace onto native MATRIX
// moved through consensus. It deliberately mirrors internal/inference.Service: a
// compute job is settled by submitting a signed buyer -> provider transfer
// through the consensus-backed Settler and confirming it via WaitForSettlement
// BEFORE the job is finalized, so the buyer is charged exactly once.
//
// It lives in internal/node (not internal/market) because the settlement types
// reference token.Account/token.Transaction, and internal/token already imports
// internal/market (token.Treasury issues onto the market ledger). Putting the
// coordinator in market would create a market -> token -> market import cycle.
// The node package sits above both and can wire them together, exactly as the
// task allows ("a thin coordinator in internal/node").

// ErrNoSigningAccount is returned when the buyer settling a compute job has no
// resolvable signing account, so no consensus transfer can be signed on its
// behalf.
var ErrNoSigningAccount = errors.New("node: no signing account for buyer")

// ErrSettlementNotApplied is returned when a compute job's settlement committed
// to a consensus block but was skipped at apply time (the buyer could not afford
// the transfer). The job is reported failed rather than completed, so no phantom
// completion is recorded for a payment that never moved credits.
var ErrSettlementNotApplied = errors.New("node: settlement did not apply (payment skipped as unaffordable)")

// DefaultComputeSettlementTimeout bounds how long SettleAndCompleteJob waits for
// a submitted consensus settlement to commit and apply before giving up. It is
// generous relative to consensus commit latency so the common case confirms
// synchronously while a stalled settlement is reported honestly instead of being
// claimed complete. It mirrors inference.DefaultSettlementTimeout.
const DefaultComputeSettlementTimeout = 5 * time.Second

// ComputeSettler is the consensus-backed settlement dependency the compute
// coordinator uses to move native MATRIX from buyer to provider when a job
// completes. It is the same shape as inference.Settler so the same consensus
// engine (consensus.Engine) satisfies both, and tests can drive it with a fake
// without a running engine.
//
// SubmitAccountTransfer submits a signed transfer and returns the transaction so
// the caller can correlate it with the committed block; WaitForSettlement blocks
// until that transfer is committed and its apply outcome (whether it actually
// moved credits) is known.
type ComputeSettler interface {
	SubmitAccountTransfer(from *token.Account, recipient string, amount, nonce uint64) (*token.Transaction, error)
	// Submit re-submits an already-signed transfer. The consensus engine dedups
	// by (sender, nonce, signature), so re-submitting the identical transaction
	// is a safe no-op if it is already in the mempool or committed. The compute
	// coordinator uses this on retry to re-drive the SAME pending transfer rather
	// than signing a fresh one with a new nonce, which is what closes the
	// retry-after-timeout double-charge window (see SettleAndCompleteJob).
	Submit(tx *token.Transaction) error
	WaitForSettlement(ctx context.Context, tx *token.Transaction) (committed bool, applied bool, err error)
}

// Accounts resolves a buyer account ID to its signing token.Account. Settling
// through consensus requires the payer's private key to sign the transfer, so
// the coordinator is given a resolver rather than assuming key custody, exactly
// as the inference Service is. A provider node holds keys for the buyer accounts
// it settles on behalf of (or a custodial wallet in a hosted deployment); tests
// supply an in-memory resolver. It is defined here (not in internal/market)
// because it references token.Account and internal/token imports
// internal/market, which would make a market-side definition a cyclic import.
type Accounts interface {
	// Account returns the signing account for id, and whether it is known.
	Account(id string) (*token.Account, bool)
}

// ComputeSettlementCoordinator routes compute-job settlement through consensus.
// It wraps the market, a consensus-backed ComputeSettler, and a
// market.Accounts resolver, and owns a per-buyer nonce sequence so repeated
// same-amount transfers from one buyer remain distinct transactions (the
// consensus transfer nonce is a uniquifier; see consensus.SubmitTransfer). It is
// safe for concurrent use.
//
// It is the compute-marketplace analogue of internal/inference.Service: where
// that settles inference jobs through consensus, this settles plain compute jobs
// through the same path so there is one currency and one authoritative ledger
// end to end.
type ComputeSettlementCoordinator struct {
	market   *market.Market
	settler  ComputeSettler
	accounts Accounts

	mu    sync.Mutex
	nonce map[string]uint64
	// pending remembers the signed transfer submitted for a job whose settlement
	// has not yet been confirmed committed+applied (or definitively failed). On a
	// retry after a WaitForSettlement timeout, the coordinator re-submits THIS
	// exact transaction instead of signing a new one. Because the consensus
	// engine dedups by (sender, nonce, signature), the late-committing original
	// and the retry are the SAME transaction and can commit at most once, so a
	// timeout-then-retry never double-charges the buyer. The entry is cleared once
	// the settlement is confirmed applied or honestly failed (unaffordable).
	pending map[string]*token.Transaction
}

// NewComputeSettlementCoordinator builds a ComputeSettlementCoordinator over the
// given market, consensus-backed settler, and account resolver. All three are
// required.
func NewComputeSettlementCoordinator(m *market.Market, settler ComputeSettler, accounts Accounts) (*ComputeSettlementCoordinator, error) {
	if m == nil {
		return nil, fmt.Errorf("node: market is required")
	}
	if settler == nil {
		return nil, fmt.Errorf("node: settler is required")
	}
	if accounts == nil {
		return nil, fmt.Errorf("node: accounts resolver is required")
	}
	return &ComputeSettlementCoordinator{
		market:   m,
		settler:  settler,
		accounts: accounts,
		nonce:    make(map[string]uint64),
		pending:  make(map[string]*token.Transaction),
	}, nil
}

// nextNonce returns and advances the per-buyer transfer nonce.
func (c *ComputeSettlementCoordinator) nextNonce(buyer string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.nonce[buyer]
	c.nonce[buyer] = n + 1
	return n
}

// pendingTx returns the previously-submitted, not-yet-confirmed transfer for a
// job, if any.
func (c *ComputeSettlementCoordinator) pendingTx(jobID string) (*token.Transaction, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, ok := c.pending[jobID]
	return tx, ok
}

// setPendingTx records the outstanding transfer for a job so a later retry
// re-drives the SAME transaction instead of signing a fresh one.
func (c *ComputeSettlementCoordinator) setPendingTx(jobID string, tx *token.Transaction) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending[jobID] = tx
}

// clearPendingTx forgets the outstanding transfer for a job once its settlement
// is confirmed applied or honestly failed.
func (c *ComputeSettlementCoordinator) clearPendingTx(jobID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, jobID)
}

// SettleAndCompleteJob settles a pending or running compute job's price from
// buyer -> provider in native MATRIX through the consensus engine and, only once
// the transfer is confirmed committed AND applied, releases the market
// reservation so the job is marked completed without a second charge.
//
// Ordering and anti-double-charge discipline (identical to
// internal/inference.Service.FulfillJob):
//   - The buyer signs a consensus transfer for exactly the job's reserved,
//     affordability-checked price and it is submitted through the settler.
//   - We WAIT (bounded) for the transfer to commit and apply. The consensus
//     apply path deterministically SKIPS an unaffordable transfer, so a
//     committed-but-not-applied outcome means no credits moved: we report
//     ErrSettlementNotApplied and cancel the reservation, never claiming
//     completion for a payment that did not land.
//   - On applied success, credits already moved buyer -> provider on the shared
//     market ledger via the consensus apply path. Calling market.CompleteJob
//     here would transfer the price a SECOND time (its direct ledger.Transfer)
//     and double-charge the buyer, so instead we finalize via
//     market.ReleaseAsCompleted, which only releases the reservation. The
//     settlement, not the reservation, charged the buyer, so the buyer is
//     charged exactly once.
//
// Replay/nonce/affordability safety is preserved: the transfer carries a
// per-buyer nonce, consensus dedups committed transactions, and the apply path
// re-checks affordability at commit time.
//
// Retry-after-timeout is exactly-once, not at-most-twice. On a WaitForSettlement
// timeout the job is deliberately left reserved so the caller can retry, and the
// signed transfer is remembered as pending for this job. A retry does NOT sign a
// fresh transfer with a new nonce (which would be a distinct transaction that
// consensus could commit IN ADDITION to a late-committing original, double-
// charging the buyer). Instead it re-submits the SAME pending transaction; the
// consensus engine dedups by (sender, nonce, signature), so the original and
// every retry collapse to one transaction that can commit at most once. This
// closes the double-charge window that a per-call fresh nonce would open.
//
// On success it returns the settled Job (COMPLETED). If the settlement cannot be
// confirmed within DefaultComputeSettlementTimeout the reservation is left intact
// and the context error is returned, so a caller can retry rather than seeing a
// phantom completion.
func (c *ComputeSettlementCoordinator) SettleAndCompleteJob(ctx context.Context, jobID string) (*market.Job, error) {
	job, ok := c.market.GetJob(jobID)
	if !ok {
		return nil, fmt.Errorf("settle job %q: %w", jobID, market.ErrJobNotFound)
	}
	if job.Status != market.JobPending && job.Status != market.JobRunning {
		return nil, fmt.Errorf("settle job %q in state %q: %w", jobID, job.Status, market.ErrInvalidJobState)
	}

	buyerAcct, ok := c.accounts.Account(job.Buyer)
	if !ok {
		return nil, fmt.Errorf("settle job %q: %w: %q", jobID, ErrNoSigningAccount, job.Buyer)
	}

	// If a previous attempt for this job already submitted a transfer that never
	// confirmed, re-drive that EXACT transaction rather than signing a new one.
	// Re-submitting the identical signed tx is a safe no-op in the engine's
	// dedup set, so the original and the retry are the same transaction and can
	// only commit once. Only sign a fresh transfer for a job's first attempt.
	tx, ok := c.pendingTx(jobID)
	if ok {
		if err := c.settler.Submit(tx); err != nil {
			return nil, fmt.Errorf("settle job %q: re-submitting pending transfer: %w", jobID, err)
		}
	} else {
		nonce := c.nextNonce(job.Buyer)
		submitted, err := c.settler.SubmitAccountTransfer(buyerAcct, job.Provider, job.Price, nonce)
		if err != nil {
			return nil, fmt.Errorf("settle job %q: %w", jobID, err)
		}
		tx = submitted
		c.setPendingTx(jobID, tx)
	}

	waitCtx, cancel := context.WithTimeout(ctx, DefaultComputeSettlementTimeout)
	defer cancel()
	committed, applied, werr := c.settler.WaitForSettlement(waitCtx, tx)
	if werr != nil {
		// Could not confirm within the timeout (or ctx ended). Leave the job
		// reserved and uncompleted and KEEP the pending transfer recorded so a
		// retry re-drives the same transaction. The transfer may still commit
		// later; because a retry reuses it rather than submitting a new nonce, the
		// buyer is charged at most once. We deliberately do NOT release capacity
		// or claim completion here.
		return nil, fmt.Errorf("settle job %q: awaiting settlement: %w", jobID, werr)
	}
	if !committed || !applied {
		// The transfer committed but was skipped as unaffordable (or did not
		// commit): no credits moved. Cancel the reservation and report the honest
		// failure instead of a COMPLETED job whose payment never landed. The
		// pending transfer is resolved (it committed unapplied, so its dedup key
		// is permanently in the committed set) and can be forgotten.
		c.clearPendingTx(jobID)
		_ = c.market.CancelJob(jobID)
		return nil, fmt.Errorf("settle job %q: %w", jobID, ErrSettlementNotApplied)
	}

	// Settlement applied: credits moved buyer -> provider on the shared market
	// ledger via consensus. Finalize by releasing the reservation (do NOT
	// CompleteJob, which would transfer the price a second time) and mark the job
	// completed. The pending transfer has committed and applied exactly once, so
	// forget it.
	c.clearPendingTx(jobID)
	completed, err := c.market.ReleaseAsCompleted(jobID, job.Price)
	if err != nil {
		return nil, fmt.Errorf("settle job %q: settled but failed to finalize: %w", jobID, err)
	}
	return completed, nil
}
