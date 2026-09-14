package consensus

import (
	"testing"
	"time"
)

// engineStateRoot reads the state root an engine expects a block at its current
// height to carry. Tests that hand-build a block need it for the same reason a
// proposer does: a validator compares the block's root against its own ledger,
// and a block with none is refused for that rather than for whatever the test is
// about.
func engineStateRoot(t *testing.T, eng *Engine) []byte {
	t.Helper()
	eng.mu.Lock()
	defer eng.mu.Unlock()
	return eng.stateRootLocked()
}

// nowFuncForTest is the ordinary wall clock, named so a test reads as choosing
// one rather than as leaving a field unset.
func nowFuncForTest() func() time.Time { return time.Now }
