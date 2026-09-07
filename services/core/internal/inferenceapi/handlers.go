package inferenceapi

import (
	"context"

	inferencev1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/inference/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ecirlabs/matrix-core/internal/inference"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// statusToProto maps an inference job status to its proto enum.
func statusToProto(s inference.InferenceJobStatus) inferencev1.InferenceJobStatus {
	switch s {
	case inference.InferenceJobPending:
		return inferencev1.InferenceJobStatus_INFERENCE_JOB_STATUS_PENDING
	case inference.InferenceJobRunning:
		return inferencev1.InferenceJobStatus_INFERENCE_JOB_STATUS_RUNNING
	case inference.InferenceJobSettling:
		return inferencev1.InferenceJobStatus_INFERENCE_JOB_STATUS_SETTLING
	case inference.InferenceJobCompleted:
		return inferencev1.InferenceJobStatus_INFERENCE_JOB_STATUS_COMPLETED
	case inference.InferenceJobFailed:
		return inferencev1.InferenceJobStatus_INFERENCE_JOB_STATUS_FAILED
	case inference.InferenceJobAwaitingPayment:
		return inferencev1.InferenceJobStatus_INFERENCE_JOB_STATUS_AWAITING_PAYMENT
	default:
		return inferencev1.InferenceJobStatus_INFERENCE_JOB_STATUS_UNSPECIFIED
	}
}

// roleToInternal maps a proto chat role to the internal inference role.
func roleToInternal(r inferencev1.ChatRole) inference.Role {
	switch r {
	case inferencev1.ChatRole_CHAT_ROLE_SYSTEM:
		return inference.RoleSystem
	case inferencev1.ChatRole_CHAT_ROLE_ASSISTANT:
		return inference.RoleAssistant
	default:
		return inference.RoleUser
	}
}

// jobToProto converts an internal inference job to its proto representation.
func jobToProto(j *inference.InferenceJob) *inferencev1.InferenceJob {
	pj := &inferencev1.InferenceJob{
		Id:         j.ID,
		Buyer:      j.Buyer,
		Provider:   j.Provider,
		Model:      j.Model,
		Status:     statusToProto(j.Status),
		Completion: j.Completion,
		Units:      j.Units,
	}
	if pj.Model == "" {
		pj.Model = j.Request.Model
	}
	if !j.CreatedAt.IsZero() {
		pj.CreatedAt = timestamppb.New(j.CreatedAt)
	}
	if !j.UpdatedAt.IsZero() {
		pj.UpdatedAt = timestamppb.New(j.UpdatedAt)
	}
	// Left nil until the job has been fulfilled, so a caller can tell "no tokens
	// reported yet" from "reported zero".
	if j.Usage != (inference.Usage{}) {
		pj.Usage = &inferencev1.TokenUsage{
			PromptTokens:     uint32(j.Usage.PromptTokens),
			CompletionTokens: uint32(j.Usage.CompletionTokens),
			TotalTokens:      uint32(j.Usage.TotalTokens),
		}
	}
	return pj
}

// requestFromProto builds an internal InferenceRequest from the proto request.
func requestFromProto(req *inferencev1.SubmitInferenceJobRequest) inference.InferenceRequest {
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

// SubmitInferenceJob reserves capacity for an inference job on a provider.
func (s *Service) SubmitInferenceJob(ctx context.Context, req *inferencev1.SubmitInferenceJobRequest) (*inferencev1.SubmitInferenceJobResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	job, err := s.inf.SubmitInferenceJob(req.GetBuyer(), req.GetProvider(), requestFromProto(req), req.GetUnitsEstimate())
	if err != nil {
		return nil, mapInferenceError(err)
	}
	return &inferencev1.SubmitInferenceJobResponse{Job: jobToProto(job)}, nil
}

// FulfillInferenceJob runs and settles a pending inference job.
func (s *Service) FulfillInferenceJob(ctx context.Context, req *inferencev1.FulfillInferenceJobRequest) (*inferencev1.FulfillInferenceJobResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	job, err := s.inf.FulfillJob(ctx, req.GetId())
	if err != nil {
		return nil, mapInferenceError(err)
	}
	return &inferencev1.FulfillInferenceJobResponse{Job: jobToProto(job)}, nil
}

// RunInferenceJob is the client-signed path: it runs the inference and returns
// the transfer to sign, without the completion.
func (s *Service) RunInferenceJob(ctx context.Context, req *inferencev1.RunInferenceJobRequest) (*inferencev1.RunInferenceJobResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	request := runRequestToInternal(req)

	// Verify the buyer's authorization BEFORE anything reserves capacity or runs
	// a model, so an unauthorized caller costs a provider nothing.
	if auth := req.GetAuthorization(); auth != nil {
		if err := s.inf.VerifyRunAuthorization(req.GetBuyer(), request, &inference.RunAuthorization{
			PublicKey: auth.GetPublicKey(),
			Provider:  req.GetProvider(),
			Model:     req.GetModel(),
			Timestamp: auth.GetTimestamp(),
			Signature: auth.GetSignature(),
		}); err != nil {
			return nil, mapInferenceError(err)
		}
	} else if s.requireRunAuth {
		return nil, status.Error(codes.Unauthenticated,
			"this node serves RunInferenceJob without an api key, so it requires the buyer's "+
				"signed authorization: set `authorization` on the request")
	}

	payment, err := s.inf.RunUnsettled(ctx, req.GetBuyer(), req.GetProvider(),
		request, req.GetUnitsEstimate())
	if err != nil {
		return nil, mapInferenceError(err)
	}
	job, ok := s.inf.GetJob(payment.JobID)
	if !ok {
		return nil, status.Errorf(codes.Internal, "inference job %q vanished after running", payment.JobID)
	}
	// The completion is withheld until the payment is signed: it is the whole
	// enforcement, so it is stripped here rather than relied on being absent.
	withheld := *job
	withheld.Completion = ""
	return &inferencev1.RunInferenceJobResponse{
		Payment: paymentToProto(payment),
		Job:     jobToProto(&withheld),
	}, nil
}

// SettleInferenceJob submits the buyer's signed transfer and returns the
// completion they have now paid for.
func (s *Service) SettleInferenceJob(ctx context.Context, req *inferencev1.SettleInferenceJobRequest) (*inferencev1.SettleInferenceJobResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	pub, err := token.ParsePublicKey(req.GetFromPublicKey())
	if err != nil {
		return nil, mapInferenceError(err)
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
	job, err := s.inf.SettleSigned(ctx, req.GetId(), tx)
	if err != nil {
		return nil, mapInferenceError(err)
	}
	return &inferencev1.SettleInferenceJobResponse{Job: jobToProto(job)}, nil
}

// StreamInferenceJob submits a job and streams its completion as it is
// produced, then settles it.
//
// The stream is closed by returning: a nil error ends it cleanly, and an error
// travels to the client in the transport's end-of-stream frame, which is the
// only way to report a failure after the first byte has gone out.
func (s *Service) StreamInferenceJob(
	req *inferencev1.StreamInferenceJobRequest,
	stream grpc.ServerStreamingServer[inferencev1.StreamInferenceJobResponse],
) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "request is required")
	}
	ctx := stream.Context()

	job, err := s.inf.SubmitInferenceJob(req.GetBuyer(), req.GetProvider(),
		streamRequestToInternal(req), req.GetUnitsEstimate())
	if err != nil {
		return mapInferenceError(err)
	}

	// Send returns its error into the callback, so a client that hangs up aborts
	// the run instead of the provider generating tokens nobody will read.
	onChunk := func(delta string) error {
		return stream.Send(&inferencev1.StreamInferenceJobResponse{
			Delta: delta,
			JobId: job.ID,
		})
	}

	settled, result, err := s.inf.StreamJob(ctx, job.ID, onChunk)
	if err != nil {
		return mapInferenceError(err)
	}

	// The final message carries the settled job. A client must not consider a
	// stream paid for until it has this, which is why it is a message rather
	// than something a caller has to go and fetch.
	final := &inferencev1.StreamInferenceJobResponse{JobId: job.ID}
	if settled != nil {
		final.Job = jobToProto(settled)
	}
	if result != nil {
		final.StreamedOneShot = result.StreamedOneShot
	}
	return stream.Send(final)
}

// streamRequestToInternal builds an internal InferenceRequest from the
// streaming request.
func streamRequestToInternal(req *inferencev1.StreamInferenceJobRequest) inference.InferenceRequest {
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

// runRequestToInternal builds an internal InferenceRequest from the
// client-signed run request. It duplicates requestFromProto rather than sharing
// it because the two proto messages are distinct types with no common interface.
func runRequestToInternal(req *inferencev1.RunInferenceJobRequest) inference.InferenceRequest {
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

// paymentToProto converts a payment request to its proto representation.
func paymentToProto(p *inference.PaymentRequest) *inferencev1.PaymentRequest {
	if p == nil {
		return nil
	}
	out := &inferencev1.PaymentRequest{
		JobId:     p.JobID,
		From:      p.From,
		To:        p.To,
		Amount:    p.Amount,
		Nonce:     p.Nonce,
		Timestamp: p.Timestamp,
		PrevHash:  p.PrevHash,
		Model:     p.Model,
	}
	if p.Usage != (inference.Usage{}) {
		out.Usage = &inferencev1.TokenUsage{
			PromptTokens:     uint32(p.Usage.PromptTokens),
			CompletionTokens: uint32(p.Usage.CompletionTokens),
			TotalTokens:      uint32(p.Usage.TotalTokens),
		}
	}
	if !p.ExpiresAt.IsZero() {
		out.ExpiresAt = timestamppb.New(p.ExpiresAt)
	}
	return out
}

// GetInferenceJob fetches a single inference job by ID.
func (s *Service) GetInferenceJob(ctx context.Context, req *inferencev1.GetInferenceJobRequest) (*inferencev1.GetInferenceJobResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	job, ok := s.inf.GetJob(req.GetId())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "inference job %q not found", req.GetId())
	}
	return &inferencev1.GetInferenceJobResponse{Job: jobToProto(job)}, nil
}
