package consensus

import (
	"fmt"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// This is a single-use, chain-specific repair for the production founder
// ceremony. Launch smoke tests spent 100 native base units into accounts that
// cannot sign, leaving the immutable 50M vault allocation under-backed by
// exactly that amount. The operation moves existing supply from the genesis
// reward pool. It does not mint.
//
// Keeping the constants in consensus code is intentional. A configurable or
// generic reward-pool withdrawal would be an administrative mint-like power.
// This operation is valid only for this account, amount, and exact pre-repair
// balance, and therefore cannot execute twice.
const (
	launchRepairRecipient        = "consensus/launch-repair/founder-shortfall-v1"
	launchRepairFounder          = "945871e41d6116218c5b9dde5f384843396e12949ab2232dc5d6d57a5be2853b"
	launchRepairAmount    uint64 = 100
	launchRepairBefore    uint64 = 49_999_999_999_999_900

	// A rolling restart used to extract validator fee dust exposed that vote
	// history is not restart-persistent. Virginia signed conflicting prevotes
	// at height 109 and was slashed. Restore only that exact bond from the pool
	// so bonded-open admission can return the launch set to three.
	validatorRepairRecipient        = "consensus/launch-repair/virginia-restart-slash-v1"
	validatorRepairAccount          = "86c3d2ec2a1378a1a647512112002c1a92e9e781e3fbcfb3e9e2c999967431fb"
	validatorRepairAmount    uint64 = 1_000_000_000_000_000
	validatorRepairBefore    uint64 = 0

	// The single equivocation record this repair pardons, identified by its
	// exact position so no other offence can match. Every launch node holds a
	// byte-identical copy of it.
	//
	// The offence was real and the evidence is valid; what was wrong was the
	// engine, which did not persist its own votes and so let an ordinary
	// service restart sign a second prevote at a position it had already taken
	// (see SelfVoteStore). Restoring the bond without this pardon restores
	// nothing: bonded-open approves a slash from STORED EVIDENCE, so the
	// validator would be slashed again at the epoch after it rejoined, which is
	// what happened twice. The evidence itself is not deleted - the record of
	// what the chain saw stays intact - it is excluded from slash approval.
	pardonedEvidenceHeight uint64 = 109
	pardonedEvidenceRound  uint64 = 0
)

// isPardonedRestartEvidence reports whether eq is that one record.
func isPardonedRestartEvidence(eq *Equivocation) bool {
	return eq != nil &&
		eq.VoterID == validatorRepairAccount &&
		eq.Height == pardonedEvidenceHeight &&
		eq.Round == pardonedEvidenceRound &&
		eq.Type == VoteTypePrevote
}

func IsLaunchRepairRecipient(to string) bool {
	return to == launchRepairRecipient || to == validatorRepairRecipient
}

type launchRepairSpec struct {
	signer      string
	destination string
	amount      uint64
	before      uint64
}

func repairSpec(to string) (launchRepairSpec, bool) {
	switch to {
	case launchRepairRecipient:
		return launchRepairSpec{
			signer:      launchRepairFounder,
			destination: launchRepairFounder,
			amount:      launchRepairAmount,
			before:      launchRepairBefore,
		}, true
	case validatorRepairRecipient:
		return launchRepairSpec{
			signer:      validatorRepairAccount,
			destination: validatorRepairAccount,
			amount:      validatorRepairAmount,
			before:      validatorRepairBefore,
		}, true
	default:
		return launchRepairSpec{}, false
	}
}

func verifyLaunchRepair(sender, to string, amount, destinationBalance uint64) error {
	spec, ok := repairSpec(to)
	if !ok {
		return fmt.Errorf("%w: not the production launch repair", ErrInvalidMessage)
	}
	if sender != spec.signer {
		return fmt.Errorf("%w: production launch repair must be signed by %s",
			ErrInvalidMessage, spec.signer)
	}
	if amount != spec.amount {
		return fmt.Errorf("%w: production launch repair amount is %d, got %d",
			ErrInvalidMessage, spec.amount, amount)
	}
	if destinationBalance != spec.before {
		return fmt.Errorf("%w: production launch repair requires destination balance %d, got %d",
			ErrInvalidMessage, spec.before, destinationBalance)
	}
	return nil
}

func (e *Engine) verifyLaunchRepairLocked(tx *token.Transaction) error {
	spec, ok := repairSpec(tx.To)
	if !ok {
		return fmt.Errorf("%w: unknown production launch repair", ErrInvalidMessage)
	}
	balance, err := e.ledger.Balance(spec.destination)
	if err != nil {
		return fmt.Errorf("consensus: read destination balance for launch repair: %w", err)
	}
	return verifyLaunchRepair(tx.SenderID(), tx.To, tx.Amount, balance)
}

// applyLaunchRepair transfers existing native units from the reward pool. The
// balance precondition is checked again inside the ledger critical section so
// two repair transactions in one block cannot both apply.
func applyLaunchRepair(ltx market.LedgerTx, tx *token.Transaction) (bool, error) {
	spec, ok := repairSpec(tx.To)
	if !ok {
		return false, nil
	}
	destinationBalance, err := ltx.Balance(spec.destination)
	if err != nil {
		return false, err
	}
	if err := verifyLaunchRepair(tx.SenderID(), tx.To, tx.Amount, destinationBalance); err != nil {
		return false, nil
	}
	pool, err := ltx.Balance(token.RewardPoolAccount)
	if err != nil {
		return false, err
	}
	if pool < spec.amount {
		return false, fmt.Errorf("consensus: launch repair reward pool has %d, needs %d",
			pool, spec.amount)
	}
	if err := ltx.Transfer(token.RewardPoolAccount, spec.destination, spec.amount); err != nil {
		return false, err
	}
	fmt.Printf("consensus: production launch repair moved %d native base units from reward pool to %s\n",
		spec.amount, spec.destination)
	return true, nil
}

// pardonVirginiaRestartSlashLocked cancels only the pending repeat slash caused
// by the already-recorded restart evidence. The validator repair restored the
// bond, and leaving the old evidence approval active would immediately slash
// that restored bond again at the next epoch. The founder repair transaction is
// the chain event that makes every node perform this cancellation identically.
// Callers hold e.mu.
func (e *Engine) pardonVirginiaRestartSlashLocked() {
	keepPending := e.pendingChanges[:0]
	for _, change := range e.pendingChanges {
		if change.Kind == SetChangeSlash && change.ValidatorID == validatorRepairAccount {
			continue
		}
		keepPending = append(keepPending, change)
	}
	e.pendingChanges = append([]SetChange(nil), keepPending...)

	keepApproved := e.approvedSpecs[:0]
	for _, change := range e.approvedSpecs {
		if change.Kind == SetChangeSlash && change.ValidatorID == validatorRepairAccount {
			continue
		}
		keepApproved = append(keepApproved, change)
	}
	e.approvedSpecs = append([]SetChange(nil), keepApproved...)
	delete(e.approvedChanges, "slash:"+validatorRepairAccount)
	delete(e.setChangeOffered, "slash:"+validatorRepairAccount)

	if e.sets != nil {
		if err := e.sets.SavePending(e.pendingChanges); err != nil {
			fmt.Printf("consensus: could not persist launch-repair slash pardon: %v\n", err)
		}
	}
}
