package cli

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// A unary interceptor does not run for a streaming RPC, so a CLI that installed
// only the unary one sent streaming calls with NO credential. The node refused
// them with "authentication required", which reads as a key that is missing
// rather than one that was never sent - so the search goes to the environment,
// where the key is sitting correctly, and not to the dial options.
//
// It stayed hidden because the CLI made no streaming calls until one was added.
// Tested per interceptor kind, because that is the axis it lived on: a test of
// the unary path alone passes while every stream goes out bare.

// sentAuth runs an interceptor and reports the authorization metadata it added.
func sentAuth(t *testing.T, stream bool) []string {
	t.Helper()
	var got []string

	capture := func(ctx context.Context) {
		md, _ := metadata.FromOutgoingContext(ctx)
		got = md.Get("authorization")
	}

	if stream {
		si := authStreamInterceptor("the-key")
		_, err := si(context.Background(), &grpc.StreamDesc{}, nil, "/svc/Method",
			func(ctx context.Context, _ *grpc.StreamDesc, _ *grpc.ClientConn, _ string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
				capture(ctx)
				return nil, nil
			})
		if err != nil {
			t.Fatalf("stream interceptor: %v", err)
		}
		return got
	}

	ui := authUnaryInterceptor("the-key")
	err := ui(context.Background(), "/svc/Method", nil, nil, nil,
		func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			capture(ctx)
			return nil
		})
	if err != nil {
		t.Fatalf("unary interceptor: %v", err)
	}
	return got
}

func TestTheCredentialTravelsOnBothKindsOfCall(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stream bool
	}{
		{"unary", false},
		{"streaming", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sentAuth(t, tc.stream)
			if len(got) != 1 || got[0] != "the-key" {
				t.Fatalf("a %s call carried authorization %v; the node refuses it as "+
					"unauthenticated and the key looks missing rather than unsent", tc.name, got)
			}
		})
	}
}

// Both interceptors are installed, and neither is installed without a key: an
// empty credential on the wire is worse than none, because it looks deliberate.
func TestDialOptionsInstallBothInterceptorsOnlyWithAKey(t *testing.T) {
	if with := len(dialOptions("the-key")); with != 3 {
		t.Fatalf("dialOptions with a key built %d options, want transport + unary + stream", with)
	}
	if without := len(dialOptions("")); without != 1 {
		t.Fatalf("dialOptions with no key built %d options, want the transport alone", without)
	}
}
