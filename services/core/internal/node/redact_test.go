package node

import "testing"

// A provider endpoint carries its credential in the path, and the node prints
// this line at every start. An API key in a log is a key in every aggregator,
// screen share and pasted bug report that log ever reaches.
func TestTheRPCEndpointIsRedactedInLogs(t *testing.T) {
	secrets := []string{
		"https://eth-sepolia.g.alchemy.com/v2/SUPERSECRETKEY",
		"https://sepolia.infura.io/v3/0123456789abcdef0123456789abcdef",
		"https://user:hunter2@rpc.example.com/path",
		"https://rpc.example.com/?apikey=SUPERSECRETKEY",
	}
	for _, raw := range secrets {
		got := redactRPCURL(raw)
		for _, leaked := range []string{"SUPERSECRETKEY", "0123456789abcdef", "hunter2"} {
			if contains(got, leaked) {
				t.Fatalf("redactRPCURL(%q) = %q, which still carries %q", raw, got, leaked)
			}
		}
		if got == "" {
			t.Fatalf("redactRPCURL(%q) said nothing; the operator still needs to know which "+
				"provider the node is pointed at", raw)
		}
	}
}

// The host has to survive, or the line stops being useful and an operator
// cannot tell a misconfigured endpoint from a working one.
func TestRedactionKeepsTheHost(t *testing.T) {
	got := redactRPCURL("https://eth-sepolia.g.alchemy.com/v2/SECRET")
	if got != "https://eth-sepolia.g.alchemy.com/..." {
		t.Fatalf("got %q, want the scheme and host with the path elided", got)
	}
	if got := redactRPCURL("http://127.0.0.1:8545"); got != "http://127.0.0.1:8545" {
		t.Fatalf("a local endpoint with no path was altered: %q", got)
	}
}

// Unparseable means nothing can be assumed about where the secret sits, so
// nothing is printed rather than a guess.
func TestAnUnparseableEndpointIsFullyRedacted(t *testing.T) {
	if got := redactRPCURL("://not a url"); got != "(redacted)" {
		t.Fatalf("got %q, want (redacted) for an endpoint that cannot be parsed", got)
	}
	if got := redactRPCURL(""); got != "" {
		t.Fatalf("got %q for an unset endpoint, want empty", got)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
