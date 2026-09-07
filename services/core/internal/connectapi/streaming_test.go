package connectapi

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"strings"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// frame is one decoded Connect streaming envelope.
type frame struct {
	flags   byte
	payload []byte
}

// decodeFrames parses the envelope stream. It is deliberately strict about
// lengths: a truncated frame is exactly the failure this framing exists to make
// visible, so silently tolerating one would defeat the test.
func decodeFrames(t *testing.T, body []byte) []frame {
	t.Helper()
	var out []frame
	for len(body) > 0 {
		if len(body) < 5 {
			t.Fatalf("trailing %d bytes are not a frame header", len(body))
		}
		flags := body[0]
		n := binary.BigEndian.Uint32(body[1:5])
		body = body[5:]
		if uint32(len(body)) < n {
			t.Fatalf("frame claims %d bytes but only %d remain", n, len(body))
		}
		out = append(out, frame{flags: flags, payload: body[:n]})
		body = body[n:]
	}
	return out
}

// streamingImpl is a hand-written server-streaming implementation. It sends
// three messages and can be made to BLOCK after the first, which is what lets a
// test prove a frame reached the client while the handler was still running.
type streamingImpl struct {
	// release, when non-nil, is waited on after the first message. A test that
	// never closes it holds the handler open forever, so a frame the client
	// manages to read can only have been flushed.
	release chan struct{}
	err     error
}

func (s *streamingImpl) Watch(_ any, stream grpc.ServerStream) error {
	var req marketv1.GetBalanceRequest
	if err := stream.RecvMsg(&req); err != nil {
		return err
	}
	for i := 0; i < 3; i++ {
		if err := stream.SendMsg(&marketv1.GetBalanceResponse{
			Account: req.GetAccount(),
			Balance: uint64(i),
		}); err != nil {
			return err
		}
		if i == 0 && s.release != nil {
			select {
			case <-s.release:
			case <-stream.Context().Done():
				return stream.Context().Err()
			}
		}
	}
	return s.err
}

func streamingDesc(impl *streamingImpl) grpc.ServiceDesc {
	return grpc.ServiceDesc{
		ServiceName: "test.Streaming",
		Streams: []grpc.StreamDesc{{
			StreamName:    "Watch",
			ServerStreams: true,
			Handler: func(srv any, stream grpc.ServerStream) error {
				return impl.Watch(srv, stream)
			},
		}},
	}
}

func streamingHandler(t *testing.T, impl *streamingImpl, auth Authenticator) http.Handler {
	t.Helper()
	desc := streamingDesc(impl)
	h, err := NewHandler(Config{
		Bindings:       []Binding{{Desc: &desc, Impl: impl}},
		Auth:           auth,
		AllowedOrigins: []string{"*"},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

func TestAServerStreamIsFramedAndTerminated(t *testing.T) {
	h := streamingHandler(t, &streamingImpl{}, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test.Streaming/Watch",
		strings.NewReader(`{"account":"alice"}`))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != streamingContentType {
		t.Fatalf("Content-Type = %q, want %q", ct, streamingContentType)
	}

	got := decodeFrames(t, rec.Body.Bytes())
	if len(got) != 4 {
		t.Fatalf("got %d frames, want 3 messages plus the end frame", len(got))
	}
	for i, f := range got[:3] {
		if f.flags != frameFlagMessage {
			t.Fatalf("frame %d flags = %#x, want a message", i, f.flags)
		}
		var msg marketv1.GetBalanceResponse
		if err := unmarshaler.Unmarshal(f.payload, &msg); err != nil {
			t.Fatalf("frame %d is not the response message: %v", i, err)
		}
		if msg.GetAccount() != "alice" || msg.GetBalance() != uint64(i) {
			t.Fatalf("frame %d = %+v, want alice/%d", i, &msg, i)
		}
	}

	// The end frame is what distinguishes a finished stream from a truncated
	// one, and on success it carries no error.
	end := got[3]
	if end.flags != frameFlagEndOfSteam {
		t.Fatalf("last frame flags = %#x, want the end-of-stream flag", end.flags)
	}
	var eos endOfStream
	if err := json.Unmarshal(end.payload, &eos); err != nil {
		t.Fatalf("end frame is not JSON: %v", err)
	}
	if eos.Error != nil {
		t.Fatalf("end frame carries an error on a successful stream: %+v", eos.Error)
	}
}

// TestAFailureMidStreamTravelsInTheEndFrame: an HTTP response that has already
// begun cannot change its status, so this is the only place it can go. A stream
// that just stopped would be indistinguishable from a complete one.
func TestAFailureMidStreamTravelsInTheEndFrame(t *testing.T) {
	h := streamingHandler(t, &streamingImpl{
		err: status.Error(codes.FailedPrecondition, "the model server hung up"),
	}, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/test.Streaming/Watch",
		strings.NewReader(`{"account":"alice"}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: the headers went out before the failure", rec.Code)
	}
	got := decodeFrames(t, rec.Body.Bytes())
	end := got[len(got)-1]
	if end.flags != frameFlagEndOfSteam {
		t.Fatal("a failed stream must still send an end frame")
	}
	var eos endOfStream
	if err := json.Unmarshal(end.payload, &eos); err != nil {
		t.Fatalf("end frame is not JSON: %v", err)
	}
	if eos.Error == nil {
		t.Fatal("the end frame must carry the failure, or a client reads a truncated stream as complete")
	}
	if eos.Error.Code != "failed_precondition" {
		t.Fatalf("code = %q, want failed_precondition", eos.Error.Code)
	}
	if eos.Error.Message == "" {
		t.Fatal("the failure needs a message")
	}
}

// TestFramesArriveBeforeTheHandlerReturns is the test that actually proves this
// streams. Without the per-frame flush the frames sit in the response buffer
// until the handler returns, which delivers the whole answer at once at the end
// - and every test that only inspects the final bytes would still pass.
func TestFramesArriveBeforeTheHandlerReturns(t *testing.T) {
	// The handler blocks after its first message until this is closed, so the
	// read below can only succeed on a frame that was actually flushed. Without
	// the block the handler finishes first and the test passes with or without
	// the flush, which is to say it proves nothing.
	release := make(chan struct{})
	impl := &streamingImpl{release: release}
	defer close(release)
	desc := streamingDesc(impl)
	h, err := NewHandler(Config{
		Bindings:       []Binding{{Desc: &desc, Impl: impl}},
		AllowedOrigins: []string{"*"},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/test.Streaming/Watch", strings.NewReader(`{"account":"alice"}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read the first frame off the live connection. If nothing were flushed this
	// read would block until the handler finished and the body closed.
	header := make([]byte, 5)
	if _, err := io.ReadFull(resp.Body, header); err != nil {
		t.Fatalf("reading the first frame header: %v", err)
	}
	n := binary.BigEndian.Uint32(header[1:])
	payload := make([]byte, n)
	if _, err := io.ReadFull(resp.Body, payload); err != nil {
		t.Fatalf("reading the first frame payload: %v", err)
	}

	var msg marketv1.GetBalanceResponse
	if err := unmarshaler.Unmarshal(payload, &msg); err != nil {
		t.Fatalf("first frame is not the response message: %v", err)
	}
	if msg.GetAccount() != "alice" {
		t.Fatalf("first frame = %+v, want alice", &msg)
	}
}

func TestAStreamIsBehindTheSameAuthAsAUnaryMethod(t *testing.T) {
	h := streamingHandler(t, &streamingImpl{}, denyAll{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/test.Streaming/Watch",
		strings.NewReader(`{"account":"alice"}`)))

	// Nothing has been written yet at auth time, so this can be a real status
	// code rather than an end frame.
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if rec.Body.Len() > 0 && rec.Body.Bytes()[0] == frameFlagMessage && rec.Body.Len() > 5 {
		t.Fatal("an unauthenticated stream must not emit any frames")
	}
}

func TestAStreamRequiresPOST(t *testing.T) {
	h := streamingHandler(t, &streamingImpl{}, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/test.Streaming/Watch", nil))

	if rec.Code == http.StatusOK {
		t.Fatal("a GET should not open a stream")
	}
}
