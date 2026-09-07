package connectapi

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// This file serves SERVER-STREAMING methods over the Connect protocol, which is
// what makes a streamed completion reachable from a browser.
//
// The handler used to REFUSE to build if any bound service declared a stream,
// which was the honest thing to do while nothing here could frame one. It also
// meant adding a streaming RPC to a served service would have stopped the node
// from starting, so this had to land with the RPC.
//
// The framing (https://connectrpc.com/docs/protocol#streaming-response) is one
// enveloped frame per message on an ordinary HTTP response:
//
//	byte 0        flags: 0 for a message, 0x02 for the end-of-stream frame
//	bytes 1..4    big-endian uint32 payload length
//	bytes 5..     the payload: the message as JSON, or for the end frame an
//	              object that is empty on success and carries `error` on failure
//
// The end frame is why this protocol exists for streaming at all: an HTTP
// response that has already begun cannot change its status code, so a failure
// halfway through a stream has to be reported IN the body. A stream that just
// stopped would be indistinguishable from a complete one, and a client would
// treat a truncated answer as the whole answer.

// Connect streaming frame flags.
const (
	frameFlagMessage    byte = 0x00
	frameFlagEndOfSteam byte = 0x02
)

// streamingContentType is the content type of a Connect streaming response. It
// is deliberately distinct from the unary `application/json`, so a client can
// tell which shape it is reading.
const streamingContentType = "application/connect+json"

// streamHandler serves one server-streaming method.
type streamHandler struct {
	desc       grpc.StreamDesc
	impl       any
	fullMethod string
	auth       Authenticator
	public     bool
}

func (h *streamHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, status.Error(codes.InvalidArgument, "this method requires POST"))
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		writeError(w, status.Error(codes.InvalidArgument, "could not read request body"))
		return
	}
	if len(body) == 0 {
		body = []byte("{}")
	}

	ctx := metadata.NewIncomingContext(r.Context(), metadataFromHeader(r.Header))
	if h.auth != nil && !h.public {
		if _, err := h.auth.Authenticate(ctx); err != nil {
			// Still before the first frame, so this can be an ordinary HTTP error
			// with a real status code rather than an end-of-stream frame.
			writeError(w, status.Error(codes.Unauthenticated, "authentication required"))
			return
		}
	}

	// A flusher is not optional. Without it the frames sit in Go's response
	// buffer until the handler returns, which delivers a "stream" that arrives
	// all at once at the end - the exact thing streaming exists to avoid - and
	// it would look like a working stream in every test that only checks the
	// final bytes.
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, status.Error(codes.Internal,
			"this server cannot stream: the response writer does not support flushing"))
		return
	}

	// Headers must go out before the first frame, and once they have, the status
	// code is fixed at 200 and every later failure travels in the end frame.
	w.Header().Set("Content-Type", streamingContentType)
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	stream := &httpServerStream{
		ctx:     ctx,
		w:       w,
		flusher: flusher,
		request: body,
	}

	err = h.desc.Handler(h.impl, stream)
	writeEndOfStream(w, flusher, err)
}

// httpServerStream adapts an HTTP response to grpc.ServerStream, so a generated
// server-streaming handler runs unmodified over this transport.
type httpServerStream struct {
	ctx     context.Context
	w       http.ResponseWriter
	flusher http.Flusher

	// request is the single request message a server-streaming call carries. It
	// is consumed by the first RecvMsg; a second returns io.EOF, which is what a
	// generated handler expects.
	request  []byte
	consumed bool
}

func (s *httpServerStream) Context() context.Context { return s.ctx }

func (s *httpServerStream) SetHeader(metadata.MD) error  { return nil }
func (s *httpServerStream) SendHeader(metadata.MD) error { return nil }
func (s *httpServerStream) SetTrailer(metadata.MD)       {}

// SendMsg writes one message frame and flushes it, so the client sees it now
// rather than when the handler returns.
func (s *httpServerStream) SendMsg(m any) error {
	msg, ok := m.(proto.Message)
	if !ok {
		return status.Error(codes.Internal, "response type is not a protobuf message")
	}
	payload, err := marshaler.Marshal(msg)
	if err != nil {
		return status.Errorf(codes.Internal, "could not marshal response: %v", err)
	}
	if err := writeFrame(s.w, frameFlagMessage, payload); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// RecvMsg decodes the one request message. A server-streaming call has exactly
// one, so a second call reports io.EOF.
func (s *httpServerStream) RecvMsg(m any) error {
	if s.consumed {
		return io.EOF
	}
	s.consumed = true
	msg, ok := m.(proto.Message)
	if !ok {
		return status.Error(codes.Internal, "request type is not a protobuf message")
	}
	if err := unmarshaler.Unmarshal(s.request, msg); err != nil {
		return status.Errorf(codes.InvalidArgument, "malformed request json: %v", err)
	}
	return nil
}

// writeFrame writes one enveloped frame.
func writeFrame(w io.Writer, flags byte, payload []byte) error {
	var header [5]byte
	header[0] = flags
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// endOfStream is the end frame's payload. `error` is absent on success, which is
// how a client tells a finished stream from a broken one.
type endOfStream struct {
	Error *connectError `json:"error,omitempty"`
}

// writeEndOfStream closes the stream, carrying the failure when there was one.
// A best-effort write: the client may already be gone, and there is nowhere left
// to report that.
func writeEndOfStream(w http.ResponseWriter, flusher http.Flusher, err error) {
	end := endOfStream{}
	if err != nil && !errors.Is(err, io.EOF) {
		st, _ := status.FromError(err)
		name, _ := connectCode(st.Code())
		end.Error = &connectError{Code: name, Message: st.Message()}
	}
	payload, mErr := json.Marshal(end)
	if mErr != nil {
		payload = []byte(fmt.Sprintf(`{"error":{"code":"internal","message":%q}}`, mErr.Error()))
	}
	_ = writeFrame(w, frameFlagEndOfSteam, payload)
	flusher.Flush()
}
