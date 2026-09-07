package inference

import (
	"context"
	"errors"
	"fmt"
)

// This file adds streaming: a completion delivered token by token instead of as
// one whole body.
//
// It is not cosmetic. A chat UI that waits for a whole answer feels broken at
// any model speed a marketplace can offer, and the OpenAI-compatible route had
// to refuse `"stream": true` outright because there was nothing behind it -
// which is a wall every client library hits on its first real request.
//
// Two things shape the design.
//
// STREAMING IS OPTIONAL FOR A BACKEND. Backend stays a one-shot Infer, and a
// backend that can stream opts in by also implementing StreamingBackend. One
// that cannot is wrapped: the caller still gets a stream, of exactly one chunk,
// after the whole completion arrives. That is worse than real streaming and
// better than a caller having to ask which backends support it and branch, and
// StreamedOneShot on the result says honestly which happened.
//
// STREAMING IS THE HOSTED PATH ONLY. On the client-signed path the node
// WITHHOLDS the completion until the buyer signs the invoice, and withholding is
// the only enforcement there because the provider has already worked. Streaming
// the answer out and then asking to be paid gives the whole thing away first, so
// the two are mutually exclusive by construction rather than by an oversight.

// ErrStreamNotSupported is returned when a stream is requested through a path
// that cannot serve one. It exists so a caller can tell "this backend cannot
// stream" (which never happens, because of the wrapper) from "streaming is not
// available on this path", which is a real and permanent answer on the
// client-signed path.
var ErrStreamNotSupported = errors.New("inference: streaming is not supported on this path")

// ChunkFunc receives each piece of the completion as it arrives. Returning an
// error aborts the stream, and that error is what the caller sees: it is how a
// transport reports that its client hung up, so the backend stops generating
// tokens nobody will read.
type ChunkFunc func(delta string) error

// StreamingBackend is the optional interface a Backend implements when it can
// produce a completion incrementally.
//
// InferStream must call onChunk with each delta as it arrives and return the
// same InferenceResponse Infer would have: the concatenated completion, the
// usage, and the units. The response is what settles, so a backend that streams
// must still account for the whole thing.
type StreamingBackend interface {
	InferStream(ctx context.Context, req InferenceRequest, onChunk ChunkFunc) (InferenceResponse, error)
}

// StreamResult reports what a stream produced.
type StreamResult struct {
	// Response is the completion, usage and units, identical to what a one-shot
	// Infer would have returned. It is what settles.
	Response InferenceResponse
	// StreamedOneShot is true when the backend could not stream and the whole
	// completion was delivered as a single chunk. A UI can use it to stop
	// pretending there is a typing effect; nobody has to guess.
	StreamedOneShot bool
}

// streamBackend runs a backend with streaming, falling back to one chunk for a
// backend that cannot. It is the one place the fallback lives, so no caller has
// to know which backends can stream.
func streamBackend(ctx context.Context, backend Backend, req InferenceRequest, onChunk ChunkFunc) (StreamResult, error) {
	if sb, ok := backend.(StreamingBackend); ok {
		resp, err := sb.InferStream(ctx, req, onChunk)
		return StreamResult{Response: resp}, err
	}

	resp, err := backend.Infer(ctx, req)
	if err != nil {
		return StreamResult{}, err
	}
	// Emit even an empty completion as a chunk: a caller counting frames should
	// see the same shape whether the backend streamed or not.
	if err := onChunk(resp.Completion); err != nil {
		return StreamResult{Response: resp, StreamedOneShot: true}, err
	}
	return StreamResult{Response: resp, StreamedOneShot: true}, nil
}

// StreamJob runs a PENDING job's inference, delivering the completion to
// onChunk as it arrives, then settles it exactly as FulfillJob does: the node
// signs the payment with a key it holds for the buyer, and the job is reported
// COMPLETED only once that settlement commits AND applies.
//
// It is the streaming counterpart to FulfillJob and shares its custody model on
// purpose. See the file comment for why streaming and the client-signed path
// cannot be combined.
//
// The returned job carries the whole completion as well, so a caller that
// buffered the chunks can check it reassembled what it was billed for.
func (s *Service) StreamJob(ctx context.Context, jobID string, onChunk ChunkFunc) (*InferenceJob, *StreamResult, error) {
	if onChunk == nil {
		return nil, nil, fmt.Errorf("inference: a chunk callback is required to stream")
	}

	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("%w: %q", ErrJobNotFound, jobID)
	}
	if job.Status == InferenceJobAwaitingPayment {
		s.mu.Unlock()
		// Named explicitly rather than reported as a lifecycle error: this is the
		// one combination that is refused by design, not by circumstance.
		return nil, nil, fmt.Errorf("%w: job %q is awaiting a buyer signature, and its "+
			"completion is withheld until then", ErrStreamNotSupported, jobID)
	}
	if job.Status != InferenceJobPending && job.Status != InferenceJobRunning {
		status := job.Status
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("%w: job %q is %s", ErrJobNotFound, jobID, status)
	}
	provider, request := job.Provider, job.Request
	job.Status = InferenceJobRunning
	job.UpdatedAt = nowUTC()
	s.mu.Unlock()

	backend, err := s.registry.Backend(provider)
	if err != nil {
		s.failJob(jobID)
		return nil, nil, fmt.Errorf("%w: %v", ErrNoBackend, err)
	}

	result, err := streamBackend(ctx, backend, request, onChunk)
	if err != nil {
		s.failJob(jobID)
		return nil, nil, fmt.Errorf("inference: streaming from provider %q failed: %w", provider, err)
	}

	// From here it is the ordinary settlement, so it goes through the same code
	// FulfillJob uses rather than a second copy of the charge computation - the
	// clamping to the reservation and the commit-and-apply confirmation are
	// exactly the properties a duplicate would eventually get wrong.
	settled, err := s.settleRun(ctx, jobID, result.Response)
	if err != nil {
		return settled, &result, err
	}
	return settled, &result, nil
}
