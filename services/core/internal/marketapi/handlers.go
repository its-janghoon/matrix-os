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
	}
}

// recordToProto converts a token chain record to the proto Transaction.
func recordToProto(rec *token.Record) *marketv1.Transaction {
	return &marketv1.Transaction{
		Height:    rec.Height,
		From:      rec.Tx.SenderID(),
		To:        rec.Tx.To,
		Amount:    rec.Tx.Amount,
		Nonce:     rec.Tx.Nonce,
		Hash:      append([]byte(nil), rec.Hash...),
		PrevHash:  append([]byte(nil), rec.Tx.PrevHash...),
		Timestamp: nanosToTimestamp(rec.Tx.Timestamp),
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
	locals := s.market.ListProviders()
	out := make([]*marketv1.Provider, 0, len(locals))
	for _, p := range locals {
		out = append(out, localProviderToProto(p))
	}

	if req.GetIncludeRemote() && s.exchange != nil {
		remotes := s.exchange.ListRemoteProviders()
		sort.Slice(remotes, func(i, j int) bool { return remotes[i].ID < remotes[j].ID })
		for _, rp := range remotes {
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

// GetTransaction reads a single settled transaction from the chain by height.
func (s *Service) GetTransaction(ctx context.Context, req *marketv1.GetTransactionRequest) (*marketv1.GetTransactionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	rec, err := s.chain.TransactionAt(req.GetHeight())
	if err != nil {
		return nil, mapMarketError(err)
	}
	return &marketv1.GetTransactionResponse{Transaction: recordToProto(rec)}, nil
}

// ListTransactions reads settled transactions from the chain in ascending
// height order, starting at start_height and returning at most limit records.
func (s *Service) ListTransactions(ctx context.Context, req *marketv1.ListTransactionsRequest) (*marketv1.ListTransactionsResponse, error) {
	length, err := s.chain.Len()
	if err != nil {
		return nil, mapMarketError(err)
	}

	start := req.GetStartHeight()
	out := make([]*marketv1.Transaction, 0)
	for height := start; height < length; height++ {
		if req.GetLimit() > 0 && uint64(len(out)) >= req.GetLimit() {
			break
		}
		rec, err := s.chain.TransactionAt(height)
		if err != nil {
			return nil, mapMarketError(err)
		}
		out = append(out, recordToProto(rec))
	}

	return &marketv1.ListTransactionsResponse{Transactions: out, ChainLength: length}, nil
}

// SubmitSignedTransfer verifies and applies a client-signed ed25519 transfer
// through the signed-settlement path (token.SettledLedger.Settle), then returns
// the settled chain record.
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
	rec, err := s.settled.Settle(tx)
	if err != nil {
		return nil, mapMarketError(err)
	}
	return &marketv1.SubmitSignedTransferResponse{Transaction: recordToProto(rec)}, nil
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
