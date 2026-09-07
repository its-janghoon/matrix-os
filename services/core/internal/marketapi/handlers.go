package marketapi

import (
	"context"
	"sort"
	"time"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/marketexchange"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// timeUnixNano rebuilds a time.Time from a unix-nanosecond count.
func timeUnixNano(ns int64) time.Time {
	return time.Unix(0, ns).UTC()
}

// jobStatusToProto maps a market.JobStatus to its proto enum.
func jobStatusToProto(s market.JobStatus) marketv1.JobStatus {
	switch s {
	case market.JobPending:
		return marketv1.JobStatus_JOB_STATUS_PENDING
	case market.JobRunning:
		return marketv1.JobStatus_JOB_STATUS_RUNNING
	case market.JobCompleted:
		return marketv1.JobStatus_JOB_STATUS_COMPLETED
	case market.JobFailed:
		return marketv1.JobStatus_JOB_STATUS_FAILED
	case market.JobCancelled:
		return marketv1.JobStatus_JOB_STATUS_CANCELLED
	default:
		return marketv1.JobStatus_JOB_STATUS_UNSPECIFIED
	}
}

// jobToProto converts a market.Job to its proto representation.
func jobToProto(j market.Job) *marketv1.Job {
	pj := &marketv1.Job{
		Id:       j.ID,
		Buyer:    j.Buyer,
		Provider: j.Provider,
		Units:    j.Units,
		Price:    j.Price,
		Status:   jobStatusToProto(j.Status),
	}
	if !j.CreatedAt.IsZero() {
		pj.CreatedAt = timestamppb.New(j.CreatedAt)
	}
	if !j.UpdatedAt.IsZero() {
		pj.UpdatedAt = timestamppb.New(j.UpdatedAt)
	}
	return pj
}

// localProviderToProto converts a local market.Provider to proto, flagged LOCAL.
func localProviderToProto(p market.Provider) *marketv1.Provider {
	return &marketv1.Provider{
		Id:           p.ID,
		Capacity:     p.Capacity,
		PricePerUnit: p.PricePerUnit,
		Available:    p.Available,
		Origin:       marketv1.ProviderOrigin_PROVIDER_ORIGIN_LOCAL,
		Models:       p.Models,
	}
}

// remoteProviderToProto converts a discovered remote provider to proto, flagged
// REMOTE and carrying the announcing peer ID.
func remoteProviderToProto(rp marketexchange.RemoteProvider) *marketv1.Provider {
	return &marketv1.Provider{
		Id:           rp.ID,
		Capacity:     rp.Capacity,
		PricePerUnit: rp.PricePerUnit,
		Available:    rp.Available,
		Origin:       marketv1.ProviderOrigin_PROVIDER_ORIGIN_REMOTE,
		PeerId:       rp.PeerID,
		Models:       rp.Models,
	}
}

// recordToProto converts a token chain record to the proto Transaction. It is
// the fallback mapping used only by a Service with no consensus transfer settler
// (tests / a library caller wiring the market on its own), where the history
// still comes from the per-node token.Chain. The record's chain height is
// reported as the transfer index in that mode.
func recordToProto(rec *token.Record) *marketv1.Transaction {
	return &marketv1.Transaction{
		Index:       rec.Height,
		From:        rec.Tx.SenderID(),
		To:          rec.Tx.To,
		Amount:      rec.Tx.Amount,
		Nonce:       rec.Tx.Nonce,
		BlockHeight: rec.Height,
		Timestamp:   nanosToTimestamp(rec.Tx.Timestamp),
	}
}

// transferViewToProto converts a consensus committed-transfer view to the proto
// Transaction. This is the mapping used when a consensus transfer settler backs
// the history RPCs, which is the node's default: the history is the ordered
// sequence of committed transfers, identical on every node.
func transferViewToProto(t TransferView) *marketv1.Transaction {
	return &marketv1.Transaction{
		Index:       t.Index,
		From:        t.From,
		To:          t.To,
		Amount:      t.Amount,
		Nonce:       t.Nonce,
		BlockHeight: t.BlockHeight,
		Timestamp:   nanosToTimestamp(t.Timestamp),
	}
}

// RegisterProvider advertises local compute capacity on the order book.
func (s *Service) RegisterProvider(ctx context.Context, req *marketv1.RegisterProviderRequest) (*marketv1.RegisterProviderResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	p := market.Provider{
		ID:           req.GetId(),
		Capacity:     req.GetCapacity(),
		PricePerUnit: req.GetPricePerUnit(),
		Models:       req.GetModels(),
	}
	if err := s.market.RegisterProvider(p); err != nil {
		return nil, mapMarketError(err)
	}
	// Re-read to return the persisted record with Available seeded to Capacity.
	stored, ok := s.market.GetProvider(req.GetId())
	if !ok {
		return nil, status.Error(codes.Internal, "provider registered but not found")
	}
	return &marketv1.RegisterProviderResponse{Provider: localProviderToProto(stored)}, nil
}

// ListProviders enumerates local providers and, when requested and available,
// remote P2P providers, each flagged by origin. Local entries are returned
// first, then remote entries, each group ordered by ID.
func (s *Service) ListProviders(ctx context.Context, req *marketv1.ListProvidersRequest) (*marketv1.ListProvidersResponse, error) {
	// A model filter narrows both groups. Locals come from ProvidersForModel so
	// the filter also drops providers with no capacity left to reserve, which is
	// what a caller asking "who can serve this model" means.
	model := req.GetModel()

	var locals []market.Provider
	if model != "" {
		locals = s.market.ProvidersForModel(model)
	} else {
		locals = s.market.ListProviders()
	}
	out := make([]*marketv1.Provider, 0, len(locals))
	for _, p := range locals {
		out = append(out, localProviderToProto(p))
	}

	if req.GetIncludeRemote() && s.exchange != nil {
		remotes := s.exchange.ListRemoteProviders()
		sort.Slice(remotes, func(i, j int) bool { return remotes[i].ID < remotes[j].ID })
		for _, rp := range remotes {
			if model != "" && (rp.Available == 0 || !rp.ServesModel(model)) {
				continue
			}
			out = append(out, remoteProviderToProto(rp))
		}
	}

	return &marketv1.ListProvidersResponse{Providers: out}, nil
}

// SubmitJob reserves provider capacity for a paid compute job.
func (s *Service) SubmitJob(ctx context.Context, req *marketv1.SubmitJobRequest) (*marketv1.SubmitJobResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	job, err := s.market.SubmitJob(req.GetBuyer(), req.GetProvider(), req.GetUnits())
	if err != nil {
		return nil, mapMarketError(err)
	}
	return &marketv1.SubmitJobResponse{Job: jobToProto(*job)}, nil
}

// GetJob fetches a single job by ID.
func (s *Service) GetJob(ctx context.Context, req *marketv1.GetJobRequest) (*marketv1.GetJobResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	job, ok := s.market.GetJob(req.GetId())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "job %q not found", req.GetId())
	}
	return &marketv1.GetJobResponse{Job: jobToProto(job)}, nil
}

// ListJobs enumerates jobs in chronological order, optionally filtered by buyer.
func (s *Service) ListJobs(ctx context.Context, req *marketv1.ListJobsRequest) (*marketv1.ListJobsResponse, error) {
	jobs := s.market.ListJobs()
	buyer := req.GetBuyer()
	out := make([]*marketv1.Job, 0, len(jobs))
	for _, j := range jobs {
		if buyer != "" && j.Buyer != buyer {
			continue
		}
		out = append(out, jobToProto(j))
	}
	return &marketv1.ListJobsResponse{Jobs: out}, nil
}

// CompleteJob settles a pending/running job, moving native MATRIX buyer ->
// provider.
//
// With a JobSettler configured - which is what the node does - the payment is a
// signed transfer from the buyer that consensus commits and every node applies.
// The buyer's signing key is therefore required, and a job whose buyer this
// node holds no key for is refused rather than charged: paying out of an
// account without its owner's signature is the thing this path exists to stop.
// The reservation is left intact on a refusal so the caller can cancel it.
func (s *Service) CompleteJob(ctx context.Context, req *marketv1.CompleteJobRequest) (*marketv1.CompleteJobResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.settler != nil {
		job, err := s.settler.SettleAndCompleteJob(ctx, req.GetId())
		if err != nil {
			return nil, mapSettlementError(err)
		}
		return &marketv1.CompleteJobResponse{Job: jobToProto(*job)}, nil
	}
	if err := s.market.CompleteJob(req.GetId()); err != nil {
		return nil, mapMarketError(err)
	}
	job, ok := s.market.GetJob(req.GetId())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "job %q not found", req.GetId())
	}
	return &marketv1.CompleteJobResponse{Job: jobToProto(job)}, nil
}

// CancelJob cancels a pending/running job and returns reserved capacity.
func (s *Service) CancelJob(ctx context.Context, req *marketv1.CancelJobRequest) (*marketv1.CancelJobResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if err := s.market.CancelJob(req.GetId()); err != nil {
		return nil, mapMarketError(err)
	}
	job, ok := s.market.GetJob(req.GetId())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "job %q not found", req.GetId())
	}
	return &marketv1.CancelJobResponse{Job: jobToProto(job)}, nil
}

// GetBalance reads a compute-credit balance.
func (s *Service) GetBalance(ctx context.Context, req *marketv1.GetBalanceRequest) (*marketv1.GetBalanceResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	bal, err := s.market.Ledger().Balance(req.GetAccount())
	if err != nil {
		return nil, mapMarketError(err)
	}
	return &marketv1.GetBalanceResponse{Account: req.GetAccount(), Balance: bal}, nil
}

// GetTransaction reads a single committed transfer by its stable index. When a
// consensus transfer settler is installed (the node's default) it reads the
// globally-agreed consensus transaction history, so the same index resolves to
// the same transfer on every node. Without one it falls back to the per-node
// token.Chain by height.
func (s *Service) GetTransaction(ctx context.Context, req *marketv1.GetTransactionRequest) (*marketv1.GetTransactionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.transferSettler != nil {
		t, err := s.transferSettler.TransferAt(req.GetIndex())
		if err != nil {
			return nil, mapTransferHistoryError(err)
		}
		return &marketv1.GetTransactionResponse{Transaction: transferViewToProto(*t)}, nil
	}
	rec, err := s.chain.TransactionAt(req.GetIndex())
	if err != nil {
		return nil, mapMarketError(err)
	}
	return &marketv1.GetTransactionResponse{Transaction: recordToProto(rec)}, nil
}

// ListTransactions reads committed transfers in ascending index (commit) order,
// starting at start_index and returning at most limit records. When a consensus
// transfer settler is installed (the node's default) it reads the
// globally-agreed consensus transaction history, so two nodes return the
// identical sequence. Without one it falls back to the per-node token.Chain.
func (s *Service) ListTransactions(ctx context.Context, req *marketv1.ListTransactionsRequest) (*marketv1.ListTransactionsResponse, error) {
	start := req.GetStartIndex()
	limit := req.GetLimit()

	if s.transferSettler != nil {
		var lim int
		if limit > 0 {
			lim = int(limit)
		}
		transfers, total, err := s.transferSettler.History(start, lim)
		if err != nil {
			return nil, mapTransferHistoryError(err)
		}
		out := make([]*marketv1.Transaction, 0, len(transfers))
		for _, t := range transfers {
			out = append(out, transferViewToProto(t))
		}
		return &marketv1.ListTransactionsResponse{Transactions: out, Total: total}, nil
	}

	length, err := s.chain.Len()
	if err != nil {
		return nil, mapMarketError(err)
	}
	out := make([]*marketv1.Transaction, 0)
	for height := start; height < length; height++ {
		if limit > 0 && uint64(len(out)) >= limit {
			break
		}
		rec, err := s.chain.TransactionAt(height)
		if err != nil {
			return nil, mapMarketError(err)
		}
		out = append(out, recordToProto(rec))
	}
	return &marketv1.ListTransactionsResponse{Transactions: out, Total: length}, nil
}

// SubmitSignedTransfer verifies a client-signed ed25519 transfer and settles it.
//
// When a consensus transfer settler is installed - which is what the node does -
// the transfer is submitted into consensus, ordered into a committed block by a
// quorum, and applied deterministically by every node to the shared ledger, so
// two nodes agree on the resulting balances and the transfer is subject to the
// (default-off) protocol fee like every other committed transfer. The signature
// and sender authorization are still required, an empty recipient and a
// self-transfer are still rejected, and a transfer that commits but is
// unaffordable at apply time returns FailedPrecondition (no credits moved),
// while a settlement that cannot be confirmed in time returns DeadlineExceeded.
//
// Without a transfer settler (a Service with no consensus engine - tests, a
// library caller) it falls back to the per-node token.SettledLedger path.
func (s *Service) SubmitSignedTransfer(ctx context.Context, req *marketv1.SubmitSignedTransferRequest) (*marketv1.SubmitSignedTransferResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	pub, err := token.ParsePublicKey(req.GetFromPublicKey())
	if err != nil {
		return nil, mapMarketError(err)
	}
	tx := &token.Transaction{
		From:      pub,
		To:        req.GetTo(),
		Amount:    req.GetAmount(),
		Nonce:     req.GetNonce(),
		Timestamp: req.GetTimestamp(),
		PrevHash:  req.GetPrevHash(),
		Signature: req.GetSignature(),
	}

	if s.transferSettler != nil {
		result, err := s.transferSettler.SettleSignedTransfer(ctx, tx)
		if err != nil {
			return nil, mapSettlementError(err)
		}
		return &marketv1.SubmitSignedTransferResponse{
			Transaction: transferViewToProto(result.Transfer),
			Committed:   result.Committed,
			Applied:     result.Applied,
		}, nil
	}

	rec, err := s.settled.Settle(tx)
	if err != nil {
		return nil, mapMarketError(err)
	}
	return &marketv1.SubmitSignedTransferResponse{
		Transaction: recordToProto(rec),
		Committed:   true,
		Applied:     true,
	}, nil
}

// FundAccount moves native MATRIX from the genesis-allocated reward pool to the
// requested account through the treasury (Funder.FundFromRewardPool), then
// returns the account's new balance. It never mints new coins: the coins already
// exist in the reward pool, so the native supply cap is respected. Requests that
// exceed the reward-pool balance map to FailedPrecondition, as do supply-cap
// violations; a node wired without a treasury returns Unimplemented.
func (s *Service) FundAccount(ctx context.Context, req *marketv1.FundAccountRequest) (*marketv1.FundAccountResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.funder == nil {
		return nil, status.Error(codes.Unimplemented, "reward-pool funding is not enabled on this node")
	}
	// Reward-pool funding carries no per-request signature (unlike
	// SubmitSignedTransfer), so it MUST NOT be served on an unauthenticated
	// surface: on an ACLs-off node any client on the wire could otherwise move
	// reward-pool MATRIX into any account it names. Require the server to enforce
	// authentication for this RPC regardless of the node's general posture. The
	// interceptor still enforces the actual credential check when auth is on; this
	// guard closes the open-node hole where no interceptor runs at all.
	if !s.authEnforced {
		return nil, status.Error(codes.FailedPrecondition,
			"reward-pool funding requires authentication: enable ACLs (security.enable_acls) to expose FundAccount")
	}
	if req.GetAccount() == "" {
		return nil, status.Error(codes.InvalidArgument, "account is required")
	}
	if req.GetAmount() == 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be greater than 0")
	}
	if err := s.funder.FundFromRewardPool(req.GetAccount(), req.GetAmount()); err != nil {
		return nil, mapMarketError(err)
	}
	bal, err := s.market.Ledger().Balance(req.GetAccount())
	if err != nil {
		return nil, mapMarketError(err)
	}
	return &marketv1.FundAccountResponse{Account: req.GetAccount(), Balance: bal}, nil
}

// GetBridgeReconciliation returns the lock-and-mint bridge backing snapshot,
// making the reconciliation report a node endpoint rather than a manual query.
//
// It is a read of the node's own escrow accounting: the reconciler verifies the
// on-ledger escrow balance equals the (locked - unlocked) accounting and returns
// the snapshot stamped with the committed block height it reflects, so the
// report is reproducible from public data. When the node has no bridge
// configured (bridge.contract unset, the default) the reconciler is nil and this
// returns FailedPrecondition rather than nil-panicking; when the escrow balance
// and the accounting disagree the reconciler errors and this returns Internal,
// since that signals a backing-invariant violation, not a client mistake. It is
// gated behind the same auth as the other surfaces by the server interceptors.
func (s *Service) GetBridgeReconciliation(ctx context.Context, req *marketv1.GetBridgeReconciliationRequest) (*marketv1.GetBridgeReconciliationResponse, error) {
	if s.reconciler == nil {
		return nil, status.Error(codes.FailedPrecondition,
			"bridge reconciliation is not available: no bridge is configured on this node (set bridge.contract)")
	}
	snap, err := s.reconciler.Reconcile()
	if err != nil {
		// A reconciliation error is an escrow/accounting mismatch: a backing
		// invariant broke, which is a server-side fault, not a bad request.
		return nil, status.Error(codes.Internal, err.Error())
	}
	outstandingERC20 := "0"
	if snap.OutstandingERC20 != nil {
		outstandingERC20 = snap.OutstandingERC20.String()
	}
	return &marketv1.GetBridgeReconciliationResponse{
		LockedNative:      snap.LockedNative,
		UnlockedNative:    snap.UnlockedNative,
		OutstandingNative: snap.OutstandingNative,
		EscrowBalance:     snap.EscrowBalance,
		OutstandingErc20:  outstandingERC20,
		BlockHeight:       snap.BlockHeight,
	}, nil
}
