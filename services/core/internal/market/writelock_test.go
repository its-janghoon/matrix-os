package market

import (
	"os"
	"strings"
	"testing"
)

// A write-lock site that takes l.mu directly is a stall the node cannot see.
//
// This is a source-level guard and it is deliberate. The watchdog in
// internal/node can only report a holder that RECORDED itself, and recording
// happens in lockWrite. A future method that reaches for l.mu.Lock() straight -
// the obvious thing to write, since that is what the other three did until
// recently - reintroduces exactly the silence that made the bridge deadlock
// take a SIGQUIT to find. There is no behavioural test for this: a critical
// section that lasts microseconds cannot be caught from another goroutine
// reliably, so a test for it would pass by luck.
func TestNoWriteLockSiteBypassesTheWatchdog(t *testing.T) {
	src, err := os.ReadFile("ledger.go")
	if err != nil {
		t.Fatalf("read ledger.go: %v", err)
	}
	var offenders []string
	for i, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "l.mu.Lock()" && trimmed != "defer l.mu.Unlock()" && trimmed != "l.mu.Unlock()" {
			continue
		}
		// The one legitimate pair, inside the helpers that do the recording.
		if inFunc(string(src), i, "func (l *Ledger) lockWrite()") ||
			inFunc(string(src), i, "func (l *Ledger) unlockWrite()") {
			continue
		}
		offenders = append(offenders, trimmed+" at ledger.go:"+itoa(i+1))
	}
	if len(offenders) > 0 {
		t.Fatalf("these take the ledger write lock without recording the holder, so a stall "+
			"in them is invisible to the node's watchdog - use lockWrite/unlockWrite:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// inFunc reports whether line index i falls inside the function whose signature
// is sig, by finding the signature and the next top-level closing brace.
func inFunc(src string, i int, sig string) bool {
	lines := strings.Split(src, "\n")
	start := -1
	for n, l := range lines {
		if strings.HasPrefix(l, sig) {
			start = n
			break
		}
	}
	if start < 0 || i < start {
		return false
	}
	for n := start + 1; n < len(lines); n++ {
		if lines[n] == "}" {
			return i <= n
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
