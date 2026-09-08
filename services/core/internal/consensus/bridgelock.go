package consensus

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// The lock half of the bridge, made consensus-ordered.
//
// WHY IT DID NOT EXIST. bridge.Lock moved native into escrow with a direct
// ledger write and had exactly one caller in the tree: cmd/bridge-attest,
// running against a throwaway ledger it seeds itself. No RPC reached it and no
// CLI command called it. So on a real node nobody could lock, no wMATRIX could
// ever be minted, and the DEX liquidity the whole on-ramp depends on could not
// be seeded. The unlock half had been made consensus-ordered; this half had no
// production path at all.
//
// WHY IT COULD NOT JUST BE EXPOSED. A direct ledger write moves collateral on
// one node and nowhere else - the same divergence that made FundAccount unsafe,
// except this one mints against the result. Exposing bridge.Lock over an RPC
// would have been the unlock bug again, in the direction that creates supply.
//
// WHY THIS IS SIMPLER THAN THE UNLOCK. The unlock needed a quorum of
// attestations because observing an Ethereum burn requires an Ethereum endpoint,
// and consensus may only depend on committed state. A lock needs no outside
// observation: the user's signed intent IS the transaction. So it is an ordinary
// signed transfer to a reserved recipient, and every node applies the same
// escrow move from the same committed block.
//
// THE LOCK ID IS DERIVED, NOT COUNTED. bridge.Lock numbered locks with a
// per-node sequence, which is exactly the kind of state two nodes disagree
// about. Here the id comes from the transaction itself: nonce, sender,
// recipient, amount. A sender cannot reuse a nonce - committedNonces refuses it
// - so (nonce, sender) is unique, and every node derives the same id from the
// same block with nothing to keep in sync.
//
// WHAT IS DELIBERATELY NOT HERE. Collecting a threshold of attestations. Each
// validator signs with its own secp256k1 attestor key, and gathering m of them
// is the client's job: it asks each validator's node for its signature over the
// committed lock. Gossiping partial signatures would be a second consensus, and
// the contract already does the counting.

const (
	// bridgeLockPrefix namespaces the reserved recipient. It shares the "bridge/"
	// namespace with the escrow account and the burn unlock, so bridge state is
	// recognizable, and it can never collide with a real account id (64 lowercase
	// hex, no slash).
	bridgeLockPrefix = "bridge/lock/"
)

// BridgeLockRecipient returns the recipient that locks native MATRIX for an
// Ethereum address. The AMOUNT is the transaction's value, not part of the
// recipient: this is the one reserved recipient that legitimately carries
// money, because moving it into escrow is the whole operation.
func BridgeLockRecipient(ethAddress [20]byte) string {
	return bridgeLockPrefix + hex.EncodeToString(ethAddress[:])
}

// IsBridgeLockRecipient reports whether a recipient is a bridge lock.
func IsBridgeLockRecipient(to string) bool {
	return strings.HasPrefix(to, bridgeLockPrefix)
}

// ParseBridgeLock decodes the Ethereum recipient from a lock recipient.
//
// Strict, and lowercase-only, for the reason every other reserved parser is: two
// spellings of one address would be two different locks against the same
// escrow, and the id is derived from the string.
func ParseBridgeLock(to string) ([20]byte, error) {
	var out [20]byte
	if !IsBridgeLockRecipient(to) {
		return out, fmt.Errorf("%w: not a bridge lock: %q", ErrInvalidMessage, to)
	}
	raw := strings.TrimPrefix(to, bridgeLockPrefix)
	if len(raw) != 40 {
		return out, fmt.Errorf("%w: bridge lock address is %d characters, want 40",
			ErrInvalidMessage, len(raw))
	}
	if raw != strings.ToLower(raw) {
		return out, fmt.Errorf("%w: bridge lock address must be lowercase hex", ErrInvalidMessage)
	}
	b, err := hex.DecodeString(raw)
	if err != nil {
		return out, fmt.Errorf("%w: bridge lock address is not hex: %v", ErrInvalidMessage, err)
	}
	copy(out[:], b)
	return out, nil
}

// BridgeLocker records a lock the chain has already applied.
//
// It records only. The escrow move happens in the consensus critical section
// before this is called, so an implementation that also transferred would double
// count. Mirrors BurnUnlocker: the engine names the capability it needs and the
// node supplies the bridge, so consensus does not import the bridge package.
type BridgeLocker interface {
	// RecordLock persists a committed lock so the bridge can attest to it later
	// and so Reconcile can account for it. It must be idempotent per lockID: a
	// node replaying a block must not double count the locked total.
	RecordLock(lockID [32]byte, from string, recipient [20]byte, nativeAmount uint64) error
	// EscrowAccount is where the consensus critical section moves the value. It
	// comes from the bridge rather than being duplicated here, so the two halves
	// cannot disagree about which account holds the collateral.
	EscrowAccount() string
}

// DeriveLockID computes the lock id for a committed lock transaction. It is
// exported because a client needs the same id to ask for attestations, and
// recomputing it wrongly would ask about a lock that does not exist.
//
// It is byte-identical to the bridge's own derivation - same fields, same
// length-prefixing - with the transaction NONCE where the bridge's per-node
// counter used to be. A test pins the two against each other, because an id that
// differed between the chain and the bridge would attest to a lock nobody could
// find.
func DeriveLockID(nonce uint64, from string, recipient [20]byte, amount uint64) [32]byte {
	h := sha256.New()
	var u [8]byte
	binary.BigEndian.PutUint64(u[:], nonce)
	h.Write(u[:])
	// Length-prefixed so a sender id and the bytes after it cannot be re-split
	// into a different pair that hashes the same.
	var lp [4]byte
	binary.BigEndian.PutUint32(lp[:], uint32(len(from)))
	h.Write(lp[:])
	h.Write([]byte(from))
	h.Write(recipient[:])
	binary.BigEndian.PutUint64(u[:], amount)
	h.Write(u[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// verifyBridgeLockLocked decides whether a lock may enter a block. Everything it
// checks is committed state or the transaction itself, so every node agrees.
func (e *Engine) verifyBridgeLockLocked(tx *token.Transaction) error {
	if _, err := ParseBridgeLock(tx.To); err != nil {
		return err
	}
	if tx.Amount == 0 {
		return fmt.Errorf("%w: a bridge lock of zero would mint nothing and still consume a "+
			"lock id", ErrInvalidMessage)
	}
	// Affordability is NOT checked here. It is checked at apply time against the
	// balance every node agrees on, the same way an ordinary transfer is: a lock
	// the sender cannot afford is deterministically skipped, not rejected.
	return nil
}

// applyBridgeLock moves the value into escrow and records the lock. It runs
// inside the block's ledger critical section, so the escrow move and the block's
// other transfers land together or not at all.
//
// It reports whether the lock was applied. A sender who cannot afford it is
// skipped deterministically - every node sees the same prior balances - rather
// than wedging the block.
func (e *Engine) applyBridgeLock(ltx market.LedgerTx, tx *token.Transaction) (bool, error) {
	recipient, err := ParseBridgeLock(tx.To)
	if err != nil {
		// Block validation already refused this, so a block was committed that
		// should not have been. Do not move money and do not wedge.
		fmt.Printf("consensus: committed block carries an unparseable bridge lock %q: %v\n", tx.To, err)
		return false, nil
	}
	if e.bridgeLocker == nil {
		// No bridge configured on this node. The escrow move must STILL happen, or
		// this node's balances would diverge from every node that has one. Escrow
		// is an ordinary reserved account; a node with no bridge simply cannot
		// attest to the lock.
		return e.moveToEscrow(ltx, tx, defaultEscrowAccount)
	}
	moved, err := e.moveToEscrow(ltx, tx, e.bridgeLocker.EscrowAccount())
	if err != nil || !moved {
		return moved, err
	}
	lockID := DeriveLockID(tx.Nonce, tx.SenderID(), recipient, tx.Amount)
	if err := e.bridgeLocker.RecordLock(lockID, tx.SenderID(), recipient, tx.Amount); err != nil {
		// The value is already in escrow and the block is already committed.
		// Failing here would wedge this node against a chain its peers accepted,
		// so report and continue: Reconcile will show the discrepancy.
		fmt.Printf("consensus: escrowed %d for lock %x but could not record it: %v\n",
			tx.Amount, lockID, err)
	}
	return true, nil
}

// moveToEscrow performs the affordability check and the transfer.
func (e *Engine) moveToEscrow(ltx market.LedgerTx, tx *token.Transaction, escrow string) (bool, error) {
	sender := tx.SenderID()
	bal, err := ltx.Balance(sender)
	if err != nil {
		return false, err
	}
	if bal < tx.Amount {
		return false, nil
	}
	// No fee. A lock is a reserved recipient so paysFee already excludes it, and
	// that is load-bearing rather than incidental: escrow must receive the FULL
	// amount, because the wrapped supply minted against it is computed from the
	// amount the user locked. A fee here would mint more wrapped than the escrow
	// holds, breaking the 1:1 backing by exactly the fee.
	if err := ltx.Transfer(sender, escrow, tx.Amount); err != nil {
		return false, err
	}
	return true, nil
}

// defaultEscrowAccount mirrors bridge.EscrowAccount for nodes with no bridge
// configured. It is a constant rather than an import because consensus must not
// depend on the bridge package; the BridgeLocker interface exists so the two can
// disagree loudly rather than silently, and a test pins that they match.
const defaultEscrowAccount = "bridge/escrow"
