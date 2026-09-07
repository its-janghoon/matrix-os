// Package marketapi implements the external gRPC surface of the Matrix OS
// compute marketplace (matrix.market.v1.MarketService). It is the "external
// API" layer that was previously deferred: it exposes the local order book, the
// remote P2P providers discovered by the marketexchange, the compute-credit
// ledger, the signed-settlement path, and the hash-chained token log to
// external buyers and providers over gRPC.
//
// The service is backed by the real subsystems the node already runs:
//   - *market.Market for the local order book (providers and jobs) and ledger;
//   - *token.Chain plus token.SettledLedger for signed settlement and chain
//     reads;
//   - *marketexchange.Exchange (optional) for remote provider discovery.
//
// It follows the internal/admin gRPC pattern: a Server owns a grpc.Server,
// registers the market service and a health service, and exposes
// Start(ctx)/Stop(ctx). Authentication is optional and mirrors admin's model:
// when an *admin.Authenticator is supplied the server installs the same
// require-auth interceptors, and health checks remain unauthenticated.
package marketapi

import (
	"context"
	"errors"
	"fmt"
	"net"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ecirlabs/matrix-core/internal/admin"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/marketexchange"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// Funder is the narrow reward-pool funding capability the market API needs to
// implement FundAccount. It is satisfied by *token.Treasury and moves native
// MATRIX out of the genesis-allocated reward pool into a recipient account
// without minting new coins. It is declared here (consumer side) so marketapi
// does not depend on the whole Treasury surface and tests can substitute a fake.
type Funder interface {
	// FundFromRewardPool moves amount native base units from the reserved reward
	// pool to recipient, returning an error (e.g. market.ErrInsufficientFunds)
	// without mutating balances when the pool cannot cover the amount.
	FundFromRewardPool(recipient string, amount uint64) error
}

// Service implements marketv1.MarketServiceServer over the node's marketplace
// subsystems. The generated stubs are built with require_unimplemented_servers
// disabled, so no embedding is required; every RPC is implemented explicitly.
type Service struct {
	market   *market.Market
	settled  *token.SettledLedger
	chain    *token.Chain
	exchange *marketexchange.Exchange
	funder   Funder
	// settler, when non-nil, settles CompleteJob through consensus instead of by
	// a direct ledger transfer. See the JobSettler doc for why that matters.
	settler JobSettler
	// transferSettler, when non-nil, settles SubmitSignedTransfer through
	// consensus and backs the GetTransaction/ListTransactions history with the
	// committed block chain. See the TransferSettler doc. When nil (a Service
	// with no consensus engine behind it - tests, a library caller wiring the
	// market on its own), SubmitSignedTransfer falls back to the per-node
	// token.SettledLedger path and the history RPCs read the token.Chain.
	transferSettler TransferSettler
	// authEnforced reports whether the server in front of this Service requires
	// authentication on every mutating RPC. FundAccount refuses to run when it is
	// false: unlike SubmitSignedTransfer, a funding request carries no per-request
	// signature, so on an unauthenticated (ACLs-off) server it would be an
	// unauthenticated reward-pool drain into any caller-named account. Requiring
	// auth for FundAccount regardless of the node's general posture keeps the
	// "every mutating RPC is authorized" invariant the market-auth note relies on.
	authEnforced bool
}

// NewService constructs a Service. market, settled and chain are required;
// exchange may be nil, in which case ListProviders simply omits remote
// providers (a node running without the P2P exchange still serves its local
// order book). funder may be nil, in which case FundAccount returns
// codes.Unimplemented (a node wired without a treasury does not expose
// reward-pool funding).
func NewService(m *market.Market, settled *token.SettledLedger, chain *token.Chain, exchange *marketexchange.Exchange, funder Funder) (*Service, error) {
	if m == nil {
		return nil, fmt.Errorf("marketapi: market is required")
	}
	if settled == nil {
		return nil, fmt.Errorf("marketapi: settled ledger is required")
	}
	if chain == nil {
		return nil, fmt.Errorf("marketapi: token chain is required")
	}
	return &Service{market: m, settled: settled, chain: chain, exchange: exchange, funder: funder}, nil
}

// TransferView is a single committed value transfer read back from the
// consensus transaction history. It is the marketapi-facing shape of a
// consensus.CommittedTransfer, declared here (consumer side) so the market API
// does not import internal/consensus (which would risk an import cycle through
// internal/node) and tests can build one directly. It backs the Transaction the
// GetTransaction/ListTransactions RPCs return now that value transfers settle
// through consensus rather than the per-node token.Chain.
type TransferView struct {
	// Index is the transfer's stable zero-based position in the consensus
	// transaction history (identical on every node).
	Index uint64
	// From is the sender account ID.
	From string
	// To is the recipient account ID.
	To string
	// Amount is the gross amount transferred, before any protocol fee.
	Amount uint64
	// Nonce is the sender's per-transfer uniquifier.
	Nonce uint64
	// BlockHeight is the committed block height the transfer landed in.
	BlockHeight uint64
	// Timestamp is the advisory wall-clock time (unix nanoseconds) the transfer
	// carried.
	Timestamp int64
}

// SettledTransferResult reports how a signed transfer settled through consensus.
// It is the marketapi-facing result of TransferSettler.SettleSignedTransfer,
// declared here for the same reason as TransferView.
type SettledTransferResult struct {
	// Transfer is the settled transfer's view (its committed index and block
	// height are populated once it has committed and applied).
	Transfer TransferView
	// Committed reports whether the transfer was ordered into a committed block.
	Committed bool
	// Applied reports whether the transfer actually moved credits.
	Applied bool
}

// TransferSettler settles an external, client-signed value transfer through
// consensus and exposes the committed transaction history.
//
// It exists because SubmitSignedTransfer used to move native MATRIX through
// token.SettledLedger.Settle: that appended to the per-node token.Chain and
// moved credits on one node, ordered by no quorum, so balances could diverge
// between nodes and the transfer escaped the protocol fee. Settling through
// consensus fixes both - a quorum orders the signed transfer into a committed
// block and every node applies it deterministically (taking the default-off
// fee) - and makes the transaction history the same ordered sequence on every
// node.
//
// It is satisfied by node.TransferSettlementCoordinator.
type TransferSettler interface {
	// SettleSignedTransfer verifies the signed transfer (signature, sender authZ,
	// non-empty recipient, no self-transfer), submits it into consensus, and
	// waits bounded for it to commit and apply. A committed-but-unaffordable
	// transfer is reported as an error wrapping a precondition failure; a
	// settlement that cannot be confirmed in time returns a context error.
	SettleSignedTransfer(ctx context.Context, tx *token.Transaction) (*SettledTransferResult, error)
	// History returns committed transfers in globally-agreed order for
	// ListTransactions.
	History(start uint64, limit int) ([]TransferView, uint64, error)
	// TransferAt returns a single committed transfer by index for
	// GetTransaction.
	TransferAt(index uint64) (*TransferView, error)
}

// JobSettler settles a compute job through consensus and finalizes it.
//
// It exists because CompleteJob used to charge the buyer with a direct
// market.Ledger transfer. That has two problems on anything but a single node.
// It is not agreed: the transfer lands on one node's copy of the ledger and no
// other node ever hears about it, so balances diverge. And it is not
// authorized: no signature from the payer is involved, so any caller who can
// reach the RPC can move any account's balance. Settling through consensus
// fixes both - the payment is a signed transfer from the buyer that a quorum
// commits, and every node applies it.
//
// It is satisfied by node.ComputeSettlementCoordinator.
type JobSettler interface {
	SettleAndCompleteJob(ctx context.Context, jobID string) (*market.Job, error)
}

// SetJobSettler installs the consensus settlement path for CompleteJob. It is
// called once during construction, before the server serves, so no locking is
// needed. With no settler installed CompleteJob falls back to the direct ledger
// transfer, which is only appropriate for a Service with no consensus engine
// behind it (tests, and a library caller wiring the market on its own).
func (s *Service) SetJobSettler(settler JobSettler) { s.settler = settler }

// SetTransferSettler installs the consensus settlement path for
// SubmitSignedTransfer and the consensus-backed transaction history. It is
// called once during construction, before the server serves, so no locking is
// needed. With no transfer settler installed SubmitSignedTransfer falls back to
// the per-node token.SettledLedger path and the history RPCs read the
// token.Chain, which is only appropriate for a Service with no consensus engine
// behind it.
func (s *Service) SetTransferSettler(settler TransferSettler) { s.transferSettler = settler }

// SetAuthEnforced records whether the server hosting this Service requires
// authentication on every mutating RPC. It is set by NewServer from cfg.Auth and
// gates FundAccount (see the Service.authEnforced doc). It is called once during
// construction, before the server starts serving, so no locking is needed.
func (s *Service) SetAuthEnforced(enforced bool) { s.authEnforced = enforced }

// Server hosts the market gRPC service on its own listener, following the
// internal/admin.Server construction pattern (grpc.NewServer, health service,
// Start/Stop). It is a parallel server to the admin server so the marketplace
// API can bind its own address and, when desired, its own auth policy without
// entangling the two surfaces.
type Server struct {
	grpcServer *grpc.Server
	healthSvc  *health.Server
	addr       string
	lis        net.Listener
	svc        *Service
}

// Config configures the market gRPC server.
type Config struct {
	// Addr is the TCP listen address (e.g. "0.0.0.0:9091").
	Addr string
	// Auth, when non-nil, enables authentication using the same API-key model as
	// the admin server: every non-health RPC requires a valid key. When nil the
	// server serves unauthenticated (guarding on nil like the admin services).
	Auth *admin.Authenticator
	// Market, Settled and Chain back the service (required). Exchange is optional.
	Market   *market.Market
	Settled  *token.SettledLedger
	Chain    *token.Chain
	Exchange *marketexchange.Exchange
	// Funder backs the FundAccount RPC (optional). When nil, FundAccount returns
	// codes.Unimplemented. It is satisfied by *token.Treasury.
	Funder Funder
	// Settler, when non-nil, makes CompleteJob settle through consensus rather
	// than by a direct ledger transfer. A node with a consensus engine must set
	// it; see the JobSettler doc.
	Settler JobSettler
	// TransferSettler, when non-nil, makes SubmitSignedTransfer settle through
	// consensus and backs the transaction-history RPCs with the committed block
	// chain. A node with a consensus engine must set it; see the TransferSettler
	// doc.
	TransferSettler TransferSettler
}

// NewServer builds a market gRPC server. It installs the admin auth
// interceptors when cfg.Auth is non-nil, registers the health service, and
// registers the MarketService implementation.
func NewServer(cfg Config) (*Server, error) {
	svc, err := NewService(cfg.Market, cfg.Settled, cfg.Chain, cfg.Exchange, cfg.Funder)
	if err != nil {
		return nil, err
	}
	// Record whether this server authenticates every mutating RPC. FundAccount
	// (unsigned, unlike SubmitSignedTransfer) refuses to run without it.
	svc.SetAuthEnforced(cfg.Auth != nil)
	svc.SetJobSettler(cfg.Settler)
	svc.SetTransferSettler(cfg.TransferSettler)

	var opts []grpc.ServerOption
	if cfg.Auth != nil {
		opts = append(opts,
			grpc.UnaryInterceptor(requireAuthUnaryInterceptor(cfg.Auth)),
			grpc.StreamInterceptor(requireAuthStreamInterceptor(cfg.Auth)),
		)
	}

	grpcServer := grpc.NewServer(opts...)
	healthSvc := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthSvc)
	marketv1.RegisterMarketServiceServer(grpcServer, svc)

	return &Server{
		grpcServer: grpcServer,
		healthSvc:  healthSvc,
		addr:       cfg.Addr,
		svc:        svc,
	}, nil
}

// Start listens on the configured address and serves in a background goroutine,
// mirroring admin.Server.Start.
func (s *Server) Start(ctx context.Context) error {
	s.healthSvc.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("marketapi: failed to listen on %s: %w", s.addr, err)
	}
	s.lis = lis

	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			fmt.Printf("market gRPC server error: %v\n", err)
		}
	}()

	return nil
}

// Stop gracefully stops the gRPC server, mirroring admin.Server.Stop.
func (s *Server) Stop(ctx context.Context) error {
	s.healthSvc.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	s.grpcServer.GracefulStop()
	return nil
}

// Service exposes the market service implementation, which is useful for
// in-process tests and for callers that want direct access.
func (s *Server) Service() *Service { return s.svc }

// Addr returns the actual network address the server is listening on after
// Start has been called. When the configured Addr used a :0 port this reflects
// the OS-assigned port, which is what in-process tests dial. It returns an empty
// string before Start.
func (s *Server) Addr() string {
	if s.lis == nil {
		return ""
	}
	return s.lis.Addr().String()
}

// requireAuthUnaryInterceptor requires authentication for every unary RPC other
// than the health check, reusing the admin authenticator.
func requireAuthUnaryInterceptor(auth *admin.Authenticator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if info.FullMethod == "/grpc.health.v1.Health/Check" {
			return handler(ctx, req)
		}
		if _, err := auth.Authenticate(ctx); err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "authentication required")
		}
		return handler(ctx, req)
	}
}

// requireAuthStreamInterceptor requires authentication for every streaming RPC
// other than the health watch, reusing the admin authenticator.
func requireAuthStreamInterceptor(auth *admin.Authenticator) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info.FullMethod == "/grpc.health.v1.Health/Watch" {
			return handler(srv, ss)
		}
		if _, err := auth.Authenticate(ss.Context()); err != nil {
			return status.Errorf(codes.Unauthenticated, "authentication required")
		}
		return handler(srv, ss)
	}
}

// mapMarketError translates market/token sentinel errors to gRPC status codes.
// Unknown errors map to codes.Internal so unexpected failures are not masked as
// benign. Nil returns nil.
func mapMarketError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, market.ErrProviderNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, market.ErrJobNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, market.ErrInsufficientFunds):
		// Covers token.ErrUnaffordable, which wraps market.ErrInsufficientFunds,
		// and reward-pool funding requests that exceed the pool balance.
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, token.ErrSupplyCapExceeded):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, market.ErrInsufficientCapacity):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, market.ErrInvalidJobState):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, market.ErrInvalidProvider):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, market.ErrSelfDealing):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, token.ErrNonceMismatch):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, token.ErrPrevHashMismatch):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, token.ErrHeightOutOfRange):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, token.ErrInvalidSignature),
		errors.Is(err, token.ErrUnsignedTransaction),
		errors.Is(err, token.ErrInvalidTransaction),
		errors.Is(err, token.ErrInvalidPublicKey):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

// ErrTransferNotFound is returned by a TransferSettler's history reads when the
// requested transfer index does not exist. It is declared here (consumer side)
// so mapTransferHistoryError can map it to codes.NotFound without importing the
// consensus package; the node coordinator wraps it around consensus's own
// out-of-range error.
var ErrTransferNotFound = errors.New("marketapi: transaction not found")

// mapTransferHistoryError maps a consensus-history read failure to a gRPC
// status. A missing index is NotFound; anything else is Internal so an
// unexpected failure is not masked as benign.
func mapTransferHistoryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrTransferNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}

// mapSettlementError turns a consensus-settlement failure into a gRPC status.
//
// It cannot match the settlement sentinels by identity: they live in
// internal/node, which imports this package, so importing it back would be a
// cycle. The market and token sentinels DO match through the wrapping, and they
// are the ones a caller can act on differently (a missing job, an unaffordable
// price, a job in the wrong state). Everything else - no signing account for
// the buyer, a settlement that committed but was skipped as unaffordable - is a
// precondition the caller has to fix, and the message says which. A settlement
// that could not be confirmed in time is DeadlineExceeded: the reservation is
// intact and retrying re-drives the same transfer rather than signing a second
// one, so a retry cannot double-charge.
func mapSettlementError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, market.ErrJobNotFound),
		errors.Is(err, market.ErrProviderNotFound),
		errors.Is(err, market.ErrInsufficientFunds),
		errors.Is(err, market.ErrInsufficientCapacity),
		errors.Is(err, market.ErrInvalidJobState),
		errors.Is(err, market.ErrInvalidProvider),
		errors.Is(err, market.ErrSelfDealing):
		return mapMarketError(err)
	default:
		return status.Error(codes.FailedPrecondition, err.Error())
	}
}

// toProtoTimestamp converts an int64 unix-nanosecond timestamp to a protobuf
// Timestamp. A zero value yields nil, matching "unset".
func nanosToTimestamp(ns int64) *timestamppb.Timestamp {
	if ns == 0 {
		return nil
	}
	return timestamppb.New(timeUnixNano(ns))
}
