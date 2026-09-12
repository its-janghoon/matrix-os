package marketapi

import (
	"bytes"
	"context"
	"encoding/hex"
	"sort"
	"strings"
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
		Id:           j.ID,
		Buyer:        j.Buyer,
		Provider:     j.Provider,
		Units:        j.Units,
		Price:        j.Price,
		PricePerUnit: j.PricePerUnit,
		QuoteId:      j.QuoteID,
		QuoteVersion: j.QuoteVersion,
		Status:       jobStatusToProto(j.Status),
	}
	if !j.CreatedAt.IsZero() {
		pj.CreatedAt = timestamppb.New(j.CreatedAt)
	}
	if !j.UpdatedAt.IsZero() {
		pj.UpdatedAt = timestamppb.New(j.UpdatedAt)
	}
	if !j.QuoteObservedAt.IsZero() {
		pj.QuoteObservedAt = timestamppb.New(j.QuoteObservedAt)
	}
	if !j.QuoteValidUntil.IsZero() {
		pj.QuoteValidUntil = timestamppb.New(j.QuoteValidUntil)
	}
	return pj
}

// localProviderToProto converts a local market.Provider to proto, flagged LOCAL.
func localProviderToProto(p market.Provider) *marketv1.Provider {
	out := &marketv1.Provider{
		Id:                p.ID,
		Capacity:          p.Capacity,
		PricePerUnit:      p.PricePerUnit,
		Available:         p.Available,
		Origin:            marketv1.ProviderOrigin_PROVIDER_ORIGIN_LOCAL,
		Models:            p.Models,
		CostPerUnit:       p.CostPerUnit,
		MarkupBasisPoints: p.MarkupBasisPoints,
		QuoteId:           p.QuoteID,
		QuoteVersion:      p.QuoteVersion,
	}
	if !p.ObservedAt.IsZero() {
		out.ObservedAt = timestamppb.New(p.ObservedAt)
	}
	if !p.ValidUntil.IsZero() {
		out.ValidUntil = timestamppb.New(p.ValidUntil)
	}
	return out
}

// remoteProviderToProto converts a discovered remote provider to proto, flagged
// REMOTE and carrying what a buyer needs to act on it: which node is offering,
// where to reach it, and how long this node has been hearing from it.
//
// Before the endpoint was carried, a caller receiving one of these learned that
// somebody somewhere sold a model and had no way to connect - the peer id is how
// NODES find each other and is not an address any HTTP client can dial.
func remoteProviderToProto(rp marketexchange.RemoteProvider) *marketv1.Provider {
	out := &marketv1.Provider{
		Id:                 rp.ID,
		NodeId:             rp.NodeID,
		Endpoint:           rp.Endpoint,
		AnnouncementsHeard: rp.Announcements,
		Capacity:           rp.Capacity,
		PricePerUnit:       rp.PricePerUnit,
		Available:          rp.Available,
		Origin:             marketv1.ProviderOrigin_PROVIDER_ORIGIN_REMOTE,
		PeerId:             rp.PeerID,
		Models:             rp.Models,
		CostPerUnit:        rp.CostPerUnit,
		MarkupBasisPoints:  rp.MarkupBasisPoints,
		QuoteId:            rp.QuoteID,
		QuoteVersion:       rp.QuoteVersion,
	}
	if !rp.ObservedAt.IsZero() {
		out.ObservedAt = timestamppb.New(rp.ObservedAt)
	}
	if !rp.ValidUntil.IsZero() {
		out.ValidUntil = timestamppb.New(rp.ValidUntil)
	}
	if !rp.FirstSeen.IsZero() {
		out.FirstSeen = timestamppb.New(rp.FirstSeen)
	}
	return out
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

// requestTime validates an optional protobuf timestamp and converts it to UTC.
// Invalid wire timestamps are client errors rather than values to normalize.
func requestTime(ts *timestamppb.Timestamp, field string) (time.Time, error) {
	if ts == nil {
		return time.Time{}, nil
	}
	if err := ts.CheckValid(); err != nil {
		return time.Time{}, status.Errorf(codes.InvalidArgument, "%s is invalid: %v", field, err)
	}
	return ts.AsTime().UTC(), nil
}

// RegisterProvider advertises local compute capacity on the order book.
func (s *Service) RegisterProvider(ctx context.Context, req *marketv1.RegisterProviderRequest) (*marketv1.RegisterProviderResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.GetCostPerUnit() > 0 && req.GetPricePerUnit() == 0 {
		return nil, status.Error(codes.InvalidArgument, "cost_per_unit is manual MATRIX-denominated metadata; price_per_unit must be a nonzero final price that already includes margin and protocol-fee gross-up")
	}
	observedAt, err := requestTime(req.GetObservedAt(), "observed_at")
	if err != nil {
		return nil, err
	}
	validUntil, err := requestTime(req.GetValidUntil(), "valid_until")
	if err != nil {
		return nil, err
	}
	p := market.Provider{
		ID:                req.GetId(),
		Capacity:          req.GetCapacity(),
		PricePerUnit:      req.GetPricePerUnit(),
		CostPerUnit:       req.GetCostPerUnit(),
		MarkupBasisPoints: req.GetMarkupBasisPoints(),
		QuoteID:           req.GetQuoteId(),
		QuoteVersion:      req.GetQuoteVersion(),
		ObservedAt:        observedAt,
		ValidUntil:        validUntil,
		Models:            req.GetModels(),
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

// UpdateProviderQuote refreshes only an existing provider's economic quote.
// Market.UpdateProviderQuote preserves capacity, current reservations, and
// advertised models.
func (s *Service) UpdateProviderQuote(ctx context.Context, req *marketv1.UpdateProviderQuoteRequest) (*marketv1.UpdateProviderQuoteResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "provider id is required")
	}
	if req.GetCostPerUnit() > 0 && req.GetPricePerUnit() == 0 {
		return nil, status.Error(codes.InvalidArgument, "cost_per_unit is manual MATRIX-denominated metadata; price_per_unit must be a nonzero final price that already includes margin and protocol-fee gross-up")
	}
	observedAt, err := requestTime(req.GetObservedAt(), "observed_at")
	if err != nil {
		return nil, err
	}
	validUntil, err := requestTime(req.GetValidUntil(), "valid_until")
	if err != nil {
		return nil, err
	}
	quote := market.Provider{
		PricePerUnit:      req.GetPricePerUnit(),
		CostPerUnit:       req.GetCostPerUnit(),
		MarkupBasisPoints: req.GetMarkupBasisPoints(),
		QuoteID:           req.GetQuoteId(),
		QuoteVersion:      req.GetQuoteVersion(),
		ObservedAt:        observedAt,
		ValidUntil:        validUntil,
	}
	if err := s.market.UpdateProviderQuote(req.GetId(), quote); err != nil {
		return nil, mapMarketError(err)
	}
	stored, ok := s.market.GetProvider(req.GetId())
	if !ok {
		return nil, status.Error(codes.Internal, "provider updated but not found")
	}
	return &marketv1.UpdateProviderQuoteResponse{Provider: localProviderToProto(stored)}, nil
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
	// Refuse legacy active jobs before either the direct or consensus settler can
	// move funds. They remain cancellable, but their missing quote identity and
	// price snapshot cannot be safely reconstructed.
	if err := s.market.ValidateJobQuoteForSettlement(req.GetId()); err != nil {
		return nil, mapMarketError(err)
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
	// Either kind of account: 32 bytes ed25519, 20 bytes an Ethereum address.
	pub, err := token.ParseSenderKey(req.GetFromPublicKey())
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
	// Funding is not a consensus transaction: it moves reward-pool MATRIX on THIS
	// node's ledger only. On a multi-validator network that diverges the nodes'
	// reward pools, and the provider emission clamps its per-block budget to the
	// pool balance it reads while applying a block - so two nodes with different
	// pools credit providers different amounts from the same block. That is a
	// fork, not a rounding difference.
	//
	// Found by running two nodes: funding an account on one left the other
	// reporting a balance of zero for it, which is the visible half of the same
	// divergence.
	if n := s.validators(); n > 1 {
		return nil, status.Errorf(codes.FailedPrecondition,
			"reward-pool funding is not consensus-ordered, so it would diverge the %d validators' "+
				"reward pools and fork the provider emission. Move value with a signed transfer instead, "+
				"or set the allocation in every node's genesis config", n)
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

// GetBridgeReadiness signs a caller's fresh challenge in a domain that is
// distinct from the mint-attestation digest. It fails closed unless the node has
// both bridge deployment parameters and a live attestor key.
func (s *Service) GetBridgeReadiness(ctx context.Context, req *marketv1.GetBridgeReadinessRequest) (*marketv1.GetBridgeReadinessResponse, error) {
	if s.readinessSigner == nil {
		return nil, status.Error(codes.FailedPrecondition,
			"bridge readiness is unavailable: this node needs both bridge.contract and bridge.attestor_keystore")
	}
	challenge := req.GetChallenge()
	if len(challenge) != 32 {
		return nil, status.Errorf(codes.InvalidArgument,
			"challenge must be exactly 32 bytes, got %d", len(challenge))
	}
	proof, err := s.readinessSigner.SignBridgeReadiness(challenge)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "sign bridge readiness: %v", err)
	}
	if proof == nil || len(proof.Challenge) != 32 || !bytes.Equal(proof.Challenge, challenge) || len(proof.Signature) != 65 {
		return nil, status.Error(codes.Internal, "bridge readiness signer returned a malformed proof")
	}
	return &marketv1.GetBridgeReadinessResponse{
		ChainId:       proof.ChainID,
		Contract:      proof.Contract,
		Attestor:      proof.Attestor,
		MinLockNative: proof.MinLockNative,
		Challenge:     append([]byte(nil), proof.Challenge...),
		Signature:     append([]byte(nil), proof.Signature...),
	}, nil
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

// GetLockAttestation returns this node's signature authorizing the wrapped mint
// for a lock the chain has already committed.
//
// It is the half of the bridge that had no network surface at all. Locking is a
// consensus-ordered transaction now, but nothing could sign the mint
// authorization, so no wMATRIX could come into existence and the DEX liquidity
// the whole on-ramp depends on could not be seeded.
//
// ONE signature, deliberately. Each validator holds its own attestor key and the
// contract counts the threshold, so a client gathers m of these by asking each
// validator's node and feeds the set to WrappedMatrix.mint.
func (s *Service) GetLockAttestation(ctx context.Context, req *marketv1.GetLockAttestationRequest) (*marketv1.GetLockAttestationResponse, error) {
	if s.lockAttestor == nil {
		return nil, status.Error(codes.FailedPrecondition,
			"this node cannot attest: it has no bridge configured (bridge.contract) or no "+
				"attestor key (bridge.attestor_keystore). Ask a validator that has both.")
	}
	raw := strings.TrimPrefix(strings.TrimSpace(req.GetLockId()), "0x")
	lockID, err := hex.DecodeString(raw)
	if err != nil || len(lockID) != 32 {
		return nil, status.Error(codes.InvalidArgument,
			"lock_id must be 32 bytes of hex, as derived from the committed lock transaction")
	}

	att, err := s.lockAttestor.AttestLock(lockID)
	if err != nil {
		// A lock id nobody committed is the ordinary client error here - a caller
		// that derived the id differently, or asked before the block committed -
		// so it is NotFound rather than Internal.
		return nil, status.Errorf(codes.NotFound,
			"no committed lock with id %s on this node: %v", raw, err)
	}
	return &marketv1.GetLockAttestationResponse{
		Recipient:    att.Recipient,
		Erc20Amount:  att.ERC20Amount.String(),
		LockId:       raw,
		Signature:    hex.EncodeToString(att.Signature),
		Attestor:     att.Attestor,
		NativeAmount: att.NativeAmount,
	}, nil
}
