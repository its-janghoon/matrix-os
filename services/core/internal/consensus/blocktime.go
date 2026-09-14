package consensus

import (
	"fmt"
	"time"
)

// The rules that make a block's timestamp worth reading.
//
// A timestamp a proposer picks freely is worth nothing: it could date a block to
// next year, or to before its own parent, and an explorer would show whatever it
// said. Two checks every validator makes turn it into a bounded claim.
//
// It is deliberately NOT a trusted clock. Inside the window below a proposer
// still chooses, and validators rotate, so a run of blocks can drift by seconds
// in either direction. It is enough to order a day's transfers, date a deposit,
// and drive an explorer; it is not enough to settle a contract on, which is one
// more thing this chain does not claim to do.

// proposalTimestampLocked picks the timestamp for a block this node proposes.
// Callers must hold e.mu.
//
// It never goes backwards from the parent even if this node's clock has: a
// proposer whose NTP just stepped back would otherwise build blocks no honest
// validator can accept, and would stall its own turns rather than anyone else's.
func (e *Engine) proposalTimestampLocked() int64 {
	now := e.now().Unix()
	if parent, ok := e.parentTimestampLocked(); ok && now <= parent {
		return parent + 1
	}
	return now
}

// verifyBlockTimestampLocked applies both rules to a block this node is deciding
// whether to vote for. Callers must hold e.mu.
//
// Genesis has no parent, so only the skew rule applies there.
func (e *Engine) verifyBlockTimestampLocked(b *Block) error {
	if b.Timestamp <= 0 {
		return fmt.Errorf("%w: block %d carries no timestamp", ErrInvalidMessage, b.Height)
	}
	stamped := time.Unix(b.Timestamp, 0)
	if delta := e.now().Sub(stamped); delta > BlockTimestampSkew || delta < -BlockTimestampSkew {
		return fmt.Errorf("%w: block %d is stamped %s, outside the %s window around this node's clock",
			ErrInvalidMessage, b.Height, stamped.UTC().Format(time.RFC3339), BlockTimestampSkew)
	}
	if parent, ok := e.parentTimestampLocked(); ok && b.Timestamp <= parent {
		return fmt.Errorf("%w: block %d is stamped %d, not after its parent's %d",
			ErrInvalidMessage, b.Height, b.Timestamp, parent)
	}
	return nil
}

// parentTimestampLocked reads the committed parent's timestamp. Callers must
// hold e.mu.
func (e *Engine) parentTimestampLocked() (int64, bool) {
	if e.height == 0 || e.chain == nil {
		return 0, false
	}
	parent, err := e.chain.BlockAt(e.height - 1)
	if err != nil || parent == nil {
		return 0, false
	}
	return parent.Timestamp, true
}
