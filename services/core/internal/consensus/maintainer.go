package consensus

import (
	"fmt"
	"strings"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// Rotating the maintainer account.
//
// THE PROBLEM. maintainer_account was startup config and nothing else. Changing
// it meant editing YAML on every validator and restarting them, and because the
// share is consensus arithmetic, a network part-way through that edit has nodes
// computing different balances from the same block - which is a fork. So the one
// operation the maintainer is most likely to need, moving to a fresh key, was
// also the one most likely to break the chain. A key with no rotation path is a
// key you can only lose.
//
// THE FIX. Rotation is a transaction. The current maintainer signs a transfer to
// a reserved recipient naming the successor; the engine applies it inside the
// same critical section as the block's transfers, and persists it so a restart
// resumes from what the chain says rather than from what the file says. Every
// node switches at the same height because they all apply the same committed
// block. No coordinated restart, no window in which nodes disagree.
//
// WHO MAY DO IT, and the choice not made. Only the CURRENT maintainer, proved by
// the transaction's own signature. A validator-quorum override was deliberately
// NOT added: it would let a validator majority redirect the maintainer's income
// to themselves, which turns the standing share from a commitment into something
// held at the set's pleasure. That may be the right governance for some network,
// but it is a decision for whoever runs one, not a default shipped in the
// engine.
//
// WHAT THIS DOES NOT FIX. A LOST key still cannot sign, so it cannot rotate. The
// accrued balance behind a lost key is gone the way any lost key's balance is
// gone, and redirecting the future stream then falls back to the coordinated
// config edit this exists to avoid. Rotation is key hygiene - move before you
// have to - not key recovery. Nothing in a chain can be.

const (
	// maintainerRotatePrefix namespaces the rotation recipient. It sits under
	// "consensus/" with the other protocol namespaces, so it can never collide
	// with a real account id (64 lowercase hex, no slash).
	maintainerRotatePrefix = "consensus/maintainer/rotate/"

	// There is deliberately NO separate persisted key for the account in force.
	// The engine already replays every committed block at startup to rebuild the
	// dedup set and the burn tallies, so replaying the rotations costs nothing
	// and leaves the CHAIN as the single authority. A second copy in the store
	// would be a second thing to keep in sync, and a crash between writing it and
	// swapping the in-memory value would leave the two disagreeing about who is
	// being paid.
)

// MaintainerRotateRecipient is the recipient that rotates the maintainer account
// to next. Exported so a client can build the transaction without duplicating
// the format.
func MaintainerRotateRecipient(next string) string {
	return maintainerRotatePrefix + next
}

// IsMaintainerRotateRecipient reports whether a recipient is a rotation.
func IsMaintainerRotateRecipient(to string) bool {
	return strings.HasPrefix(to, maintainerRotatePrefix)
}

// ParseMaintainerRotate decodes the successor account from a rotation recipient.
//
// It applies the same shape check New applies to the configured account, and for
// the same reason: a rotation to a malformed id would send the fee somewhere
// nobody holds a key for, every block, forever, and look exactly like it worked.
// Refusing here means such a transaction can never be committed at all.
func ParseMaintainerRotate(to string) (string, error) {
	if !IsMaintainerRotateRecipient(to) {
		return "", fmt.Errorf("%w: not a maintainer rotation: %q", ErrInvalidMessage, to)
	}
	next := strings.TrimPrefix(to, maintainerRotatePrefix)
	if next == "" {
		return "", fmt.Errorf("%w: maintainer rotation names no successor", ErrInvalidMessage)
	}
	if !isAccountID(next) {
		return "", fmt.Errorf("%w: maintainer rotation target %q is not an account id "+
			"(64 lowercase hex characters)", ErrInvalidMessage, next)
	}
	return next, nil
}

// verifyMaintainerRotateLocked decides whether a rotation may enter a block.
//
// Two conditions, both checkable from committed state alone so every node
// reaches the same answer: the successor parses, and the SENDER is the account
// currently being paid. The second is the whole authorization - the signature
// over the transaction is already verified by the time this runs, so proving the
// sender is the maintainer proves the maintainer authorized it.
func (e *Engine) verifyMaintainerRotateLocked(tx *token.Transaction) error {
	next, err := ParseMaintainerRotate(tx.To)
	if err != nil {
		return err
	}
	current := e.MaintainerAccountInForce()
	if current == "" {
		return fmt.Errorf("%w: no maintainer account is configured, so there is nothing to "+
			"rotate; set consensus.maintainer_account first", ErrInvalidMessage)
	}
	if tx.SenderID() != current {
		return fmt.Errorf("%w: only the current maintainer (%s) may rotate the maintainer "+
			"account; this transaction is from %s", ErrInvalidMessage, current, tx.SenderID())
	}
	if next == current {
		return fmt.Errorf("%w: maintainer rotation names the account already in force",
			ErrInvalidMessage)
	}
	return nil
}

// applyMaintainerRotate switches the account. It runs inside the block's ledger
// critical section, so the rotation and the block's transfers land together or
// not at all.
//
// It takes no lock, for the reason applyBurnAttestation takes none: e.mu under
// the ledger lock would establish a second lock order over the same two locks.
// The account is an atomic instead, the same shape the validator set already
// uses for exactly this situation.
func (e *Engine) applyMaintainerRotate(tx *token.Transaction) error {
	next, err := ParseMaintainerRotate(tx.To)
	if err != nil {
		// Block validation already refused this, so reaching here means a block
		// was committed that should not have been. Do not rotate and do not wedge.
		fmt.Printf("consensus: committed block carries an unparseable maintainer rotation %q: %v\n",
			tx.To, err)
		return nil
	}
	prev := e.MaintainerAccountInForce()
	e.setMaintainerAccount(next)
	fmt.Printf("consensus: maintainer account rotated from %s to %s\n", prev, next)
	return nil
}

// replayMaintainerRotate applies a rotation seen while rebuilding state from the
// persisted chain at startup. Same effect, no log line: a restart should not
// reprint the network's whole rotation history.
func (e *Engine) replayMaintainerRotate(tx *token.Transaction) {
	if next, err := ParseMaintainerRotate(tx.To); err == nil {
		e.setMaintainerAccount(next)
	}
}

// MaintainerAccountInForce returns the account the fee share is paid to right
// now: the rotated value if the chain has one, otherwise the configured one.
func (e *Engine) MaintainerAccountInForce() string {
	if p := e.maintainerAccount.Load(); p != nil {
		return *p
	}
	return ""
}

// setMaintainerAccount swaps the account in force.
func (e *Engine) setMaintainerAccount(id string) {
	e.maintainerAccount.Store(&id)
}
