package inferenceapi

import (
	"context"

	inferencev1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/inference/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ecirlabs/matrix-core/internal/inference"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// The ESCROWED path over the wire. Four calls, and the order is the design:
// reserve, fund, stream, settle.
//
// What each one does and why is on the RPCs in the proto. What lives here is the
// translation, plus the one rule this layer is responsible for: the run
// authorization is checked at RESERVE, before capacity is held, so an
// unauthorized caller costs a provider nothing - the same place and the same
// reason RunInferenceJob checks it.

// ReserveInferenceEscrow submits a job and names the reservation that pays for
// it. Nothing runs and no token moves.
func (s *Service) ReserveInferenceEscrow(
	ctx context.Context,
	req *inferencev1.ReserveInferenceEscrowRequest,
) (*inferencev1.ReserveInferenceEscrowResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	request := escrowRequestToInternal(req)

	// The authorization message is the same one RunInferenceJob verifies, so it
	// is rebuilt into that request shape rather than duplicating the check. A
	// second transcription is one that eventually stops matching, and the half
	// that drifts is the half nobody tested.
	if err := s.authorizeRun(&inferencev1.RunInferenceJobRequest{
		Buyer:         req.GetBuyer(),
		Provider:      req.GetProvider(),
		Model:         req.GetModel(),
		Prompt:        req.GetPrompt(),
		Messages:      req.GetMessages(),
		MaxTokens:     req.GetMaxTokens(),
		Temperature:   req.GetTemperature(),
		UnitsEstimate: req.GetUnitsEstimate(),
		Authorization: req.GetAuthorization(),
	}, request); err != nil {
		return nil, err
	}

	job, err := s.inf.SubmitInferenceJob(req.GetBuyer(), req.GetProvider(), request, req.GetUnitsEstimate())
	if err != nil {
		return nil, mapInferenceError(err)
	}
	plan, err := s.inf.ReserveEscrow(job.ID, req.GetAuthorization().GetSignature())
	if err != nil {
		return nil, mapInferenceError(err)
	}
	current, ok := s.inf.GetJob(job.ID)
	if !ok {
		return nil, status.Errorf(codes.Internal, "inference job %q vanished after reserving", job.ID)
	}
	return &inferencev1.ReserveInferenceEscrowResponse{
		Payment:       paymentToProto(plan.Request),
		Job:           jobToProto(current),
		EscrowAccount: plan.Account,
		ClaimableAt:   plan.ExpiresAt.Unix(),
		UnitsReserved: plan.UnitsReserved,
		PricePerUnit:  plan.PricePerUnit,
	}, nil
}

// FundInferenceEscrow submits the signed deposit and waits for it to commit AND
// apply.
func (s *Service) FundInferenceEscrow(
	ctx context.Context,
	req *inferencev1.FundInferenceEscrowRequest,
) (*inferencev1.FundInferenceEscrowResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	tx, err := signedTransferFromFields(req.GetFromPublicKey(), req.GetTo(), req.GetAmount(),
		req.GetNonce(), req.GetTimestamp(), req.GetPrevHash(), req.GetSignature())
	if err != nil {
		return nil, err
	}
	job, err := s.inf.FundEscrow(ctx, req.GetId(), tx)
	if err != nil {
		return nil, mapInferenceError(err)
	}
	return &inferencev1.FundInferenceEscrowResponse{Job: jobToProto(job)}, nil
}

// StreamEscrowedInferenceJob runs a funded job and streams the answer as it is
// produced, ending with the settlement to sign.
//
// It streams the COMPLETION and not a progress count, which is the whole
// difference from RunInferenceJobProgress: there the text is withheld because
// withholding is the only enforcement, and here the provider has already been
// paid the most the job can cost, so there is nothing left to hold back.
func (s *Service) StreamEscrowedInferenceJob(
	req *inferencev1.StreamEscrowedInferenceJobRequest,
	stream grpc.ServerStreamingServer[inferencev1.StreamEscrowedInferenceJobResponse],
) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "request is required")
	}
	jobID := req.GetId()

	// Send's error travels into the callback, so a client that hangs up aborts
	// the run rather than leaving a provider generating tokens nobody will read.
	// The money is already in escrow either way, and the buyer settles for what
	// they received - or the provider claims the lot at the expiry.
	onChunk := func(delta string) error {
		return stream.Send(&inferencev1.StreamEscrowedInferenceJobResponse{
			Delta: delta,
			JobId: jobID,
		})
	}

	payment, result, err := s.inf.StreamEscrowed(stream.Context(), jobID,
		req.GetAuthorization().GetSignature(), onChunk)
	if err != nil {
		return mapInferenceError(err)
	}

	final := &inferencev1.StreamEscrowedInferenceJobResponse{JobId: jobID}
	if payment != nil {
		final.Payment = paymentToProto(payment)
	}
	if job, ok := s.inf.GetJob(jobID); ok {
		final.Job = jobToProto(job)
	}
	if result != nil {
		final.StreamedOneShot = result.StreamedOneShot
	}
	return stream.Send(final)
}

// SettleEscrowedInferenceJob submits the signed settlement: consensus pays the
// actual to the provider and returns the rest to the payer.
func (s *Service) SettleEscrowedInferenceJob(
	ctx context.Context,
	req *inferencev1.SettleEscrowedInferenceJobRequest,
) (*inferencev1.SettleEscrowedInferenceJobResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	tx, err := signedTransferFromFields(req.GetFromPublicKey(), req.GetTo(), req.GetAmount(),
		req.GetNonce(), req.GetTimestamp(), req.GetPrevHash(), req.GetSignature())
	if err != nil {
		return nil, err
	}
	job, err := s.inf.SettleEscrowed(ctx, req.GetId(), tx)
	if err != nil {
		return nil, mapInferenceError(err)
	}
	return &inferencev1.SettleEscrowedInferenceJobResponse{Job: jobToProto(job)}, nil
}

// signedTransferFromFields rebuilds the transfer a client signed.
//
// One function rather than a copy in each handler, because every field here is
// part of what the signature covers: a transcription that dropped one would
// produce a transaction that verifies against different bytes than the client
// signed, and the failure would read as a bad signature rather than as a bug.
func signedTransferFromFields(
	fromPublicKey []byte, to string, amount, nonce uint64, timestamp int64, prevHash, signature []byte,
) (*token.Transaction, error) {
	// Either kind of account: 32 bytes ed25519, 20 bytes an Ethereum address.
	pub, err := token.ParseSenderKey(fromPublicKey)
	if err != nil {
		return nil, mapInferenceError(err)
	}
	return &token.Transaction{
		From:      pub,
		To:        to,
		Amount:    amount,
		Nonce:     nonce,
		Timestamp: timestamp,
		PrevHash:  prevHash,
		Signature: signature,
	}, nil
}

// escrowRequestToInternal builds an internal InferenceRequest from the reserve
// request.
func escrowRequestToInternal(req *inferencev1.ReserveInferenceEscrowRequest) inference.InferenceRequest {
	msgs := make([]inference.Message, 0, len(req.GetMessages()))
	for _, m := range req.GetMessages() {
		msgs = append(msgs, inference.Message{
			Role:    roleToInternal(m.GetRole()),
			Content: m.GetContent(),
		})
	}
	return inference.InferenceRequest{
		Model:       req.GetModel(),
		Prompt:      req.GetPrompt(),
		Messages:    msgs,
		MaxTokens:   int(req.GetMaxTokens()),
		Temperature: req.GetTemperature(),
	}
}

// RecoverEscrowedInferenceJob returns the settlement for a funded job the buyer
// is no longer streaming.
//
// The settlement rides on the stream's last frame, so a buyer who cancelled,
// reloaded, or lost their connection mid-answer never received it - and without
// it they cannot settle, which costs them the WHOLE reservation when the
// provider claims it. This is how they come back for it.
func (s *Service) RecoverEscrowedInferenceJob(
	ctx context.Context,
	req *inferencev1.RecoverEscrowedInferenceJobRequest,
) (*inferencev1.RecoverEscrowedInferenceJobResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	payment, job, cutShort, err := s.inf.RecoverEscrowSettlement(req.GetId(),
		req.GetAuthorization().GetSignature())
	if err != nil {
		return nil, mapInferenceError(err)
	}
	return &inferencev1.RecoverEscrowedInferenceJobResponse{
		Payment:  paymentToProto(payment),
		Job:      jobToProto(job),
		CutShort: cutShort,
	}, nil
}
