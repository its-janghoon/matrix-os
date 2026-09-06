package inferenceapi

import (
	"context"

	inferencev1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/inference/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ecirlabs/matrix-core/internal/inference"
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
