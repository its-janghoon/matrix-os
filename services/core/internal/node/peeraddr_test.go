package node

import (
	"strings"
	"testing"
)

// TestPeerAddrReachabilityNote. The multiaddrs a node prints are the ones an
// operator pastes into another node's `network.bootstrap_peers`, and libp2p
// gives no useful diagnostic when the address is simply unreachable: the dial
// fails and the two nodes never commit a block together.
//
// The loopback label existed. The PRIVATE one did not, which is the case that
// matters for the deployment everyone actually attempts - cloud instances, each
// with a private interface address and a separate public/Elastic IP the node
// cannot observe. Multi-region makes it certain: every address the node knows
// about is wrong, and nothing said so.
func TestPeerAddrReachabilityNote(t *testing.T) {
	cases := []struct {
		name string
		addr string
		want string // substring the note must contain; "" means routable
	}{
		{"loopback v4", "/ip4/127.0.0.1/tcp/9000", "loopback"},
		{"loopback v6", "/ip6/::1/tcp/9000", "loopback"},
		// The three RFC1918 ranges. 172.31 is the default EC2 VPC range and
		// 10.x is the other common one, so these are not hypothetical.
		{"ec2 default vpc", "/ip4/172.31.14.22/tcp/9000", "private"},
		{"ten net", "/ip4/10.77.0.10/tcp/9000", "private"},
		{"192.168", "/ip4/192.168.50.20/tcp/9000", "private"},
		{"ipv6 unique local", "/ip6/fd00::1/tcp/9000", "private"},
		{"link local", "/ip4/169.254.169.254/tcp/9000", "link-local"},
		{"wildcard", "/ip4/0.0.0.0/tcp/9000", "unspecified"},
		// A real public address must NOT be labelled, or the label means nothing.
		{"public v4", "/ip4/52.79.100.4/tcp/9000", ""},
		{"public v6", "/ip6/2600:1f18::4/tcp/9000", ""},
		// 172.32 is outside 172.16/12 and is public. An implementation that
		// matched on the "172." prefix would wrongly flag it.
		{"172.32 is public", "/ip4/172.32.0.1/tcp/9000", ""},
		// Not an IP multiaddr: claiming anything would be a guess.
		{"dns", "/dns4/validator-1.example.org/tcp/9000", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := peerAddrReachabilityNote(tc.addr)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("%s got note %q, want none: labelling a routable address "+
						"trains an operator to ignore the labels", tc.addr, got)
				}
				return
			}
			if got == "" {
				t.Fatalf("%s got no note, want one mentioning %q: an operator pastes this "+
					"into a remote node and gets a peer that silently never connects",
					tc.addr, tc.want)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("%s note = %q, want it to mention %q", tc.addr, got, tc.want)
			}
		})
	}
}

func TestMultiaddrIP(t *testing.T) {
	cases := map[string]string{
		"/ip4/10.0.0.1/tcp/9000":        "10.0.0.1",
		"/ip6/fd00::1/tcp/9000":         "fd00::1",
		"/dns4/host.example.org/tcp/90": "",
		"":                              "",
	}
	for addr, want := range cases {
		if got := multiaddrIP(addr); got != want {
			t.Errorf("multiaddrIP(%q) = %q, want %q", addr, got, want)
		}
	}
}
