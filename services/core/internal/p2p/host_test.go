package p2p

import (
	"context"
	"testing"
)

// TestListenMultiaddr covers the address forms node.Config may feed into the
// p2p host: empty (random port), a full multiaddr, and the plain host:port form
// that the default listen_addr uses. The host:port cases are the regression
// guard: feeding "0.0.0.0:9000" straight into a multiaddr template is invalid,
// so it must be split and rebuilt here.
func TestListenMultiaddr(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  string
		valid bool
	}{
		{name: "empty defaults to all interfaces random port", in: "", want: "/ip4/0.0.0.0/tcp/0", valid: true},
		{name: "full multiaddr passes through", in: "/ip4/127.0.0.1/tcp/9000", want: "/ip4/127.0.0.1/tcp/9000", valid: true},
		{name: "ipv4 host:port (default config form)", in: "0.0.0.0:9000", want: "/ip4/0.0.0.0/tcp/9000", valid: true},
		{name: "loopback host:port", in: "127.0.0.1:19000", want: "/ip4/127.0.0.1/tcp/19000", valid: true},
		{name: "bare port listens on all interfaces", in: ":9000", want: "/ip4/0.0.0.0/tcp/9000", valid: true},
		{name: "hostname maps to dns", in: "example.com:9000", want: "/dns/example.com/tcp/9000", valid: true},
		{name: "ipv6 host:port", in: "[::1]:9000", want: "/ip6/::1/tcp/9000", valid: true},
		{name: "garbage is rejected", in: "not-an-address", valid: false},
		{name: "bad port is rejected", in: "127.0.0.1:notaport", valid: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ma, err := listenMultiaddr(tc.in)
			if !tc.valid {
				if err == nil {
					t.Fatalf("listenMultiaddr(%q) = %v, want error", tc.in, ma)
				}
				return
			}
			if err != nil {
				t.Fatalf("listenMultiaddr(%q) error: %v", tc.in, err)
			}
			if got := ma.String(); got != tc.want {
				t.Fatalf("listenMultiaddr(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNewHostFromHostPortDefault proves the node's default listen_addr shape
// (a plain host:port on loopback) actually boots a libp2p host, which was the
// bug that stopped `matrixd --init && matrixd` from starting.
func TestNewHostFromHostPortDefault(t *testing.T) {
	h, err := New(context.Background(), &Config{ListenAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("New with host:port listen addr: %v", err)
	}
	defer func() { _ = h.Close() }()

	if len(h.GetAddrs()) == 0 {
		t.Fatal("expected the host to be listening on at least one address")
	}
	if h.GetPeerID() == "" {
		t.Fatal("expected a non-empty peer ID")
	}
}

// TestNewHostFromMultiaddr proves the full-multiaddr form also boots.
func TestNewHostFromMultiaddr(t *testing.T) {
	h, err := New(context.Background(), &Config{ListenAddr: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatalf("New with multiaddr listen addr: %v", err)
	}
	defer func() { _ = h.Close() }()
	if len(h.GetAddrs()) == 0 {
		t.Fatal("expected the host to be listening on at least one address")
	}
}
