package node

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ecirlabs/matrix-core/internal/consensus"
	"github.com/ecirlabs/matrix-core/internal/marketapi"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// This file adds the consensus-backed settlement path for external, client-
// signed value transfers (the matrix.market.v1 SubmitSignedTransfer RPC and
// `matrix wallet transfer`). It deliberately mirrors the compute/inference
// settlement model: an already-signed buyer -> recipient transfer is submitted
// into the consensus engine and confirmed committed AND applied before the RPC
// reports success, so every node applies the identical transfer to the shared
// ledger and two nodes agree on the resulting balances.
//
// Before this, SubmitSignedTransfer moved native MATRIX through
// token.SettledLedger.Settle: it appended to the per-node token.Chain and moved
// credits on ONE node, ordered by no quorum. That let balances diverge between
// nodes and was the one way to move MATRIX without paying the protocol fee
// (token.SettledLedger does not run the fee path). Routing the same signed
// transfer through consensus fixes both: consensus.commitAndApply is the single
// deterministic apply path, and it takes the (default-off) fee.

// ErrTransferNotApplied is returned when a signed transfer committed to a
// consensus block but was skipped at apply time because the sender could not
// afford it. No credits moved, so the transfer is reported as an honest
// precondition failure rather than as a successful settlement.
var ErrTransferNotApplied = errors.New("node: transfer did not apply (skipped as unaffordable)")

// ErrEmptyRecipient rejects a transfer with no recipient before it is submitted
// to consensus, matching the guard token.SettledLedger.Settle enforced so the
// consensus path is not a regression. It wraps token.ErrInvalidTransaction so a
// caller (and marketapi's error mapping) sees a structural-validation error.
var ErrEmptyRecipient = fmt.Errorf("%w: recipient must not be empty", token.ErrInvalidTransaction)

// ErrSelfTransfer rejects a transfer whose sender and recipient are the same
// account before it is submitted to consensus. Such a transfer would move no
// credits yet still occupy the ordered log, so it is rejected up front, matching
// token.SettledLedger.Settle. It wraps token.ErrSelfTransfer so callers may
// match on the same sentinel.
var ErrSelfTransfer = token.ErrSelfTransfer

// DefaultTransferSettlementTimeout bounds how long SettleSignedTransfer waits
// for a submitted transfer to commit and apply before returning a deadline
// error. It matches DefaultComputeSettlementTimeout and inference's timeout so
// the common case confirms synchronously while a stalled settlement is reported
// honestly.
const DefaultTransferSettlementTimeout = 5 * time.Second

// TransferEngine is the narrow consensus capability the transfer coordinator
// needs: submit an already-signed transfer, wait for it to commit and apply,
// and read committed transfer history back for the transaction-list RPCs. It is
// satisfied by *consensus.Engine and lets the coordinator (and its tests) depend
// on a small surface rather than the whole engine.
type TransferEngine interface {
	// Submit enqueues an already-signed transfer into consensus. The engine
	// dedups by (sender, nonce, signature), so re-submitting an identical
	// transaction is a safe no-op.
	Submit(tx *token.Transaction) error
	// WaitForSettlement blocks until tx is committed and its apply outcome is
	// known, or ctx is done.
	WaitForSettlement(ctx context.Context, tx *token.Transaction) (committed bool, applied bool, err error)
	// CommittedTransfers returns committed value transfers in globally-agreed
	// order for the transaction-history RPCs.
	CommittedTransfers(start uint64, limit int) ([]consensus.CommittedTransfer, uint64, error)
	// CommittedTransferAt returns a single committed transfer by index.
	CommittedTransferAt(index uint64) (*consensus.CommittedTransfer, error)
}

// TransferSettlementCoordinator routes external, client-signed value transfers
// through consensus. It is the value-transfer analogue of
// ComputeSettlementCoordinator: where that settles compute jobs, this settles a
// standalone signed transfer submitted via the market API. It holds only a
// TransferEngine because, unlike the job coordinators, the transfer arrives
// already signed by its sender (the wallet signs it), so no account resolver or
// nonce sequencing is needed here.
type TransferSettlementCoordinator struct {
	engine  TransferEngine
	timeout time.Duration
}

// NewTransferSettlementCoordinator builds a coordinator over the consensus
// engine. engine is required.
func NewTransferSettlementCoordinator(engine TransferEngine) (*TransferSettlementCoordinator, error) {
	if engine == nil {
		return nil, fmt.Errorf("node: transfer engine is required")
	}
	return &TransferSettlementCoordinator{engine: engine, timeout: DefaultTransferSettlementTimeout}, nil
}

// SettleSignedTransfer verifies a client-signed transfer, submits it into
// consensus, and waits (bounded) for it to commit and apply before returning.
//
// It preserves every guard the old token.SettledLedger.Settle path enforced so
// the consensus path is not a regression:
//   - the transfer must carry a valid ed25519 signature from its declared sender
//     (tx.Verify), so an unsigned or forged transfer is rejected before it ever
//     reaches consensus;
//   - an empty recipient is rejected (it would otherwise credit a bare key);
//   - a self-transfer is rejected (it moves nothing yet occupies the log);
//   - an unaffordable transfer is reported as a precondition failure: consensus
//     deterministically skips it at apply time, so a committed-but-not-applied
//     outcome means no credits moved and we return ErrTransferNotApplied.
//
// Replay protection comes from the engine's committed-transaction dedup set:
// re-submitting the identical signed transfer is a no-op, and the nonce is a
// per-sender uniquifier that keeps otherwise-identical transfers distinct.
func (c *TransferSettlementCoordinator) SettleSignedTransfer(ctx context.Context, tx *token.Transaction) (*marketapi.SettledTransferResult, error) {
	if tx == nil {
		return nil, fmt.Errorf("%w: transfer is required", token.ErrInvalidTransaction)
	}
	// Verify the signature and sender authZ first: a transfer must still be
	// signed by the sender. This rejects unsigned/forged transfers before any
	// consensus submission.
	if err := tx.Verify(); err != nil {
		return nil, err
	}
	if tx.To == "" {
		return nil, ErrEmptyRecipient
	}
	sender := tx.SenderID()
	if tx.To == sender {
		return nil, fmt.Errorf("settle from %s to itself: %w", sender, ErrSelfTransfer)
	}

	if err := c.engine.Submit(tx); err != nil {
		return nil, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	committed, applied, err := c.engine.WaitForSettlement(waitCtx, tx)
	if err != nil {
		return nil, err
	}

	result := &marketapi.SettledTransferResult{
		Transfer: marketapi.TransferView{
			From:      sender,
			To:        tx.To,
			Amount:    tx.Amount,
			Nonce:     tx.Nonce,
			Timestamp: tx.Timestamp,
		},
		Committed: committed,
		Applied:   applied,
	}
	if !committed || !applied {
		// Committed but skipped as unaffordable (or not committed): no credits
		// moved. Report the honest precondition failure rather than a settlement.
		return result, fmt.Errorf("settle %d from %s: %w", tx.Amount, sender, ErrTransferNotApplied)
	}

	// Locate the applied transfer in the committed history so the caller can
	// report its stable index and block height. Best-effort: an applied transfer
	// is always present, but a lookup miss is not fatal to the settlement itself.
	if idx, height, ok := c.locate(sender, tx.To, tx.Amount, tx.Nonce); ok {
		result.Transfer.Index = idx
		result.Transfer.BlockHeight = height
	}
	return result, nil
}

// locate finds the committed transfer matching the given fields and returns its
// stable index and block height. It scans the committed history from the end is
// not possible (the engine pages from the start), so it pages forward and
// remembers the last match, which is the just-committed transfer for a unique
// (sender, nonce) pair.
func (c *TransferSettlementCoordinator) locate(from, to string, amount, nonce uint64) (uint64, uint64, bool) {
	var start uint64
	var foundIdx, foundHeight uint64
	var found bool
	for {
		transfers, total, err := c.engine.CommittedTransfers(start, 512)
		if err != nil || len(transfers) == 0 {
			break
		}
		for _, t := range transfers {
			if t.From == from && t.To == to && t.Amount == amount && t.Nonce == nonce {
				foundIdx = t.Index
				foundHeight = t.Height
				found = true
			}
		}
		last := transfers[len(transfers)-1].Index
		start = last + 1
		if start >= total {
			break
		}
	}
	return foundIdx, foundHeight, found
}

// viewFromCommitted maps a consensus.CommittedTransfer to the marketapi view
// the history RPCs return.
func viewFromCommitted(t consensus.CommittedTransfer) marketapi.TransferView {
	return marketapi.TransferView{
		Index:       t.Index,
		From:        t.From,
		To:          t.To,
		Amount:      t.Amount,
		Nonce:       t.Nonce,
		BlockHeight: t.Height,
		Timestamp:   t.Timestamp,
	}
}

// History returns committed transfers in globally-agreed order for the
// transaction-list RPC. It is a thin pass-through to the engine so marketapi can
// read consensus history without importing the consensus package.
func (c *TransferSettlementCoordinator) History(start uint64, limit int) ([]marketapi.TransferView, uint64, error) {
	transfers, total, err := c.engine.CommittedTransfers(start, limit)
	if err != nil {
		return nil, 0, err
	}
	out := make([]marketapi.TransferView, 0, len(transfers))
	for _, t := range transfers {
		out = append(out, viewFromCommitted(t))
	}
	return out, total, nil
}

// TransferAt returns a single committed transfer by index for the
// transaction-get RPC.
func (c *TransferSettlementCoordinator) TransferAt(index uint64) (*marketapi.TransferView, error) {
	t, err := c.engine.CommittedTransferAt(index)
	if err != nil {
		// Translate consensus's out-of-range error into the marketapi sentinel so
		// the RPC maps a missing index to NotFound without marketapi importing the
		// consensus package.
		if errors.Is(err, consensus.ErrHeightOutOfRange) {
			return nil, fmt.Errorf("%w: index %d: %v", marketapi.ErrTransferNotFound, index, err)
		}
		return nil, err
	}
	v := viewFromCommitted(*t)
	return &v, nil
}
